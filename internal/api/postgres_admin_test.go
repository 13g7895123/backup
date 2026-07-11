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
