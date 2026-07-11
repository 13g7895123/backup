package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"backup-manager/internal/agent"
	"backup-manager/internal/store"
)

type agentsHandler struct{ store *store.Store }

func RegisterAgentsRoutes(mux *http.ServeMux, s *store.Store) {
	h := &agentsHandler{store: s}
	mux.HandleFunc("GET /api/agents", h.list)
	mux.HandleFunc("POST /api/agents", h.create)
	mux.HandleFunc("GET /api/agents/{id}", h.get)
	mux.HandleFunc("PUT /api/agents/{id}", h.update)
	mux.HandleFunc("GET /api/agents/{id}/config", h.getConfig)
	mux.HandleFunc("POST /api/agents/{id}/config", h.updateConfig)
	mux.HandleFunc("GET /api/agents/{id}/installer", h.getInstaller)
	mux.HandleFunc("GET /api/agents/{id}/upgrade-script", h.getUpgradeScript)
	mux.HandleFunc("GET /api/nas-targets", h.listNASTargets)
	mux.HandleFunc("POST /api/agents/{id}/test/preflight", h.preflightTest)
}

type agentConfigPayload struct {
	SaveMode        string `json:"save_mode,omitempty"`
	Name            string `json:"name"`
	Code            string `json:"code"`
	BaseURL         string `json:"base_url"`
	Token           string `json:"token"`
	DashboardURL    string `json:"dashboard_url"`
	AgentAddr       string `json:"agent_addr"`
	HostPrefix      string `json:"host_prefix"`
	NASBase         string `json:"nas_base"`
	BuildWorkdir    string `json:"build_workdir"`
	BuildScript     string `json:"build_script"`
	ReleasesDir     string `json:"releases_dir"`
	SlackWebhookURL string `json:"slack_webhook_url"`
	EnvContent      string `json:"env_content,omitempty"`
	ApplyCommand    string `json:"apply_command,omitempty"`
	RestartCommand  string `json:"restart_command,omitempty"`
	TokenPreview    string `json:"token_preview,omitempty"`
	CreatedAgent    any    `json:"created_agent,omitempty"`
	UpdatedAgent    any    `json:"updated_agent,omitempty"`
}

type agentInstallPayload struct {
	Agent            *store.Agent        `json:"agent"`
	AgentConfig      agentConfigPayload  `json:"agent_config"`
	Release          *agentReleaseDetail `json:"release,omitempty"`
	CurrentVersion   string              `json:"current_version,omitempty"`
	LatestVersion    string              `json:"latest_version,omitempty"`
	SelectedVersion  string              `json:"selected_version,omitempty"`
	VersionState     string              `json:"version_state,omitempty"`
	VersionNote      string              `json:"version_note,omitempty"`
	InstallCommand   string              `json:"install_command,omitempty"`
	ProcessCommand   string              `json:"process_command,omitempty"`
	ApplyCommand     string              `json:"apply_command,omitempty"`
	RestartCommand   string              `json:"restart_command,omitempty"`
	DownloadURL      string              `json:"download_url,omitempty"`
	ChecksumURL      string              `json:"checksum_url,omitempty"`
	InstallScriptURL string              `json:"install_script_url,omitempty"`
	UpgradeScriptURL string              `json:"upgrade_script_url,omitempty"`
	UpgradeScript    string              `json:"upgrade_script,omitempty"`
	UpgradeCommand   string              `json:"upgrade_command,omitempty"`
	ConfigContent    string              `json:"config_content,omitempty"`
	InstallScript    string              `json:"install_script,omitempty"`
	ServiceContent   string              `json:"service_content,omitempty"`
	DiagnoseScript   string              `json:"diagnose_script,omitempty"`
}

type agentListItem struct {
	store.Agent
	LatestReleaseVersion string `json:"latest_release_version,omitempty"`
	VersionState         string `json:"version_state,omitempty"`
	VersionNote          string `json:"version_note,omitempty"`
}

