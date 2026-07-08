# 新增 Agent 並使用 Upload 模式備份到 Dashboard / NAS 操作手冊

> 適用於目前 `Backup Manager` 專案的多 VM agent 架構。
>
> 目標情境：
> 新增一台新的 `backup-agent` VM，讓該 VM 執行備份，先把備份檔上傳到 `dashboard`，再由 `dashboard` 寫入 NAS。

## 1. 適用情境

當符合以下條件時，建議使用這份流程：

- 新 VM 可以連到 `dashboard`
- 新 VM 不方便直接掛載 NAS
- 希望由 `dashboard` 統一接收備份檔並落地到 NAS
- 專案要用 `agent` 執行，而不是 `dashboard` 本機執行

此模式的關鍵設定為：

- `projects.executor_type = agent`
- `projects.executor_agent_id = <新 agent id>`
- `projects.transfer_mode = upload`

## 2. 架構說明

`upload` 模式的資料流如下：

1. 新 VM 上的 `backup-agent` 執行備份
2. agent 先把備份檔寫到本機 staging 目錄
3. agent 呼叫 dashboard API：
   `POST /api/agent/records/{id}/upload`
4. dashboard 驗證 agent 身分與檔案 checksum
5. dashboard 將備份檔寫到 NAS
6. dashboard 更新 `backup_records`

這和 `direct` 模式不同：

- `direct`：agent 直接寫 NAS
- `upload`：agent 上傳給 dashboard，由 dashboard 寫 NAS

## 3. 前置條件

在開始前，請確認：

- dashboard 已正常運作
- dashboard 容器可以掛載 NAS
- 新 VM 可以連到 dashboard URL
- 新 VM 已安裝 `systemd`
- 新 VM 可執行專案內需要的外部指令
  - `tar`
  - `pg_dump` 或其他 DB dump 指令
  - 若備份流程需要，也可能包含 `docker`
- 你有 dashboard UI 管理權限

## 4. 新增 Agent 的必要資訊

建立新 agent 前，先準備這些資料：

- `name`
  - 顯示名稱，例如 `vm-03`
- `code`
  - agent 唯一識別碼，例如 `vm-03`
- `token`
  - dashboard 與 agent 之間的認證 token
- `base_url`
  - dashboard 用來呼叫 agent 的 URL
  - 例如 `http://192.168.3.50:9090`
- `dashboard_url`
  - agent 用來呼叫 dashboard 的 URL
  - 例如 `https://backup.example.com`

## 5. 在 Dashboard 建立新 Agent

可透過 dashboard UI 的 Agent 管理頁面操作。

### 5.1 建立方式

建議使用以下其中一種：

1. `Agent 設定產生器`
2. `Agent 下載與安裝`

這兩個功能會幫你產出：

- agent env 內容
- 套用 env 的指令
- 安裝或部署 agent 的指令

### 5.2 建立後應確認

建立完成後，確認 dashboard 內有新的 agent 紀錄，且欄位正確：

- `name`
- `code`
- `base_url`
- `enabled = true`

剛建立時狀態通常會是：

- `offline`

等新 VM 上 agent 啟動並成功 heartbeat 後，才會變成：

- `online`

## 6. 在新 VM 安裝與部署 Agent

### 6.1 如果用 dashboard 產生 installer

可直接在新 VM 執行 dashboard 提供的安裝指令。

### 6.2 如果用 repo 內建腳本

在 repo 根目錄執行：

```bash
bash scripts/build-agent.sh
bash scripts/deploy-agent.sh
```

### 6.3 必要環境變數

新 VM 的 agent 至少要有這些 env：

```bash
DASHBOARD_URL=https://backup.example.com
AGENT_CODE=vm-03
AGENT_TOKEN=your-secret-token
AGENT_ADDR=:9090
```

建議補上：

```bash
BACKUP_AGENT_STAGING_DIR=/var/tmp/backup-agent/staging
```

如需對應本機路徑，也可保留：

```bash
HOST_PREFIX=/host
NAS_BASE=/mnt/nas/backups
```

### 6.4 建議的 `/etc/backup-agent/env` 範例

```bash
DASHBOARD_URL=https://backup.example.com
AGENT_CODE=vm-03
AGENT_TOKEN=replace-me
AGENT_ADDR=:9090
BACKUP_AGENT_STAGING_DIR=/var/tmp/backup-agent/staging
HOST_PREFIX=/host
NAS_BASE=/mnt/nas/backups
```

### 6.5 啟動 agent

```bash
sudo systemctl restart backup-agent
sudo systemctl status backup-agent --no-pager
```

## 7. 安裝後的基礎驗證

