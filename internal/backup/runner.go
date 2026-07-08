package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"backup-manager/internal/notify"
	"backup-manager/internal/store"
)

// BackingStore 是 runner 需要的底層儲存介面。
// store.Store 和 client.DashboardClient 都實作此介面。
type BackingStore interface {
	GetProject(ctx context.Context, id int) (*store.Project, error)
	ListTargets(ctx context.Context, projectID int) ([]store.BackupTarget, error)
	ListRetention(ctx context.Context, projectID int) ([]store.RetentionPolicy, error)
	CreateRecord(ctx context.Context, r *store.BackupRecord) (int64, error)
	UpdateRecord(ctx context.Context, r *store.BackupRecord) error
	ListRecords(ctx context.Context, f store.ListRecordsFilter) ([]store.BackupRecord, int64, error)
	DeleteRecord(ctx context.Context, id int64) (string, error)
}

type Uploader interface {
	UploadBackup(ctx context.Context, recordID int64, filePath, checksum string) error
}

// Runner 執行備份並寫入紀錄
type Runner struct {
	Store    BackingStore
	Uploader Uploader
	Notifier *notify.Slack
}

type RunOptions struct {
	ScheduleID  *int
	TriggeredBy string
	Smoke       bool
	SmokeRunID  string
}

// RunTarget 執行單一 backup target，寫入 backup_records
func (r *Runner) RunTarget(ctx context.Context, proj *store.Project, target *store.BackupTarget, scheduleID *int, triggeredBy string) error {
	return r.RunTargetWithOptions(ctx, proj, target, RunOptions{
		ScheduleID:  scheduleID,
		TriggeredBy: triggeredBy,
	})
}

