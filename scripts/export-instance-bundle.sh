#!/usr/bin/env bash
#
# 匯出目前 Backup Manager instance 的遷移 bundle：
# - PostgreSQL dump
# - .env
# - secrets/pg_password.txt
# - /etc/backup-agent/env（若存在）
#
# 用法：
#   bash scripts/export-instance-bundle.sh
#   OUT_DIR=/path/to/output bash scripts/export-instance-bundle.sh
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/migration_bundles}"
TMP_BASE="${TMPDIR:-/tmp}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
HOSTNAME_VALUE="$(hostname 2>/dev/null || echo unknown-host)"
GIT_COMMIT="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
WORK_DIR="$(mktemp -d "${TMP_BASE}/backup-manager-export-${TIMESTAMP}-XXXXXX")"
BUNDLE_NAME="backup-manager-migration_${TIMESTAMP}_${GIT_COMMIT}"
BUNDLE_DIR="${WORK_DIR}/${BUNDLE_NAME}"
DB_DIR="${BUNDLE_DIR}/db"
CONFIG_DIR="${BUNDLE_DIR}/config"
META_DIR="${BUNDLE_DIR}/meta"
AGENT_ENV_FILE="${AGENT_ENV_FILE:-/etc/backup-agent/env}"

cleanup() {
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

mkdir -p "${OUT_DIR}" "${DB_DIR}" "${CONFIG_DIR}/secrets" "${META_DIR}"

echo "[export] 建立 DB dump"
BACKUP_DIR="${DB_DIR}" bash "${ROOT_DIR}/scripts/backup-current-db.sh"

if [[ -f "${ROOT_DIR}/.env" ]]; then
  cp "${ROOT_DIR}/.env" "${CONFIG_DIR}/.env"
  chmod 600 "${CONFIG_DIR}/.env"
fi

if [[ -f "${ROOT_DIR}/secrets/pg_password.txt" ]]; then
  cp "${ROOT_DIR}/secrets/pg_password.txt" "${CONFIG_DIR}/secrets/pg_password.txt"
  chmod 600 "${CONFIG_DIR}/secrets/pg_password.txt"
fi

if [[ -f "${AGENT_ENV_FILE}" ]]; then
  cp "${AGENT_ENV_FILE}" "${CONFIG_DIR}/backup-agent.env"
  chmod 600 "${CONFIG_DIR}/backup-agent.env"
fi

cat > "${META_DIR}/manifest.txt" <<EOF
bundle_name=${BUNDLE_NAME}
created_at=$(date -Iseconds)
source_root=${ROOT_DIR}
hostname=${HOSTNAME_VALUE}
git_commit=${GIT_COMMIT}
db_dump=$(find "${DB_DIR}" -maxdepth 1 -type f -name '*.sql.gz' -printf '%f\n' | head -n 1)
env_file=$(if [[ -f "${CONFIG_DIR}/.env" ]]; then echo ".env"; fi)
secret_file=$(if [[ -f "${CONFIG_DIR}/secrets/pg_password.txt" ]]; then echo "secrets/pg_password.txt"; fi)
agent_env_file=$(if [[ -f "${CONFIG_DIR}/backup-agent.env" ]]; then echo "backup-agent.env"; fi)
EOF

tar -C "${WORK_DIR}" -czf "${OUT_DIR}/${BUNDLE_NAME}.tar.gz" "${BUNDLE_NAME}"
sha256sum "${OUT_DIR}/${BUNDLE_NAME}.tar.gz" > "${OUT_DIR}/${BUNDLE_NAME}.tar.gz.sha256"

echo "[export] 完成"
echo "  bundle : ${OUT_DIR}/${BUNDLE_NAME}.tar.gz"
echo "  sha256 : ${OUT_DIR}/${BUNDLE_NAME}.tar.gz.sha256"
