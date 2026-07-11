package backup

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateDataSQL(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		ok   bool
	}{
		{"create and insert", "CREATE TABLE IF NOT EXISTS users (id bigint); INSERT INTO users (id) VALUES (1);", true},
		{"comments", "-- seed\nINSERT INTO users (id) VALUES (2);", true},
		{"reject update", "UPDATE users SET id=2;", false},
		{"reject drop", "DROP TABLE users;", false},
		{"reject psql meta", "\\i /tmp/file.sql", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDataSQL(tt.sql)
			if (err == nil) != tt.ok {
				t.Fatalf("validateDataSQL() error = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestCompareSQLGzipFiles(t *testing.T) {
	dir := t.TempDir()
	left := writeTestGzip(t, dir, "left.sql.gz", "-- generated\nCREATE TABLE a(id int);\nINSERT INTO a VALUES (1);\n")
	right := writeTestGzip(t, dir, "right.sql.gz", "-- other timestamp\nCREATE TABLE a(id int);\nINSERT INTO a VALUES (2);\n")
	diff, err := CompareSQLGzipFiles(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Identical || diff.AddedLines != 1 || diff.RemovedLines != 1 {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	if len(diff.AddedSample) != 1 || diff.AddedSample[0] != "INSERT INTO a VALUES (2);" {
		t.Fatalf("unexpected added sample: %#v", diff.AddedSample)
	}
}

func writeTestGzip(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
