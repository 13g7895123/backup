package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"backup-manager/internal/backup"
	"backup-manager/internal/store"
)

type restoreRequest struct {
	RecordID int64  `json:"record_id"`
	Strategy string `json:"strategy"`
	Target   string `json:"target"`
	Confirm  string `json:"confirm"`
}

type restoreHandler struct {
	store *store.Store
}

func RegisterRestoreRoute(mux *http.ServeMux, s *store.Store) {
	h := &restoreHandler{store: s}
	mux.HandleFunc("POST /api/restore", h.restore)
	mux.HandleFunc("GET /api/restores", h.list)
	mux.HandleFunc("GET /api/projects/{id}/restores", h.listByProject)
}

func (h *restoreHandler) restore(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	if req.RecordID == 0 {
		writeError(w, http.StatusBadRequest, "record_id 不可為空")
		return
	}
	if req.Strategy == "" {
		req.Strategy = "new"
	}
	if req.Strategy != "new" && req.Strategy != "overwrite" {
		writeError(w, http.StatusBadRequest, "strategy 必須為 new 或 overwrite")
		return
	}
	if req.Strategy == "overwrite" && req.Confirm != "RESTORE" {
		writeError(w, http.StatusBadRequest, "overwrite 需 confirm=RESTORE")
		return
	}
	if req.Strategy == "new" && req.Target == "" {
		writeError(w, http.StatusBadRequest, "new restore 需指定 target")
		return
	}

	rec, err := h.store.GetRecord(r.Context(), req.RecordID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到備份紀錄")
		return
	}
	if rec.ProjectID == nil {
		writeError(w, http.StatusBadRequest, "record missing project")
		return
	}
	if rec.Status != "success" {
		writeError(w, http.StatusBadRequest, "只能還原成功的備份紀錄")
		return
	}
	project, err := h.store.GetProject(r.Context(), *rec.ProjectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}

	restoreRec := &store.RestoreRecord{
		BackupRecordID: rec.ID,
		ProjectID:      rec.ProjectID,
		ProjectName:    project.Name,
		Type:           rec.Type,
		Strategy:       req.Strategy,
		Target:         req.Target,
	}

	var agent *store.Agent
	if project.ExecutorType == "agent" {
		if project.ExecutorAgentID == nil {
			writeError(w, http.StatusBadRequest, "project 未設定 executor agent")
			return
		}
		agent, err = h.store.GetAgent(r.Context(), *project.ExecutorAgentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "找不到 executor agent")
			return
		}
		if !agent.Enabled {
			writeError(w, http.StatusBadRequest, "executor agent 未啟用")
			return
		}
		restoreRec.AgentID = &agent.ID
		restoreRec.AgentName = agent.Name
		restoreRec.RunHost = agent.HostName
		restoreRec.Status = "pending"
	}
	restoreID, err := h.store.CreateRestoreRecord(r.Context(), restoreRec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "建立還原紀錄失敗: "+err.Error())
		return
	}

	var out map[string]any
	if agent != nil {
		payload, _ := json.Marshal(RestoreBackupCommandPayload{
			RecordID:  req.RecordID,
			RestoreID: restoreID,
			Strategy:  req.Strategy,
			Target:    req.Target,
			Confirm:   req.Confirm,
		})
		cmd, cmdErr := enqueueAgentCommand(r.Context(), h.store, &store.AgentCommand{
			AgentID:         agent.ID,
			ProjectID:       rec.ProjectID,
			RestoreRecordID: &restoreID,
			Type:            store.AgentCommandTypeRestoreBackup,
			Payload:         payload,
		})
		if cmdErr != nil {
			h.store.FinishRestoreRecord(r.Context(), restoreID, "failed", "", cmdErr.Error()) //nolint
			writeError(w, http.StatusInternalServerError, "建立 restore command 失敗: "+cmdErr.Error())
			return
		}
		out = map[string]any{
			"status":      "queued",
			"record_id":   req.RecordID,
			"restore_id":  restoreID,
			"command_id":  cmd.ID,
			"type":        rec.Type,
			"strategy":    req.Strategy,
			"target":      req.Target,
			"agent_id":    agent.ID,
			"agent_name":  agent.Name,
			"queued_at":   time.Now(),
			"executor":    "agent",
		}
		writeJSON(w, http.StatusAccepted, out)
		return
	} else {
		out, err = h.restoreLocal(r.Context(), rec, project, req)
	}
	if err != nil {
		h.store.FinishRestoreRecord(r.Context(), restoreID, "failed", "", err.Error()) //nolint
		writeError(w, http.StatusBadGateway, "restore 失敗: "+err.Error())
		return
	}
	snapshotPath, _ := out["snapshot_path"].(string)
	if err := h.store.FinishRestoreRecord(r.Context(), restoreID, "success", snapshotPath, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "更新還原紀錄失敗: "+err.Error())
		return
	}
	out["restore_id"] = restoreID
	writeJSON(w, http.StatusOK, out)
}

