#!/usr/bin/env bash
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_project_env
load_agent_env

section "Test 02: Agent Direct"
print_context

if ! systemctl is-active --quiet backup-agent; then
  echo ""
  warn "backup-agent.service is inactive; direct agent checks will fail until it is started"
fi

echo ""
echo "[healthz]"
status="$(http_status GET "${AGENT_LOCAL_URL}/healthz")"
echo "HTTP ${status}"
print_last_body

echo ""
echo "[schedules/enabled with code+token]"
curl_args=(-H "X-Agent-Code: ${AGENT_CODE_VALUE}")
if [[ -n "${AGENT_TOKEN_VALUE}" ]]; then
  curl_args+=(-H "X-Agent-Token: ${AGENT_TOKEN_VALUE}")
fi
status="$(http_status GET "${AGENT_LOCAL_URL}/api/agent/schedules/enabled" "${curl_args[@]}")"
echo "HTTP ${status}"
print_last_body

echo ""
echo "[disk-usage with code+token]"
disk_args=(-H "X-Agent-Code: ${AGENT_CODE_VALUE}")
if [[ -n "${AGENT_TOKEN_VALUE}" ]]; then
  disk_args+=(-H "X-Agent-Token: ${AGENT_TOKEN_VALUE}")
fi
status="$(http_status GET "${AGENT_LOCAL_URL}/disk-usage" "${disk_args[@]}")"
echo "HTTP ${status}"
print_last_body