### 7.1 驗證本機 healthz

```bash
curl -i http://127.0.0.1:9090/healthz
```

預期結果：

- HTTP `200 OK`

### 7.2 驗證 agent service

```bash
sudo systemctl status backup-agent --no-pager
sudo journalctl -u backup-agent -n 200 --no-pager
```

應確認：

- service 為 `active (running)`
- 沒有持續重啟
- 沒有認證錯誤
- 沒有 dashboard 連線錯誤

### 7.3 驗證 dashboard 是否看到新 agent

在 dashboard UI 內確認：

- agent 狀態變為 `online`
- `last_seen_at` 有更新
- 無明顯 `last_error`

## 8. 驗證 Agent 與 Dashboard 的 API 認證

在新 VM 上執行：

```bash
source /etc/backup-agent/env

curl -s \
  -H "X-Agent-Code: $AGENT_CODE" \
  -H "X-Agent-Token: $AGENT_TOKEN" \
  "$DASHBOARD_URL/api/agent/schedules/enabled"
```

預期：

- 回傳 JSON 陣列
- 若目前沒有分配排程給這台 agent，也至少應回傳 `[]`

如果收到：

- `401`
  - `AGENT_CODE` 或 `AGENT_TOKEN` 不一致
- 連線錯誤
  - `DASHBOARD_URL` 錯誤、DNS 錯誤、TLS 問題或防火牆問題

## 9. 建立要走 Upload 模式的 Project

在 dashboard UI 建立新 project，或修改既有 project。

### 9.1 必要欄位

以下三個欄位必須同時正確：

- `executor_type = agent`
- `executor_agent_id = 新 agent`
- `transfer_mode = upload`

如果少任一個，流程就不會走你要的「agent 上傳到 dashboard，再由 dashboard 寫 NAS」。

### 9.2 設定建議

建議先用一個小型測試 project，避免一開始就用正式大資料量。

## 10. 建立最小測試用 Files Target

先不要一開始測資料庫，也不要一開始測大檔。

建議先在新 VM 建立一個小目錄：

```bash
mkdir -p /tmp/backup-smoke
echo "hello upload path" > /tmp/backup-smoke/test.txt
```

然後在 project 下新增一個 `files` target：

- `type = files`
- `source = /tmp/backup-smoke`
- `compress = true`

這樣可以先驗證：

- agent 是否能讀取來源
- agent 是否能產生壓縮檔
- agent 是否能上傳 dashboard
- dashboard 是否能寫入 NAS

## 11. 手動觸發測試

在 dashboard UI 中，對該 project 執行手動備份。

建議先只測：

- `files`

不要一開始就一起測：

- `database`
- `system`

## 12. 成功時要看到的結果

### 12.1 在 Agent VM 上

檢查：

```bash
sudo journalctl -u backup-agent -n 200 --no-pager
```

你應該能看到類似訊息：

- 接到 trigger
- 開始執行備份
- `upload start record_id=...`
- `run success`

### 12.2 在 Dashboard 上

檢查：

```bash
docker compose logs --tail=200 dashboard
```

應可看到：

- 有收到 agent 的 API 請求
- record 建立成功
- upload 完成

### 12.3 在 Dashboard UI

檢查備份紀錄：

- `status = success`
- `triggered_by = manual`
- `agent_name = 新 agent`
- `path` 指向 NAS 上的實際路徑

### 12.4 在 NAS 上

確認檔案真的存在。

注意：

- `upload` 模式是由 dashboard 落地到 NAS
- 檔案最終在 dashboard 解析出的 NAS 路徑上
- 不是 agent VM 本機 NAS 路徑

## 13. 排程測試

手動觸發成功後，再做排程測試。

### 13.1 建議的測試排程

先建立一個很快會到的排程，例如：

```text
23 10 * * *
```

請注意這是：

- 每天上午 `10:23`

不是晚上 `10:23`。

如果你要測晚上，請換成對應的 24 小時制時間。

### 13.2 立即驗證 agent 是否有載入排程

在新 VM 上執行：

```bash
source /etc/backup-agent/env

curl -s \
  -H "X-Agent-Code: $AGENT_CODE" \
  -H "X-Agent-Token: $AGENT_TOKEN" \
  "$DASHBOARD_URL/api/agent/schedules/enabled"
```

檢查回傳內容中是否包含：

- 該 project
- 該 schedule id
- `enabled = true`

### 13.3 等排程時間到後檢查

檢查：

```bash
sudo journalctl -u backup-agent -n 200 --no-pager
```

以及 dashboard UI / backup records：

- `last_run_at` 是否更新
- `next_run_at` 是否更新
- `last_run_status` 是否更新

