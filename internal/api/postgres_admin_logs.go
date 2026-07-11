package api

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	postgresLogCapacity = 500
	postgresLogBodyMax  = 64 * 1024
)

type postgresOperationLog struct {
	ID           uint64    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	ProjectID    int       `json:"project_id,omitempty"`
	Operation    string    `json:"operation"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	RemoteAddr   string    `json:"remote_addr"`
	Status       int       `json:"status"`
	Success      bool      `json:"success"`
	DurationMS   int64     `json:"duration_ms"`
	RequestBody  string    `json:"request_body,omitempty"`
	ResponseBody string    `json:"response_body,omitempty"`
	Truncated    bool      `json:"truncated,omitempty"`
}

var postgresOperationLogs = struct {
	sync.RWMutex
	items []postgresOperationLog
}{}
var postgresOperationLogID atomic.Uint64

type postgresLogResponseWriter struct {
	http.ResponseWriter
	status    int
	body      bytes.Buffer
	truncated bool
}

func (w *postgresLogResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *postgresLogResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	remaining := postgresLogBodyMax - w.body.Len()
	if remaining > 0 {
		part := p
		if len(part) > remaining {
			part = part[:remaining]
			w.truncated = true
		}
		_, _ = w.body.Write(part)
	} else if len(p) > 0 {
		w.truncated = true
	}
	return w.ResponseWriter.Write(p)
}

func (h *postgresAdminHandler) withOperationLog(operation string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestBody, requestTruncated := readPostgresLogRequest(r)
		lw := &postgresLogResponseWriter{ResponseWriter: w}
		next(lw, r)
		if lw.status == 0 {
			lw.status = http.StatusOK
		}
		projectID, _ := strconv.Atoi(r.PathValue("id"))
		entry := postgresOperationLog{
			ID: postgresOperationLogID.Add(1), CreatedAt: started.UTC(), ProjectID: projectID,
			Operation: operation, Method: r.Method, Path: r.URL.RequestURI(), RemoteAddr: r.RemoteAddr,
			Status: lw.status, Success: lw.status >= 200 && lw.status < 400,
			DurationMS: time.Since(started).Milliseconds(), RequestBody: requestBody,
			ResponseBody: strings.TrimSpace(lw.body.String()), Truncated: requestTruncated || lw.truncated,
		}
		appendPostgresOperationLog(entry)
		log.Printf("[postgres-admin] id=%d project_id=%d operation=%s method=%s path=%s status=%d duration_ms=%d remote=%s request=%q response=%q truncated=%t",
			entry.ID, entry.ProjectID, entry.Operation, entry.Method, entry.Path, entry.Status, entry.DurationMS,
			entry.RemoteAddr, entry.RequestBody, entry.ResponseBody, entry.Truncated)
	}
}

func readPostgresLogRequest(r *http.Request) (string, bool) {
	if r.Body == nil {
		return "", false
	}
	// The handlers accept at most 2 MiB. Keep the complete body for replay while
	// retaining only the first 64 KiB in the diagnostic log.
	b, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024+1))
	if err != nil {
		return "<讀取 request body 失敗: " + err.Error() + ">", false
	}
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(b), r.Body))
	logBody := b
	truncated := len(logBody) > postgresLogBodyMax
	if truncated {
		logBody = logBody[:postgresLogBodyMax]
	}
	return strings.TrimSpace(string(logBody)), truncated
}

func appendPostgresOperationLog(entry postgresOperationLog) {
	postgresOperationLogs.Lock()
	defer postgresOperationLogs.Unlock()
	postgresOperationLogs.items = append(postgresOperationLogs.items, entry)
	if extra := len(postgresOperationLogs.items) - postgresLogCapacity; extra > 0 {
		copy(postgresOperationLogs.items, postgresOperationLogs.items[extra:])
		postgresOperationLogs.items = postgresOperationLogs.items[:postgresLogCapacity]
	}
}

// listOperationLogs intentionally has no authentication, matching the PostgreSQL admin routes.
func (h *postgresAdminHandler) listOperationLogs(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= postgresLogCapacity {
			limit = n
		}
	}
	operation := strings.TrimSpace(r.URL.Query().Get("operation"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	postgresOperationLogs.RLock()
	items := make([]postgresOperationLog, 0, limit)
	for i := len(postgresOperationLogs.items) - 1; i >= 0 && len(items) < limit; i-- {
		item := postgresOperationLogs.items[i]
		if item.ProjectID != 0 && item.ProjectID != projectID {
			continue
		}
		if operation != "" && item.Operation != operation {
			continue
		}
		if status == "success" && !item.Success || status == "failed" && item.Success {
			continue
		}
		items = append(items, item)
	}
	postgresOperationLogs.RUnlock()
	if items == nil {
		items = []postgresOperationLog{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": items, "count": len(items), "capacity": postgresLogCapacity})
}
