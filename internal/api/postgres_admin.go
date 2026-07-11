package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"backup-manager/internal/backup"
	"backup-manager/internal/store"
)

type postgresAdminHandler struct{ store *store.Store }

type postgresDatabaseRequest struct {
	Name    string `json:"name"`
	Confirm string `json:"confirm"`
}

type postgresDataRequest struct {
	Database string `json:"database"`
	SQL      string `json:"sql"`
	Confirm  string `json:"confirm"`
}

type postgresCompareRequest struct {
	LeftDatabase  string `json:"left_database"`
	RightDatabase string `json:"right_database"`
}

type postgresApplyBackupRequest struct {
	RecordID int64  `json:"record_id"`
	Database string `json:"database"`
	Confirm  string `json:"confirm"`
}

type backupCompareRequest struct {
	LeftRecordID  int64 `json:"left_record_id"`
	RightRecordID int64 `json:"right_record_id"`
}

func RegisterPostgresAdminRoutes(mux *http.ServeMux, s *store.Store) {
	h := &postgresAdminHandler{store: s}
	mux.HandleFunc("GET /api/projects/{id}/postgres/databases", h.listDatabases)
	mux.HandleFunc("GET /api/projects/{id}/postgres/diagnose", h.diagnose)
	mux.HandleFunc("POST /api/projects/{id}/postgres/databases", h.createDatabase)
	mux.HandleFunc("DELETE /api/projects/{id}/postgres/databases/{name}", h.deleteDatabase)
	mux.HandleFunc("POST /api/projects/{id}/postgres/data", h.applyData)
	mux.HandleFunc("POST /api/projects/{id}/postgres/test-data", h.createTestData)
	mux.HandleFunc("POST /api/projects/{id}/postgres/compare", h.compareDatabases)
	mux.HandleFunc("POST /api/projects/{id}/postgres/backup-preview", h.previewBackupToDatabase)
	mux.HandleFunc("POST /api/projects/{id}/postgres/apply-backup", h.applyBackupToDatabase)
	mux.HandleFunc("POST /api/backups/compare", h.compareBackups)
	mux.HandleFunc("GET /api/backups/{bid}/restore-preview", h.restorePreview)
}

func (h *postgresAdminHandler) createTestData(w http.ResponseWriter, r *http.Request) {
	_, cfg, ok := h.projectConfig(w, r)
	if !ok {
		return
	}
	var req postgresDataRequest
	if err := decodeLimitedJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Database = strings.TrimSpace(req.Database)
	if err := h.requireManageableDatabase(cfg, req.Database); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tableName := "test_" + time.Now().Format("20060102_150405_000000000")
	sql := fmt.Sprintf(`CREATE TABLE %s (
    id BIGSERIAL PRIMARY KEY,
    test_text TEXT NOT NULL,
    test_number INTEGER NOT NULL,
    test_boolean BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO %s (test_text, test_number, test_boolean) VALUES
    ('測試資料 A', 100, true),
    ('測試資料 B', 200, false),
    ('測試資料 C', 300, true);`, tableName, tableName)
	if err := backup.ApplyPostgresData(cfg, req.Database, sql); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": "created", "database": req.Database, "table": tableName, "rows": 3,
		"columns": []string{"id", "test_text", "test_number", "test_boolean", "created_at"},
	})
}

func (h *postgresAdminHandler) requireManageableDatabase(cfg *backup.DatabaseConfig, database string) error {
	if err := validDatabaseName(database); err != nil {
		return err
	}
	names, err := backup.ListPostgresDatabases(cfg)
	if err != nil {
		return fmt.Errorf("無法確認測試資料庫: %w", err)
	}
	for _, name := range manageablePostgresDatabases(names, cfg.Name) {
		if name == database {
			return nil
		}
	}
	return fmt.Errorf("只能在可管理的獨立測試資料庫建立測試資料")
}

