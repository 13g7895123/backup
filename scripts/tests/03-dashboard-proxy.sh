#!/usr/bin/env bash
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_project_env
load_agent_env

section "Test 03: Dashboard Proxy"
print_context

echo ""
echo "[dashboard healthz]"
status="$(http_status GET "${DASHBOARD_LOCAL_URL}/healthz")"
echo "HTTP ${status}"
print_last_body

echo ""
echo "[dashboard /api/disk-usage]"
status="$(http_status GET "${DASHBOARD_LOCAL_URL}/api/disk-usage")"
echo "HTTP ${status}"
print_last_body

echo ""
echo "[dashboard /api/ssh-audit]"
status="$(http_status GET "${DASHBOARD_LOCAL_URL}/api/ssh-audit?since=2026-01-01%2000:00:00")"
echo "HTTP ${status}"
print_last_body

echo ""
echo "[dashboard container env]"
docker compose -f "${ROOT_DIR}/docker-compose.yml" exec -T dashboard env | grep -E '^(AGENT_URL|AGENT_CODE|AGENT_TOKEN|DASHBOARD_ADDR|HOST_PREFIX|NAS_BASE)=' || true