func (r *Runner) RunTargetWithOptions(ctx context.Context, proj *store.Project, target *store.BackupTarget, opts RunOptions) error {
	timestamp := time.Now().Format("20060102_150405")
	destDir := filepath.Join(projectDestRoot(proj, opts), target.Type)

	filename := fmt.Sprintf("%s_%s_%s.tar.gz", proj.Name, target.Type, timestamp)
	if target.Type == "database" {
		filename = fmt.Sprintf("%s_%s_%s.sql.gz", proj.Name, target.Type, timestamp)
	}
	if target.Type == "system" {
		filename = fmt.Sprintf("system_%s.tar.gz", timestamp)
	}

	destPath := filepath.Join(destDir, filename)
	backupDestDir := destDir
	backupDestPath := destPath
	uploadMode := strings.EqualFold(proj.TransferMode, "upload")
	if uploadMode {
		if r.Uploader == nil {
			return fmt.Errorf("project transfer_mode=upload 但 runner 未設定 uploader")
		}
		stagingRoot := os.Getenv("BACKUP_AGENT_STAGING_DIR")
		if stagingRoot == "" {
			stagingRoot = "/var/tmp/backup-agent/staging"
		}
		backupDestDir = filepath.Join(stagingRoot, fmt.Sprintf("record_%d", time.Now().UnixNano()), target.Type)
		backupDestPath = filepath.Join(backupDestDir, filename)
	}
	runID := buildRunID(proj.Name, target.Type, opts.Smoke)
	logRef, logger, closeLog, err := newRunLogger(runID)
	if err != nil {
		return fmt.Errorf("建立執行 log 失敗: %w", err)
	}
	defer closeLog()

	// 建立執行中紀錄
	rec := &store.BackupRecord{
		ProjectID:   &proj.ID,
		ProjectName: proj.Name,
		TargetID:    &target.ID,
		ScheduleID:  opts.ScheduleID,
		Type:        target.Type,
		Label:       target.Label,
		Filename:    filename,
		Path:        destPath,
		TriggeredBy: opts.TriggeredBy,
		LogRef:      logRef,
	}
	logger.Printf("run start project=%q project_id=%d target=%s smoke=%t path=%s",
		proj.Name, proj.ID, target.Type, opts.Smoke, destPath)

	recID, err := r.Store.CreateRecord(ctx, rec)
	if err != nil {
		logger.Printf("create record failed: %v", err)
		return fmt.Errorf("建立備份紀錄失敗: %w", err)
	}
	rec.ID = recID

	start := time.Now()
	var checksum string
	var size int64
	var backupErr error

	switch target.Type {
	case "files":
		logger.Printf("files backup label=%q", target.Label)
		var cfg FilesConfig
		if err := json.Unmarshal(target.Config, &cfg); err != nil {
			backupErr = fmt.Errorf("解析 files config 失敗: %w", err)
		} else {
			checksum, size, backupErr = BackupFiles(cfg, backupDestPath)
		}

	case "database":
		logger.Printf("database backup label=%q", target.Label)
		cfg, err := ParseDatabaseConfig(target.Config)
		if err != nil {
			backupErr = fmt.Errorf("解析 database config 失敗: %w", err)
		} else {
			// 若 target config 未設定連線資訊，自動套用 project-level 設定
			if cfg.ContainerName == "" && cfg.Host == "" {
				if proj.DockerDbContainer != "" {
					cfg.ContainerName = proj.DockerDbContainer
					if cfg.DBType == "" {
						cfg.DBType = proj.DbType
					}
					if cfg.Name == "" {
						cfg.Name = proj.DbName
					}
					if cfg.User == "" {
						cfg.User = proj.DbUser
					}
					if cfg.PasswordEnv == "" {
						cfg.PasswordEnv = proj.DbPasswordEnv
					}
				} else if proj.DbHost != "" {
					cfg.Host = proj.DbHost
					cfg.Port = proj.DbPort
					if cfg.DBType == "" {
						cfg.DBType = proj.DbType
					}
					if cfg.Name == "" {
						cfg.Name = proj.DbName
					}
					if cfg.User == "" {
						cfg.User = proj.DbUser
					}
					if cfg.PasswordEnv == "" {
						cfg.PasswordEnv = proj.DbPasswordEnv
					}
				}
			}
			rec.SubType = cfg.DBType
			checksum, size, backupErr = BackupDatabase(cfg, backupDestPath)
		}

	case "system":
		logger.Printf("system backup label=%q", target.Label)
		cfg, err := ParseSystemConfig(target.Config)
		if err != nil {
			backupErr = fmt.Errorf("解析 system config 失敗: %w", err)
		} else {
			rec.SubType = "debian"
			checksum, size, backupErr = BackupSystem(cfg, backupDestDir, timestamp)
		}

	default:
		backupErr = fmt.Errorf("未知備份類型: %s", target.Type)
	}

	duration := time.Since(start).Seconds()

	// 計算保留期限
	retainedUntil := computeRetainedUntil(proj.ID, target.Type, r.Store, ctx)

	// 更新紀錄
	rec.SizeBytes = size
	rec.Checksum = checksum
	rec.DurationSec = duration
	rec.RetainedUntil = retainedUntil
	if backupErr != nil {
		rec.Status = "failed"
		rec.ErrorMsg = backupErr.Error()
		logger.Printf("run failed duration_sec=%.2f err=%v", duration, backupErr)
	} else {
		if uploadMode {
			logger.Printf("upload start record_id=%d file=%s", rec.ID, backupDestPath)
			backupErr = r.Uploader.UploadBackup(ctx, rec.ID, backupDestPath, checksum)
			if removeErr := os.RemoveAll(filepath.Dir(filepath.Dir(backupDestPath))); removeErr != nil {
				logger.Printf("cleanup staging warning: %v", removeErr)
			}
		}
		if backupErr != nil {
			rec.Status = "failed"
			rec.ErrorMsg = backupErr.Error()
			logger.Printf("upload failed duration_sec=%.2f err=%v", duration, backupErr)
		} else {
			rec.Status = "success"
			logger.Printf("run success duration_sec=%.2f size_bytes=%d checksum=%s", duration, size, checksum)
		}
	}

	r.Store.UpdateRecord(ctx, rec) //nolint

	// 發送通知
	if r.Notifier != nil {
		if backupErr != nil {
			r.Notifier.SendFailure(proj.Name, target.Type, backupErr.Error())
		}
	}

	if backupErr != nil {
		return backupErr
	}

	fmt.Printf("[backup] ✓ %s/%s → %s (%.1fs, %.2f MB)\n",
		proj.Name, target.Type, filename, duration, float64(size)/1024/1024)
	return nil
}

func projectNASRoot(proj *store.Project) string {
	if proj.NasSubpath == "" {
		return proj.NasBase
	}
	return filepath.Join(proj.NasBase, proj.NasSubpath)
}

