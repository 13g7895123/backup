package backup

import (
	"io"
	"strings"
	"testing"
)

func TestPostgresCompatibleRestoreReader(t *testing.T) {
	input := "SET statement_timeout = 0;\nSET transaction_timeout = 0;\nCREATE TABLE example (id integer);\n"
	want := "SET statement_timeout = 0;\nCREATE TABLE example (id integer);\n"
	got, err := io.ReadAll(postgresCompatibleRestoreReader(strings.NewReader(input), "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("filtered dump = %q, want %q", got, want)
	}
}

func TestPostgresCompatibleRestoreReaderLeavesMySQLUntouched(t *testing.T) {
	input := "SET transaction_timeout = 0;\n"
	got, err := io.ReadAll(postgresCompatibleRestoreReader(strings.NewReader(input), "mysql"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != input {
		t.Fatalf("mysql input changed: %q", got)
	}
}
