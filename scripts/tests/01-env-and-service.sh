#!/usr/bin/env bash
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_project_env
load_agent_env

section "Test 01: Env And Service"
print_context

if [[ -f "${PROJECT_ENV}" ]]; then
  ok "dashboard .env exists"
else
  fail "dashboard .env missing: ${PROJECT_ENV}"
fi

if [[ -f "${AGENT_ENV}" ]]; then
  ok "agent env exists"
else
  fail "agent env missing: ${AGENT_ENV}"
fi

echo ""
echo "[dashboard .env]"
echo "DASHBOARD_PORT=${DASHBOARD_PORT_VALUE:-}"
echo "AGENT_URL=${AGENT_URL_VALUE:-}"
echo "AGENT_CODE=${PROJECT_AGENT_CODE_VALUE:-}"
echo "AGENT_TOKEN=${PROJECT_AGENT_TOKEN_VALUE:+(set)}"

echo ""
echo "[/etc/backup-agent/env]"
echo "DASHBOARD_URL=${DASHBOARD_URL_VALUE:-}"
echo "AGENT_CODE=${AGENT_CODE_VALUE:-}"
echo "AGENT_TOKEN=${AGENT_TOKEN_VALUE:+(set)}"
echo "AGENT_ADDR=${AGENT_ADDR_VALUE:-}"

echo ""
echo "[systemctl]"
systemctl status backup-agent --no-pager || true

echo ""
echo "[recent journal]"
journalctl -u backup-agent -n 50 --no-pager || true
