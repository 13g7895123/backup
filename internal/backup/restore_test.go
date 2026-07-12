package backup

import (
	"strings"
	"testing"
)

func TestReplacePostgresDatabaseGuards(t *testing.T) {
	dump := writeTestGzip(t, t.TempDir(), "dump.sql.gz", "CREATE TABLE a(id int);\n")
	postgres := &DatabaseConfig{DBType: "postgres", Name: "app_db", User: "app", Host: "localhost"}

	tests := []struct {
		name    string
		archive string
		cfg     *DatabaseConfig
		snap    string
		wantMsg string
	}{
		{"nil config", dump, nil, "/tmp/snap.sql.gz", "僅支援 PostgreSQL"},
		{"mysql rejected", dump, &DatabaseConfig{DBType: "mysql", Name: "app_db", User: "app"}, "/tmp/snap.sql.gz", "僅支援 PostgreSQL"},
		{"empty database name", dump, &DatabaseConfig{DBType: "postgres", User: "app"}, "/tmp/snap.sql.gz", "database name required"},
		{"missing snapshot", dump, postgres, "", "snapshot"},
		{"unreadable dump", "/nonexistent/dump.sql.gz", postgres, "/tmp/snap.sql.gz", ".sql.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ReplacePostgresDatabase(tt.archive, tt.cfg, tt.snap)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("error %q does not contain %q", err, tt.wantMsg)
			}
		})
	}
}
