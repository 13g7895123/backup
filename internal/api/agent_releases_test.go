package api

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectReleaseCapabilityAvailable(t *testing.T) {
	workdir := t.TempDir()
	scriptDir := filepath.Join(workdir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptDir, "build-agent.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENT_BUILD_WORKDIR", workdir)
	t.Setenv("AGENT_BUILD_SCRIPT", filepath.Join("scripts", "build-agent.sh"))

	cap := detectReleaseCapability()
	if !cap.Available {
		t.Fatalf("expected capability available, got unavailable: %+v", cap)
	}
	if cap.Workdir != workdir {
		t.Fatalf("unexpected workdir: %s", cap.Workdir)
	}
	if cap.Script != scriptPath {
		t.Fatalf("unexpected script path: %s", cap.Script)
	}
}

func TestDetectReleaseCapabilityMissingScript(t *testing.T) {
	workdir := t.TempDir()
	t.Setenv("AGENT_BUILD_WORKDIR", workdir)
	t.Setenv("AGENT_BUILD_SCRIPT", filepath.Join("scripts", "build-agent.sh"))

	cap := detectReleaseCapability()
	if cap.Available {
		t.Fatalf("expected capability unavailable, got available: %+v", cap)
	}
	if !strings.Contains(cap.Reason, "build script 不存在") {
		t.Fatalf("unexpected reason: %s", cap.Reason)
	}
}

func TestDetectReleaseCapabilityMissingWorkdir(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "missing")
	t.Setenv("AGENT_BUILD_WORKDIR", workdir)
	t.Setenv("AGENT_BUILD_SCRIPT", filepath.Join("scripts", "build-agent.sh"))

	cap := detectReleaseCapability()
	if cap.Available {
		t.Fatalf("expected capability unavailable, got available: %+v", cap)
	}
	if !strings.Contains(cap.Reason, "找不到 build workspace") {
		t.Fatalf("unexpected reason: %s", cap.Reason)
	}
}

func TestBuildReleaseCleansUpOnFailure(t *testing.T) {
	workdir := t.TempDir()
	scriptDir := filepath.Join(workdir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptDir, "build-agent.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\nprintf 'ok\\n' > /dev/null\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "backup-agent"), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "install-agent.sh"), []byte("#!/usr/bin/env bash\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "backup-agent.service"), []byte("[Service]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENT_BUILD_WORKDIR", workdir)
	t.Setenv("AGENT_BUILD_SCRIPT", filepath.Join("scripts", "build-agent.sh"))

	req := httptest.NewRequest("POST", "http://example.com/api/admin/agent-releases/build", nil)
	logger := log.New(io.Discard, "", 0)
	rootDir := t.TempDir()

	_, err := buildReleaseLocally(context.Background(), rootDir, "1.2.3", req, logger, "/tmp/build.log")
	if err == nil {
		t.Fatal("expected buildRelease to fail")
	}
	if !strings.Contains(err.Error(), "diagnose-agent.sh") {
		t.Fatalf("unexpected error: %v", err)
	}

	releaseDir := filepath.Join(rootDir, "1.2.3")
	if _, statErr := os.Stat(releaseDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected release dir to be removed, stat err=%v", statErr)
	}
}
