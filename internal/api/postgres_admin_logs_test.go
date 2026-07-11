package api

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadPostgresLogRequestReplaysBody(t *testing.T) {
	body := `{"database":"test_db","sql":"INSERT INTO t VALUES (1)","confirm":"APPLY"}`
	req := httptest.NewRequest("POST", "/api/projects/1/postgres/data", strings.NewReader(body))

	logged, truncated := readPostgresLogRequest(req)
	if truncated {
		t.Fatal("small body must not be truncated")
	}
	if logged != body {
		t.Fatalf("logged body = %q, want %q", logged, body)
	}
	replayed, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed) != body {
		t.Fatalf("replayed body = %q, want %q", replayed, body)
	}
}

func TestReadPostgresLogRequestTruncatesLogOnly(t *testing.T) {
	body := strings.Repeat("x", postgresLogBodyMax+100)
	req := httptest.NewRequest("POST", "/api/projects/1/postgres/data", strings.NewReader(body))

	logged, truncated := readPostgresLogRequest(req)
	if !truncated || len(logged) != postgresLogBodyMax {
		t.Fatalf("logged length = %d, truncated = %t", len(logged), truncated)
	}
	replayed, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed) != body {
		t.Fatal("request body was changed while logging")
	}
}