## 14. Upload 模式的重點限制

### 14.1 Upload 只支援 agent executor

也就是：

- `transfer_mode = upload`
- 必須搭配 `executor_type = agent`

### 14.2 Dashboard 必須能寫 NAS

即使 agent 可以成功上傳，若 dashboard 端沒掛到 NAS 或權限不足，還是會失敗。

### 14.3 大檔傳輸會經過 dashboard

因此 dashboard 會成為：

- 流量瓶頸
- 單點

大檔上傳時要注意：

- dashboard CPU
- dashboard 記憶體
- 容器網路
- NAS 寫入速度

## 15. 常見錯誤與排查

### 15.1 Agent 沒有出現在 dashboard 或一直 offline

檢查：

```bash
sudo systemctl status backup-agent --no-pager
sudo journalctl -u backup-agent -n 200 --no-pager
cat /etc/backup-agent/env
```

確認：

- `DASHBOARD_URL` 正確
- `AGENT_CODE` 正確
- `AGENT_TOKEN` 正確
- dashboard 可從該 VM 存取

### 15.2 Dashboard 可以手動打 agent，但 agent 上傳失敗

表示：

- `base_url` 可能正確
- 但 `DASHBOARD_URL` 可能錯誤

請檢查：

```bash
source /etc/backup-agent/env
curl -i "$DASHBOARD_URL/healthz"
```

### 15.3 手動備份正常，但排程沒跑

先分清楚：

- `dashboard` 的 scheduler 只會載入 `executor_type = local`
- `agent` 的 scheduler 才會載入 `executor_type = agent`

所以若 project 是 agent 模式，請看：

```bash
sudo journalctl -u backup-agent -n 200 --no-pager
```

不要只看 dashboard container log。

### 15.4 Upload 模式失敗但 direct 模式正常

通常優先檢查：

- `transfer_mode` 是否真的是 `upload`
- `DASHBOARD_URL` 是否可達
- dashboard 是否能寫 NAS
- agent token 是否正確
- staging 路徑是否可寫

### 15.5 Database 備份失敗，顯示找不到 docker

若你的 database target 依賴：

```bash
docker exec <container> pg_dump ...
```

那麼新 VM 上必須：

- 安裝 `docker`
- 或調整備份設定，改用可直接連 DB 的 host/port 模式

否則排程雖然會觸發，執行仍然會失敗。

## 16. 建議的正式上線順序

建議依照這個順序：

1. 建立新 agent
2. 在新 VM 安裝並啟動 agent
3. 驗證 `healthz`
4. 驗證 `/api/agent/schedules/enabled`
5. 建立小型測試 project
6. 設 `executor_type=agent`
7. 設 `executor_agent_id=新 agent`
8. 設 `transfer_mode=upload`
9. 先測 `files` 小目錄手動備份
10. 確認 dashboard UI record 成功
11. 確認 NAS 實際落檔
12. 再測 `database`
13. 最後才開正式排程

## 17. 建議的驗證指令清單

### 17.1 Agent VM

```bash
sudo systemctl status backup-agent --no-pager
sudo journalctl -u backup-agent -n 200 --no-pager
curl -i http://127.0.0.1:9090/healthz
source /etc/backup-agent/env
curl -s \
  -H "X-Agent-Code: $AGENT_CODE" \
  -H "X-Agent-Token: $AGENT_TOKEN" \
  "$DASHBOARD_URL/api/agent/schedules/enabled"
```

### 17.2 Dashboard VM

```bash
docker compose ps
docker compose logs --tail=200 dashboard
curl -i http://127.0.0.1:<DASHBOARD_PORT>/healthz
```

### 17.3 NAS 落檔確認

```bash
ls -lah /mnt/nas/backups
find /mnt/nas/backups -type f | tail -20
```

## 18. 成功判定標準

以下條件都滿足，才算新 agent + upload 路徑驗證成功：

- agent 在 dashboard 顯示 `online`
- 手動備份成功
- `backup_records.status = success`
- `backup_records.agent_name` 是新的 agent
- NAS 上存在對應備份檔
- 排程時間到時，agent 能自動觸發成功

## 19. 參考文件

- [docs/backup-restore-guide.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/backup-restore-guide.zh-tw.md)
- [docs/upload-transfer-restore-design.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/upload-transfer-restore-design.zh-tw.md)
- [docs/phase7-upload-transfer-restore.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/phase7-upload-transfer-restore.zh-tw.md)
- [docs/agent-config-sync.zh-tw.md](/home/jarvis/project/bonus/05_rootadviser/07_server/01_backup/docs/agent-config-sync.zh-tw.md)