func (h *postgresAdminHandler) diagnose(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return
	}
	project, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	result := map[string]any{
		"project_id": project.ID, "project_name": project.Name,
		"executor_type": project.ExecutorType, "transfer_mode": project.TransferMode,
	}
	cfg, cfgErr := dashboardPostgresConfig()
	dbResult := map[string]any{"ok": false}
	if cfgErr != nil {
		dbResult["error"] = cfgErr.Error()
	} else {
		dbResult["host"] = cfg.Host
		dbResult["port"] = cfg.Port
		dbResult["user"] = cfg.User
		dbResult["database"] = cfg.Name
		names, err := backup.ListPostgresDatabases(cfg)
		if err != nil {
			dbResult["error"] = err.Error()
		} else {
			dbResult["ok"] = true
			dbResult["database_count"] = len(names)
		}
	}
	result["postgres"] = dbResult
	records, total, recordsErr := h.store.ListRecords(r.Context(), store.ListRecordsFilter{ProjectID: &id, Type: "database", Status: "success", Limit: 100})
	nasResult := map[string]any{"ok": false, "record_count": total}
	if recordsErr != nil {
		nasResult["error"] = recordsErr.Error()
	} else {
		readable := 0
		var unreadable []map[string]any
		for i := range records {
			path := h.dashboardRecordPath(r.Context(), project, &records[i])
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				readable++
			} else if len(unreadable) < 10 {
				msg := "path is a directory"
				if err != nil {
					msg = err.Error()
				}
				unreadable = append(unreadable, map[string]any{"record_id": records[i].ID, "filename": records[i].Filename, "path": path, "error": msg})
			}
		}
		nasResult["ok"] = readable == len(records)
		nasResult["checked_count"] = len(records)
		nasResult["readable_count"] = readable
		nasResult["unreadable_count"] = len(records) - readable
		nasResult["unreadable"] = unreadable
	}
	result["nas_backups"] = nasResult
	writeJSON(w, http.StatusOK, result)
}

