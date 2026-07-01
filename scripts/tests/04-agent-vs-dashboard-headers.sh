#!/usr/bin/env bash
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_project_env
load_agent_env

section "Test 04: Header Behavior"
print_context

if ! systemctl is-active --quiet backup-agent; then
  echo ""
  warn "backup-agent.service is inactive; header checks will fail until it is started"
fi

echo ""
echo "[agent /disk-usage without headers]"
status="$(http_status GET "${AGENT_LOCAL_URL}/disk-usage")"
echo "HTTP ${status}"
print_last_body

echo ""
echo "[agent /disk-usage token only]"
if [[ -n "${AGENT_TOKEN_VALUE}" ]]; then
  status="$(http_status GET "${AGENT_LOCAL_URL}/disk-usage" -H "X-Agent-Token: ${AGENT_TOKEN_VALUE}")"
  echo "HTTP ${status}"
  print_last_body
else
  warn "AGENT_TOKEN empty, skip token-only request"
fi

echo ""
echo "[agent /disk-usage code only]"
status="$(http_status GET "${AGENT_LOCAL_URL}/disk-usage" -H "X-Agent-Code: ${AGENT_CODE_VALUE}")"
echo "HTTP ${status}"
print_last_body

echo ""
echo "[agent /disk-usage code+token]"
args=(-H "X-Agent-Code: ${AGENT_CODE_VALUE}")
if [[ -n "${AGENT_TOKEN_VALUE}" ]]; then
  args+=(-H "X-Agent-Token: ${AGENT_TOKEN_VALUE}")
fi
status="$(http_status GET "${AGENT_LOCAL_URL}/disk-usage" "${args[@]}")"
echo "HTTP ${status}"
print_last_body
