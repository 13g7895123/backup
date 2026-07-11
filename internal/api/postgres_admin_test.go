package api

import "testing"

func TestValidDatabaseName(t *testing.T) {
	for _, name := range []string{"app", "app_staging", "App2026"} {
		if err := validDatabaseName(name); err != nil {
			t.Errorf("validDatabaseName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "1app", "app-name", "app name", "資料庫"} {
		if err := validDatabaseName(name); err == nil {
			t.Errorf("validDatabaseName(%q) expected error", name)
		}
	}
}

func TestDashboardPostgresConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://backup:p%40ss@postgres:5433/backup_manager?sslmode=disable")
	cfg, err := dashboardPostgresConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "postgres" || cfg.Port != 5433 || cfg.User != "backup" || cfg.Password != "p@ss" || cfg.Name != "backup_manager" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
