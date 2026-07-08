# 備份與還原操作指南

> 適用於目前 `Backup Manager` 的實作版本，涵蓋 `dashboard`、`backup-agent`、`direct/upload` 傳輸模式、手動備份、smoke backup、還原流程與常見排錯。

---

## 目錄

1. [適用範圍](#適用範圍)
2. [系統角色與責任](#系統角色與責任)
3. [支援的備份類型](#支援的備份類型)
4. [執行模式與傳輸模式](#執行模式與傳輸模式)
5. [備份資料流](#備份資料流)
6. [還原資料流](#還原資料流)
7. [關鍵資料表](#關鍵資料表)
8. [前置條件](#前置條件)
9. [建立可備份專案](#建立可備份專案)
10. [手動觸發備份](#手動觸發備份)
11. [smoke backup](#smoke-backup)
12. [檢視備份結果](#檢視備份結果)
13. [還原操作](#還原操作)
14. [API 一覽](#api-一覽)
15. [檔案與目錄慣例](#檔案與目錄慣例)
16. [風險與限制](#風險與限制)
17. [排錯指南](#排錯指南)
18. [建議維運流程](#建議維運流程)
19. [實作對照](#實作對照)

---

## 適用範圍

本文件說明的是這個 repo 目前已實作的備份與還原能力，不是抽象設計稿。重點包括：

- `files` / `database` / `system` 三種備份類型
- `local` 與 `agent` 兩種執行位置
- `direct` 與 `upload` 兩種備份檔傳輸模式
- dashboard 端還原觸發
- agent 端實際還原執行
- 還原歷史與備份紀錄的觀察方式

如果你要看較偏設計階段的資料流與背景，可再搭配：

- [docs/upload-transfer-restore-design.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/upload-transfer-restore-design.zh-tw.md)
- [docs/phase7-upload-transfer-restore.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/phase7-upload-transfer-restore.zh-tw.md)
- [docs/multi-agent-vm-backup-design.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/multi-agent-vm-backup-design.zh-tw.md)

---

## 系統角色與責任

### dashboard

- 提供 UI 與 API
- 直接連 PostgreSQL
- 在 `executor_type=local` 時執行備份
- 在 `transfer_mode=upload` 時接收 agent 上傳的備份檔並寫入 NAS
- 在還原時負責驗證 record、建立 `restore_records`、轉發到 agent 或本機執行

入口：

- [cmd/dashboard/main.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/cmd/dashboard/main.go)

### backup-agent

- 不直接連 PostgreSQL
- 透過 dashboard API 讀取 project / target / schedule / record
- 在 host 上執行檔案打包、DB dump、還原、診斷
- 在 `transfer_mode=upload` 時，把暫存備份檔上傳給 dashboard

入口：

- [cmd/agent/main.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/cmd/agent/main.go)

### 共用核心

- `Runner`：決定如何跑單次備份與如何寫 `backup_records`
- `Scheduler`：決定何時執行備份

核心檔案：

- [internal/backup/runner.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/runner.go)
- [internal/scheduler/scheduler.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/scheduler/scheduler.go)

---

## 支援的備份類型

### files

- 用途：備份專案目錄、上傳目錄、設定檔目錄
- 輸出格式：`tar.gz`
- 主要設定：
  - `source`
  - `compress`
  - `exclude`

實作：

- [internal/backup/files.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/files.go)

### database

- 用途：備份 PostgreSQL 或 MySQL
- 輸出格式：`.sql.gz`
- 支援兩種來源：
  - 直連 DB host/port
  - `docker exec` 到 container 內執行 dump

主要設定：

- `db_type`
- `host`
- `port`
- `name`
- `user`
- `password`
- `password_env`
- `container_name`

實作：

- [internal/backup/database.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/database.go)
- [internal/backup/database_exec.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/database_exec.go)

### system

- 用途：備份系統檔案、套件清單、服務清單
- 輸出格式：`tar.gz`
- 常用於主機狀態快照，而不是應用程式資料主備份

實作：

- [internal/backup/system.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/system.go)

---

## 執行模式與傳輸模式

這兩件事要分開看。

### 執行模式：`projects.executor_type`

- `local`
  - 由 dashboard 容器執行備份
- `agent`
  - 由指定的 agent host 執行備份

### 傳輸模式：`projects.transfer_mode`

- `direct`
  - 執行備份的那一端直接寫 NAS
  - 適合 agent 主機本身掛得到 NAS
- `upload`
  - agent 先產出到本機暫存，再上傳到 dashboard
  - dashboard 再把檔案落地到 NAS
  - 適合 agent 主機碰不到 NAS，但 dashboard 碰得到 NAS

重要限制：

- `transfer_mode=upload` 只支援 `executor_type=agent`
- `executor_type=agent` 且 `transfer_mode=direct` 時，該 agent 必須支援該 project 的 NAS target

相關實作：

- [internal/api/projects.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/projects.go)
- [migrations/021_projects_transfer_mode.sql](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/migrations/021_projects_transfer_mode.sql)

---

## 備份資料流

### 1. local + direct

1. dashboard 讀取 project / targets
2. `Runner` 直接在 dashboard 端產出備份檔
3. 備份檔直接寫到 `projectNASRoot(project)/<type>/...`
4. 建立或更新 `backup_records`

### 2. agent + direct

1. dashboard 透過 `/trigger` 或 scheduler 轉發到 agent
2. agent 透過 `DashboardClient` 讀取 project / targets
3. `Runner` 在 agent host 產出備份檔
4. 備份檔直接寫到 agent 視角可用的 NAS 路徑
5. agent 透過 API 寫回 `backup_records`

### 3. agent + upload

1. dashboard 轉發任務到 agent
2. agent 在本機暫存目錄產出備份檔
3. 建立 `backup_records`，先記錄目標 NAS path
4. agent 呼叫：
   - `POST /api/agent/records/{id}/upload`
5. dashboard 驗證 agent 身分與 record 歸屬
6. dashboard 串流寫入專案目錄，重算 sha256
7. dashboard 更新 record 的 `filename` / `path` / `size_bytes` / `checksum`
8. agent 清除暫存檔，最後把 record 狀態改成 `success` 或 `failed`

相關實作：

- [internal/api/agent.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/agent.go)
- [internal/client/client.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/client/client.go)
- [internal/backup/runner.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/runner.go)

---

## 還原資料流

### dashboard 觸發

1. 使用者在前端選一筆成功的 `backup_record`
2. 前端送出：
   - `POST /api/restore`
3. dashboard 驗證：
   - `record_id` 存在
   - record 狀態是 `success`
   - strategy 是 `new` 或 `overwrite`
   - `overwrite` 必須帶 `confirm=RESTORE`
4. dashboard 建立一筆 `restore_records`，狀態先為 `running`

### local restore

如果 project 是 `executor_type=local`：

1. dashboard 直接執行 `restoreLocal`
2. 依 record type 決定：
   - `files` / `system`：解 archive 到目標路徑
   - `database`：解 gzip 後導回 DB
3. 成功後更新 `restore_records.status=success`
4. 失敗則寫 `error_msg`

### agent restore

如果 project 是 `executor_type=agent`：

1. dashboard 轉發到 agent `POST /restore`
2. agent 先透過：
   - `GET /api/agent/records/{id}/download`
   下載備份檔到本機暫存
3. agent 依 type 執行還原
4. 成功或失敗結果回 dashboard
5. dashboard 更新 `restore_records`

### overwrite 的保護機制

- API 層要求 `confirm=RESTORE`
- 前端會要求使用者明確輸入 `RESTORE`
- `files` / `system` / `database` 在 overwrite 前會嘗試建立 snapshot

相關實作：

- [internal/api/restore.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/restore.go)
- [internal/backup/restore.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/restore.go)
- [cmd/agent/main.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/cmd/agent/main.go)
- [migrations/022_restore_records.sql](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/migrations/022_restore_records.sql)

---

## 關鍵資料表

### `projects`

重點欄位：

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

### `backup_targets`

- `type`: `files|database|system`
- `config`: JSONB

### `schedules`

- `cron_expr`
- `target_types`
- `enabled`
- `last_run_at`
- `next_run_at`
- `last_run_status`

### `backup_records`

重點欄位：

- `path`
- `filename`
- `status`
- `checksum`
- `triggered_by`
- `agent_id`
- `agent_name`
- `run_host`
- `log_ref`
- `error_msg`

### `restore_records`

重點欄位：

- `backup_record_id`
- `strategy`
- `target`
- `status`
- `snapshot_path`
- `error_msg`

---

## 前置條件

### dashboard 需要

- `DATABASE_URL`
- `DASHBOARD_ADDR`
- `HOST_PREFIX`
- `NAS_BASE`

### agent 需要

- `DASHBOARD_URL`
- `AGENT_CODE`
- `AGENT_TOKEN`
- `AGENT_ADDR`

### 外部指令需求

很多功能不是純 Go 完成，會依賴外部指令：

- `tar`
- `pg_dump`
- `psql`
- `mysqldump`
- `mysql`
- `docker`
- `systemctl`
- `journalctl`

如果是 agent 模式，真正需要這些指令的是 agent host。

---

## 建立可備份專案

### 檔案備份專案最少需要

- `name`
- `project_path`
- `backup_dirs`
- `nas_base`
- `nas_subpath`

### 資料庫備份專案額外需要

擇一：

- `docker_db_container`
- 或 `db_host` / `db_port` / `db_name` / `db_user` / `db_password(_env)`

### agent 專案額外需要

- `executor_type=agent`
- `executor_agent_id`

### upload 模式額外需要

- `executor_type` 必須是 `agent`
- dashboard 必須可寫對應 NAS 路徑

---

## 手動觸發備份

### UI

可從專案頁或備份頁按「立即備份」。

### API

```http
POST /api/backups/trigger
Content-Type: application/json

{
  "project_id": 1,
  "target_type": "all"
}
```

`target_type` 可用：

- `all`
- `files`
- `database`
- `system`

相關實作：

- [internal/api/trigger.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/trigger.go)

---

## smoke backup

`smoke backup` 是用來做低風險驗證，不是正式備份。

特性：

- `triggered_by = smoke-backup`
- 寫到測試目錄，不進正式專案目錄
- 仍會建立 `backup_records`

API：

```http
POST /api/projects/{id}/test-backup
Content-Type: application/json

{
  "target_type": "all"
}
```

適用場景：

- 新 agent 上線前驗證
- 新 NAS path 調整後驗證
- 新專案初次設定驗證
- release / migration 後的回歸測試

參考：

- [docs/phase4-smoke-backup-log.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/phase4-smoke-backup-log.zh-tw.md)

---

## 檢視備份結果

### 透過 UI

- 專案頁的 Records 分頁
- 全域 Backups 頁

可看到：

- 類型
- 檔名
- checksum
- 執行時間
- 狀態
- 錯誤原因
- agent 名稱 / host

### 透過 API

```http
GET /api/backups?limit=50
GET /api/projects/{id}/backups?limit=30
```

### 透過資料表

直接查 `backup_records`：

```sql
SELECT id, project_name, type, filename, path, status, checksum, error_msg, created_at
FROM backup_records
ORDER BY id DESC
LIMIT 50;
```

### 透過 run log

每次備份執行都會有 run log，路徑通常在：

- `/var/log/backup-agent/runs/`
- 或 fallback 到暫存目錄

可從 `backup_records.log_ref` 看到實際位置。

---

## 還原操作

### 還原策略

#### `new`

- files：還原到新路徑
- database：還原到新資料庫名稱
- 風險低，適合先驗證內容

#### `overwrite`

- files：覆蓋原本路徑
- database：覆蓋原本資料庫
- 風險高，會先要求 `RESTORE` 確認
- 系統會盡量先做 snapshot，但不代表能保證完全回復

### 還原流程建議

1. 先在 UI 找到一筆 `success` 的 record
2. 優先使用 `new`
3. 驗證新目錄或新資料庫內容
4. 確認沒問題後才做人工切換
5. 非必要不要直接用 `overwrite`

### 還原 API 範例

```http
POST /api/restore
Content-Type: application/json

{
  "record_id": 123,
  "strategy": "new",
  "target": "/tmp/restore-check"
}
```

覆蓋範例：

```http
POST /api/restore
Content-Type: application/json

{
  "record_id": 123,
  "strategy": "overwrite",
  "target": "",
  "confirm": "RESTORE"
}
```

### 查看還原歷史

```http
GET /api/restores?limit=50
GET /api/projects/{id}/restores?limit=20
```

或查 `restore_records`：

```sql
SELECT id, backup_record_id, strategy, target, status, snapshot_path, error_msg, started_at, finished_at
FROM restore_records
ORDER BY id DESC
LIMIT 50;
```

---

## API 一覽

### dashboard 對外

- `POST /api/backups/trigger`
- `POST /api/projects/{id}/test-backup`
- `GET /api/backups`
- `GET /api/projects/{id}/backups`
- `DELETE /api/backups/{id}`
- `POST /api/restore`
- `GET /api/restores`
- `GET /api/projects/{id}/restores`

### dashboard 給 agent 用

- `POST /api/agent/heartbeat`
- `GET /api/agent/projects/{id}`
- `GET /api/agent/projects/{id}/targets`
- `GET /api/agent/projects/{id}/retention`
- `POST /api/agent/records`
- `GET /api/agent/records/{id}`
- `PUT /api/agent/records/{id}`
- `POST /api/agent/records/{id}/upload`
- `GET /api/agent/records/{id}/download`

### agent 本地入口

- `POST /trigger`
- `POST /test-backup`
- `POST /restore`

---

## 檔案與目錄慣例

### dashboard / local 模式

- NAS 根通常是 `/mnt/nas/backups`

### agent upload 模式暫存

- 預設：`/var/tmp/backup-agent/staging`
- 可用 `BACKUP_AGENT_STAGING_DIR` 覆蓋

### restore snapshot

- dashboard 預設：
  - `DASHBOARD_RESTORE_SNAPSHOT_DIR`
  - 預設 fallback：`$TMPDIR/backup-dashboard-restore-snapshots`
- agent 預設：
  - `BACKUP_AGENT_RESTORE_SNAPSHOT_DIR`
  - 預設 fallback：`$TMPDIR/backup-agent-restore-snapshots`

### restore 下載暫存

- agent 下載還原檔預設放：
  - `$TMPDIR/backup-agent-restore`

---

## 風險與限制

### 1. `delete record` 不會刪 NAS 實體檔

目前刪除 backup record 只刪資料列，不會刪 NAS 上的檔案。

### 2. overwrite restore 風險高

即使系統有 snapshot，也不是所有情況都能無痛 rollback，尤其：

- 大型目錄覆蓋
- 正在使用中的資料庫
- 權限或 ownership 不一致

### 3. upload 模式會經過 dashboard

優點：

- agent 不必直接碰 NAS

代價：

- dashboard 變成流量中繼點
- 大檔時更依賴 dashboard 網路與磁碟表現

### 4. restore 目前是同步等待型操作

對大檔或大型 DB 還原，HTTP 連線時間會比較長。

### 5. 多數能力依賴外部指令

如果 `pg_dump` / `psql` / `mysql` / `docker` 缺失，功能會失敗，不是 Go 程式自己能補救。

---

## 排錯指南

### 備份失敗但 UI 只有 failed

先看：

1. `backup_records.error_msg`
2. `backup_records.log_ref`
3. agent systemd log
4. dashboard container log

常用指令：

```bash
docker compose logs --tail=100 dashboard
systemctl status backup-agent --no-pager
journalctl -u backup-agent -n 200 --no-pager
```

### upload 模式失敗

優先檢查：

1. dashboard 是否可寫 NAS
2. agent 到 dashboard 的網路
3. sha256 mismatch
4. record path 是否超出專案目錄

### database restore 失敗

優先檢查：

1. `db_type` 是否正確
2. `db_name` / `db_user` / `password_env` 是否正確
3. `docker_db_container` 是否存在
4. `psql` 或 `mysql` 是否可用

### direct 模式 agent 寫不到 NAS

檢查：

1. 該 VM 是否真的掛到 NAS
2. `agent_nas_targets` 是否有對應
3. project 的 `nas_target_id` 是否正確

### restore 失敗

可查：

```http
GET /api/projects/{id}/restores?limit=20
```

重點看：

- `status`
- `snapshot_path`
- `error_msg`

---

## 建議維運流程

### 正式環境建議

1. 新專案上線前先跑一次 `smoke backup`
2. 正式排程上線後定期檢查 `backup_records`
3. 每個重要專案至少做過一次 `new` restore drill
4. 避免把第一次 restore 就做成 `overwrite`
5. release 或部署前先做 DB safety backup

### 做重大變更前

建議順序：

1. `bash scripts/backup-current-db.sh`
2. 執行 smoke backup
3. 做 schema 或設定調整
4. 驗證 dashboard / agent
5. 視需要做一次 `new` restore drill

---

## 實作對照

### API

- [internal/api/trigger.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/trigger.go)
- [internal/api/records.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/records.go)
- [internal/api/restore.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/restore.go)
- [internal/api/agent.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/api/agent.go)

### 備份核心

- [internal/backup/runner.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/runner.go)
- [internal/backup/files.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/files.go)
- [internal/backup/database.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/database.go)
- [internal/backup/system.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/system.go)
- [internal/backup/restore.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/backup/restore.go)

### agent / client

- [cmd/agent/main.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/cmd/agent/main.go)
- [internal/client/client.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/client/client.go)

### store / migration

- [internal/store/models.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/store/models.go)
- [internal/store/store.go](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/internal/store/store.go)
- [migrations/021_projects_transfer_mode.sql](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/migrations/021_projects_transfer_mode.sql)
- [migrations/022_restore_records.sql](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/migrations/022_restore_records.sql)

---

## 補充建議

如果後續要把這塊再往上補強，優先順序建議是：

1. 補 automated restore drill
2. 補 restore 的非同步 job 化
3. 補刪除 record 時的實體檔刪除策略
4. 補 upload / restore 的更多整合測試
5. 補更細的權限與容量告警
