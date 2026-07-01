package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backup-manager/internal/store"
)

type agentContextKey string

const currentAgentKey agentContextKey = "current-agent"

func currentAgentFromContext(ctx context.Context) (*store.Agent, bool) {
	agent, ok := ctx.Value(currentAgentKey).(*store.Agent)
	return agent, ok
}

func agentMiddleware(s *store.Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimSpace(r.Header.Get("X-Agent-Code"))
		token := r.Header.Get("X-Agent-Token")
		if code == "" || token == "" {
			writeError(w, http.StatusUnauthorized, "missing agent credentials")
			return
		}

		agent, err := s.GetAgentByCode(r.Context(), code)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid agent code")
			return
		}
		if !agent.Enabled {
			writeError(w, http.StatusForbidden, "agent disabled")
			return
		}
		if token != agent.TokenHash {
			writeError(w, http.StatusUnauthorized, "invalid agent token")
			return
		}

		ctx := context.WithValue(r.Context(), currentAgentKey, agent)
		next(w, r.WithContext(ctx))
	}
}

type agentHandler struct{ store *store.Store }

func RegisterAgentRoutes(mux *http.ServeMux, s *store.Store) {
	h := &agentHandler{store: s}

	mux.HandleFunc("POST /api/agent/heartbeat", agentMiddleware(s, h.heartbeat))
	mux.HandleFunc("GET /api/agent/schedules/enabled", agentMiddleware(s, h.listEnabledSchedules))
	mux.HandleFunc("GET /api/agent/schedules/{id}", agentMiddleware(s, h.getSchedule))
	mux.HandleFunc("POST /api/agent/schedules/{id}/runtime", agentMiddleware(s, h.updateRuntime))
	mux.HandleFunc("POST /api/agent/schedules/{id}/status", agentMiddleware(s, h.updateStatus))
	mux.HandleFunc("GET /api/agent/projects/{id}", agentMiddleware(s, h.getProject))
	mux.HandleFunc("GET /api/agent/projects/{id}/targets", agentMiddleware(s, h.listTargets))
	mux.HandleFunc("GET /api/agent/projects/{id}/retention", agentMiddleware(s, h.listRetention))
	mux.HandleFunc("POST /api/agent/records", agentMiddleware(s, h.createRecord))
	mux.HandleFunc("PUT /api/agent/records/{id}", agentMiddleware(s, h.updateRecord))
}

func (h *agentHandler) requireAgent(w http.ResponseWriter, r *http.Request) (*store.Agent, bool) {
	agent, ok := currentAgentFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing agent context")
		return nil, false
	}
	return agent, true
}

func (h *agentHandler) heartbeat(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	var body store.AgentHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON")
		return
	}
	if err := h.store.TouchAgentHeartbeat(r.Context(), agent.ID, body); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := h.store.GetAgent(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *agentHandler) listEnabledSchedules(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	schedules, err := h.store.ListEnabledSchedulesForAgent(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, schedules)
}

func (h *agentHandler) getSchedule(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	owns, err := h.store.AgentOwnsSchedule(r.Context(), agent.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !owns {
		writeError(w, http.StatusForbidden, "schedule not assigned to agent")
		return
	}
	sch, err := h.store.GetSchedule(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到排程")
		return
	}
	writeJSON(w, http.StatusOK, sch)
}

func (h *agentHandler) updateRuntime(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	owns, err := h.store.AgentOwnsSchedule(r.Context(), agent.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !owns {
		writeError(w, http.StatusForbidden, "schedule not assigned to agent")
		return
	}
	var body struct {
		LastRunAt time.Time `json:"last_run_at"`
		NextRunAt time.Time `json:"next_run_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON")
		return
	}
	if err := h.store.UpdateScheduleRunTime(r.Context(), id, body.LastRunAt, body.NextRunAt); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *agentHandler) updateStatus(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	owns, err := h.store.AgentOwnsSchedule(r.Context(), agent.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !owns {
		writeError(w, http.StatusForbidden, "schedule not assigned to agent")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON")
		return
	}
	if err := h.store.UpdateScheduleStatus(r.Context(), id, body.Status); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *agentHandler) getProject(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	owns, err := h.store.AgentOwnsProject(r.Context(), agent.ID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !owns {
		writeError(w, http.StatusForbidden, "project not assigned to agent")
		return
	}
	project, err := h.store.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	if err := h.store.ResolveProjectNASForAgent(r.Context(), project, agent.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (h *agentHandler) listTargets(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	owns, err := h.store.AgentOwnsProject(r.Context(), agent.ID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !owns {
		writeError(w, http.StatusForbidden, "project not assigned to agent")
		return
	}
	targets, err := h.store.ListTargets(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targets)
}

func (h *agentHandler) listRetention(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	owns, err := h.store.AgentOwnsProject(r.Context(), agent.ID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !owns {
		writeError(w, http.StatusForbidden, "project not assigned to agent")
		return
	}
	policies, err := h.store.ListRetention(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policies)
}

func (h *agentHandler) createRecord(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	var rec store.BackupRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	rec.AgentID = &agent.ID
	rec.AgentName = agent.Name
	rec.RunHost = agent.HostName
	id, err := h.store.CreateRecord(r.Context(), &rec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (h *agentHandler) updateRecord(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.requireAgent(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	var rec store.BackupRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	rec.ID = id
	rec.AgentID = &agent.ID
	rec.AgentName = agent.Name
	rec.RunHost = agent.HostName
	if err := h.store.UpdateRecord(r.Context(), &rec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
