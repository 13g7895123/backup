# Phase 3 單一 NAS 操作說明

這份文件對應目前 `phase3` 的實作，但採用簡化部署模型：

- 只有一台 NAS
- NAS 掛載由你在每台 VM 上自行完成
- dashboard / agent 只負責記錄、分派與驗證

也就是說，系統不會幫你做 `mount.nfs` 或 `fstab` 設定；它只會檢查「現在這台 VM 上這個路徑是不是真的存在、可寫、能不能用來備份」。

## 1. 目前功能

`phase3` 現在提供兩件事：

1. `project` 可綁定 `nas_target_id` 與 `nas_subpath`
2. 可呼叫 `POST /api/agents/{id}/test/preflight` 做執行前檢查

這版同時支援單一 NAS 的預設行為：

- 若系統中只有一筆啟用中的 `nas_targets`，建立或更新 project 時會自動套用它
- 若 `nas_subpath` 沒填，會自動產生預設值
- 若某台 agent 沒有明確設定 `agent_nas_targets`，系統會先退回使用 `project.nas_base`

這樣你可以先用固定掛載路徑跑通，不需要一開始就把多 NAS mapping 做滿。

## 2. 建議部署規則

若目前只有一台 NAS，建議所有 agent VM 都統一掛到同一路徑，例如：

```text
/mnt/nas/backups
```

這樣最簡單，因為：

- `project.nas_base` 可以統一用 `/mnt/nas/backups`
- agent 沒有額外 NAS mapping 時，也能直接工作
- preflight 檢查的結果會最直觀

若之後某台 VM 掛載點不同，再補 `agent_nas_targets.mount_base` 即可。

## 3. 建議初始化資料

若系統裡只有一台 NAS，建議先建立一筆 `nas_targets`：

```sql
INSERT INTO nas_targets
  (code, name, description, mount_type, remote_path, default_subpath, enabled)
VALUES
  (
    'nas-default',
    'Default NAS',
    'single NAS for all agents',
    'nfs',
    '192.168.1.10:/volume1/backups',
    'projects',
    true
  );
```

欄位意義：

- `code`: NAS 代號
- `remote_path`: 實際 NAS 來源，只是記錄用途
- `default_subpath`: project 未填 `nas_subpath` 時的預設前綴

如果你現在不打算做每台 agent 的細部 mapping，可以先不要新增 `agent_nas_targets`。

## 4. Project 預設行為

當系統裡只有一筆啟用中的 `nas_targets` 時：

- `nas_target_id` 會自動套用那一筆
- `nas_subpath` 若留空，會自動產生：

```text
<default_subpath>/<project-slug>
```

例如：

- NAS `default_subpath = projects`
- project 名稱是 `Root Adviser API`

則預設會變成：

```text
projects/root-adviser-api
```

最終備份根路徑會是：

```text
<nas_base>/<nas_subpath>
```

例如：

```text
/mnt/nas/backups/projects/root-adviser-api
```

而實際備份檔會再落在：

```text
/mnt/nas/backups/projects/root-adviser-api/files/2026-05-31/
/mnt/nas/backups/projects/root-adviser-api/database/2026-05-31/
```

## 5. VM 端需要對齊的條件

每台要執行 agent 的 VM，至少要保證下面幾件事：

1. NAS 已掛載到你要使用的路徑，例如 `/mnt/nas/backups`
2. agent process 對該路徑有寫入權限
3. `files` target 的來源目錄在該 VM 上真的存在
4. 若要做 DB dump：
   - 使用 `docker exec` 時，container 名稱要存在
   - 使用 TCP 直連時，`db_host:db_port` 要能從該 VM 連到

如果 agent 跑在 container 裡，且資料目錄在 host：

- `HOST_PREFIX` 要能對應到 host mount
- 例如 project path 是 `/srv/app`
- container 裡看到的 host mount 是 `/host`
- 那 `HOST_PREFIX=/host`

## 6. Preflight API

dashboard 端可呼叫：

```http
POST /api/agents/{id}/test/preflight
Content-Type: application/json

{
  "project_id": 12
}
```

成功時會回傳：

```json
{
  "status": "success",
  "started_at": "2026-05-31T10:10:00Z",
  "finished_at": "2026-05-31T10:10:02Z",
  "steps": [
    {
      "name": "check_project_path",
      "status": "success",
      "detail": "checked: /srv/app, /srv/app/storage"
    },
    {
      "name": "check_nas_write",
      "status": "success",
      "detail": "writable: /mnt/nas/backups/projects/root-adviser-api"
    },
    {
      "name": "check_db_connectivity",
      "status": "success",
      "detail": "docker container: postgres-app"
    }
  ]
}
```

失敗時會回傳類似：

```json
{
  "status": "failed",
  "error_msg": "路徑不存在或不可讀: /srv/app/storage (...略...)",
  "steps": [
    {
      "name": "check_project_path",
      "status": "failed",
      "error_msg": "路徑不存在或不可讀: /srv/app/storage (...略...)"
    }
  ]
}
```

## 7. 建議操作順序

1. 在每台 VM 上手動掛載 NAS
2. 確認 agent 使用者能寫入掛載點
3. 在 DB 建立單一 `nas_targets`
4. 建立或更新 project，讓它使用預設 `nas_target_id`
5. 呼叫 `preflight`
6. `preflight` 成功後再做正式 trigger

## 8. 目前簡化邏輯的限制

這版是刻意偏向單一 NAS / 單一路徑的務實實作，所以有幾個限制：

- 若 agent 完全沒有 `agent_nas_targets` 設定，系統會直接信任 `project.nas_base`
- `preflight` 現在檢查的是「可到達 / 可寫」，不是完整 restore 驗證
- `preflight` 的 DB 檢查：
  - `docker exec` 模式只檢查 container 是否存在
  - TCP 模式只檢查 port 是否可達

這足夠做第一階段上線；若之後要更嚴格，再往 `smoke backup` 與 `restore drill` 擴充。
