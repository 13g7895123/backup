#!/usr/bin/env bash
# scripts/check-agent-binary.sh
# 檢查 VM 上 backup-agent binary / env / service 狀態，並可互動式修補。
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

BINARY_DST="/usr/local/bin/backup-agent"
SERVICE_NAME="backup-agent"
SERVICE_DST="/etc/systemd/system/${SERVICE_NAME}.service"
ENV_DIR="/etc/backup-agent"
ENV_FILE="${ENV_DIR}/env"
LOCAL_BINARY_CANDIDATES=(
  "${SCRIPT_DIR}/backup-agent-linux-amd64"
  "${SCRIPT_DIR}/backup-agent"
  "${ROOT_DIR}/backup-agent"
)
LOCAL_SERVICE_CANDIDATES=(
  "${SCRIPT_DIR}/backup-agent.service"
  "${ROOT_DIR}/scripts/backup-agent.service"
)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

CHECK_ERRORS=0
CHECK_WARNINGS=0
FIXABLE_ISSUES=()

RUN_FIXES=1
if [[ "${1:-}" == "--check-only" ]]; then
  RUN_FIXES=0
fi

ok() { echo -e "  ${GREEN}[OK]${NC}  $*"; }
warn() { echo -e "  ${YELLOW}[WARN]${NC} $*"; }
fail() { echo -e "  ${RED}[FAIL]${NC} $*"; }
info() { echo -e "  ${BLUE}[INFO]${NC} $*"; }

record_fixable() {
  FIXABLE_ISSUES+=("$1")
}

reset_results() {
  CHECK_ERRORS=0
  CHECK_WARNINGS=0
  FIXABLE_ISSUES=()
}

parse_env_file() {
  local key="$1"
  [[ -f "$ENV_FILE" ]] || return 0
  grep -E "^${key}=" "$ENV_FILE" | head -1 | sed 's/#.*//' | cut -d= -f2- | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

run_root() {
  if [[ "${EUID}" -eq 0 ]]; then
    "$@"
    return
  fi
  if command_exists sudo; then
    sudo "$@"
    return
  fi
  echo "[error] 需要 root 權限執行：$*" >&2
  return 1
}

prompt_yes_no() {
  local prompt="$1"
  local default="${2:-Y}"
  local answer=""
  while true; do
    read -r -p "$prompt " answer || return 1
    answer="${answer:-$default}"
    case "$answer" in
      Y|y|yes|YES) return 0 ;;
      N|n|no|NO) return 1 ;;
      *) echo "請輸入 y 或 n" ;;
    esac
  done
}

prompt_value() {
  local prompt="$1"
  local default="${2:-}"
  local answer=""
  if [[ -n "$default" ]]; then
    read -r -p "$prompt [$default]: " answer || return 1
    echo "${answer:-$default}"
  else
    read -r -p "$prompt: " answer || return 1
    echo "$answer"
  fi
}

find_local_binary() {
  local path
  for path in "${LOCAL_BINARY_CANDIDATES[@]}"; do
    if [[ -f "$path" ]]; then
      echo "$path"
      return 0
    fi
  done
  return 1
}

find_local_service() {
  local path
  for path in "${LOCAL_SERVICE_CANDIDATES[@]}"; do
    if [[ -f "$path" ]]; then
      echo "$path"
      return 0
    fi
  done
  return 1
}

upsert_env_key() {
  local key="$1"
  local value="$2"
  local tmp
  tmp="$(mktemp)"
  if [[ -f "$ENV_FILE" ]]; then
    cp "$ENV_FILE" "$tmp"
  fi

  if grep -qE "^${key}=" "$tmp" 2>/dev/null; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$tmp"
  else
    printf '%s=%s\n' "$key" "$value" >>"$tmp"
  fi

  run_root mkdir -p "$ENV_DIR"
  run_root cp "$tmp" "$ENV_FILE"
  run_root chmod 600 "$ENV_FILE"
  rm -f "$tmp"
}

install_binary_interactive() {
  local src="${1:-}"
  if [[ -z "$src" ]]; then
    if ! src="$(find_local_binary)"; then
      warn "找不到可安裝的本地 binary。請先將 backup-agent 放在 release 目錄或專案根目錄。"
      return 1
    fi
  fi
  info "安裝 binary: $src -> $BINARY_DST"
  run_root cp "$src" "$BINARY_DST" || return 1
  run_root chmod 755 "$BINARY_DST"
  return 0
}