func (h *postgresAdminHandler) previewBackupToDatabase(w http.ResponseWriter, r *http.Request) {
	p, cfg, req, ok := h.backupApplyRequest(w, r)
	if !ok {
		return
	}
	tmpDir, err := os.MkdirTemp("", "backup-apply-preview-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)
	targetCfg := *cfg
	targetCfg.Name = req.Database
	currentPath, err := backup.SnapshotDatabase(&targetCfg, tmpDir, "current_"+req.Database)
	if err != nil {
		writeError(w, http.StatusBadGateway, "讀取目標資料庫失敗: "+err.Error())
		return
	}
	rec, err := h.validProjectDatabaseRecord(r, p, req.RecordID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	diff, err := backup.CompareSQLGzipFiles(currentPath, rec.Path)
	if err != nil {
		writeError(w, http.StatusBadGateway, "讀取 NAS 備份失敗: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"database": req.Database, "record_id": rec.ID, "backup": filepath.Base(rec.Path), "diff": diff})
}

func (h *postgresAdminHandler) applyBackupToDatabase(w http.ResponseWriter, r *http.Request) {
	p, cfg, req, ok := h.backupApplyRequest(w, r)
	if !ok {
		return
	}
	if req.Confirm != req.Database {
		writeError(w, http.StatusBadRequest, "confirm 必須等於完整目標資料庫名稱")
		return
	}
	rec, err := h.validProjectDatabaseRecord(r, p, req.RecordID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snapshotDir := os.Getenv("DASHBOARD_RESTORE_SNAPSHOT_DIR")
	if snapshotDir == "" {
		snapshotDir = filepath.Join(os.TempDir(), "backup-dashboard-restore-snapshots")
	}
	targetCfg := *cfg
	targetCfg.Name = req.Database
	snapshotPath, err := backup.SnapshotDatabase(&targetCfg, snapshotDir, "before_apply_"+req.Database)
	if err != nil {
		writeError(w, http.StatusBadGateway, "套用前 snapshot 失敗: "+err.Error())
		return
	}
	if err := backup.DeletePostgresDatabase(&targetCfg, req.Database); err != nil {
		writeError(w, http.StatusBadGateway, "重建目標資料庫失敗: "+err.Error())
		return
	}
	if err := backup.CreateDatabase(&targetCfg); err != nil {
		writeError(w, http.StatusBadGateway, "重建目標資料庫失敗: "+err.Error())
		return
	}
	if err := backup.RestoreDatabase(rec.Path, &targetCfg, backup.RestoreOptions{Strategy: "overwrite", Target: req.Database}); err != nil {
		rollbackErr := h.rollbackDatabase(&targetCfg, snapshotPath)
		msg := "套用 NAS 備份失敗: " + err.Error()
		if rollbackErr != nil {
			msg += "; snapshot 回復也失敗: " + rollbackErr.Error()
		} else {
			msg += "; 已使用 snapshot 回復目標資料庫"
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "applied", "database": req.Database, "record_id": rec.ID, "backup": filepath.Base(rec.Path), "snapshot_path": snapshotPath})
}

func (h *postgresAdminHandler) rollbackDatabase(cfg *backup.DatabaseConfig, snapshotPath string) error {
	if err := backup.DeletePostgresDatabase(cfg, cfg.Name); err != nil {
		return err
	}
	if err := backup.CreateDatabase(cfg); err != nil {
		return err
	}
	return backup.RestoreDatabase(snapshotPath, cfg, backup.RestoreOptions{Strategy: "overwrite", Target: cfg.Name})
}

func (h *postgresAdminHandler) backupApplyRequest(w http.ResponseWriter, r *http.Request) (*store.Project, *backup.DatabaseConfig, postgresApplyBackupRequest, bool) {
	p, cfg, ok := h.projectConfig(w, r)
	if !ok {
		return nil, nil, postgresApplyBackupRequest{}, false
	}
	var req postgresApplyBackupRequest
	if err := decodeLimitedJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, nil, req, false
	}
	if req.RecordID == 0 {
		writeError(w, http.StatusBadRequest, "請選擇備份檔案")
		return nil, nil, req, false
	}
	req.Database = strings.TrimSpace(req.Database)
	if err := validDatabaseName(req.Database); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, nil, req, false
	}
	if req.Database == cfg.Name || req.Database == p.DbName || req.Database == "postgres" || req.Database == "template0" || req.Database == "template1" {
		writeError(w, http.StatusBadRequest, "只能套用到獨立測試資料庫，不可選擇專案預設或系統資料庫")
		return nil, nil, req, false
	}
	return p, cfg, req, true
}

func (h *postgresAdminHandler) validProjectDatabaseRecord(r *http.Request, project *store.Project, recordID int64) (*store.BackupRecord, error) {
	rec, err := h.store.GetRecord(r.Context(), recordID)
	if err != nil {
		return nil, fmt.Errorf("找不到備份紀錄")
	}
	if rec.ProjectID == nil || *rec.ProjectID != project.ID {
		return nil, fmt.Errorf("備份不屬於此專案")
	}
	if rec.Type != "database" || rec.Status != "success" {
		return nil, fmt.Errorf("只能套用成功的資料庫備份")
	}
	rec.Path = h.dashboardRecordPath(r.Context(), project, rec)
	return rec, nil
}

func (h *postgresAdminHandler) dashboardRecordPath(ctx context.Context, project *store.Project, rec *store.BackupRecord) string {
	if rec == nil {
		return ""
	}
	if project == nil || rec.AgentID == nil || project.NasBase == "" {
		return rec.Path
	}
	agentBase, err := h.store.GetAgentNASMountBase(ctx, *rec.AgentID, project.NasTargetID)
	if err != nil || agentBase == "" {
		return rec.Path
	}
	cleanRecord, cleanAgent := filepath.Clean(rec.Path), filepath.Clean(agentBase)
	if cleanRecord == cleanAgent {
		return filepath.Clean(project.NasBase)
	}
	prefix := cleanAgent + string(os.PathSeparator)
	if strings.HasPrefix(cleanRecord, prefix) {
		return filepath.Join(filepath.Clean(project.NasBase), strings.TrimPrefix(cleanRecord, prefix))
	}
	return rec.Path
}

func (h *postgresAdminHandler) compareDatabases(w http.ResponseWriter, r *http.Request) {
	_, cfg, ok := h.projectConfig(w, r)
	if !ok {
		return
	}
	var req postgresCompareRequest
	if err := decodeLimitedJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.LeftDatabase = strings.TrimSpace(req.LeftDatabase)
	req.RightDatabase = strings.TrimSpace(req.RightDatabase)
	if err := validDatabaseName(req.LeftDatabase); err != nil {
		writeError(w, http.StatusBadRequest, "左側資料庫名稱無效: "+err.Error())
		return
	}
	if err := validDatabaseName(req.RightDatabase); err != nil {
		writeError(w, http.StatusBadRequest, "右側資料庫名稱無效: "+err.Error())
		return
	}
	if req.LeftDatabase == req.RightDatabase {
		writeError(w, http.StatusBadRequest, "請選擇兩個不同的資料庫")
		return
	}
	tmpDir, err := os.MkdirTemp("", "backup-db-compare-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)
	leftCfg, rightCfg := *cfg, *cfg
	leftCfg.Name, rightCfg.Name = req.LeftDatabase, req.RightDatabase
	leftPath, err := backup.SnapshotDatabase(&leftCfg, tmpDir, "left_"+req.LeftDatabase)
	if err != nil {
		writeError(w, http.StatusBadGateway, "讀取左側資料庫失敗: "+err.Error())
		return
	}
	rightPath, err := backup.SnapshotDatabase(&rightCfg, tmpDir, "right_"+req.RightDatabase)
	if err != nil {
		writeError(w, http.StatusBadGateway, "讀取右側資料庫失敗: "+err.Error())
		return
	}
	diff, err := backup.CompareSQLGzipFiles(leftPath, rightPath)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"left_database": req.LeftDatabase, "right_database": req.RightDatabase, "diff": diff})
}

func (h *postgresAdminHandler) listDatabases(w http.ResponseWriter, r *http.Request) {
	_, cfg, ok := h.projectConfig(w, r)
	if !ok {
		return
	}
	names, err := backup.ListPostgresDatabases(cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"databases":       manageablePostgresDatabases(names, cfg.Name),
		"database_count":  len(manageablePostgresDatabases(names, cfg.Name)),
		"system_database": cfg.Name,
	})
}

func manageablePostgresDatabases(names []string, systemDatabase string) []string {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if name == systemDatabase || name == "postgres" || name == "template0" || name == "template1" {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func (h *postgresAdminHandler) createDatabase(w http.ResponseWriter, r *http.Request) {
	_, cfg, ok := h.projectConfig(w, r)
	if !ok {
		return
	}
	var req postgresDatabaseRequest
	if err := decodeLimitedJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := validDatabaseName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.Name = req.Name
	if err := backup.CreateDatabase(cfg); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "database": req.Name})
}

func (h *postgresAdminHandler) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	_, cfg, ok := h.projectConfig(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == cfg.Name {
		writeError(w, http.StatusBadRequest, "不可刪除 Backup Manager 系統資料庫")
		return
	}
	var req postgresDatabaseRequest
	if err := decodeLimitedJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Confirm != name {
		writeError(w, http.StatusBadRequest, "confirm 必須等於完整資料庫名稱")
		return
	}
	if err := backup.DeletePostgresDatabase(cfg, name); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "database": name})
}

