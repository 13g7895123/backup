#!/usr/bin/env bash
# scripts/install-agent.sh
# 在 Debian 主機上安裝 backup-agent 為 systemd 服務
# 需要以 root 執行：sudo ./install-agent.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

BINARY_SRC="${AGENT_BINARY_SRC:-}"
if [[ -z "$BINARY_SRC" ]]; then
  if [[ -f "${SCRIPT_DIR}/backup-agent-linux-amd64" ]]; then
    BINARY_SRC="${SCRIPT_DIR}/backup-agent-linux-amd64"
  elif [[ -f "${SCRIPT_DIR}/backup-agent" ]]; then
    BINARY_SRC="${SCRIPT_DIR}/backup-agent"
  else
    BINARY_SRC="${ROOT_DIR}/backup-agent"
  fi
fi

BINARY_DST="/usr/local/bin/backup-agent"
ENV_DIR="/etc/backup-agent"
ENV_FILE="${ENV_DIR}/env"
SERVICE_SRC="${SCRIPT_DIR}/backup-agent.service"
if [[ ! -f "$SERVICE_SRC" ]]; then
  SERVICE_SRC="${ROOT_DIR}/scripts/backup-agent.service"
fi
SERVICE_DST="/etc/systemd/system/backup-agent.service"

# ── 確認 binary 已編譯 ────────────────────────────────────────────
if [[ ! -f "$BINARY_SRC" ]]; then
  echo "[error] 找不到 backup-agent binary：$BINARY_SRC"
  echo "        可先執行 scripts/build-agent.sh，或在執行時指定 AGENT_BINARY_SRC=/path/to/backup-agent-linux-amd64"
  exit 1
fi

# ── 安裝 binary ───────────────────────────────────────────────────
echo "[install] 複製 binary → $BINARY_DST"
cp "$BINARY_SRC" "$BINARY_DST"
chmod 755 "$BINARY_DST"

# ── 建立設定目錄與 env 檔 ─────────────────────────────────────────
mkdir -p "$ENV_DIR"
if [[ ! -f "$ENV_FILE" ]]; then
  # 嘗試從 .env 讀取 PG_PASSWORD
  PROJ_ENV="${ROOT_DIR}/.env"
  DASHBOARD_URL=""
  AGENT_TOKEN=""
  AGENT_CODE=""
  if [[ -f "$PROJ_ENV" ]]; then
    DASHBOARD_URL=$(grep -E '^DASHBOARD_URL=' "$PROJ_ENV" | cut -d= -f2 | tr -d '\r\n' || true)
    AGENT_TOKEN=$(grep -E '^AGENT_TOKEN='    "$PROJ_ENV" | cut -d= -f2 | tr -d '\r\n' || true)
    AGENT_CODE=$(grep -E '^AGENT_CODE='      "$PROJ_ENV" | cut -d= -f2 | tr -d '\r\n' || true)
  fi
  # 若 .env 未設，使用預設值
  DASHBOARD_URL="${DASHBOARD_URL:-http://127.0.0.1:8105}"
  AGENT_CODE="${AGENT_CODE:-vm-app-01}"
  cat > "$ENV_FILE" <<EOF
# backup-agent 環境設定
# Dashboard API 位址（agent 透過 HTTP 與 dashboard 溝通，不直連 DB）
DASHBOARD_URL=${DASHBOARD_URL}

# Agent 固定識別碼（需與 dashboard agents.code 對應）
AGENT_CODE=${AGENT_CODE}

# Agent Token（選填，與 dashboard AGENT_TOKEN 對應）
AGENT_TOKEN=${AGENT_TOKEN}

# Agent HTTP 監聽位址
AGENT_ADDR=:9090

# HOST_PREFIX 留空 = agent 直接讀取 host 路徑（不走 Docker volume 前綴）
HOST_PREFIX=

# NAS 掛載點（agent 寫入備份的目標）
NAS_BASE=/mnt/nas/backups

# Agent release build workspace（留空表示未啟用 agent 建版）
AGENT_BUILD_WORKDIR=

# Agent release build script（相對於 AGENT_BUILD_WORKDIR）
AGENT_BUILD_SCRIPT=scripts/build-agent.sh

# Agent release artifact 暫存與下載來源目錄
AGENT_RELEASES_DIR=/var/lib/backup-agent/releases

# Slack 失敗通知（選填）
SLACK_WEBHOOK_URL=
EOF
  echo "[install] 建立設定檔：$ENV_FILE（請確認 DASHBOARD_URL 正確）"
else
  echo "[install] 設定檔已存在，跳過：$ENV_FILE"
fi

# ── 安裝 systemd service ──────────────────────────────────────────
echo "[install] 安裝 systemd service → $SERVICE_DST"
cp "$SERVICE_SRC" "$SERVICE_DST"
systemctl daemon-reload
systemctl enable backup-agent
systemctl restart backup-agent

echo ""
echo "[install] 完成！"
echo "  狀態：systemctl status backup-agent"
echo "  日誌：journalctl -fu backup-agent"
echo "  設定：$ENV_FILE"
