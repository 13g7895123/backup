#!/usr/bin/env bash
#
# 完整備份目前 PostgreSQL 的 schema + data。
# 輸出為可直接 restore 的 plain SQL gzip 檔。
#
# 用法：
#   bash scripts/backup-current-db.sh
#   BACKUP_DIR=/path/to/backups bash scripts/backup-current-db.sh
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-${ROOT_DIR}/docker-compose.yml}"
ENV_FILE="${ENV_FILE:-${ROOT_DIR}/.env}"
SECRET_FILE="${SECRET_FILE:-${ROOT_DIR}/secrets/pg_password.txt}"
BACKUP_DIR="${BACKUP_DIR:-${ROOT_DIR}/db_backups}"

POSTGRES_SERVICE="${POSTGRES_SERVICE:-postgres}"
POSTGRES_DB="${POSTGRES_DB:-backup_manager}"
POSTGRES_USER="${POSTGRES_USER:-backup}"

timestamp="$(date +%Y%m%d_%H%M%S)"
git_commit="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
backup_base="backup_manager_${timestamp}_${git_commit}"
dump_path="${BACKUP_DIR}/${backup_base}.sql.gz"
meta_path="${BACKUP_DIR}/${backup_base}.meta"

mkdir -p "${BACKUP_DIR}"

if [[ ! -f "${COMPOSE_FILE}" ]]; then
  echo "[error] 找不到 docker-compose.yml: ${COMPOSE_FILE}" >&2
  exit 1
fi

if [[ -f "${SECRET_FILE}" ]]; then
  PG_PASSWORD="$(tr -d '\r\n' < "${SECRET_FILE}")"
elif [[ -f "${ENV_FILE}" ]]; then
  PG_PASSWORD="$(grep -E '^PG_PASSWORD=' "${ENV_FILE}" | head -1 | cut -d= -f2- | tr -d '\r\n' || true)"
else
  PG_PASSWORD=""
fi

if [[ -z "${PG_PASSWORD}" ]]; then
  echo "[error] 無法取得 PG_PASSWORD，請確認 ${SECRET_FILE} 或 ${ENV_FILE}" >&2
  exit 1
fi

echo "[backup-db] 輸出檔案: ${dump_path}"

docker compose -f "${COMPOSE_FILE}" exec -T \
  -e "PGPASSWORD=${PG_PASSWORD}" \
  "${POSTGRES_SERVICE}" \
  pg_dump -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" --clean --if-exists --create --no-owner --no-privileges \
  | gzip -c > "${dump_path}"

sha256="$(sha256sum "${dump_path}" | awk '{print $1}')"

cat > "${meta_path}" <<EOF
timestamp=${timestamp}
git_commit=${git_commit}
compose_file=${COMPOSE_FILE}
postgres_service=${POSTGRES_SERVICE}
postgres_db=${POSTGRES_DB}
postgres_user=${POSTGRES_USER}
dump_file=$(basename "${dump_path}")
sha256=${sha256}
EOF

echo "[backup-db] 完成"
echo "  dump : ${dump_path}"
echo "  meta : ${meta_path}"
echo "  sha256: ${sha256}"