install_service_interactive() {
  local service_src="${1:-}"
  if [[ -z "$service_src" ]]; then
    if ! service_src="$(find_local_service)"; then
      warn "找不到可安裝的 service 檔。"
      return 1
    fi
  fi
  info "安裝 service: $service_src -> $SERVICE_DST"
  run_root cp "$service_src" "$SERVICE_DST" || return 1
  run_root systemctl daemon-reload || return 1
  run_root systemctl enable "$SERVICE_NAME" || return 1
  return 0
}

configure_env_interactive() {
  local current_dashboard current_code current_token current_addr current_host_prefix current_nas_base current_slack
  current_dashboard="$(parse_env_file DASHBOARD_URL)"
  current_code="$(parse_env_file AGENT_CODE)"
  current_token="$(parse_env_file AGENT_TOKEN)"
  current_addr="$(parse_env_file AGENT_ADDR)"
  current_host_prefix="$(parse_env_file HOST_PREFIX)"
  current_nas_base="$(parse_env_file NAS_BASE)"
  current_slack="$(parse_env_file SLACK_WEBHOOK_URL)"

  local dashboard_url agent_code agent_token agent_addr host_prefix nas_base slack_webhook
  dashboard_url="$(prompt_value "DASHBOARD_URL" "${current_dashboard:-http://127.0.0.1:8105}")" || return 1
  agent_code="$(prompt_value "AGENT_CODE" "${current_code:-vm-app-01}")" || return 1
  agent_token="$(prompt_value "AGENT_TOKEN（可留空）" "${current_token}")" || return 1
  agent_addr="$(prompt_value "AGENT_ADDR" "${current_addr:-:9090}")" || return 1
  host_prefix="$(prompt_value "HOST_PREFIX（可留空）" "${current_host_prefix}")" || return 1
  nas_base="$(prompt_value "NAS_BASE" "${current_nas_base:-/mnt/nas/backups}")" || return 1
  slack_webhook="$(prompt_value "SLACK_WEBHOOK_URL（可留空）" "${current_slack}")" || return 1

  upsert_env_key "DASHBOARD_URL" "$dashboard_url" || return 1
  upsert_env_key "AGENT_CODE" "$agent_code" || return 1
  upsert_env_key "AGENT_TOKEN" "$agent_token" || return 1
  upsert_env_key "AGENT_ADDR" "$agent_addr" || return 1
  upsert_env_key "HOST_PREFIX" "$host_prefix" || return 1
  upsert_env_key "NAS_BASE" "$nas_base" || return 1
  upsert_env_key "SLACK_WEBHOOK_URL" "$slack_webhook" || return 1
  ok "已更新 $ENV_FILE"
}

restart_service_interactive() {
  info "重新啟動 ${SERVICE_NAME}.service"
  run_root systemctl daemon-reload || return 1
  run_root systemctl restart "$SERVICE_NAME" || return 1
  return 0
}

check_binary() {
  echo ""
  echo "【1】Binary 狀態"
  if [[ ! -f "$BINARY_DST" ]]; then
    fail "找不到 $BINARY_DST"
    CHECK_ERRORS=$((CHECK_ERRORS + 1))
    record_fixable "binary_missing"
    return
  fi

  ok "binary 存在：$BINARY_DST"
  if [[ ! -x "$BINARY_DST" ]]; then
    fail "binary 沒有執行權限"
    CHECK_ERRORS=$((CHECK_ERRORS + 1))
    record_fixable "binary_not_executable"
  else
    ok "binary 具備執行權限"
  fi

  if command_exists stat; then
    echo "  大小: $(stat -c '%s bytes' "$BINARY_DST" 2>/dev/null || echo unknown)"
    echo "  修改時間: $(stat -c '%y' "$BINARY_DST" 2>/dev/null || echo unknown)"
  fi
  if command_exists sha256sum; then
    echo "  SHA256: $(sha256sum "$BINARY_DST" | awk '{print $1}')"
  fi
  if command_exists file; then
    echo "  型態: $(file -b "$BINARY_DST")"
  fi
}

check_env() {
  echo ""
  echo "【2】環境設定"
  if [[ ! -f "$ENV_FILE" ]]; then
    fail "找不到 $ENV_FILE"
    CHECK_ERRORS=$((CHECK_ERRORS + 1))
    record_fixable "env_missing"
    return
  fi

  ok "env 檔存在：$ENV_FILE"
  local dashboard_url agent_code agent_token agent_addr host_prefix nas_base
  dashboard_url="$(parse_env_file DASHBOARD_URL)"
  agent_code="$(parse_env_file AGENT_CODE)"
  agent_token="$(parse_env_file AGENT_TOKEN)"
  agent_addr="$(parse_env_file AGENT_ADDR)"
  host_prefix="$(parse_env_file HOST_PREFIX)"
  nas_base="$(parse_env_file NAS_BASE)"

  echo "  DASHBOARD_URL=${dashboard_url:-(未設定)}"
  echo "  AGENT_CODE=${agent_code:-(未設定)}"
  echo "  AGENT_TOKEN=$([[ -n "$agent_token" ]] && echo '(已設定)' || echo '(未設定，可選)')"
  echo "  AGENT_ADDR=${agent_addr:-(未設定)}"
  echo "  HOST_PREFIX=${host_prefix:-}"
  echo "  NAS_BASE=${nas_base:-(未設定)}"

  if [[ -z "$dashboard_url" || -z "$agent_code" || -z "$agent_addr" || -z "$nas_base" ]]; then
    fail "必要 env 缺少 DASHBOARD_URL / AGENT_CODE / AGENT_ADDR / NAS_BASE"
    CHECK_ERRORS=$((CHECK_ERRORS + 1))
    record_fixable "env_incomplete"
  else
    ok "必要 env 已齊全"
  fi
}

check_service() {
  echo ""
  echo "【3】systemd service"
  if [[ ! -f "$SERVICE_DST" ]]; then
    fail "找不到 $SERVICE_DST"
    CHECK_ERRORS=$((CHECK_ERRORS + 1))
    record_fixable "service_missing"
    return
  fi

  ok "service 檔存在：$SERVICE_DST"

  if command_exists systemctl; then
    if systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
      ok "${SERVICE_NAME}.service 已啟用"
    else
      warn "${SERVICE_NAME}.service 尚未 enable"
      CHECK_WARNINGS=$((CHECK_WARNINGS + 1))
      record_fixable "service_not_enabled"
    fi

    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
      ok "${SERVICE_NAME}.service 正在運行"
    else
      fail "${SERVICE_NAME}.service 未運行"
      CHECK_ERRORS=$((CHECK_ERRORS + 1))
      record_fixable "service_not_running"
      echo "  ActiveState: $(systemctl show "$SERVICE_NAME" --property=ActiveState --value 2>/dev/null || echo unknown)"
      echo "  SubState: $(systemctl show "$SERVICE_NAME" --property=SubState --value 2>/dev/null || echo unknown)"
      if command_exists journalctl; then
        echo "  最近 5 行日誌:"
        journalctl -u "$SERVICE_NAME" -n 5 --no-pager 2>/dev/null | sed 's/^/    /'
      fi
    fi
  else
    warn "系統沒有 systemctl，略過服務狀態檢查"
    CHECK_WARNINGS=$((CHECK_WARNINGS + 1))
  fi
}

check_runtime() {
  echo ""
  echo "【4】Runtime / API"
  local agent_addr dashboard_url agent_code agent_token health_url api_url
  agent_addr="$(parse_env_file AGENT_ADDR)"
  dashboard_url="$(parse_env_file DASHBOARD_URL)"
  agent_code="$(parse_env_file AGENT_CODE)"
  agent_token="$(parse_env_file AGENT_TOKEN)"

  if ! command_exists curl; then
    warn "找不到 curl，略過 health/API 檢查"
    CHECK_WARNINGS=$((CHECK_WARNINGS + 1))
    return
  fi

  if [[ -n "$agent_addr" ]]; then
    local host_port="${agent_addr#:}"
    if [[ "$agent_addr" == :* && "$host_port" =~ ^[0-9]+$ ]]; then
      health_url="http://127.0.0.1:${host_port}/healthz"
      local health_code
      health_code="$(curl -s -o /dev/null -w '%{http_code}' "$health_url" 2>/dev/null || echo FAIL)"
      if [[ "$health_code" == "200" ]]; then
        ok "agent healthz 正常：$health_url"
      else
        warn "agent healthz 異常：$health_url -> $health_code"
        CHECK_WARNINGS=$((CHECK_WARNINGS + 1))
      fi
    else
      warn "AGENT_ADDR 不是 :port 格式，略過本機 healthz 檢查"
      CHECK_WARNINGS=$((CHECK_WARNINGS + 1))
    fi
  fi

  if [[ -z "$dashboard_url" || -z "$agent_code" ]]; then
    warn "缺少 DASHBOARD_URL 或 AGENT_CODE，略過 dashboard API 檢查"
    CHECK_WARNINGS=$((CHECK_WARNINGS + 1))
    return
  fi

  api_url="${dashboard_url%/}/api/agent/schedules/enabled"
  local curl_args=(
    -s
    -o /dev/null
    -w '%{http_code}'
    -H "X-Agent-Code: ${agent_code}"
  )
  if [[ -n "$agent_token" ]]; then
    curl_args+=(-H "X-Agent-Token: ${agent_token}")
  fi
  local api_code
  api_code="$(curl "${curl_args[@]}" "$api_url" 2>/dev/null || echo FAIL)"
  case "$api_code" in
    200)
      ok "dashboard API 認證正常：$api_url"
      ;;
    401|403)
      fail "dashboard API 認證失敗：HTTP $api_code"
      CHECK_ERRORS=$((CHECK_ERRORS + 1))
      record_fixable "dashboard_auth_failed"
      ;;
    000|FAIL)
      fail "無法連線 dashboard API：$api_url"
      CHECK_ERRORS=$((CHECK_ERRORS + 1))
      record_fixable "dashboard_unreachable"
      ;;
    *)
      warn "dashboard API 回應 HTTP $api_code"
      CHECK_WARNINGS=$((CHECK_WARNINGS + 1))
      ;;
  esac
}

