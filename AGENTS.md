# AGENTS.md

## purpose

- `Backup Manager`
- 管理專案檔案備份、資料庫備份、系統備份、syslog 備份、GCP 同步、還原、agent 心跳與排程

## stack

- Go `1.23`
- PostgreSQL `16`
- `pgx/v5`
- `robfig/cron/v3`
- Docker Compose for `dashboard + postgres`
- systemd for `backup-agent`

## entrypoints

- dashboard: `cmd/dashboard/main.go`
- agent: `cmd/agent/main.go`
- frontend SPA: `cmd/dashboard/web/index.html`

## architecture

- `dashboard`:
  - 提供 UI + API
  - 直接連 PostgreSQL
  - 執行 local backup/schedule
- `agent`:
  - 不直接連 DB
  - 透過 `internal/client/client.go` 呼叫 dashboard `/api/agent/*`
  - 在 host 執行 backup/restore/diagnose/upload
- 共用核心：
  - `internal/backup/runner.go`
  - `internal/scheduler/scheduler.go`

## runtime-model

- 執行位置由 `projects.executor_type` 決定：
  - `local`
  - `agent`
- 指派 agent 由 `projects.executor_agent_id` 決定
- 寫入模式由 `projects.transfer_mode` 決定：
  - `direct`: 直接寫 NAS
  - `upload`: agent 寫 staging，再串流上傳到 dashboard
- agent 視角 NAS 路徑轉換由：
  - `agent_nas_targets`
  - `ResolveProjectNASForAgent()`

## directories

```text
cmd/
internal/api/
internal/backup/
internal/scheduler/
internal/store/
internal/client/
internal/agent/
internal/notify/
migrations/
scripts/
docs/
```

## startup

### dashboard

```bash
bash init.sh
docker compose up -d --build
```

required env:

- `DATABASE_URL`
- `DASHBOARD_ADDR`
- `HOST_PREFIX`
- `NAS_BASE`

### agent

```bash
bash scripts/build-agent.sh
bash scripts/deploy-agent.sh
```

required env:

- `DASHBOARD_URL`
- `AGENT_CODE`
- `AGENT_TOKEN` optional
- `AGENT_ADDR`

## core-data

### tables-core

- `projects`
- `backup_targets`
- `schedules`
- `retention_policies`
- `backup_records`
- `restore_records`

### tables-agent

- `agents`
- `nas_targets`
- `agent_nas_targets`

### tables-other

- `syslog_configs`
- `gcp_configs`
- `api_keys`
- `syslog_api_keys`
- `system_api_keys`

## key-fields

### projects

- `executor_type`
- `executor_agent_id`
- `transfer_mode`
- `nas_base`
- `nas_target_id`
- `nas_subpath`
- `project_path`
- `backup_dirs`
- `db_type`
- `db_host`
- `db_port`
- `db_name`
- `db_user`
- `db_password`
- `db_password_env`
- `docker_db_container`

### backup_targets

- `type`: `files|database|system`
- `config`: JSONB

### schedules

- `cron_expr`
- `target_types`
- `enabled`
- `last_run_at`
- `next_run_at`
- `last_run_status`

### backup_records

- `path`
- `status`
- `checksum`
- `triggered_by`
- `agent_id`
- `agent_name`
- `run_host`
- `log_ref`

### restore_records

- `backup_record_id`
- `strategy`
- `target`
- `status`
- `snapshot_path`

## backup-types

### files

- code: `internal/backup/files.go`
- config fields:
  - `source`
  - `compress`
  - `exclude`

### database

- code: `internal/backup/database.go`
- config fields:
  - `db_type`
  - `host`
  - `port`
  - `name`
  - `user`
  - `password`
  - `password_env`
  - `container_name`

### system

- code: `internal/backup/system.go`
- config fields:
  - `include`
  - `exclude`
  - `backup_packages`
  - `backup_services`

## route-groups

### dashboard-ui

- `GET /`
- `GET /healthz`
- `GET /api/capabilities`

### project-crud

- `/api/projects*`
- `/api/projects/{id}/targets*`
- `/api/projects/{id}/schedules*`
- `/api/projects/{id}/retention`
- `/api/backups*`
- `/api/restore*`

