#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for script in \
  "${SCRIPT_DIR}/01-env-and-service.sh" \
  "${SCRIPT_DIR}/02-agent-direct.sh" \
  "${SCRIPT_DIR}/03-dashboard-proxy.sh" \
  "${SCRIPT_DIR}/04-agent-vs-dashboard-headers.sh"
do
  echo ""
  echo "##### running $(basename "${script}") #####"
  bash "${script}" || true
done
