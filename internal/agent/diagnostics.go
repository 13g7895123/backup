package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"backup-manager/internal/backup"
	"backup-manager/internal/store"
)

type ProjectReader interface {
	GetProject(ctx context.Context, id int) (*store.Project, error)
	ListTargets(ctx context.Context, projectID int) ([]store.BackupTarget, error)
}

type PreflightRequest struct {
	ProjectID int `json:"project_id"`
}

type PreflightStep struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	ErrorMsg string `json:"error_msg,omitempty"`
}

type PreflightResult struct {
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Steps      []PreflightStep `json:"steps"`
	ErrorMsg   string          `json:"error_msg,omitempty"`
}

func RunPreflight(ctx context.Context, reader ProjectReader, req PreflightRequest) (*PreflightResult, error) {
	if req.ProjectID == 0 {
		return nil, fmt.Errorf("project_id required")
	}

	result := &PreflightResult{
		Status:    "success",
		StartedAt: time.Now().UTC(),
	}
	defer func() {
		result.FinishedAt = time.Now().UTC()
	}()

	project, err := reader.GetProject(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	targets, err := reader.ListTargets(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}

	result.Steps = append(result.Steps, checkProjectPath(project, targets))
	result.Steps = append(result.Steps, checkNASWritable(project))
	result.Steps = append(result.Steps, checkDatabaseConnectivity(project, targets))

	for _, step := range result.Steps {
		if step.Status == "failed" {
			result.Status = "failed"
			result.ErrorMsg = step.ErrorMsg
			break
		}
	}
	return result, nil
}

func checkProjectPath(project *store.Project, targets []store.BackupTarget) PreflightStep {
	step := PreflightStep{Name: "check_project_path", Status: "success"}

	var paths []string
	if project.ProjectPath != "" {
		paths = append(paths, withHostPrefix(project.ProjectPath))
	}
	for _, target := range targets {
		if target.Type != "files" {
			continue
		}
		var cfg backup.FilesConfig
		if err := json.Unmarshal(target.Config, &cfg); err != nil {
			return PreflightStep{Name: "check_project_path", Status: "failed", ErrorMsg: "無法解析 files target config: " + err.Error()}
		}
		if cfg.Source != "" {
			paths = append(paths, withHostPrefix(cfg.Source))
		}
	}

	if len(paths) == 0 {
		step.Status = "skipped"
		step.Detail = "沒有需要檢查的 project/files 路徑"
		return step
	}

	seen := map[string]struct{}{}
	checked := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		info, err := os.Stat(path)
		if err != nil {
			return PreflightStep{Name: "check_project_path", Status: "failed", ErrorMsg: fmt.Sprintf("路徑不存在或不可讀: %s (%v)", path, err)}
		}
		if !info.IsDir() {
			return PreflightStep{Name: "check_project_path", Status: "failed", ErrorMsg: fmt.Sprintf("路徑不是目錄: %s", path)}
		}
		checked = append(checked, path)
	}
	step.Detail = "checked: " + strings.Join(checked, ", ")
	return step
}

func checkNASWritable(project *store.Project) PreflightStep {
	step := PreflightStep{Name: "check_nas_write", Status: "success"}
	root := project.NasBase
	if strings.TrimSpace(project.NasSubpath) != "" {
		root = filepath.Join(root, project.NasSubpath)
	}
	if strings.TrimSpace(root) == "" {
		return PreflightStep{Name: "check_nas_write", Status: "failed", ErrorMsg: "NAS 路徑未設定"}
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return PreflightStep{Name: "check_nas_write", Status: "failed", ErrorMsg: "建立 NAS 路徑失敗: " + err.Error()}
	}

	testDir := filepath.Join(root, "_tests", "preflight")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		return PreflightStep{Name: "check_nas_write", Status: "failed", ErrorMsg: "建立測試目錄失敗: " + err.Error()}
	}
	testFile := filepath.Join(testDir, fmt.Sprintf("preflight-%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(testFile, []byte("ok"), 0644); err != nil {
		return PreflightStep{Name: "check_nas_write", Status: "failed", ErrorMsg: "NAS 不可寫: " + err.Error()}
	}
	_ = os.Remove(testFile)
	step.Detail = "writable: " + root
	return step
}

