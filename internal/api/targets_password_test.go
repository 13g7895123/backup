package api

import (
	"encoding/json"
	"testing"
)

func TestRedactTargetPassword(t *testing.T) {
	redacted := redactTargetPassword(json.RawMessage(`{"db_type":"postgres","password":"secret"}`))
	var values map[string]any
	if err := json.Unmarshal(redacted, &values); err != nil {
		t.Fatal(err)
	}
	if values["password"] != "" {
		t.Fatal("target config exposed password")
	}
}

func TestPreserveTargetPassword(t *testing.T) {
	current := json.RawMessage(`{"db_type":"postgres","password":"","name":"new_name"}`)
	previous := json.RawMessage(`{"db_type":"postgres","password":"secret","name":"old_name"}`)
	merged := preserveTargetPassword(current, previous)
	var values map[string]any
	if err := json.Unmarshal(merged, &values); err != nil {
		t.Fatal(err)
	}
	if values["password"] != "secret" || values["name"] != "new_name" {
		t.Fatalf("unexpected merged config: %s", merged)
	}
}