func (h *restoreHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	records, total, err := h.store.ListRestoreRecords(r.Context(), nil, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records, "total": total})
}

func (h *restoreHandler) listByProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	records, total, err := h.store.ListRestoreRecords(r.Context(), &projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records, "total": total})
}

func (h *restoreHandler) restoreLocal(ctx context.Context, rec *store.BackupRecord, project *store.Project, req restoreRequest) (map[string]any, error) {
	if rec.TargetID == nil {
		return nil, fmt.Errorf("record missing target")
	}
	target, err := h.store.GetTarget(ctx, *rec.TargetID)
	if err != nil {
		return nil, fmt.Errorf("backup target not found: %w", err)
	}
	restoreTarget := req.Target
	snapshotPath := ""
	snapshotDir := os.Getenv("DASHBOARD_RESTORE_SNAPSHOT_DIR")
	if snapshotDir == "" {
		snapshotDir = filepath.Join(os.TempDir(), "backup-dashboard-restore-snapshots")
	}
	switch rec.Type {
	case "files", "system":
		if restoreTarget == "" && req.Strategy == "overwrite" && rec.Type == "files" {
			var cfg backup.FilesConfig
			if err := json.Unmarshal(target.Config, &cfg); err != nil {
				return nil, err
			}
			restoreTarget = cfg.Source
		}
		if restoreTarget == "" && req.Strategy == "overwrite" && rec.Type == "system" {
			restoreTarget = "/"
		}
		if req.Strategy == "overwrite" {
			path, err := backup.SnapshotFiles(restoreTarget, snapshotDir, project.Name)
			if err != nil {
				return nil, fmt.Errorf("overwrite 前 snapshot 失敗: %w", err)
			}
			snapshotPath = path
		}
		if err := backup.RestoreFiles(rec.Path, backup.RestoreOptions{Strategy: req.Strategy, Target: restoreTarget}); err != nil {
			return nil, err
		}
	case "database":
		cfg, err := backup.ParseDatabaseConfig(target.Config)
		if err != nil {
			return nil, err
		}
		applyProjectDatabaseDefaults(project, cfg)
		restoreCfg := *cfg
		if restoreTarget != "" {
			restoreCfg.Name = restoreTarget
		}
		if req.Strategy == "overwrite" {
			path, err := backup.SnapshotDatabase(&restoreCfg, snapshotDir, project.Name)
			if err != nil {
				return nil, fmt.Errorf("overwrite 前 DB snapshot 失敗: %w", err)
			}
			snapshotPath = path
		}
		if req.Strategy == "overwrite" && restoreCfg.DBType == "postgres" {
			// 仿 apply-backup:先重建目標資料庫再灌入 dump,
			// 避免 plain dump 撞到既有資料表;失敗時以 snapshot 回復。
			if _, _, err := backup.ReplacePostgresDatabase(rec.Path, &restoreCfg, snapshotPath); err != nil {
				return nil, err
			}
		} else if err := backup.RestoreDatabase(rec.Path, cfg, backup.RestoreOptions{Strategy: req.Strategy, Target: restoreTarget}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported restore type: %s", rec.Type)
	}
	return map[string]any{
		"status":        "restored",
		"record_id":     rec.ID,
		"type":          rec.Type,
		"strategy":      req.Strategy,
		"target":        restoreTarget,
		"snapshot_path": snapshotPath,
	}, nil
}

func applyProjectDatabaseDefaults(project *store.Project, cfg *backup.DatabaseConfig) {
	if cfg.ContainerName == "" && cfg.Host == "" {
		if project.DockerDbContainer != "" {
			cfg.ContainerName = project.DockerDbContainer
			if cfg.DBType == "" {
				cfg.DBType = project.DbType
			}
			if cfg.Name == "" {
				cfg.Name = project.DbName
			}
			if cfg.User == "" {
				cfg.User = project.DbUser
			}
			if cfg.PasswordEnv == "" {
				cfg.PasswordEnv = project.DbPasswordEnv
			}
			if cfg.Password == "" {
				cfg.Password = project.DbPassword
			}
		} else if project.DbHost != "" {
			cfg.Host = project.DbHost
			cfg.Port = project.DbPort
			if cfg.DBType == "" {
				cfg.DBType = project.DbType
			}
			if cfg.Name == "" {
				cfg.Name = project.DbName
			}
			if cfg.User == "" {
				cfg.User = project.DbUser
			}
			if cfg.PasswordEnv == "" {
				cfg.PasswordEnv = project.DbPasswordEnv
			}
			if cfg.Password == "" {
				cfg.Password = project.DbPassword
			}
		}
	}
}