func projectDestRoot(proj *store.Project, opts RunOptions) string {
	if !opts.Smoke {
		return projectNASRoot(proj)
	}
	return filepath.Join(proj.NasBase, "_tests", projectSlug(proj.Name), opts.SmokeRunID)
}

// RunProject 執行一個專案下所有 enabled targets（依 targetTypes 篩選）
func (r *Runner) RunProject(ctx context.Context, projectID int, targetTypes []string, scheduleID *int, triggeredBy string) error {
	return r.RunProjectWithOptions(ctx, projectID, targetTypes, RunOptions{
		ScheduleID:  scheduleID,
		TriggeredBy: triggeredBy,
	})
}

func (r *Runner) RunProjectWithOptions(ctx context.Context, projectID int, targetTypes []string, opts RunOptions) error {
	proj, err := r.Store.GetProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("找不到專案 id=%d: %w", projectID, err)
	}
	if opts.TriggeredBy == "" {
		opts.TriggeredBy = "manual"
	}
	if opts.Smoke && opts.SmokeRunID == "" {
		opts.SmokeRunID = time.Now().Format("20060102_150405")
	}

	targets, err := r.Store.ListTargets(ctx, projectID)
	if err != nil {
		return err
	}

	typeSet := make(map[string]struct{})
	for _, t := range targetTypes {
		typeSet[t] = struct{}{}
	}
	all := len(typeSet) == 0 || contains(targetTypes, "all")

	var lastErr error
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		if !all {
			if _, ok := typeSet[t.Type]; !ok {
				continue
			}
		}
		t := t
		if err := r.RunTargetWithOptions(ctx, proj, &t, opts); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func projectSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
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
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "project"
	}
	return slug
}

func buildRunID(projectName, targetType string, smoke bool) string {
	prefix := "run"
	if smoke {
		prefix = "smoke"
	}
	return fmt.Sprintf("%s-%s-%s-%s",
		prefix,
		time.Now().Format("20060102-150405"),
		projectSlug(projectName),
		targetType,
	)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func computeRetainedUntil(projectID int, targetType string, s BackingStore, ctx context.Context) *time.Time {
	policies, err := s.ListRetention(ctx, projectID)
	if err != nil {
		return nil
	}
	// 優先找 targetType 精確匹配，否則找 'all'
	var keepDays int
	for _, p := range policies {
		if p.TargetType == targetType {
			keepDays = p.KeepDaily
			break
		}
		if p.TargetType == "all" {
			keepDays = p.KeepDaily
		}
	}
	if keepDays == 0 {
		keepDays = 7
	}
	t := time.Now().AddDate(0, 0, keepDays)
	return &t
}

// DeleteExpiredBackups 清除過期備份檔案與紀錄
func (r *Runner) DeleteExpiredBackups(ctx context.Context) {
	records, _, err := r.Store.ListRecords(ctx, store.ListRecordsFilter{
		Status: "success",
		Limit:  1000,
	})
	if err != nil {
		return
	}
	now := time.Now()
	for _, rec := range records {
		if rec.RetainedUntil != nil && rec.RetainedUntil.Before(now) {
			path, err := r.Store.DeleteRecord(ctx, rec.ID)
			if err == nil && path != "" {
				os.Remove(path)
				fmt.Printf("[cleanup] 刪除過期備份: %s\n", path)
			}
		}
	}
}

func newRunLogger(runID string) (string, *log.Logger, func(), error) {
	logDir := os.Getenv("BACKUP_RUN_LOG_DIR")
	if logDir == "" {
		logDir = "/var/log/backup-agent/runs"
	}
	path, file, err := createLogFile(logDir, runID)
	if err != nil {
		fallbackDir := filepath.Join(os.TempDir(), "backup-agent", "runs")
		path, file, err = createLogFile(fallbackDir, runID)
		if err != nil {
			return "", nil, nil, err
		}
	}
	logger := log.New(file, "", log.LstdFlags|log.Lmicroseconds)
	closeFn := func() {
		_ = file.Close()
	}
	return path, logger, closeFn, nil
}

func createLogFile(logDir, runID string) (string, *os.File, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return "", nil, err
	}
	path := filepath.Join(logDir, runID+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", nil, err
	}
	return path, file, nil
}