func (h *postgresAdminHandler) applyData(w http.ResponseWriter, r *http.Request) {
	_, cfg, ok := h.projectConfig(w, r)
	if !ok {
		return
	}
	var req postgresDataRequest
	if err := decodeLimitedJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Confirm != "APPLY" {
		writeError(w, http.StatusBadRequest, "資料匯入需 confirm=APPLY")
		return
	}
	if err := h.requireManageableDatabase(cfg, strings.TrimSpace(req.Database)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := backup.ApplyPostgresData(cfg, req.Database, req.SQL); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied", "database": req.Database})
}

func (h *postgresAdminHandler) compareBackups(w http.ResponseWriter, r *http.Request) {
	var req backupCompareRequest
	if err := decodeLimitedJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	left, right, err := h.databaseRecords(r, req.LeftRecordID, req.RightRecordID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	diff, err := backup.CompareSQLGzipFiles(left.Path, right.Path)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"left": left, "right": right, "diff": diff})
}

func (h *postgresAdminHandler) restorePreview(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("bid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 backup id")
		return
	}
	rec, err := h.store.GetRecord(r.Context(), id)
	if err != nil || rec.ProjectID == nil || rec.TargetID == nil || rec.Type != "database" || rec.Status != "success" {
		writeError(w, http.StatusBadRequest, "只能預覽成功的資料庫備份")
		return
	}
	previous, err := h.store.GetPreviousSuccessfulRecord(r.Context(), rec)
	if err != nil {
		writeError(w, http.StatusNotFound, "這是此備份目標的第一份成功備份，沒有上次備份可比較")
		return
	}
	project, err := h.store.GetProject(r.Context(), *rec.ProjectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	rec.Path = h.dashboardRecordPath(r.Context(), project, rec)
	previous.Path = h.dashboardRecordPath(r.Context(), project, previous)
	diff, err := backup.CompareSQLGzipFiles(previous.Path, rec.Path)
	if err != nil {
		writeError(w, http.StatusBadGateway, "無法讀取備份檔案: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record_id": rec.ID, "previous_record_id": previous.ID,
		"previous": filepath.Base(previous.Path), "backup": filepath.Base(rec.Path), "diff": diff,
	})
}

