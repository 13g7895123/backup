package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"backup-manager/internal/store"
)

type agentCommandHandler struct {
	store *store.Store
}

func RegisterAgentCommandRoutes(mux *http.ServeMux, s *store.Store) {
	h := &agentCommandHandler{store: s}
	mux.HandleFunc("GET /api/admin/agent-commands", h.list)
	mux.HandleFunc("GET /api/admin/agent-commands/{id}", h.get)
}

func enqueueAgentCommand(ctx context.Context, s *store.Store, cmd *store.AgentCommand) (*store.AgentCommand, error) {
	if cmd == nil {
		return nil, nil
	}
	if cmd.Status == "" {
		cmd.Status = store.AgentCommandStatusPending
	}
	return s.CreateAgentCommand(ctx, cmd)
}

func (h *agentCommandHandler) list(w http.ResponseWriter, r *http.Request) {
	var filter store.ListAgentCommandsFilter
	if raw := r.URL.Query().Get("agent_id"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "無效的 agent_id")
			return
		}
		filter.AgentID = &id
	}
	filter.Status = r.URL.Query().Get("status")
	filter.Type = r.URL.Query().Get("type")
	filter.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	filter.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

	commands, total, err := h.store.ListAgentCommands(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": commands, "total": total})
}

func (h *agentCommandHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 command id")
		return
	}
	cmd, err := h.store.GetAgentCommand(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 command")
		return
	}
	writeJSON(w, http.StatusOK, cmd)
}

type TriggerBackupCommandPayload struct {
	ProjectID  int    `json:"project_id"`
	TargetType string `json:"target_type"`
	Smoke      bool   `json:"smoke,omitempty"`
}

type RestoreBackupCommandPayload struct {
	RecordID   int64  `json:"record_id"`
	RestoreID  int64  `json:"restore_id"`
	Strategy   string `json:"strategy"`
	Target     string `json:"target"`
	Confirm    string `json:"confirm,omitempty"`
}

type ScheduleCommandPayload struct {
	ScheduleID int `json:"schedule_id"`
}

type agentCommandFinishRequest struct {
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result"`
	LogOutput string          `json:"log_output"`
	LogRef    string          `json:"log_ref"`
	ErrorMsg  string          `json:"error_msg"`
}

type agentHeartbeatResponse struct {
	Agent    *store.Agent          `json:"agent"`
	Commands []store.AgentCommand  `json:"commands"`
}
