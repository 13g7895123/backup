#!/usr/bin/env bash
#
# 查詢並修正 projects 資料表中的 nas_base / nas_subpath 路徑設定。
#
# 用法：
#   bash scripts/fix-nas-path.sh              # 互動模式：列出後可選擇修改
#   bash scripts/fix-nas-path.sh --list       # 只列出，不修改
#   bash scripts/fix-nas-path.sh --id 3 --nas-base /mnt/nas/backups --nas-subpath ""   # 直接更新
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-${ROOT_DIR}/docker-compose.yml}"
ENV_FILE="${ENV_FILE:-${ROOT_DIR}/.env}"
SECRET_FILE="${SECRET_FILE:-${ROOT_DIR}/secrets/pg_password.txt}"

POSTGRES_SERVICE="${POSTGRES_SERVICE:-postgres}"
POSTGRES_DB="${POSTGRES_DB:-backup_manager}"
POSTGRES_USER="${POSTGRES_USER:-backup}"

# ── 取得 PG_PASSWORD ─────────────────────────────────────────────────────────
get_pg_password() {
  if [[ -f "${SECRET_FILE}" ]]; then
    tr -d '\r\n' < "${SECRET_FILE}"
  elif [[ -f "${ENV_FILE}" ]]; then
    grep -E '^PG_PASSWORD=' "${ENV_FILE}" | head -1 | cut -d= -f2- | tr -d '\r\n' || true
  else
    echo ""
  fi
}

# ── 執行 psql 指令（透過 docker compose exec）────────────────────────────────
run_psql() {
  local sql="$1"
  local pg_pw
  pg_pw="$(get_pg_password)"

  if [[ -z "${pg_pw}" ]]; then
    echo "[錯誤] 無法取得 PG_PASSWORD，請確認 ${SECRET_FILE} 或 ${ENV_FILE}" >&2
    exit 1
  fi

  docker compose -f "${COMPOSE_FILE}" exec -T \
    -e "PGPASSWORD=${pg_pw}" \
    "${POSTGRES_SERVICE}" \
    psql -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" \
    --no-align --tuples-only -c "${sql}"
}

# ── 列出所有專案路徑 ─────────────────────────────────────────────────────────
list_projects() {
  echo ""
  echo "══════════════════════════════════════════════════════════════════════"
  echo "  projects 備份路徑設定"
  echo "══════════════════════════════════════════════════════════════════════"
  printf "  %-4s  %-20s  %-35s  %-20s\n" "ID" "名稱" "nas_base" "nas_subpath"
  echo "  ──────────────────────────────────────────────────────────────────"

  local rows
  rows="$(run_psql "SELECT id, name, COALESCE(nas_base,'(空)'), COALESCE(nas_subpath,'(空)') FROM projects ORDER BY id;")"

  if [[ -z "${rows}" ]]; then
    echo "  （無資料）"
  else
    while IFS='|' read -r id name nas_base nas_subpath; do
      # 去除前後空白
      id="${id// /}"
      name="${name// /}"
      nas_base="${nas_base// /}"
      nas_subpath="${nas_subpath// /}"

      # 標記舊格式：nas_subpath 含有 YYYY-MM-DD 或 nas_base 含有日期
      local flag=""
      if echo "${nas_base}/${nas_subpath}" | grep -qE '[0-9]{4}-[0-9]{2}-[0-9]{2}'; then
        flag="  ← [!] 含日期子目錄"
      fi

      printf "  %-4s  %-20s  %-35s  %-20s%s\n" "${id}" "${name}" "${nas_base}" "${nas_subpath}" "${flag}"
    done <<< "${rows}"
  fi

  echo ""
}

# ── 更新單筆專案路徑 ─────────────────────────────────────────────────────────
update_project() {
  local project_id="$1"
  local new_nas_base="$2"
  local new_nas_subpath="$3"

  local subpath_sql
  if [[ -z "${new_nas_subpath}" ]]; then
    subpath_sql="nas_subpath = ''"
  else
    subpath_sql="nas_subpath = '${new_nas_subpath}'"
  fi

  run_psql "UPDATE projects SET nas_base = '${new_nas_base}', ${subpath_sql} WHERE id = ${project_id};" > /dev/null
  echo "  [OK] 已更新 project id=${project_id}"
  echo "       nas_base    = ${new_nas_base}"
  echo "       nas_subpath = ${new_nas_subpath:-(空)}"
}