func (h *agentsHandler) list(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	release, _ := latestAgentReleaseDetail(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), r)
	items := make([]agentListItem, 0, len(agents))
	for i := range agents {
		state, note := agentVersionState(&agents[i], release)
		items = append(items, agentListItem{
			Agent:                agents[i],
			LatestReleaseVersion: releaseVersionOrEmpty(release),
			VersionState:         state,
			VersionNote:          note,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *agentsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	agent, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent")
		return
	}
	release, _ := latestAgentReleaseDetail(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), r)
	state, note := agentVersionState(agent, release)
	writeJSON(w, http.StatusOK, agentListItem{
		Agent:                *agent,
		LatestReleaseVersion: releaseVersionOrEmpty(release),
		VersionState:         state,
		VersionNote:          note,
	})
}

func (h *agentsHandler) create(w http.ResponseWriter, r *http.Request) {
	var a store.Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(a.Code) == "" || strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.BaseURL) == "" || strings.TrimSpace(a.TokenHash) == "" {
		writeError(w, http.StatusBadRequest, "code, name, base_url, token_hash 不可為空")
		return
	}
	if a.Status == "" {
		a.Status = "offline"
	}
	a.Enabled = true
	created, err := h.store.CreateAgent(r.Context(), &a)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *agentsHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	current, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent")
		return
	}
	var a store.Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	a.ID = id
	if strings.TrimSpace(a.TokenHash) == "" {
		a.TokenHash = current.TokenHash
	}
	if a.Status == "" {
		a.Status = current.Status
	}
	if a.HostName == "" {
		a.HostName = current.HostName
	}
	if a.IPAddress == "" {
		a.IPAddress = current.IPAddress
	}
	if a.Version == "" {
		a.Version = current.Version
	}
	if a.LastError == "" {
		a.LastError = current.LastError
	}
	if err := h.store.UpdateAgent(r.Context(), &a); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *agentsHandler) listNASTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := h.store.ListNASTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targets)
}

func (h *agentsHandler) getConfig(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	agentRow, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent")
		return
	}
	cfg := buildAgentConfigPayload(agentRow, nil, r)
	writeJSON(w, http.StatusOK, cfg)
}

