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
	InstallCommand   string              `json:"install_command,omitempty"`
	ApplyCommand     string              `json:"apply_command,omitempty"`
	RestartCommand   string              `json:"restart_command,omitempty"`
	DownloadURL      string              `json:"download_url,omitempty"`
	ChecksumURL      string              `json:"checksum_url,omitempty"`
	InstallScriptURL string              `json:"install_script_url,omitempty"`
}

func (h *agentsHandler) list(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
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
	writeJSON(w, http.StatusOK, agent)
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
		ApplyCommand:   cfg.ApplyCommand,
		RestartCommand: cfg.RestartCommand,
	}

	release, err := latestAgentReleaseDetail(envOr("AGENT_RELEASES_DIR", "artifacts/backup-agent"), r)
	if err != nil {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	payload.Release = release
	payload.InstallCommand = buildDebianInstallCommand(release, cfg)
	payload.DownloadURL = releaseAssetURL(release, ".tar.gz")
	payload.ChecksumURL = releaseAssetURL(release, "_checksums.txt")
	payload.InstallScriptURL = releaseAssetURL(release, "install-agent.sh")
	writeJSON(w, http.StatusOK, payload)
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
sudo install -d -m 755 /etc/backup-agent
sudo tee /etc/backup-agent/env >/dev/null <<'EOF'
%sEOF
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
tmpdir="$(mktemp -d)" && cd "$tmpdir"
curl -fsSLO %s
tar -xzf %s
cd %s
sudo install -d -m 755 /etc/backup-agent
sudo tee /etc/backup-agent/env >/dev/null <<'EOF'
%sEOF
sudo AGENT_BINARY_SRC=./backup-agent-linux-amd64 ./install-agent.sh
`, tarURL, tarName, extractDir, cfg.EnvContent))
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
