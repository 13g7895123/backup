# Phase 4 Smoke Backup 與 Run Log

這份文件對應 `phase4` 的兩個後端功能：

1. `smoke-backup`
2. 單次執行 log

## 1. Smoke Backup API

dashboard 端提供：

```http
POST /api/projects/{id}/test-backup
Content-Type: application/json

{
  "target_type": "all"
}
```

`target_type` 可用：

- `all`
- `files`
- `database`
- `system`

回應是非同步觸發：

```json
{
  "status": "triggered",
  "project_id": 12,
  "type": "all",
  "mode": "smoke-backup",
  "executor_type": "agent"
}
```

若 project 指派給 agent，dashboard 會轉發到該 agent。  
若 project 是 `local`，則由 dashboard 本機直接執行。

## 2. Smoke Backup 寫入路徑

正式備份仍然寫到：

```text
<nas_base>/<nas_subpath>/
```

`smoke-backup` 則固定寫到：

```text
<nas_base>/_tests/<project-slug>/<yyyyMMddHHmmss>/
```

例如：

```text
/mnt/nas/backups/_tests/root-adviser-api/20260531_153012/
```

在該目錄下，依 target type 再分層：

```text
/mnt/nas/backups/_tests/root-adviser-api/20260531_153012/files/2026-05-31/
/mnt/nas/backups/_tests/root-adviser-api/20260531_153012/database/2026-05-31/
```

這樣測試備份不會污染正式目錄，也不會和正式 retention 混在一起。

## 3. Backup Records 行為

`smoke-backup` 也會建立 `backup_records`，因此 dashboard 一樣看得到：

- `status`
- `error_msg`
- `agent_name`
- `run_host`
- `log_ref`

目前 `triggered_by` 會寫成：

```text
smoke-backup
```

## 4. Run Log

每一個 target run 都會建立一份獨立 log。

預設路徑：

```text
/var/log/backup-agent/runs/<run_id>.log
```

如果該路徑不可寫，會退回：

```text
<tmp>/backup-agent/runs/<run_id>.log
```

你也可以用環境變數覆蓋：

```env
BACKUP_RUN_LOG_DIR=/some/path/runs
```

## 5. log_ref

`backup_records.log_ref` 目前會寫入實際 log 檔案路徑，例如：

```text
/var/log/backup-agent/runs/smoke-20260531-153012-root-adviser-api-files.log
```

這樣出錯時可直接從 record 追到單次執行 log。

## 6. 目前記錄內容

目前每份 log 至少會記錄：

- run start
- project / target / smoke mode
- 輸出目標路徑
- success / failed
- duration
- checksum
- size
- error

這一版先優先保證「有獨立 log 可追」；若後續要更完整，再把外部指令 stdout/stderr 一併納入。