func (h *agentsHandler) updateConfig(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	current, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent")
		return
	}

	var body agentConfigPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}

	mode := strings.TrimSpace(body.SaveMode)
	if mode == "" {
		mode = "create_new"
	}

	target := *current
	target.Name = strings.TrimSpace(body.Name)
	target.Code = strings.TrimSpace(body.Code)
	target.BaseURL = strings.TrimSpace(body.BaseURL)
	target.TokenHash = body.Token

	if strings.TrimSpace(target.Name) == "" || strings.TrimSpace(target.Code) == "" || strings.TrimSpace(target.BaseURL) == "" || strings.TrimSpace(target.TokenHash) == "" {
		writeError(w, http.StatusBadRequest, "name, code, base_url, token 不可為空")
		return
	}

	resp := buildAgentConfigPayload(&target, &body, r)
	resp.SaveMode = mode
	switch mode {
	case "update_existing":
		target.ID = current.ID
		target.Enabled = current.Enabled
		target.Status = current.Status
		target.HostName = current.HostName
		target.IPAddress = current.IPAddress
		target.Version = current.Version
		target.LastSeenAt = current.LastSeenAt
		target.LastError = current.LastError
		target.CreatedAt = current.CreatedAt
		target.UpdatedAt = current.UpdatedAt

		if err := h.store.UpdateAgent(r.Context(), &target); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp.UpdatedAgent = target
	case "create_new":
		if target.Code == current.Code {
			writeError(w, http.StatusBadRequest, "建立新 agent 時，code 不可與目前 dashboard agent 相同")
			return
		}
		target.ID = 0
		target.Enabled = true
		target.Status = "offline"
		target.HostName = ""
		target.IPAddress = ""
		target.Version = ""
		target.LastSeenAt = nil
		target.LastError = ""
		target.CreatedAt = time.Time{}
		target.UpdatedAt = time.Time{}

		created, err := h.store.CreateAgent(r.Context(), &target)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp.CreatedAgent = created
	default:
		writeError(w, http.StatusBadRequest, "未知的 save_mode")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *agentsHandler) getInstaller(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	agentRow, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent")
		return
	}

	cfg := buildAgentConfigPayload(agentRow, nil, r)
	payload := agentInstallPayload{
		Agent:          agentRow,
		AgentConfig:    cfg,
		CurrentVersion: strings.TrimSpace(agentRow.Version),
		ApplyCommand:   cfg.ApplyCommand,
		RestartCommand: cfg.RestartCommand,
		ConfigContent:  cfg.EnvContent,
	}

	release, err := resolveAgentReleaseDetail(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), strings.TrimSpace(r.URL.Query().Get("version")), r)
	if err != nil {
		payload.VersionState = "no_release"
		payload.VersionNote = "尚未建立任何 agent release"
		writeJSON(w, http.StatusOK, payload)
		return
	}
	payload.Release = release
	payload.LatestVersion = latestAgentReleaseVersion(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"))
	payload.SelectedVersion = release.Version
	payload.VersionState, payload.VersionNote = agentVersionState(agentRow, release)
	payload.ProcessCommand = buildDebianProcessCommand(release, cfg)
	payload.InstallCommand = payload.ProcessCommand
	payload.DownloadURL = releaseAssetURL(release, ".tar.gz")
	payload.ChecksumURL = releaseAssetURL(release, "_checksums.txt")
	payload.InstallScriptURL = releaseAssetURL(release, "install-agent.sh")
	payload.UpgradeScriptURL = fmt.Sprintf("%s/api/agents/%d/upgrade-script?version=%s", baseURL(r), agentRow.ID, release.Version)
	payload.UpgradeScript = buildDebianUpgradeScript(release, agentRow)
	payload.UpgradeCommand = buildUpgradeScriptCommand(payload.UpgradeScriptURL)
	payload.InstallScript = readReleaseTextAsset(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), release.Version, "install-agent.sh")
	payload.ServiceContent = readReleaseTextAsset(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), release.Version, "backup-agent.service")
	payload.DiagnoseScript = readReleaseTextAsset(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), release.Version, "diagnose-agent.sh")
	writeJSON(w, http.StatusOK, payload)
}

func (h *agentsHandler) getUpgradeScript(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	agentRow, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent")
		return
	}
	release, err := resolveAgentReleaseDetail(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), strings.TrimSpace(r.URL.Query().Get("version")), r)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent release")
		return
	}
	fileName := fmt.Sprintf("upgrade-agent-%s-%s.sh", sanitizeReleaseToken(agentRow.Code), sanitizeReleaseToken(release.Version))
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	_, _ = io.WriteString(w, buildDebianUpgradeScript(release, agentRow))
}

