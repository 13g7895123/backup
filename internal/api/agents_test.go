package api

import (
	"strings"
	"testing"
)

func TestBuildDebianProcessCommand(t *testing.T) {
	rel := &agentReleaseDetail{
		agentReleaseManifest: agentReleaseManifest{
			Version: "v1.2.3",
			Files: []agentReleaseFile{
				{
					Name:        "backup-agent_v1.2.3_linux_amd64.tar.gz",
					DownloadURL: "https://dashboard.example/api/admin/agent-releases/v1.2.3/download/backup-agent_v1.2.3_linux_amd64.tar.gz",
				},
			},
		},
	}
	cfg := agentConfigPayload{
		EnvContent: "DASHBOARD_URL=http://dashboard:8080\nAGENT_CODE=vm-app-01\n",
	}

	got := buildDebianProcessCommand(rel, cfg)
	wants := []string{
		"sudo apt-get update",
		"sudo apt-get install -y curl tar ca-certificates",
		"curl -fsSLo",
		"backup-agent_v1.2.3_linux_amd64.tar.gz",
		"cat >\"$tmpenv\" <<'EOF'",
		"DASHBOARD_URL=http://dashboard:8080",
		"AGENT_CODE=vm-app-01",
		"sudo install -m 600 \"$tmpenv\" /etc/backup-agent/env",
		"sudo AGENT_BINARY_SRC=./backup-agent-linux-amd64 ./install-agent.sh",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("command missing %q\n%s", want, got)
		}
	}
}

func TestBuildAgentApplyCommand(t *testing.T) {
	env := "DASHBOARD_URL=http://dashboard:8080\nAGENT_CODE=vm-app-01\n"
	got := buildAgentApplyCommand(env)
	wants := []string{
		"tmpenv=\"$(mktemp)\"",
		"cat >\"$tmpenv\" <<'EOF'",
		"DASHBOARD_URL=http://dashboard:8080",
		"sudo install -m 600 \"$tmpenv\" /etc/backup-agent/env",
		"sudo systemctl restart backup-agent",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("apply command missing %q\n%s", want, got)
		}
	}
}
