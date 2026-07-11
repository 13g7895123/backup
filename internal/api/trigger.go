package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"

	"backup-manager/internal/backup"
	"backup-manager/internal/store"
)

type triggerHandler struct {
	store  *store.Store
	runner *backup.Runner
}

func RegisterTriggerRoute(mux *http.ServeMux, s *store.Store, r *backup.Runner) {
	h := &triggerHandler{store: s, runner: r}
	mux.HandleFunc("POST /api/backups/trigger", h.trigger)
	mux.HandleFunc("POST /api/projects/{id}/test-backup", h.testBackup)
}

type triggerRequest struct {
	ProjectID  int    `json:"project_id"`
	TargetType string `json:"target_type"` // "files" | "database" | "system" | "all"
}

func (h *triggerHandler) trigger(w http.ResponseWriter, r *http.Request) {
	var req triggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	if req.ProjectID == 0 {
		writeError(w, http.StatusBadRequest, "project_id 不可為空")
		return
	}
	if req.TargetType == "" {
		req.TargetType = "all"
	}

	project, err := h.store.GetProject(r.Context(), req.ProjectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}

	response := map[string]any{
		"status":     "triggered",
		"project_id": req.ProjectID,
		"type":       req.TargetType,
	}

	if project.ExecutorType == "agent" {
		if project.ExecutorAgentID == nil {
			writeError(w, http.StatusBadRequest, "project 未設定 executor agent")
			return
		}
		agent, err := h.store.GetAgent(r.Context(), *project.ExecutorAgentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "找不到 executor agent")
			return
		}
		if !agent.Enabled {
			writeError(w, http.StatusBadRequest, "executor agent 未啟用")
			return
		}
		payload, _ := json.Marshal(TriggerBackupCommandPayload{
			ProjectID:  req.ProjectID,
			TargetType: req.TargetType,
		})
		cmd, err := enqueueAgentCommand(r.Context(), h.store, &store.AgentCommand{
			AgentID:   agent.ID,
			ProjectID: &req.ProjectID,
			Type:      store.AgentCommandTypeTriggerBackup,
			Payload:   payload,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "建立 agent command 失敗: "+err.Error())
			return
		}
		log.Printf("[trigger] 已排入 agent command id=%d agent=%s project_id=%d type=%s", cmd.ID, agent.Code, req.ProjectID, req.TargetType)
		response["executor_type"] = "agent"
		response["agent_id"] = agent.ID
		response["agent_name"] = agent.Name
		response["command_id"] = cmd.ID
		response["message"] = "備份已排入 " + agent.Name + " 的待執行佇列"
	} else {
		h.runLocal(req)
		response["executor_type"] = "local"
		response["message"] = "備份已由本機開始執行，可至 /api/projects/" + strconv.Itoa(req.ProjectID) + "/backups 查看進度"
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *triggerHandler) runLocal(req triggerRequest) {
	go func() {
		if err := h.runner.RunProject(context.Background(), req.ProjectID, []string{req.TargetType}, nil, "manual"); err != nil {
			log.Printf("[trigger] project_id=%d type=%s 備份失敗: %v", req.ProjectID, req.TargetType, err)
		} else {
			log.Printf("[trigger] project_id=%d type=%s 備份完成", req.ProjectID, req.TargetType)
		}
	}()
}

func (h *triggerHandler) testBackup(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}

	var body struct {
		TargetType string `json:"target_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	if body.TargetType == "" {
		body.TargetType = "all"
	}

	project, err := h.store.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}

	response := map[string]any{
		"status":     "triggered",
		"project_id": projectID,
		"type":       body.TargetType,
		"mode":       "smoke-backup",
	}

	if project.ExecutorType == "agent" {
		if project.ExecutorAgentID == nil {
			writeError(w, http.StatusBadRequest, "project 未設定 executor agent")
			return
		}
		agent, err := h.store.GetAgent(r.Context(), *project.ExecutorAgentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "找不到 executor agent")
			return
		}
		if !agent.Enabled {
			writeError(w, http.StatusBadRequest, "executor agent 未啟用")
			return
		}
		payload, _ := json.Marshal(TriggerBackupCommandPayload{
			ProjectID:  projectID,
			TargetType: body.TargetType,
			Smoke:      true,
		})
		cmd, err := enqueueAgentCommand(r.Context(), h.store, &store.AgentCommand{
			AgentID:   agent.ID,
			ProjectID: &projectID,
			Type:      store.AgentCommandTypeTriggerBackup,
			Payload:   payload,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "建立 agent command 失敗: "+err.Error())
			return
		}
		response["executor_type"] = "agent"
		response["agent_id"] = agent.ID
		response["agent_name"] = agent.Name
		response["command_id"] = cmd.ID
		response["message"] = "smoke backup 已排入 " + agent.Name + " 的待執行佇列"
	} else {
		go func() {
			err := h.runner.RunProjectWithOptions(context.Background(), projectID, []string{body.TargetType}, backup.RunOptions{
				TriggeredBy: "smoke-backup",
				Smoke:       true,
			})
			if err != nil {
				log.Printf("[smoke-backup] project_id=%d type=%s 失敗: %v", projectID, body.TargetType, err)
				return
			}
			log.Printf("[smoke-backup] project_id=%d type=%s 完成", projectID, body.TargetType)
		}()
		response["executor_type"] = "local"
		response["message"] = "smoke backup 已由本機開始執行"
	}
	writeJSON(w, http.StatusAccepted, response)
}