func (h *agentsHandler) preflightTest(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "無效的 id")
		return
	}
	agentRow, err := h.store.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到 agent")
		return
	}
	if !agentRow.Enabled {
		writeError(w, http.StatusBadRequest, "agent 未啟用")
		return
	}

	var req agent.PreflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "無效的 JSON: "+err.Error())
		return
	}
	if req.ProjectID == 0 {
		writeError(w, http.StatusBadRequest, "project_id 不可為空")
		return
	}

	project, err := h.store.GetProject(r.Context(), req.ProjectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到專案")
		return
	}
	if project.ExecutorType == "agent" && project.ExecutorAgentID != nil && *project.ExecutorAgentID != agentRow.ID {
		writeError(w, http.StatusBadRequest, "該專案未指派給此 agent")
		return
	}

	result, err := forwardAgentPreflight(agentRow.BaseURL, agentRow.Code, agentRow.TokenHash, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "preflight 失敗: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func forwardAgentPreflight(agentURL, agentCode, token string, reqBody agent.PreflightRequest) (*agent.PreflightResult, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(context.Background(), "POST", fmt.Sprintf("%s/test/preflight", agentURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if agentCode != "" {
		req.Header.Set("X-Agent-Code", agentCode)
	}
	if token != "" {
		req.Header.Set("X-Agent-Token", token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent 回應 %d: %s", resp.StatusCode, string(b))
	}
	var result agent.PreflightResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func buildAgentConfigPayload(agentRow *store.Agent, override *agentConfigPayload, r *http.Request) agentConfigPayload {
	payload := agentConfigPayload{
		Name:            agentRow.Name,
		Code:            agentRow.Code,
		BaseURL:         agentRow.BaseURL,
		Token:           agentRow.TokenHash,
		DashboardURL:    baseURL(r),
		AgentAddr:       ":9090",
		HostPrefix:      "",
		NASBase:         "/mnt/nas/backups",
		BuildWorkdir:    "",
		BuildScript:     "scripts/build-agent.sh",
		ReleasesDir:     "/var/lib/backup-agent/releases",
		SlackWebhookURL: "",
	}
	if override != nil {
		if strings.TrimSpace(override.Name) != "" {
			payload.Name = strings.TrimSpace(override.Name)
		}
		if strings.TrimSpace(override.Code) != "" {
			payload.Code = strings.TrimSpace(override.Code)
		}
		if strings.TrimSpace(override.BaseURL) != "" {
			payload.BaseURL = strings.TrimSpace(override.BaseURL)
		}
		if strings.TrimSpace(override.Token) != "" {
			payload.Token = override.Token
		}
		if strings.TrimSpace(override.DashboardURL) != "" {
			payload.DashboardURL = strings.TrimSpace(override.DashboardURL)
		}
		if override.AgentAddr != "" {
			payload.AgentAddr = strings.TrimSpace(override.AgentAddr)
		}
		if override.HostPrefix != "" {
			payload.HostPrefix = override.HostPrefix
		}
		if override.NASBase != "" {
			payload.NASBase = strings.TrimSpace(override.NASBase)
		}
		if override.BuildWorkdir != "" {
			payload.BuildWorkdir = strings.TrimSpace(override.BuildWorkdir)
		}
		if override.BuildScript != "" {
			payload.BuildScript = strings.TrimSpace(override.BuildScript)
		}
		if override.ReleasesDir != "" {
			payload.ReleasesDir = strings.TrimSpace(override.ReleasesDir)
		}
		if override.SlackWebhookURL != "" {
			payload.SlackWebhookURL = strings.TrimSpace(override.SlackWebhookURL)
		}
	}

	payload.TokenPreview = previewToken(payload.Token)
	payload.EnvContent = buildAgentEnvContent(payload)
	payload.RestartCommand = "sudo systemctl restart backup-agent && sudo systemctl status backup-agent --no-pager"
	payload.ApplyCommand = buildAgentApplyCommand(payload.EnvContent)
	return payload
}

func buildAgentEnvContent(cfg agentConfigPayload) string {
	return strings.TrimSpace(fmt.Sprintf(`
# backup-agent 環境設定
DASHBOARD_URL=%s
AGENT_CODE=%s
AGENT_TOKEN=%s
AGENT_ADDR=%s
HOST_PREFIX=%s
NAS_BASE=%s
AGENT_BUILD_WORKDIR=%s
AGENT_BUILD_SCRIPT=%s
AGENT_RELEASES_DIR=%s
SLACK_WEBHOOK_URL=%s
`, cfg.DashboardURL, cfg.Code, cfg.Token, cfg.AgentAddr, cfg.HostPrefix, cfg.NASBase, cfg.BuildWorkdir, cfg.BuildScript, cfg.ReleasesDir, cfg.SlackWebhookURL)) + "\n"
}

func buildAgentApplyCommand(envContent string) string {
	return strings.TrimSpace(fmt.Sprintf(`
tmpenv="$(mktemp)"
cat >"$tmpenv" <<'EOF'
%sEOF
sudo install -d -m 755 /etc/backup-agent
sudo install -m 600 "$tmpenv" /etc/backup-agent/env
rm -f "$tmpenv"
sudo systemctl daemon-reload
sudo systemctl restart backup-agent
sudo systemctl status backup-agent --no-pager
`, envContent))
}

func latestAgentReleaseDetail(rootDir string, r *http.Request) (*agentReleaseDetail, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, err
	}

	items := make([]agentReleaseManifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(rootDir, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var manifest agentReleaseManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			continue
		}
		items = append(items, manifest)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("找不到 agent release")
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].BuiltAt.After(items[j].BuiltAt)
	})
	manifest := items[0]
	detail := &agentReleaseDetail{
		agentReleaseManifest: manifest,
		InstallCommand:       (&releaseHandler{}).installCommand(r, manifest.Version),
		UpgradeCommand:       (&releaseHandler{}).installCommand(r, manifest.Version),
	}
	for i := range detail.Files {
		detail.Files[i].DownloadURL = fmt.Sprintf("%s/api/admin/agent-releases/%s/download/%s", baseURL(r), manifest.Version, detail.Files[i].Name)
	}
	return detail, nil
}

