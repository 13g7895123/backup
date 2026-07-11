package backup

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// SQLDiff is a bounded, human-readable comparison of two PostgreSQL plain SQL dumps.
type SQLDiff struct {
	LeftLines     int      `json:"left_lines"`
	RightLines    int      `json:"right_lines"`
	AddedLines    int      `json:"added_lines"`
	RemovedLines  int      `json:"removed_lines"`
	AddedSample   []string `json:"added_sample"`
	RemovedSample []string `json:"removed_sample"`
	Identical     bool     `json:"identical"`
}

func ListPostgresDatabases(cfg *DatabaseConfig) ([]string, error) {
	if err := requirePostgres(cfg); err != nil {
		return nil, err
	}
	out, err := runPostgres(cfg, "postgres", `SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname;`)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func DeletePostgresDatabase(cfg *DatabaseConfig, name string) error {
	if err := requirePostgres(cfg); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "postgres" || name == "template0" || name == "template1" {
		return fmt.Errorf("不可刪除空白、postgres 或 template 資料庫")
	}
	admin := *cfg
	admin.Name = name
	password := databasePassword(&admin)
	var cmd *exec.Cmd
	if admin.ContainerName != "" {
		args := []string{"exec"}
		if password != "" {
			args = append(args, "-e", "PGPASSWORD="+password)
		}
		args = append(args, admin.ContainerName, "dropdb", "--if-exists", "--force", "-U", admin.User, name)
		cmd = exec.Command("docker", args...)
	} else {
		cmd = exec.Command("dropdb", "--if-exists", "--force", "-h", admin.Host, "-p", fmt.Sprintf("%d", admin.Port), "-U", admin.User, name)
		if password != "" {
			cmd.Env = append(cmd.Environ(), "PGPASSWORD="+password)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dropdb failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ApplyPostgresData intentionally accepts only CREATE TABLE and INSERT statements.
func ApplyPostgresData(cfg *DatabaseConfig, databaseName, sql string) error {
	if err := requirePostgres(cfg); err != nil {
		return err
	}
	if len(sql) == 0 || len(sql) > 1024*1024 {
		return fmt.Errorf("SQL 必須介於 1 byte 與 1 MiB")
	}
	if err := validateDataSQL(sql); err != nil {
		return err
	}
	target := *cfg
	if strings.TrimSpace(databaseName) != "" {
		target.Name = strings.TrimSpace(databaseName)
	}
	if target.Name == "" {
		return fmt.Errorf("database name required")
	}
	return executePostgresReader(&target, strings.NewReader("BEGIN;\n"+sql+"\nCOMMIT;\n"))
}

func CompareSQLGzipFiles(leftPath, rightPath string) (*SQLDiff, error) {
	left, err := normalizedSQLLines(leftPath)
	if err != nil {
		return nil, fmt.Errorf("讀取左側備份失敗: %w", err)
	}
	right, err := normalizedSQLLines(rightPath)
	if err != nil {
		return nil, fmt.Errorf("讀取右側備份失敗: %w", err)
	}
	removed, added := multisetDifference(left, right), multisetDifference(right, left)
	return &SQLDiff{LeftLines: len(left), RightLines: len(right), AddedLines: len(added), RemovedLines: len(removed), AddedSample: sampleLines(added, 40), RemovedSample: sampleLines(removed, 40), Identical: len(added) == 0 && len(removed) == 0}, nil
}

func requirePostgres(cfg *DatabaseConfig) error {
	if cfg == nil {
		return fmt.Errorf("database config required")
	}
	if cfg.DBType != "postgres" {
		return fmt.Errorf("此功能僅支援 PostgreSQL")
	}
	if cfg.User == "" {
		return fmt.Errorf("database user required")
	}
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	return nil
}

func databasePassword(cfg *DatabaseConfig) string {
	if cfg.Password != "" {
		return cfg.Password
	}
	if cfg.PasswordEnv != "" {
		return os.Getenv(cfg.PasswordEnv)
	}
	return ""
}

func runPostgres(cfg *DatabaseConfig, databaseName, sql string) (string, error) {
	password := databasePassword(cfg)
	var cmd *exec.Cmd
	if cfg.ContainerName != "" {
		args := []string{"exec"}
		if password != "" {
			args = append(args, "-e", "PGPASSWORD="+password)
		}
		args = append(args, cfg.ContainerName, "psql", "-U", cfg.User, "-d", databaseName, "-At", "-v", "ON_ERROR_STOP=1", "-c", sql)
		cmd = exec.Command("docker", args...)
	} else {
		cmd = exec.Command("psql", "-h", cfg.Host, "-p", fmt.Sprintf("%d", cfg.Port), "-U", cfg.User, "-d", databaseName, "-At", "-v", "ON_ERROR_STOP=1", "-c", sql)
		if password != "" {
			cmd.Env = append(cmd.Environ(), "PGPASSWORD="+password)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("psql failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func executePostgresReader(cfg *DatabaseConfig, r io.Reader) error {
	password := databasePassword(cfg)
	var cmd *exec.Cmd
	if cfg.ContainerName != "" {
		args := []string{"exec", "-i"}
		if password != "" {
			args = append(args, "-e", "PGPASSWORD="+password)
		}
		args = append(args, cfg.ContainerName, "psql", "-U", cfg.User, "-d", cfg.Name, "-v", "ON_ERROR_STOP=1")
		cmd = exec.Command("docker", args...)
	} else {
		cmd = exec.Command("psql", "-h", cfg.Host, "-p", fmt.Sprintf("%d", cfg.Port), "-U", cfg.User, "-d", cfg.Name, "-v", "ON_ERROR_STOP=1")
		if password != "" {
			cmd.Env = append(cmd.Environ(), "PGPASSWORD="+password)
		}
	}
	cmd.Stdin = r
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql data import failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func validateDataSQL(sql string) error {
	clean := strings.TrimSpace(sql)
	if strings.Contains(clean, `\`) {
		return fmt.Errorf("不允許 psql meta command")
	}
	for _, raw := range strings.Split(clean, ";") {
		stmt := strings.TrimSpace(raw)
		for strings.HasPrefix(stmt, "--") {
			if i := strings.IndexByte(stmt, '\n'); i >= 0 {
				stmt = strings.TrimSpace(stmt[i+1:])
			} else {
				stmt = ""
			}
		}
		if stmt == "" {
			continue
		}
		upper := strings.ToUpper(stmt)
		if !strings.HasPrefix(upper, "CREATE TABLE ") && !strings.HasPrefix(upper, "CREATE TABLE IF NOT EXISTS ") && !strings.HasPrefix(upper, "INSERT INTO ") {
			return fmt.Errorf("只允許 CREATE TABLE 與 INSERT INTO；拒絕語句: %.40s", stmt)
		}
	}
	return nil
}

func normalizedSQLLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	s := bufio.NewScanner(gz)
	s.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lines := make([]string, 0, 1024)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, `\restrict`) || strings.HasPrefix(line, `\unrestrict`) {
			continue
		}
		lines = append(lines, line)
	}
	return lines, s.Err()
}

func multisetDifference(a, b []string) []string {
	counts := make(map[string]int, len(b))
	for _, line := range b {
		counts[line]++
	}
	var out []string
	for _, line := range a {
		if counts[line] > 0 {
			counts[line]--
		} else {
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out
}

func sampleLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	return lines[:max]
}