func (h *postgresAdminHandler) projectConfig(w http.ResponseWriter, r *http.Request) (*store.Project, *backup.DatabaseConfig, bool) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 project id")
		return nil, nil, false
	}
	p, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return nil, nil, false
	}
	cfg, err := dashboardPostgresConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, nil, false
	}
	return p, cfg, true
}

func dashboardPostgresConfig() (*backup.DatabaseConfig, error) {
	raw := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if raw == "" {
		return nil, fmt.Errorf("DATABASE_URL 未設定，無法連線 Backup Manager PostgreSQL")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, fmt.Errorf("DATABASE_URL 不是有效的 PostgreSQL URL")
	}
	password, _ := u.User.Password()
	port := 5432
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return nil, fmt.Errorf("DATABASE_URL port 無效")
		}
	}
	name := strings.TrimPrefix(u.Path, "/")
	if u.Hostname() == "" || u.User.Username() == "" || name == "" {
		return nil, fmt.Errorf("DATABASE_URL 缺少 host、user 或 database")
	}
	return &backup.DatabaseConfig{DBType: "postgres", Host: u.Hostname(), Port: port, Name: name, User: u.User.Username(), Password: password}, nil
}

func (h *postgresAdminHandler) databaseRecords(r *http.Request, leftID, rightID int64) (*store.BackupRecord, *store.BackupRecord, error) {
	if leftID == 0 || rightID == 0 || leftID == rightID {
		return nil, nil, fmt.Errorf("請選擇兩筆不同的備份")
	}
	left, err := h.store.GetRecord(r.Context(), leftID)
	if err != nil {
		return nil, nil, fmt.Errorf("找不到左側備份")
	}
	right, err := h.store.GetRecord(r.Context(), rightID)
	if err != nil {
		return nil, nil, fmt.Errorf("找不到右側備份")
	}
	if left.Type != "database" || right.Type != "database" || left.Status != "success" || right.Status != "success" {
		return nil, nil, fmt.Errorf("只能比較成功的資料庫備份")
	}
	if left.ProjectID == nil || right.ProjectID == nil || *left.ProjectID != *right.ProjectID {
		return nil, nil, fmt.Errorf("只能比較同一專案的備份")
	}
	return left, right, nil
}

func validDatabaseName(name string) error {
	if name == "" || len(name) > 63 {
		return fmt.Errorf("資料庫名稱長度需為 1–63")
	}
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return fmt.Errorf("資料庫名稱只能使用英文字母、數字與底線，且不能以數字開頭")
		}
	}
	return nil
}

func decodeLimitedJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("無效的 JSON: %w", err)
	}
	return nil
}