func releaseAssetURL(rel *agentReleaseDetail, suffix string) string {
	if rel == nil {
		return ""
	}
	for _, file := range rel.Files {
		if strings.HasSuffix(file.Name, suffix) {
			return file.DownloadURL
		}
	}
	return ""
}

func buildDebianInstallCommand(rel *agentReleaseDetail, cfg agentConfigPayload) string {
	return buildDebianProcessCommand(rel, cfg)
}

func buildDebianProcessCommand(rel *agentReleaseDetail, cfg agentConfigPayload) string {
	if rel == nil {
		return ""
	}
	tarURL := releaseAssetURL(rel, ".tar.gz")
	if tarURL == "" {
		return ""
	}
	tarName := filepath.Base(tarURL)
	extractDir := strings.TrimSuffix(tarName, ".tar.gz")
	return strings.TrimSpace(fmt.Sprintf(`
sudo apt-get update
sudo apt-get install -y curl tar ca-certificates
tmpdir="$(mktemp -d)" && cd "$tmpdir"
curl -fsSLo %s %s
tar -xzf %s
cd %s
tmpenv="$(mktemp)"
cat >"$tmpenv" <<'EOF'
%sEOF
sudo install -d -m 755 /etc/backup-agent
sudo install -m 600 "$tmpenv" /etc/backup-agent/env
rm -f "$tmpenv"
sudo AGENT_BINARY_SRC=./backup-agent-linux-amd64 ./install-agent.sh
`, shQuote(tarName), shQuote(tarURL), shQuote(tarName), shQuote(extractDir), cfg.EnvContent))
}