func checkDatabaseConnectivity(project *store.Project, targets []store.BackupTarget) PreflightStep {
	cfg, ok, err := resolveDatabaseConfig(project, targets)
	if err != nil {
		return PreflightStep{Name: "check_db_connectivity", Status: "failed", ErrorMsg: err.Error()}
	}
	if !ok {
		return PreflightStep{Name: "check_db_connectivity", Status: "skipped", Detail: "沒有資料庫備份設定"}
	}

	if cfg.ContainerName != "" {
		cmd := exec.Command("docker", "inspect", cfg.ContainerName)
		if out, err := cmd.CombinedOutput(); err != nil {
			return PreflightStep{
				Name:     "check_db_connectivity",
				Status:   "failed",
				ErrorMsg: fmt.Sprintf("docker container 不存在或不可存取: %s (%s)", cfg.ContainerName, strings.TrimSpace(string(out))),
			}
		}
		return PreflightStep{Name: "check_db_connectivity", Status: "success", Detail: "docker container: " + cfg.ContainerName}
	}

	if cfg.Host == "" || cfg.Port == 0 {
		return PreflightStep{Name: "check_db_connectivity", Status: "failed", ErrorMsg: "資料庫 host/port 未設定"}
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return PreflightStep{Name: "check_db_connectivity", Status: "failed", ErrorMsg: err.Error()}
	}
	_ = conn.Close()
	return PreflightStep{Name: "check_db_connectivity", Status: "success", Detail: "tcp reachable: " + addr}
}

func resolveDatabaseConfig(project *store.Project, targets []store.BackupTarget) (*backup.DatabaseConfig, bool, error) {
	for _, target := range targets {
		if target.Type != "database" {
			continue
		}
		cfg, err := backup.ParseDatabaseConfig(target.Config)
		if err != nil {
			return nil, false, fmt.Errorf("解析 database target 失敗: %w", err)
		}
		applyProjectDBDefaults(project, cfg)
		return cfg, true, nil
	}

	if project.DockerDbContainer == "" && project.DbHost == "" {
		return nil, false, nil
	}
	cfg := &backup.DatabaseConfig{
		DBType:        project.DbType,
		Host:          project.DbHost,
		Port:          project.DbPort,
		Name:          project.DbName,
		User:          project.DbUser,
		Password:      project.DbPassword,
		PasswordEnv:   project.DbPasswordEnv,
		ContainerName: project.DockerDbContainer,
	}
	if cfg.Port == 0 {
		if cfg.DBType == "postgres" {
			cfg.Port = 5432
		} else {
			cfg.Port = 3306
		}
	}
	return cfg, true, nil
}

func applyProjectDBDefaults(project *store.Project, cfg *backup.DatabaseConfig) {
	if cfg.ContainerName == "" && cfg.Host == "" {
		cfg.ContainerName = project.DockerDbContainer
		cfg.Host = project.DbHost
		cfg.Port = project.DbPort
	}
	if cfg.DBType == "" {
		cfg.DBType = project.DbType
	}
	if cfg.Name == "" {
		cfg.Name = project.DbName
	}
	if cfg.User == "" {
		cfg.User = project.DbUser
	}
	if cfg.Password == "" {
		cfg.Password = project.DbPassword
	}
	if cfg.PasswordEnv == "" {
		cfg.PasswordEnv = project.DbPasswordEnv
	}
	if cfg.Port == 0 {
		if cfg.DBType == "postgres" {
			cfg.Port = 5432
		} else {
			cfg.Port = 3306
		}
	}
}

func withHostPrefix(path string) string {
	hostPrefix := os.Getenv("HOST_PREFIX")
	if hostPrefix == "" {
		return path
	}
	return hostPrefix + path
}
