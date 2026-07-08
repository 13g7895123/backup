package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type RestoreOptions struct {
	Strategy string
	Target   string
}

func RestoreFiles(archivePath string, opts RestoreOptions) error {
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		return fmt.Errorf("restore target required")
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(filepath.Clean(hdr.Name), string(filepath.Separator))
		dest := filepath.Join(cleanTarget, name)
		if !strings.HasPrefix(dest, cleanTarget+string(filepath.Separator)) && dest != cleanTarget {
			return fmt.Errorf("archive path escapes target: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			os.Remove(dest) //nolint
			if err := os.Symlink(hdr.Linkname, dest); err != nil {
				return err
			}
		}
	}
}

func RestoreDatabase(archivePath string, cfg *DatabaseConfig, opts RestoreOptions) error {
	if cfg == nil {
		return fmt.Errorf("database config required")
	}
	targetName := strings.TrimSpace(opts.Target)
	restoreCfg := *cfg
	if targetName != "" {
		restoreCfg.Name = targetName
	}
	if restoreCfg.Name == "" {
		return fmt.Errorf("database name required")
	}
	if opts.Strategy == "new" && targetName != "" {
		if err := CreateDatabase(&restoreCfg); err != nil {
			return err
		}
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	password := restoreCfg.Password
	if password == "" && restoreCfg.PasswordEnv != "" {
		password = os.Getenv(restoreCfg.PasswordEnv)
	}

	switch {
	case restoreCfg.ContainerName != "":
		return restoreDatabaseViaDocker(gz, &restoreCfg, password)
	case restoreCfg.DBType == "postgres":
		return restorePostgres(gz, &restoreCfg, password)
	case restoreCfg.DBType == "mysql":
		return restoreMySQL(gz, &restoreCfg, password)
	default:
		return fmt.Errorf("不支援的資料庫類型: %s", restoreCfg.DBType)
	}
}

func SnapshotFiles(source, snapshotDir, label string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("snapshot source required")
	}
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("%s_files_snapshot_%s.tar.gz", safeSnapshotLabel(label), time.Now().Format("20060102_150405"))
	path := filepath.Join(snapshotDir, filename)
	_, _, err := BackupFiles(FilesConfig{Source: source, Compress: "gzip"}, path)
	if err != nil {
		return "", err
	}
	return path, nil
}

func SnapshotDatabase(cfg *DatabaseConfig, snapshotDir, label string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("database config required")
	}
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("%s_database_snapshot_%s.sql.gz", safeSnapshotLabel(label), time.Now().Format("20060102_150405"))
	path := filepath.Join(snapshotDir, filename)
	_, _, err := BackupDatabase(cfg, path)
	if err != nil {
		return "", err
	}
	return path, nil
}

func CreateDatabase(cfg *DatabaseConfig) error {
	if cfg == nil {
		return fmt.Errorf("database config required")
	}
	password := cfg.Password
	if password == "" && cfg.PasswordEnv != "" {
		password = os.Getenv(cfg.PasswordEnv)
	}
	switch {
	case cfg.ContainerName != "":
		return createDatabaseViaDocker(cfg, password)
	case cfg.DBType == "postgres":
		return createPostgresDatabase(cfg, password)
	case cfg.DBType == "mysql":
		return createMySQLDatabase(cfg, password)
	default:
		return fmt.Errorf("不支援的資料庫類型: %s", cfg.DBType)
	}
}

func createDatabaseViaDocker(cfg *DatabaseConfig, password string) error {
	switch cfg.DBType {
	case "postgres":
		args := []string{"exec"}
		if password != "" {
			args = append(args, "-e", "PGPASSWORD="+password)
		}
		args = append(args, cfg.ContainerName, "createdb", "-U", cfg.User, cfg.Name)
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("createdb failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "mysql":
		args := []string{"exec", cfg.ContainerName, "mysql", "--user=" + cfg.User}
		if password != "" {
			args = append(args, "--password="+password)
		}
		args = append(args, "-e", "CREATE DATABASE `"+strings.ReplaceAll(cfg.Name, "`", "``")+"`;")
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("mysql create database failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	default:
		return fmt.Errorf("docker create database 不支援的資料庫類型: %s", cfg.DBType)
	}
}

func createPostgresDatabase(cfg *DatabaseConfig, password string) error {
	args := []string{"-h", cfg.Host, "-p", fmt.Sprintf("%d", cfg.Port), "-U", cfg.User, cfg.Name}
	cmd := exec.Command("createdb", args...)
	if password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+password)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("createdb failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func createMySQLDatabase(cfg *DatabaseConfig, password string) error {
	args := []string{fmt.Sprintf("--host=%s", cfg.Host), fmt.Sprintf("--port=%d", cfg.Port), "--user=" + cfg.User}
	if password != "" {
		args = append(args, "--password="+password)
	}
	args = append(args, "-e", "CREATE DATABASE `"+strings.ReplaceAll(cfg.Name, "`", "``")+"`;")
	out, err := exec.Command("mysql", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mysql create database failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func safeSnapshotLabel(label string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && b.String()[b.Len()-1] != '_' {
				b.WriteByte('_')
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "restore"
	}
	return out
}

func restoreDatabaseViaDocker(r io.Reader, cfg *DatabaseConfig, password string) error {
	var args []string
	switch cfg.DBType {
	case "postgres":
		args = []string{"exec", "-i"}
		if password != "" {
			args = append(args, "-e", "PGPASSWORD="+password)
		}
		args = append(args, cfg.ContainerName, "psql", "-U", cfg.User, "-d", cfg.Name, "-v", "ON_ERROR_STOP=1")
	case "mysql":
		args = []string{"exec", "-i", cfg.ContainerName, "mysql", "--user=" + cfg.User}
		if password != "" {
			args = append(args, "--password="+password)
		}
		args = append(args, cfg.Name)
	default:
		return fmt.Errorf("docker exec restore 不支援的資料庫類型: %s", cfg.DBType)
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdin = r
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restore docker exec failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func restorePostgres(r io.Reader, cfg *DatabaseConfig, password string) error {
	args := []string{"-h", cfg.Host, "-p", fmt.Sprintf("%d", cfg.Port), "-U", cfg.User, "-d", cfg.Name, "-v", "ON_ERROR_STOP=1"}
	cmd := exec.Command("psql", args...)
	if password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+password)
	}
	cmd.Stdin = r
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql restore failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func restoreMySQL(r io.Reader, cfg *DatabaseConfig, password string) error {
	args := []string{fmt.Sprintf("--host=%s", cfg.Host), fmt.Sprintf("--port=%d", cfg.Port), "--user=" + cfg.User}
	if password != "" {
		args = append(args, "--password="+password)
	}
	args = append(args, cfg.Name)
	cmd := exec.Command("mysql", args...)
	cmd.Stdin = r
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mysql restore failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
