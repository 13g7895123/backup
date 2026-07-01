#!/usr/bin/env bash
set -u

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${TEST_DIR}/../.." && pwd)"
PROJECT_ENV="${ROOT_DIR}/.env"
AGENT_ENV="/etc/backup-agent/env"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

ok()   { echo -e "${GREEN}[OK]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
info() { echo -e "${BLUE}[INFO]${NC} $*"; }

section() {
  echo ""
  echo "=========================================="
  echo "$1"
  echo "=========================================="
}

parse_env_value() {
  local file="$1"
  local key="$2"
  if [[ ! -f "$file" ]]; then
    return 0
  fi
  grep -E "^${key}=" "$file" | head -1 | sed 's/#.*//' | cut -d= -f2- | xargs || true
}

load_agent_env() {
  AGENT_CODE_VALUE="$(parse_env_value "${AGENT_ENV}" "AGENT_CODE")"
  AGENT_TOKEN_VALUE="$(parse_env_value "${AGENT_ENV}" "AGENT_TOKEN")"
  AGENT_ADDR_VALUE="$(parse_env_value "${AGENT_ENV}" "AGENT_ADDR")"
  DASHBOARD_URL_VALUE="$(parse_env_value "${AGENT_ENV}" "DASHBOARD_URL")"
  if [[ -z "${AGENT_ADDR_VALUE}" ]]; then
    AGENT_ADDR_VALUE=":9090"
  fi
  AGENT_LOCAL_URL="http://127.0.0.1${AGENT_ADDR_VALUE}"
}

load_project_env() {
  DASHBOARD_PORT_VALUE="$(parse_env_value "${PROJECT_ENV}" "DASHBOARD_PORT")"
  AGENT_URL_VALUE="$(parse_env_value "${PROJECT_ENV}" "AGENT_URL")"
  PROJECT_AGENT_CODE_VALUE="$(parse_env_value "${PROJECT_ENV}" "AGENT_CODE")"
  PROJECT_AGENT_TOKEN_VALUE="$(parse_env_value "${PROJECT_ENV}" "AGENT_TOKEN")"
  if [[ -z "${DASHBOARD_PORT_VALUE}" ]]; then
    DASHBOARD_PORT_VALUE="8105"
  fi
  DASHBOARD_LOCAL_URL="http://127.0.0.1:${DASHBOARD_PORT_VALUE}"
}

print_context() {
  info "ROOT_DIR=${ROOT_DIR}"
  info "PROJECT_ENV=${PROJECT_ENV}"
  info "AGENT_ENV=${AGENT_ENV}"
  info "DASHBOARD_LOCAL_URL=${DASHBOARD_LOCAL_URL:-unknown}"
  info "AGENT_LOCAL_URL=${AGENT_LOCAL_URL:-unknown}"
}

http_status() {
  local method="$1"
  local url="$2"
  shift 2
  curl -sS -o /tmp/backup-agent-test-body.$$ -w "%{http_code}" -X "$method" "$url" "$@" || echo "CURL_ERROR"
}

print_last_body() {
  if [[ -f /tmp/backup-agent-test-body.$$ ]]; then
    echo "--- body ---"
    cat /tmp/backup-agent-test-body.$$
    echo ""
    rm -f /tmp/backup-agent-test-body.$$
  fi
}