# ── 解析參數 ─────────────────────────────────────────────────────────────────
MODE="interactive"
OPT_ID=""
OPT_NAS_BASE=""
OPT_NAS_SUBPATH=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --list)       MODE="list" ;;
    --id)         OPT_ID="$2";         shift ;;
    --nas-base)   OPT_NAS_BASE="$2";   shift ;;
    --nas-subpath) OPT_NAS_SUBPATH="$2"; shift ;;
    -h|--help)
      sed -n '3,9p' "$0" | sed 's/^#//'
      exit 0
      ;;
    *) echo "[錯誤] 未知參數: $1" >&2; exit 1 ;;
  esac
  shift
done

# ── 直接更新模式 ─────────────────────────────────────────────────────────────
if [[ -n "${OPT_ID}" ]]; then
  if [[ -z "${OPT_NAS_BASE}" ]]; then
    echo "[錯誤] --nas-base 為必填" >&2
    exit 1
  fi
  echo ""
  list_projects
  echo "──────────────────────────────────────────────────────────────────────"
  update_project "${OPT_ID}" "${OPT_NAS_BASE}" "${OPT_NAS_SUBPATH}"
  echo ""
  list_projects
  exit 0
fi

# ── 只列出模式 ───────────────────────────────────────────────────────────────
if [[ "${MODE}" == "list" ]]; then
  list_projects
  exit 0
fi

# ── 互動模式 ─────────────────────────────────────────────────────────────────
list_projects

echo "──────────────────────────────────────────────────────────────────────"
echo "  輸入要修改的 project ID（多個以逗號分隔），或直接 Enter 離開："
printf "  > "
read -r input_ids

if [[ -z "${input_ids}" ]]; then
  echo "  （未選擇，結束）"
  exit 0
fi

IFS=',' read -ra ids <<< "${input_ids}"

for raw_id in "${ids[@]}"; do
  proj_id="${raw_id// /}"
  if ! [[ "${proj_id}" =~ ^[0-9]+$ ]]; then
    echo "  [跳過] 無效 ID: ${proj_id}"
    continue
  fi

  # 取得目前值
  current="$(run_psql "SELECT COALESCE(nas_base,''), COALESCE(nas_subpath,'') FROM projects WHERE id = ${proj_id};" | head -1)"
  cur_base="$(echo "${current}" | cut -d'|' -f1)"
  cur_sub="$(echo "${current}" | cut -d'|' -f2)"

  echo ""
  echo "  ── project id=${proj_id} ──────────────────────────────────────────"
  echo "  目前 nas_base    = ${cur_base}"
  echo "  目前 nas_subpath = ${cur_sub:-(空)}"
  echo ""

  printf "  新的 nas_base（Enter 保留目前值）: "
  read -r new_base
  new_base="${new_base:-${cur_base}}"

  printf "  新的 nas_subpath（Enter 保留，輸入 - 清空）: "
  read -r new_sub
  if [[ "${new_sub}" == "-" ]]; then
    new_sub=""
  elif [[ -z "${new_sub}" ]]; then
    new_sub="${cur_sub}"
  fi

  echo ""
  echo "  即將更新："
  echo "    nas_base    : ${cur_base}  →  ${new_base}"
  echo "    nas_subpath : ${cur_sub:-(空)}  →  ${new_sub:-(空)}"
  printf "  確認？ [y/N] "
  read -r confirm
  if [[ "${confirm,,}" != "y" ]]; then
    echo "  （已取消）"
    continue
  fi

  update_project "${proj_id}" "${new_base}" "${new_sub}"
done

echo ""
echo "══════════════════════════════════════════════════════════════════════"
echo "  更新後狀態："
list_projects
