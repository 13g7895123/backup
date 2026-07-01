#!/usr/bin/env bash
#
# 用法：
#   ./scripts/deploy.sh production
#   ./scripts/deploy.sh production --with-agent
#
# 正式部署流程：
# 1. 建立部署前快照（含 DB 備份）
# 2. 啟動 / 更新 postgres
# 3. build 並重啟 dashboard
# 4. 若指定 --with-agent，且本機存在 backup-agent.service，則重編 agent binary 並重啟 agent
# 5. 驗證 dashboard healthz
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_ENV="${1:-}"
DEPLOY_AGENT=0
COMPOSE_FILE="${COMPOSE_FILE:-${ROOT_DIR}/docker-compose.yml}"
DEPLOY_LOG_DIR="${DEPLOY_LOG_DIR:-${ROOT_DIR}/deploy_logs}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
mkdir -p "${DEPLOY_LOG_DIR}"
LOG_FILE="${DEPLOY_LOG_DIR}/deploy_${TARGET_ENV:-unknown}_${TIMESTAMP}.log"

exec > >(tee -a "${LOG_FILE}") 2>&1

log() {
  echo "[deploy] $*"
}

die() {
  echo "[deploy][error] $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少必要指令: $1"
}

usage() {
  cat <<'EOF'
用法：
  ./scripts/deploy.sh production
  ./scripts/deploy.sh production --with-agent

說明：
  production 會執行完整正式部署：
  - 部署前快照
  - PostgreSQL / dashboard 更新
  - 預設不更新本機 backup-agent
  - 若加上 --with-agent，才會更新本機 backup-agent
  - dashboard healthz 驗證
EOF
}

wait_dashboard_health() {
  local port
  port="$(grep -E '^DASHBOARD_PORT=' "${ROOT_DIR}/.env" | head -1 | cut -d= -f2- | tr -d '\r\n' || true)"
  port="${port:-8080}"
  local url="http://127.0.0.1:${port}/healthz"

  log "等待 dashboard healthz: ${url}"
  for _ in $(seq 1 30); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      log "dashboard healthz 正常"
      return 0
    fi
    sleep 2
  done
  return 1
}

deploy_production() {
  require_cmd docker
  require_cmd curl
  require_cmd bash

  [[ -f "${COMPOSE_FILE}" ]] || die "找不到 docker-compose.yml: ${COMPOSE_FILE}"
  [[ -f "${ROOT_DIR}/.env" ]] || die "找不到 .env: ${ROOT_DIR}/.env"

  log "開始正式部署"
  log "專案路徑: ${ROOT_DIR}"
  log "log file: ${LOG_FILE}"

  log "1/6 建立部署前快照"
  bash "${ROOT_DIR}/scripts/pre-deploy-snapshot.sh"

  log "2/6 啟動 / 更新 PostgreSQL"
  docker compose -f "${COMPOSE_FILE}" up -d postgres

  log "3/6 build 並重啟 dashboard"
  docker compose -f "${COMPOSE_FILE}" up -d --build dashboard

  if [[ "${DEPLOY_AGENT}" == "1" ]]; then
    if systemctl list-unit-files 2>/dev/null | grep -q '^backup-agent.service'; then
      if [[ -x "${ROOT_DIR}/scripts/build-agent.sh" ]]; then
        log "4/6 更新本機 backup-agent"
        bash "${ROOT_DIR}/scripts/build-agent.sh"
        sudo cp "${ROOT_DIR}/backup-agent" /usr/local/bin/backup-agent
        sudo systemctl restart backup-agent
        sudo systemctl status backup-agent --no-pager || true
      else
        log "4/6 略過 agent 更新：找不到 scripts/build-agent.sh"
      fi
    else
      log "4/6 略過 agent 更新：本機未安裝 backup-agent.service"
    fi
  else
    log "4/6 略過 agent 更新：未指定 --with-agent"
  fi

  log "5/6 驗證 docker compose 狀態"
  docker compose -f "${COMPOSE_FILE}" ps

  log "6/6 驗證 dashboard healthz"
  wait_dashboard_health || die "dashboard healthz 驗證失敗，請檢查 docker compose logs dashboard"

  log "部署完成"
  log "建議後續檢查："
  echo "  docker compose logs --tail=50 dashboard"
  echo "  docker compose ps"
  echo "  systemctl status backup-agent --no-pager"
}

case "${2:-}" in
  "")
    ;;
  --with-agent)
    DEPLOY_AGENT=1
    ;;
  *)
    die "不支援的參數: ${2:-}"
    ;;
esac

case "${TARGET_ENV}" in
  production)
    deploy_production
    ;;
  ""|-h|--help|help)
    usage
    ;;
  *)
    die "不支援的部署目標: ${TARGET_ENV}"
    ;;
esac