### agent-management

- `/api/agents*`
- `/api/nas-targets`

### agent-internal-contract

- `/api/agent/heartbeat`
- `/api/agent/schedules/*`
- `/api/agent/projects/*`
- `/api/agent/records/*`

### syslog-gcp

- `/api/syslogs*`
- `/api/gcpconfigs*`
- `/api/integrated*`

### external-api

- `/api/admin/*api-keys*`
- `/api/v1/project/*`
- `/api/v1/syslog/*`
- `/api/v1/system/disk`

### agent-local

- `/trigger`
- `/test-backup`
- `/restore`
- `/ssh-audit`
- `/disk-usage`
- `/syslogs/*`
- `/gcp/*`
- `/releases/*`
- `/schedules/{id}/reload`
- `/schedules/{id}/remove`

## hot-files

### api

- route registration: `cmd/dashboard/main.go`
- handlers: `internal/api/*.go`
- agent client sync: `internal/client/client.go`

### backup

- `internal/backup/runner.go`
- `internal/backup/files.go`
- `internal/backup/database.go`
- `internal/backup/system.go`
- `internal/backup/restore.go`

### store

- migrations: `migrations/*.sql`
- models: `internal/store/models.go`
- CRUD: `internal/store/*.go`

### scheduler

- `internal/scheduler/scheduler.go`
- `internal/api/schedules.go`

### agent-nas

- `internal/api/agent.go`
- `internal/api/agents.go`
- `internal/store/agents.go`

### frontend

- `cmd/dashboard/web/index.html`

## workflows

### add-api

1. add/update handler in `internal/api/*.go`
2. register route in `cmd/dashboard/main.go`
3. if agent uses it, sync `internal/client/client.go`
4. run `go test ./...`

### add-schema

1. append new migration to `migrations/`
2. add model fields in `internal/store/models.go`
3. update store CRUD
4. if API affected, update handlers/client
5. run `go test ./...`

### add-backup-feature

1. implement in `internal/backup/*`
2. wire into `internal/backup/runner.go`
3. confirm dashboard + agent both support it
4. if config shape changes, sync target JSON handling
5. test manually + `go test ./...`

### add-agent-feature

1. define dashboard `/api/agent/*` contract
2. update `internal/client/client.go`
3. update `cmd/agent/main.go` local route if needed
4. verify auth headers:
   - `X-Agent-Code`
   - `X-Agent-Token`

## validation

```bash
go test ./...
bash scripts/tests/run-all.sh
```

## do-not-touch

- `internal/client/client.go` without syncing `/api/agent/*`
- migration order in `internal/store/store.go`
- `projects.executor_type`
- `projects.executor_agent_id`
- `projects.transfer_mode`
- `agent_nas_targets`
- `ResolveProjectNASForAgent()`
- `backup_records.path`
- restore overwrite flow
- full rewrite of `cmd/dashboard/web/index.html` without clear plan

## migration-rules

- migrations are hardcoded in `internal/store/store.go`
- append only
- do not reorder
- do not edit old migration unless unavoidable
- SQL must be idempotent
- there is no migration version table

## known-debts

- frontend is single huge HTML file
- migrations rely on rerunnable SQL, no schema version table
- retention auto-delete cron is disabled
- Slack success notify exists but main flow only sends failure
- capability API `build_time` is request time, not real build metadata
- syslog/gcp/release/restore logic is split across dashboard + agent
- repo root contains built artifacts:
  - `agent`
  - `dashboard`
  - `backup-agent`
- many features depend on external commands:
  - `pg_dump`
  - `mysqldump`
  - `tar`
  - `systemctl`
  - `journalctl`
  - `ssh`
  - `rsync`

## reading-order

1. `cmd/dashboard/main.go`
2. `cmd/agent/main.go`
3. `internal/backup/runner.go`
4. `internal/scheduler/scheduler.go`
5. `internal/store/models.go`
6. `internal/client/client.go`
7. target feature file in `internal/api/*.go`
8. `cmd/dashboard/web/index.html` if UI work is needed
