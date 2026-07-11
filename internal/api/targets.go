package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"backup-manager/internal/store"
)

type targetHandler struct{ store *store.Store }

func RegisterTargetRoutes(mux *http.ServeMux, s *store.Store) {
	h := &targetHandler{store: s}
	mux.HandleFunc("GET /api/projects/{id}/targets", h.list)
	mux.HandleFunc("GET /api/projects/{id}/targets/{tid}", h.get)
	mux.HandleFunc("POST /api/projects/{id}/targets", h.create)
	mux.HandleFunc("PUT /api/projects/{id}/targets/{tid}", h.update)
	mux.HandleFunc("DELETE /api/projects/{id}/targets/{tid}", h.delete)
}

func (h *targetHandler) get(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.Atoi(r.PathValue("tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 target id")
		return
	}
	t, err := h.store.GetTarget(r.Context(), tid)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到備份目標")
		return
	}
	writeJSON(w, http.StatusOK, targetWithoutPassword(t))
}

func (h *targetHandler) list(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	targets, err := h.store.ListTargets(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targetsWithoutPasswords(targets))
}

func (h *targetHandler) create(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	var t store.BackupTarget
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	if t.Type != "files" && t.Type != "database" && t.Type != "system" {
		writeError(w, http.StatusBadRequest, "type 必須為 files | database | system")
		return
	}
	t.ProjectID = projectID
	t.Enabled = true
	result, err := h.store.CreateTarget(r.Context(), &t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, targetWithoutPassword(result))
}

func (h *targetHandler) update(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.Atoi(r.PathValue("tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 target id")
		return
	}
	var t store.BackupTarget
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	t.ID = tid
	before, err := h.store.GetTarget(r.Context(), tid)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到備份目標")
		return
	}
	t.Config = preserveTargetPassword(t.Config, before.Config)
	if err := h.store.UpdateTarget(r.Context(), &t); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func targetWithoutPassword(target *store.BackupTarget) *store.BackupTarget {
	if target == nil {
		return nil
	}
	result := *target
	result.Config = redactTargetPassword(result.Config)
	return &result
}

func targetsWithoutPasswords(targets []store.BackupTarget) []store.BackupTarget {
	result := make([]store.BackupTarget, len(targets))
	copy(result, targets)
	for i := range result {
		result[i].Config = redactTargetPassword(result[i].Config)
	}
	return result
}

func redactTargetPassword(config json.RawMessage) json.RawMessage {
	var values map[string]any
	if json.Unmarshal(config, &values) != nil {
		return config
	}
	if _, exists := values["password"]; exists {
		values["password"] = ""
	}
	redacted, err := json.Marshal(values)
	if err != nil {
		return config
	}
	return redacted
}

func preserveTargetPassword(current, previous json.RawMessage) json.RawMessage {
	var currentValues, previousValues map[string]any
	if json.Unmarshal(current, &currentValues) != nil || json.Unmarshal(previous, &previousValues) != nil {
		return current
	}
	password, _ := currentValues["password"].(string)
	if password != "" {
		return current
	}
	if previousPassword, exists := previousValues["password"]; exists {
		currentValues["password"] = previousPassword
	}
	merged, err := json.Marshal(currentValues)
	if err != nil {
		return current
	}
	return merged
}

func (h *targetHandler) delete(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.Atoi(r.PathValue("tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 target id")
		return
	}
	if err := h.store.DeleteTarget(r.Context(), tid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