print_summary() {
  echo ""
  echo "======================================"
  if [[ "$CHECK_ERRORS" -eq 0 ]]; then
    echo -e "  ${GREEN}檢查完成：沒有阻斷性問題${NC}"
  else
    echo -e "  ${RED}檢查完成：${CHECK_ERRORS} 個錯誤${NC}"
  fi
  if [[ "$CHECK_WARNINGS" -gt 0 ]]; then
    echo -e "  ${YELLOW}另外有 ${CHECK_WARNINGS} 個警告${NC}"
  fi
  echo "======================================"
}

run_all_checks() {
  reset_results
  echo "======================================"
  echo " backup-agent binary 檢查工具"
  echo "======================================"
  check_binary
  check_env
  check_service
  check_runtime
  print_summary
}

fix_issues_interactive() {
  local needs_restart=0
  local binary_src service_src

  if [[ "${#FIXABLE_ISSUES[@]}" -eq 0 ]]; then
    info "沒有可自動修補的項目"
    return 0
  fi

  echo ""
  echo "可修補項目："
  printf '  - %s\n' "${FIXABLE_ISSUES[@]}"

  if printf '%s\n' "${FIXABLE_ISSUES[@]}" | grep -Eq 'binary_missing|binary_not_executable'; then
    if binary_src="$(find_local_binary)"; then
      info "找到本地 binary：$binary_src"
    else
      info "未找到本地 binary 候選，可稍後手動指定"
    fi
    if prompt_yes_no "是否安裝或覆蓋 backup-agent binary？ [Y/n]" "Y"; then
      if [[ -z "${binary_src:-}" ]]; then
        binary_src="$(prompt_value "請輸入 binary 路徑")" || return 1
      fi
      install_binary_interactive "$binary_src" || return 1
      needs_restart=1
    fi
  fi

  if printf '%s\n' "${FIXABLE_ISSUES[@]}" | grep -Eq 'env_missing|env_incomplete|dashboard_auth_failed|dashboard_unreachable'; then
    if prompt_yes_no "是否互動式更新 agent env？ [Y/n]" "Y"; then
      configure_env_interactive || return 1
      needs_restart=1
    fi
  fi

  if printf '%s\n' "${FIXABLE_ISSUES[@]}" | grep -Eq 'service_missing|service_not_enabled'; then
    if service_src="$(find_local_service)"; then
      info "找到本地 service 檔：$service_src"
    fi
    if prompt_yes_no "是否安裝或更新 systemd service？ [Y/n]" "Y"; then
      if [[ -z "${service_src:-}" ]]; then
        service_src="$(prompt_value "請輸入 service 檔路徑")" || return 1
      fi
      install_service_interactive "$service_src" || return 1
      needs_restart=1
    fi
  fi

  if printf '%s\n' "${FIXABLE_ISSUES[@]}" | grep -Eq 'service_not_running'; then
    if prompt_yes_no "是否現在啟動或重啟 backup-agent.service？ [Y/n]" "Y"; then
      restart_service_interactive || return 1
      needs_restart=0
    fi
  elif [[ "$needs_restart" -eq 1 ]]; then
    if prompt_yes_no "已更新檔案，是否現在重啟 backup-agent.service？ [Y/n]" "Y"; then
      restart_service_interactive || return 1
    fi
  fi
}

main() {
  run_all_checks

  if [[ "$RUN_FIXES" -eq 1 && "$CHECK_ERRORS" -gt 0 ]]; then
    echo ""
    if prompt_yes_no "是否進行互動式修補？ [Y/n]" "Y"; then
      fix_issues_interactive || exit 1
      echo ""
      info "重新檢查狀態 ..."
      run_all_checks
    fi
  fi
}

main "$@"
