#!/usr/bin/env bash
#
# 正式部署前快照：
# 1. 備份目前資料庫
# 2. 記錄目前 git commit / branch / status
# 3. 備份 .env / secrets / docker-compose.yml / scripts
# 4. 產生 rollback 指令說明
#
# 用法：
#   bash scripts/pre-deploy-snapshot.sh
#   SNAPSHOT_DIR=/path/to/store bash scripts/pre-deploy-snapshot.sh
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SNAPSHOT_ROOT="${SNAPSHOT_DIR:-${ROOT_DIR}/deploy_snapshots}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
COMMIT_FULL="$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || echo unknown)"
COMMIT_SHORT="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BRANCH_NAME="$(git -C "${ROOT_DIR}" rev-parse --abbrev-ref HEAD 2>/dev/null || echo detached)"
SNAPSHOT_NAME="${TIMESTAMP}_${COMMIT_SHORT}"
SNAPSHOT_PATH="${SNAPSHOT_ROOT}/${SNAPSHOT_NAME}"

mkdir -p "${SNAPSHOT_PATH}"

echo "[snapshot] 建立部署前快照: ${SNAPSHOT_PATH}"

echo "[snapshot] 1/5 備份資料庫"
BACKUP_DIR="${SNAPSHOT_PATH}/db" bash "${ROOT_DIR}/scripts/backup-current-db.sh"

echo "[snapshot] 2/5 記錄 git 狀態"
{
  echo "timestamp=${TIMESTAMP}"
  echo "commit_full=${COMMIT_FULL}"
  echo "commit_short=${COMMIT_SHORT}"
  echo "branch=${BRANCH_NAME}"
  echo "root_dir=${ROOT_DIR}"
} > "${SNAPSHOT_PATH}/git-info.meta"

git -C "${ROOT_DIR}" status --short > "${SNAPSHOT_PATH}/git-status.txt" || true
git -C "${ROOT_DIR}" log -1 --stat > "${SNAPSHOT_PATH}/git-last-commit.txt" || true

echo "[snapshot] 3/5 備份設定檔"
mkdir -p "${SNAPSHOT_PATH}/config"

copy_if_exists() {
  local src="$1"
  local dst="$2"
  if [[ -e "${src}" ]]; then
    cp -a "${src}" "${dst}"
  fi
}

copy_if_exists "${ROOT_DIR}/.env" "${SNAPSHOT_PATH}/config/.env"
copy_if_exists "${ROOT_DIR}/.env.example" "${SNAPSHOT_PATH}/config/.env.example"
copy_if_exists "${ROOT_DIR}/docker-compose.yml" "${SNAPSHOT_PATH}/config/docker-compose.yml"
copy_if_exists "${ROOT_DIR}/secrets" "${SNAPSHOT_PATH}/config/secrets"

echo "[snapshot] 4/5 備份 rollback 相關腳本"
mkdir -p "${SNAPSHOT_PATH}/scripts"
copy_if_exists "${ROOT_DIR}/scripts/backup-current-db.sh" "${SNAPSHOT_PATH}/scripts/backup-current-db.sh"
copy_if_exists "${ROOT_DIR}/scripts/rollback-to-prod.sh" "${SNAPSHOT_PATH}/scripts/rollback-to-prod.sh"
copy_if_exists "${ROOT_DIR}/scripts/pre-deploy-snapshot.sh" "${SNAPSHOT_PATH}/scripts/pre-deploy-snapshot.sh"

echo "[snapshot] 5/5 產生 rollback 說明"
LATEST_DB_BACKUP="$(find "${SNAPSHOT_PATH}/db" -maxdepth 1 -type f -name '*.sql.gz' | sort | tail -n 1)"
cat > "${SNAPSHOT_PATH}/ROLLBACK_README.txt" <<EOF
部署前快照建立時間：${TIMESTAMP}
當前 branch：${BRANCH_NAME}
當前 commit：${COMMIT_FULL}

本快照包含：
1. 資料庫備份：${LATEST_DB_BACKUP}
2. Git 狀態：git-status.txt
3. 目前設定：config/
4. rollback 腳本：scripts/

若部署後要回復到目前這個狀態，可參考：

1. 先確認要回復的 code commit
2. 使用 rollback 腳本回到正式站 baseline：
   bash scripts/rollback-to-prod.sh ${LATEST_DB_BACKUP}

3. 若你要回到這次 snapshot 的 code 狀態，而不是 baseline，
   請手動執行：
   git reset --hard ${COMMIT_FULL}
   git clean -fd

4. 還原 .env / secrets：
   cp -a ${SNAPSHOT_PATH}/config/.env ${ROOT_DIR}/.env
   cp -a ${SNAPSHOT_PATH}/config/secrets ${ROOT_DIR}/secrets

5. 重新啟動服務：
   docker compose up -d --build
EOF

echo "[snapshot] 完成"
echo "  snapshot : ${SNAPSHOT_PATH}"
echo "  db backup : ${LATEST_DB_BACKUP}"
echo "  commit    : ${COMMIT_FULL}"
