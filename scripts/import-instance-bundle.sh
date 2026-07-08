#!/usr/bin/env bash
#
# 在新 clone 的 repo 匯入舊 instance 匯出的 migration bundle。
# 會先建立目前 DB 的 safety backup，再還原 bundle 內的 DB dump。
#
# 用法：
#   bash scripts/import-instance-bundle.sh /path/to/bundle.tar.gz
#   FORCE=1 bash scripts/import-instance-bundle.sh /path/to/bundle.tar.gz
#   FORCE=1 RESTORE_AGENT_ENV=1 bash scripts/import-instance-bundle.sh /path/to/bundle.tar.gz
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUNDLE_FILE="${1:-${BUNDLE_FILE:-}}"
COMPOSE_FILE="${COMPOSE_FILE:-${ROOT_DIR}/docker-compose.yml}"
ENV_FILE="${ENV_FILE:-${ROOT_DIR}/.env}"
SECRET_FILE="${SECRET_FILE:-${ROOT_DIR}/secrets/pg_password.txt}"
POSTGRES_SERVICE="${POSTGRES_SERVICE:-postgres}"
POSTGRES_DB="${POSTGRES_DB:-backup_manager}"
POSTGRES_USER="${POSTGRES_USER:-backup}"
SAFETY_BACKUP_DIR="${SAFETY_BACKUP_DIR:-/tmp/backup-manager-import-safety}"
RESTORE_AGENT_ENV="${RESTORE_AGENT_ENV:-0}"
FORCE_CONFIG="${FORCE_CONFIG:-0}"
TMP_BASE="${TMPDIR:-/tmp}"
WORK_DIR="$(mktemp -d "${TMP_BASE}/backup-manager-import-XXXXXX")"

cleanup() {
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

die() {
  echo "[import][error] $*" >&2
  exit 1
}

if [[ -z "${BUNDLE_FILE}" ]]; then
  die "請提供 bundle 路徑，例如：bash scripts/import-instance-bundle.sh /path/to/bundle.tar.gz"
fi

if [[ ! -f "${BUNDLE_FILE}" ]]; then
  die "找不到 bundle: ${BUNDLE_FILE}"
fi

if [[ ! -f "${COMPOSE_FILE}" ]]; then
  die "找不到 docker-compose.yml: ${COMPOSE_FILE}"
fi

tar -C "${WORK_DIR}" -xzf "${BUNDLE_FILE}"
BUNDLE_ROOT="$(find "${WORK_DIR}" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
[[ -n "${BUNDLE_ROOT}" ]] || die "bundle 格式錯誤：找不到根目錄"

DB_DUMP="$(find "${BUNDLE_ROOT}/db" -maxdepth 1 -type f -name '*.sql.gz' | head -n 1)"
[[ -n "${DB_DUMP}" ]] || die "bundle 內沒有 DB dump"

if [[ "${FORCE:-0}" != "1" ]]; then
  echo "警告：這會覆蓋目前 repo 綁定的 PostgreSQL 資料庫，並可能覆蓋 .env / secrets。"
  echo "bundle: ${BUNDLE_FILE}"
  echo "target repo: ${ROOT_DIR}"
  read -r -p "若要繼續，請輸入 YES: " confirm
  [[ "${confirm}" == "YES" ]] || die "已取消"
fi

mkdir -p "${ROOT_DIR}/secrets"

if [[ -f "${BUNDLE_ROOT}/config/.env" ]]; then
  if [[ ! -f "${ENV_FILE}" || "${FORCE_CONFIG}" == "1" ]]; then
    cp "${BUNDLE_ROOT}/config/.env" "${ENV_FILE}"
    chmod 600 "${ENV_FILE}"
    echo "[import] 已還原 .env"
  else
    echo "[import] 略過 .env（目標檔已存在，若要覆蓋請設 FORCE_CONFIG=1）"
  fi
fi

if [[ -f "${BUNDLE_ROOT}/config/secrets/pg_password.txt" ]]; then
  if [[ ! -f "${SECRET_FILE}" || "${FORCE_CONFIG}" == "1" ]]; then
    cp "${BUNDLE_ROOT}/config/secrets/pg_password.txt" "${SECRET_FILE}"
    chmod 600 "${SECRET_FILE}"
    echo "[import] 已還原 secrets/pg_password.txt"
  else
    echo "[import] 略過 pg_password.txt（目標檔已存在，若要覆蓋請設 FORCE_CONFIG=1）"
  fi
fi

if [[ "${RESTORE_AGENT_ENV}" == "1" && -f "${BUNDLE_ROOT}/config/backup-agent.env" ]]; then
  sudo mkdir -p /etc/backup-agent
  sudo cp "${BUNDLE_ROOT}/config/backup-agent.env" /etc/backup-agent/env
  sudo chmod 600 /etc/backup-agent/env
  echo "[import] 已還原 /etc/backup-agent/env"
fi

if [[ -f "${SECRET_FILE}" ]]; then
  PG_PASSWORD="$(tr -d '\r\n' < "${SECRET_FILE}")"
elif [[ -f "${ENV_FILE}" ]]; then
  PG_PASSWORD="$(grep -E '^PG_PASSWORD=' "${ENV_FILE}" | head -1 | cut -d= -f2- | tr -d '\r\n' || true)"
else
  PG_PASSWORD=""
fi
[[ -n "${PG_PASSWORD}" ]] || die "無法取得 PG_PASSWORD，請確認 .env 或 secrets/pg_password.txt"

echo "[import] 停止 dashboard"
docker compose -f "${COMPOSE_FILE}" stop dashboard || true

echo "[import] 啟動 postgres"
docker compose -f "${COMPOSE_FILE}" up -d "${POSTGRES_SERVICE}"

echo "[import] 等待 postgres ready"
ready=0
for _ in $(seq 1 30); do
  if docker compose -f "${COMPOSE_FILE}" exec -T \
    -e "PGPASSWORD=${PG_PASSWORD}" \
    "${POSTGRES_SERVICE}" \
    pg_isready -U "${POSTGRES_USER}" -d postgres >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done
[[ "${ready}" == "1" ]] || die "postgres 未在預期時間內 ready"

echo "[import] 建立目前 DB safety backup"
mkdir -p "${SAFETY_BACKUP_DIR}"
BACKUP_DIR="${SAFETY_BACKUP_DIR}" bash "${ROOT_DIR}/scripts/backup-current-db.sh"

echo "[import] 還原 DB dump: ${DB_DUMP}"
gunzip -c "${DB_DUMP}" | docker compose -f "${COMPOSE_FILE}" exec -T \
  -e "PGPASSWORD=${PG_PASSWORD}" \
  "${POSTGRES_SERVICE}" \
  psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER}" -d postgres

echo "[import] 啟動 dashboard"
docker compose -f "${COMPOSE_FILE}" up -d --build dashboard

echo "[import] 完成"
echo "  bundle        : ${BUNDLE_FILE}"
echo "  safety backup : ${SAFETY_BACKUP_DIR}"
echo "  next step     : docker compose ps && curl -fsS http://127.0.0.1:\${DASHBOARD_PORT:-8080}/healthz"
