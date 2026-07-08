#!/usr/bin/env bash

set -u

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-${ROOT_DIR}/docker-compose.yml}"
ENV_FILE="${ROOT_DIR}/.env"
NOW="$(date '+%Y-%m-%d %H:%M:%S %Z')"

section() {
  echo
  echo "===== $* ====="
}

run() {
  local desc="$1"
  shift
  echo "--- ${desc}"
  "$@" 2>&1 || echo "[warn] command failed: $*"
}

run_shell() {
  local desc="$1"
  local cmd="$2"
  echo "--- ${desc}"
  bash -lc "$cmd" 2>&1 || echo "[warn] command failed: $cmd"
}

get_dashboard_port() {
  if [[ -f "${ENV_FILE}" ]]; then
    grep -E '^DASHBOARD_PORT=' "${ENV_FILE}" | tail -1 | cut -d= -f2- | tr -d '\r\n'
    return
  fi
  echo ""
}

dashboard_cid() {
  docker compose -f "${COMPOSE_FILE}" ps -q dashboard 2>/dev/null | head -1
}

postgres_cid() {
  docker compose -f "${COMPOSE_FILE}" ps -q postgres 2>/dev/null | head -1
}

section "meta"
echo "timestamp=${NOW}"
echo "root_dir=${ROOT_DIR}"
echo "compose_file=${COMPOSE_FILE}"
echo "env_file=${ENV_FILE}"
echo "hostname=$(hostname)"
echo "whoami=$(whoami)"

section "host time"
run "date -R" date -R
run "timedatectl" timedatectl
run "uptime" uptime

section "docker compose status"
run "docker compose ps" docker compose -f "${COMPOSE_FILE}" ps
run "docker ps" docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
run "docker network ls" docker network ls

DB_PORT="$(get_dashboard_port)"
if [[ -z "${DB_PORT}" ]]; then
  DB_PORT="8080"
fi

section "dashboard health"
echo "dashboard_port=${DB_PORT}"
run_shell "curl healthz" "curl -i --max-time 10 http://127.0.0.1:${DB_PORT}/healthz"

DASHBOARD_CID="$(dashboard_cid)"
POSTGRES_CID="$(postgres_cid)"

section "container ids"
echo "dashboard_cid=${DASHBOARD_CID:-<none>}"
echo "postgres_cid=${POSTGRES_CID:-<none>}"

if [[ -n "${DASHBOARD_CID}" ]]; then
  section "dashboard container"
  run "dashboard inspect state" docker inspect "${DASHBOARD_CID}" --format '{{json .State}}'
  run "dashboard inspect networks" docker inspect "${DASHBOARD_CID}" --format '{{json .NetworkSettings.Networks}}'
  run "dashboard env time vars" docker compose -f "${COMPOSE_FILE}" exec -T dashboard sh -lc 'env | sort | grep -E "^(TZ|DASHBOARD_ADDR|HOST_PREFIX|NAS_BASE|CORS_ORIGINS)="'
  run "dashboard date" docker compose -f "${COMPOSE_FILE}" exec -T dashboard date -R
  run "dashboard listen ports" docker compose -f "${COMPOSE_FILE}" exec -T dashboard sh -lc 'ss -ltnp || netstat -ltnp || true'
  run "dashboard can reach postgres:5432" docker compose -f "${COMPOSE_FILE}" exec -T dashboard sh -lc 'getent hosts postgres || true; (echo > /dev/tcp/postgres/5432 && echo tcp_ok) || echo tcp_fail'
fi

if [[ -n "${POSTGRES_CID}" ]]; then
  section "postgres container"
  run "postgres inspect state" docker inspect "${POSTGRES_CID}" --format '{{json .State}}'
  run "postgres inspect networks" docker inspect "${POSTGRES_CID}" --format '{{json .NetworkSettings.Networks}}'
  run "postgres date" docker compose -f "${COMPOSE_FILE}" exec -T postgres date -R
  run "pg_isready" docker compose -f "${COMPOSE_FILE}" exec -T postgres pg_isready -U backup -d backup_manager

  section "database: schedules"
  run_shell "schedules summary" "docker compose -f '${COMPOSE_FILE}' exec -T postgres psql -U backup -d backup_manager -P pager=off -c \"SELECT s.id, s.project_id, p.name AS project_name, p.enabled AS project_enabled, p.executor_type, p.executor_agent_id, s.label, s.cron_expr, s.enabled AS schedule_enabled, s.last_run_at, s.next_run_at, s.last_run_status FROM schedules s JOIN projects p ON p.id = s.project_id ORDER BY s.id;\""

  section "database: local enabled schedules"
  run_shell "local enabled schedules" "docker compose -f '${COMPOSE_FILE}' exec -T postgres psql -U backup -d backup_manager -P pager=off -c \"SELECT s.id, p.name, s.cron_expr, s.enabled, p.enabled, p.executor_type, s.last_run_at, s.next_run_at, s.last_run_status FROM schedules s JOIN projects p ON p.id = s.project_id WHERE s.enabled = true AND p.enabled = true AND p.executor_type = 'local' ORDER BY s.id;\""

  section "database: recent backup records"
  run_shell "recent backup records" "docker compose -f '${COMPOSE_FILE}' exec -T postgres psql -U backup -d backup_manager -P pager=off -c \"SELECT id, project_id, type, status, triggered_by, agent_id, run_host, created_at, started_at, completed_at FROM backup_records ORDER BY id DESC LIMIT 20;\""

  section "database: recent failed backup records"
  run_shell "recent failed backup records" "docker compose -f '${COMPOSE_FILE}' exec -T postgres psql -U backup -d backup_manager -P pager=off -c \"SELECT id, project_id, type, status, triggered_by, LEFT(COALESCE(error_msg,''), 200) AS error_msg, created_at FROM backup_records WHERE status = 'failed' ORDER BY id DESC LIMIT 20;\""
fi

section "dashboard logs"
run "dashboard logs last 200" docker compose -f "${COMPOSE_FILE}" logs --tail=200 dashboard
run_shell "dashboard logs filtered scheduler" "docker compose -f '${COMPOSE_FILE}' logs dashboard 2>&1 | grep -Ei '\\[scheduler\\]|schedule|cron|healthz' | tail -200"

section "agent service"
if systemctl list-unit-files 2>/dev/null | grep -q '^backup-agent.service'; then
  run "systemctl status backup-agent" systemctl status backup-agent --no-pager
  run "journalctl backup-agent last 200" journalctl -u backup-agent -n 200 --no-pager
else
  echo "backup-agent.service not installed on this host"
fi

section "nginx / reverse proxy quick view"
run "ss listen 80/443/8080/8105/9090" bash -lc "ss -ltnp | grep -E ':(80|443|8080|8105|9090)\\b' || true"

section "done"
echo "請把完整輸出提供回來，至少包含："
echo "1. docker compose ps"
echo "2. database: schedules"
echo "3. dashboard logs filtered scheduler"
echo "4. agent service"
