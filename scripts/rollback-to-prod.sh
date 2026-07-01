#!/usr/bin/env bash
#
# 將程式碼與資料庫一起回復到目前正式站基準版本。
# 目標 commit: 8dd92c5cb99c958fcb21f74457e0bf796fb308b1
#
# 這支腳本會做：
# 1. 停止 dashboard
# 2. 先備份目前資料庫狀態（避免事故現場遺失）
# 3. git reset --hard 到正式站 commit，並清除 untracked 檔案
# 4. 啟動 postgres
# 5. 用指定的 SQL 備份檔完整還原資料庫
# 6. 重新 build 並啟動 dashboard
#
# 用法：
#   bash scripts/rollback-to-prod.sh /path/to/backup.sql.gz
#   FORCE=1 bash scripts/rollback-to-prod.sh /path/to/backup.sql.gz
#
set -euo pipefail

TARGET_COMMIT="8dd92c5cb99c958fcb21f74457e0bf796fb308b1"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-${ROOT_DIR}/docker-compose.yml}"
ENV_FILE="${ENV_FILE:-${ROOT_DIR}/.env}"
SECRET_FILE="${SECRET_FILE:-${ROOT_DIR}/secrets/pg_password.txt}"

POSTGRES_SERVICE="${POSTGRES_SERVICE:-postgres}"
POSTGRES_DB="${POSTGRES_DB:-backup_manager}"
POSTGRES_USER="${POSTGRES_USER:-backup}"
ROLLBACK_SAFETY_BACKUP_DIR="${ROLLBACK_SAFETY_BACKUP_DIR:-/tmp/backup-manager-rollback-backups}"

RESTORE_FILE="${1:-${RESTORE_FILE:-}}"

if [[ -z "${RESTORE_FILE}" ]]; then
  echo "[error] 請提供要還原的 SQL gzip 備份檔路徑" >&2
  echo "        例：bash scripts/rollback-to-prod.sh db_backups/backup_manager_20260531_120000_abcd123.sql.gz" >&2
  exit 1
fi

if [[ ! -f "${RESTORE_FILE}" ]]; then
  echo "[error] 找不到備份檔: ${RESTORE_FILE}" >&2
  exit 1
fi

RESTORE_BASENAME="$(basename "${RESTORE_FILE}")"
RESTORE_COPY="/tmp/${RESTORE_BASENAME}"
cp "${RESTORE_FILE}" "${RESTORE_COPY}"
RESTORE_FILE="${RESTORE_COPY}"

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

if [[ "${FORCE:-0}" != "1" ]]; then
  echo "警告：這個操作會執行 git reset --hard、git clean -fd，並覆蓋目前資料庫。"
  echo "目標 commit: ${TARGET_COMMIT}"
  echo "restore file: ${RESTORE_FILE}"
  read -r -p "若要繼續，請輸入 YES: " confirm
  if [[ "${confirm}" != "YES" ]]; then
    echo "已取消"
    exit 1
  fi
fi

if ! git -C "${ROOT_DIR}" rev-parse --verify "${TARGET_COMMIT}^{commit}" >/dev/null 2>&1; then
  echo "[error] 本地找不到 target commit: ${TARGET_COMMIT}" >&2
  exit 1
fi

echo "[rollback] 先備份目前資料庫狀態"
BACKUP_DIR="${ROLLBACK_SAFETY_BACKUP_DIR}" bash "${ROOT_DIR}/scripts/backup-current-db.sh"

echo "[rollback] 停止 dashboard"
docker compose -f "${COMPOSE_FILE}" stop dashboard || true

echo "[rollback] git reset 到正式站 commit"
git -C "${ROOT_DIR}" reset --hard "${TARGET_COMMIT}"
git -C "${ROOT_DIR}" clean -fd

echo "[rollback] 啟動 postgres"
docker compose -f "${COMPOSE_FILE}" up -d "${POSTGRES_SERVICE}"

echo "[rollback] 等待 postgres ready"
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

if [[ "${ready:-0}" != "1" ]]; then
  echo "[error] postgres 未在預期時間內 ready" >&2
  exit 1
fi

echo "[rollback] 還原資料庫: ${RESTORE_FILE}"
gunzip -c "${RESTORE_FILE}" | docker compose -f "${COMPOSE_FILE}" exec -T \
  -e "PGPASSWORD=${PG_PASSWORD}" \
  "${POSTGRES_SERVICE}" \
  psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER}" -d postgres

echo "[rollback] 啟動 dashboard"
docker compose -f "${COMPOSE_FILE}" up -d --build dashboard

echo "[rollback] 完成"
echo "  code commit : ${TARGET_COMMIT}"
echo "  db restore  : ${RESTORE_FILE}"
echo "  safety dump : ${ROLLBACK_SAFETY_BACKUP_DIR}"
echo "  next step   : 檢查 docker compose ps 與 /healthz"
