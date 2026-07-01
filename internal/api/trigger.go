package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

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
		if err := forwardToAgent(agent.BaseURL, agent.Code, agent.TokenHash, req.ProjectID, req.TargetType); err != nil {
			writeError(w, http.StatusBadGateway, "轉發 agent 失敗: "+err.Error())
			return
		}
		log.Printf("[trigger] 已轉發至 agent=%s project_id=%d type=%s", agent.Code, req.ProjectID, req.TargetType)
		response["executor_type"] = "agent"
		response["agent_id"] = agent.ID
		response["agent_name"] = agent.Name
		response["message"] = "備份已交由 " + agent.Name + " 執行"
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
		if err := forwardToAgentEndpoint(agent.BaseURL, "/test-backup", agent.Code, agent.TokenHash, map[string]any{
			"project_id":  projectID,
			"target_type": body.TargetType,
		}); err != nil {
			writeError(w, http.StatusBadGateway, "轉發 agent 失敗: "+err.Error())
			return
		}
		response["executor_type"] = "agent"
		response["agent_id"] = agent.ID
		response["agent_name"] = agent.Name
		response["message"] = "smoke backup 已交由 " + agent.Name + " 執行"
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

// forwardToAgent 將 trigger 請求轉發給 host agent 的 HTTP server
func forwardToAgent(agentURL, agentCode, token string, projectID int, targetType string) error {
	return forwardToAgentEndpoint(agentURL, "/trigger", agentCode, token, map[string]any{
		"project_id":  projectID,
		"target_type": targetType,
	})
}

func forwardToAgentEndpoint(agentURL, path, agentCode, token string, payload map[string]any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		fmt.Sprintf("%s%s", agentURL, path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if agentCode != "" {
		req.Header.Set("X-Agent-Code", agentCode)
	}
	if token != "" {
		req.Header.Set("X-Agent-Token", token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("連線 agent 失敗: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent 回應 %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
