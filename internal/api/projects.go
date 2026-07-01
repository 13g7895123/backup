package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"backup-manager/internal/scheduler"
	"backup-manager/internal/store"
)

// projectHandler 持有 store
type projectHandler struct {
	store     *store.Store
	scheduler *scheduler.DynamicScheduler
}

func RegisterProjectRoutes(mux *http.ServeMux, s *store.Store, sc *scheduler.DynamicScheduler) {
	h := &projectHandler{store: s, scheduler: sc}

	mux.HandleFunc("GET /api/projects", h.list)
	mux.HandleFunc("POST /api/projects", h.create)
	mux.HandleFunc("GET /api/projects/export-all", h.exportAll)
	mux.HandleFunc("GET /api/projects/{id}", h.get)
	mux.HandleFunc("PUT /api/projects/{id}", h.update)
	mux.HandleFunc("DELETE /api/projects/{id}", h.delete)
	mux.HandleFunc("PATCH /api/projects/{id}/toggle", h.toggle)
	mux.HandleFunc("GET /api/projects/{id}/export", h.exportOne)
}

func (h *projectHandler) list(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *projectHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	proj, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	writeJSON(w, http.StatusOK, proj)
}

func (h *projectHandler) create(w http.ResponseWriter, r *http.Request) {
	var p store.Project
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		writeError(w, http.StatusBadRequest, "name 不可為空")
		return
	}
	h.applyProjectDefaults(r.Context(), &p)
	if p.NasBase == "" {
		p.NasBase = "/mnt/nas/backups"
	}
	if err := h.validateProject(r.Context(), &p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p.Enabled = true
	result, err := h.store.CreateProject(r.Context(), &p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 依專案設定自動建立備份目標
	h.autoCreateTargets(r.Context(), result)
	writeJSON(w, http.StatusCreated, result)
}

// autoCreateTargets 根據專案的 backup_dirs / DB 設定自動建立 backup_targets
func (h *projectHandler) autoCreateTargets(ctx context.Context, p *store.Project) {
	// 1. 每個備份目錄建立一個 files target
	for _, dir := range p.BackupDirs {
		if dir == "" {
			continue
		}
		cfgBytes, _ := json.Marshal(map[string]any{
			"source":   dir,
			"compress": "gzip",
			"exclude":  []string{},
		})
		h.store.CreateTarget(ctx, &store.BackupTarget{ //nolint
			ProjectID: p.ID,
			Type:      "files",
			Label:     dir,
			Config:    json.RawMessage(cfgBytes),
			Enabled:   true,
		})
	}

	// 2. 若有 DB 設定，建立一個 database target
	if p.DockerDbContainer == "" && p.DbHost == "" {
		return
	}
	dbCfg := map[string]any{
		"db_type":      p.DbType,
		"name":         p.DbName,
		"user":         p.DbUser,
		"password":     p.DbPassword,
		"password_env": p.DbPasswordEnv,
	}
	label := "DB"
	if p.DockerDbContainer != "" {
		dbCfg["container_name"] = p.DockerDbContainer
		label = "DB (" + p.DockerDbContainer + ")"
	} else {
		dbCfg["host"] = p.DbHost
		dbCfg["port"] = p.DbPort
		label = "DB (" + p.DbHost + ")"
	}
	cfgBytes, _ := json.Marshal(dbCfg)
	h.store.CreateTarget(ctx, &store.BackupTarget{ //nolint
		ProjectID: p.ID,
		Type:      "database",
		Label:     label,
		Config:    json.RawMessage(cfgBytes),
		Enabled:   true,
	})
}

func (h *projectHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	before, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	var p store.Project
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	p.ID = id
	h.applyProjectDefaults(r.Context(), &p)
	if err := h.validateProject(r.Context(), &p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.UpdateProject(r.Context(), &p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 若專案有 DB 設定，且目前尚無任何 database target，則自動補建
	h.ensureDbTarget(r.Context(), &p)
	if err := h.syncProjectSchedules(r.Context(), before, &p); err != nil {
		writeError(w, http.StatusInternalServerError, "專案已更新，但同步排程失敗: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *projectHandler) validateProject(ctx context.Context, p *store.Project) error {
	if p.ExecutorType == "" {
		p.ExecutorType = "local"
	}
	if p.ExecutorType != "local" && p.ExecutorType != "agent" {
		return &validationError{msg: "executor_type 必須為 local 或 agent"}
	}
	if p.ExecutorType == "agent" {
		if p.ExecutorAgentID == nil {
			return &validationError{msg: "executor_agent_id 不可為空"}
		}
		agent, err := h.store.GetAgent(ctx, *p.ExecutorAgentID)
		if err != nil || !agent.Enabled {
			return &validationError{msg: "指定的 executor agent 不存在或未啟用"}
		}
		ok, err := h.store.AgentSupportsProjectNAS(ctx, agent.ID, p.NasTargetID)
		if err != nil {
			return err
		}
		if !ok {
			return &validationError{msg: "指定的 agent 不支援該 NAS target"}
		}
	}
	return nil
}

func (h *projectHandler) applyProjectDefaults(ctx context.Context, p *store.Project) {
	if p.NasBase == "" {
		p.NasBase = "/mnt/nas/backups"
	}
	if p.NasTargetID == nil {
		if target, err := h.store.GetDefaultNASTarget(ctx); err == nil && target != nil {
			p.NasTargetID = &target.ID
			if strings.TrimSpace(p.NasSubpath) == "" {
				p.NasSubpath = buildDefaultNASSubpath(target.DefaultSubpath, p.Name)
			}
		}
	}
	if strings.TrimSpace(p.NasSubpath) == "" && p.Name != "" {
		p.NasSubpath = buildDefaultNASSubpath("", p.Name)
	}
}

func buildDefaultNASSubpath(base, projectName string) string {
	slug := slugify(projectName)
	if slug == "" {
		slug = "project"
	}
	if strings.TrimSpace(base) == "" {
		return slug
	}
	return filepath.ToSlash(filepath.Join(base, slug))
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

// ensureDbTarget 確保專案若有 DB 設定，就存在至少一個 database target
func (h *projectHandler) ensureDbTarget(ctx context.Context, p *store.Project) {
	if p.DockerDbContainer == "" && p.DbHost == "" {
		return
	}
	targets, err := h.store.ListTargets(ctx, p.ID)
	if err != nil {
		return
	}
	for _, t := range targets {
		if t.Type == "database" {
			return // 已有 database target，不重複建立
		}
	}
	// 無 database target，自動建立
	h.autoCreateTargets(ctx, p)
}

func (h *projectHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	project, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	if err := h.syncProjectSchedules(r.Context(), project, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "刪除前移除排程失敗: "+err.Error())
		return
	}
	if err := h.store.DeleteProject(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *projectHandler) toggle(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	before, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON")
		return
	}
	if err := h.store.ToggleProject(r.Context(), id, body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	after := *before
	after.Enabled = body.Enabled
	after.UpdatedAt = time.Now()
	if err := h.syncProjectSchedules(r.Context(), before, &after); err != nil {
		writeError(w, http.StatusInternalServerError, "專案狀態已更新，但同步排程失敗: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

func (h *projectHandler) syncProjectSchedules(ctx context.Context, before, after *store.Project) error {
	projectID := 0
	switch {
	case after != nil:
		projectID = after.ID
	case before != nil:
		projectID = before.ID
	default:
		return nil
	}

	schedules, err := h.store.ListSchedules(ctx, projectID)
	if err != nil {
		return err
	}

	for _, sch := range schedules {
		if before != nil {
			if err := h.detachSchedule(ctx, before, sch.ID); err != nil {
				return err
			}
		}
		if after != nil && after.Enabled && sch.Enabled {
			if err := h.attachSchedule(ctx, after, sch.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *projectHandler) detachSchedule(ctx context.Context, project *store.Project, scheduleID int) error {
	if project.ExecutorType == "agent" {
		if project.ExecutorAgentID == nil {
			return nil
		}
		agent, err := h.store.GetAgent(ctx, *project.ExecutorAgentID)
		if err != nil {
			return err
		}
		h.notifyAgent(agent, scheduleID, "remove")
		return nil
	}
	if h.scheduler != nil {
		h.scheduler.Remove(scheduleID)
	}
	return nil
}

func (h *projectHandler) attachSchedule(ctx context.Context, project *store.Project, scheduleID int) error {
	if project.ExecutorType == "agent" {
		if project.ExecutorAgentID == nil {
			return fmt.Errorf("project executor_agent_id missing")
		}
		agent, err := h.store.GetAgent(ctx, *project.ExecutorAgentID)
		if err != nil {
			return err
		}
		h.notifyAgent(agent, scheduleID, "reload")
		return nil
	}
	if h.scheduler == nil {
		return nil
	}
	return h.scheduler.Reload(ctx, scheduleID)
}

func (h *projectHandler) notifyAgent(agent *store.Agent, scheduleID int, action string) {
	go func() {
		req, err := http.NewRequestWithContext(context.Background(), "POST",
			fmt.Sprintf("%s/schedules/%d/%s", agent.BaseURL, scheduleID, action), nil)
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Code", agent.Code)
		req.Header.Set("X-Agent-Token", agent.TokenHash)
		cl := &http.Client{Timeout: 5 * time.Second}
		resp, err := cl.Do(req)
		if err == nil && resp != nil {
			resp.Body.Close()
		}
	}()
}

// pathID 從 {id} path value 解析整數
func pathID(r *http.Request, key string) (int, error) {
	return strconv.Atoi(r.PathValue(key))
}

// ── 匯出結構 ──────────────────────────────────────────────────────────────────

type ProjectExport struct {
	Version   string               `json:"version"`
	Project   *store.Project       `json:"project"`
	Targets   []store.BackupTarget `json:"targets"`
	Schedules []store.Schedule     `json:"schedules"`
}

func buildExport(ctx context.Context, s *store.Store, p *store.Project) (*ProjectExport, error) {
	targets, err := s.ListTargets(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	schedules, err := s.ListSchedules(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if targets == nil {
		targets = []store.BackupTarget{}
	}
	if schedules == nil {
		schedules = []store.Schedule{}
	}
	return &ProjectExport{
		Version:   "1",
		Project:   p,
		Targets:   targets,
		Schedules: schedules,
	}, nil
}

func (h *projectHandler) exportOne(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	p, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	exp, err := buildExport(r.Context(), h.store, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, exp)
}

func (h *projectHandler) exportAll(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var exports []*ProjectExport
	for i := range projects {
		exp, err := buildExport(r.Context(), h.store, &projects[i])
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		exports = append(exports, exp)
	}
	if exports == nil {
		exports = []*ProjectExport{}
	}
	writeJSON(w, http.StatusOK, exports)
}