func buildDebianUpgradeScript(rel *agentReleaseDetail, agentRow *store.Agent) string {
	if rel == nil {
		return ""
	}
	tarURL := releaseAssetURL(rel, ".tar.gz")
	checksumURL := releaseAssetURL(rel, "_checksums.txt")
	if tarURL == "" {
		return ""
	}
	tarName := filepath.Base(tarURL)
	checksumName := filepath.Base(checksumURL)
	extractDir := strings.TrimSuffix(tarName, ".tar.gz")
	expectedVersion := rel.Version
	return strings.TrimSpace(fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

VERSION=%s
TAR_URL=%s
CHECKSUM_URL=%s
TAR_NAME=%s
CHECKSUM_NAME=%s
EXTRACT_DIR=%s
TMPDIR="$(mktemp -d)"

cleanup() {
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

echo "[upgrade] agent=%s target_version=${VERSION}"
sudo apt-get update
sudo apt-get install -y curl tar ca-certificates

cd "$TMPDIR"
curl -fsSLo "$TAR_NAME" "$TAR_URL"
if [[ -n "$CHECKSUM_URL" ]]; then
  curl -fsSLo "$CHECKSUM_NAME" "$CHECKSUM_URL"
  if command -v sha256sum >/dev/null 2>&1; then
    grep "  ${TAR_NAME}$" "$CHECKSUM_NAME" | sha256sum -c -
  fi
fi

tar -xzf "$TAR_NAME"
cd "$EXTRACT_DIR"
sudo AGENT_BINARY_SRC=./backup-agent-linux-amd64 ./install-agent.sh

echo "[upgrade] service status"
sudo systemctl --no-pager --full status backup-agent || true

echo "[upgrade] recent logs"
sudo journalctl -u backup-agent -n 40 --no-pager || true

echo "[upgrade] expected heartbeat version: ${VERSION}"
echo "[upgrade] dashboard should show agent version updated after next heartbeat"
`, shQuote(expectedVersion), shQuote(tarURL), shQuote(checksumURL), shQuote(tarName), shQuote(checksumName), shQuote(extractDir), shQuote(agentRow.Code))) + "\n"
}

func buildUpgradeScriptCommand(scriptURL string) string {
	if strings.TrimSpace(scriptURL) == "" {
		return ""
	}
	return fmt.Sprintf("curl -fsSL %s -o backup-agent-upgrade.sh && bash backup-agent-upgrade.sh", shQuote(scriptURL))
}

func readReleaseTextAsset(rootDir, version, fileName string) string {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(version) == "" || strings.TrimSpace(fileName) == "" {
		return ""
	}
	path := filepath.Join(rootDir, version, filepath.Base(fileName))
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func shQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func previewToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 6 {
		return token
	}
	return token[:3] + "..." + token[len(token)-3:]
}

func resolveAgentReleaseDetail(rootDir, version string, r *http.Request) (*agentReleaseDetail, error) {
	if strings.TrimSpace(version) == "" {
		return latestAgentReleaseDetail(rootDir, r)
	}
	manifestPath := filepath.Join(rootDir, version, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest agentReleaseManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	detail := &agentReleaseDetail{
		agentReleaseManifest: manifest,
		InstallCommand:       (&releaseHandler{}).installCommand(r, manifest.Version),
		UpgradeCommand:       (&releaseHandler{}).installCommand(r, manifest.Version),
	}
	for i := range detail.Files {
		detail.Files[i].DownloadURL = fmt.Sprintf("%s/api/admin/agent-releases/%s/download/%s", baseURL(r), manifest.Version, detail.Files[i].Name)
	}
	return detail, nil
}

func latestAgentReleaseVersion(rootDir string) string {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return ""
	}
	var (
		latestVersion string
		latestBuiltAt time.Time
	)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(rootDir, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var manifest agentReleaseManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			continue
		}
		if latestVersion == "" || manifest.BuiltAt.After(latestBuiltAt) {
			latestVersion = manifest.Version
			latestBuiltAt = manifest.BuiltAt
		}
	}
	return latestVersion
}

func releaseVersionOrEmpty(rel *agentReleaseDetail) string {
	if rel == nil {
		return ""
	}
	return rel.Version
}

func agentVersionState(agentRow *store.Agent, release *agentReleaseDetail) (string, string) {
	if release == nil {
		return "no_release", "尚未建立 release"
	}
	current := strings.TrimSpace(agentRow.Version)
	if current == "" {
		return "unknown", "agent 尚未回報版本"
	}
	if current == "dev" {
		return "unknown", "agent 仍使用未標版 binary"
	}
	if current == release.Version {
		return "up_to_date", "已是最新版本"
	}
	return "outdated", fmt.Sprintf("目前 %s，最新 %s", current, release.Version)
}
