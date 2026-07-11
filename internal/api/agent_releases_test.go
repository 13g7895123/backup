package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
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

func TestImportReleaseBundleSuccess(t *testing.T) {
	rootDir := t.TempDir()
	h := &releaseHandler{rootDir: rootDir}

	bundle := makeReleaseBundle(t, "1.2.3", map[string]string{
		"backup-agent-linux-amd64":               "binary",
		"backup-agent_1.2.3_linux_amd64.tar.gz":  "tarball",
		"backup-agent_1.2.3_checksums.txt":       "checksums",
		"install-agent.sh":                       "#!/usr/bin/env bash\n",
		"diagnose-agent.sh":                      "#!/usr/bin/env bash\n",
		"backup-agent.service":                   "[Service]\n",
		"manifest.json":                          releaseManifestJSON(t, "1.2.3"),
	})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("version", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("bundle", "release.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bundle); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/admin/agent-releases/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	h.importRelease(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("unexpected status=%d body=%s", rr.Code, rr.Body.String())
	}

	var detail agentReleaseDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Version != "1.2.3" {
		t.Fatalf("unexpected version: %s", detail.Version)
	}
	if len(detail.Files) != 6 {
		t.Fatalf("unexpected files count: %d", len(detail.Files))
	}
	if detail.LogRef == "" {
		t.Fatal("expected log_ref to be populated")
	}
	if _, err := os.Stat(filepath.Join(rootDir, "1.2.3", "manifest.json")); err != nil {
		t.Fatalf("expected imported manifest: %v", err)
	}
}

func TestImportReleaseBundleMissingRequiredFile(t *testing.T) {
	rootDir := t.TempDir()
	h := &releaseHandler{rootDir: rootDir}

	bundle := makeReleaseBundle(t, "1.2.4", map[string]string{
		"backup-agent-linux-amd64":               "binary",
		"backup-agent_1.2.4_linux_amd64.tar.gz":  "tarball",
		"backup-agent_1.2.4_checksums.txt":       "checksums",
		"install-agent.sh":                       "#!/usr/bin/env bash\n",
		"backup-agent.service":                   "[Service]\n",
		"manifest.json":                          releaseManifestJSON(t, "1.2.4"),
	})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("version", "1.2.4"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("bundle", "release.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bundle); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/admin/agent-releases/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	h.importRelease(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "release 缺少必要檔案: diagnose-agent.sh") {
		t.Fatalf("unexpected body=%s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(rootDir, "1.2.4")); !os.IsNotExist(err) {
		t.Fatalf("expected release dir absent, stat err=%v", err)
	}
}

func makeReleaseBundle(t *testing.T, version string, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarDir(t, tw, version)
	for name, content := range files {
		fullName := version + "/" + name
		hdr := &tar.Header{
			Name: fullName,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if strings.HasSuffix(name, ".sh") || name == "backup-agent-linux-amd64" {
			hdr.Mode = 0755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTarDir(t *testing.T, tw *tar.Writer, name string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name + "/", Mode: 0755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
}

func releaseManifestJSON(t *testing.T, version string) string {
	t.Helper()
	manifest := agentReleaseManifest{
		Version: version,
		Files: []agentReleaseFile{
			{Name: "backup-agent-linux-amd64", OS: "linux", Arch: "amd64"},
			{Name: "backup-agent_" + version + "_linux_amd64.tar.gz", OS: "linux", Arch: "amd64"},
			{Name: "install-agent.sh"},
			{Name: "diagnose-agent.sh"},
			{Name: "backup-agent.service"},
			{Name: "backup-agent_" + version + "_checksums.txt"},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
