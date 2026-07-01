# Agent 設定同步流程

這份文件對應新加入的 Agent 設定 API 與前端設定面板。

## 1. 目的

這套流程處理兩件事：

1. 以 dashboard 上既有 agent 當模板
2. 產生其他 VM 可直接套用的 `/etc/backup-agent/env`
3. 視需要建立新的 agent 紀錄，或更新既有遠端 agent

它不會直接 SSH 進 VM，也不會直接幫你重啟遠端服務。  
設計上仍由維運人員把產生的 env 或 shell 指令貼到目標 VM 執行。

## 2. 可用 API

- `GET /api/agents/{id}/config`
- `POST /api/agents/{id}/config`

## 3. GET 行為

`GET /api/agents/{id}/config` 會回傳：

- 目前 dashboard 端的 `name / code / base_url / token`
- 預設的 agent env 內容
- 可直接套用的 shell 指令

## 4. POST 行為

`POST /api/agents/{id}/config` 會：

1. 預設建立一筆新的遠端 agent 紀錄，不修改來源 dashboard agent
2. 重新產生對應的 env 內容
3. 回傳可直接在 VM 執行的 shell 指令

可透過 `save_mode` 控制行為：

- `create_new`：建立新主機 agent，來源 agent 不會被修改
- `update_existing`：直接更新目前這筆 agent

範例：

```json
{
  "save_mode": "create_new",
  "name": "VM App 01",
  "code": "vm-app-01",
  "base_url": "http://192.168.1.21:9090",
  "token": "replace-me",
  "dashboard_url": "http://192.168.1.10:8080",
  "agent_addr": ":9090",
  "host_prefix": "",
  "nas_base": "/mnt/nas/backups",
  "slack_webhook_url": ""
}
```

## 5. 建議操作順序

建議固定照這個順序：

1. 在管理頁打開 Agent 設定
2. 維持預設的「建立新主機 Agent」
3. 填入目標 VM 的 `name / code / base_url / token`
4. 儲存
5. 複製回傳的 shell 指令
6. 到目標 VM 執行
7. 確認 `systemctl status backup-agent`
8. 回 dashboard 檢查 heartbeat 與 `last_seen_at`

## 6. 風險點

如果使用 `update_existing` 且只做了一半，agent 會立即失聯。  
預設的 `create_new` 模式不會影響原本 dashboard agent。

### 情況 A

- dashboard 已更新
- VM 尚未更新 env

結果：

- agent 用舊 `code/token` heartbeat
- dashboard 會回 `401` 或 `invalid agent code`

### 情況 B

- VM 已更新 env
- dashboard 尚未更新

結果：

- agent 會帶新 `code/token`
- dashboard 仍用舊資料驗證
- heartbeat 一樣失敗

所以這套流程的重點不是只有 API 可改，而是「兩邊要成對同步」。

## 7. 前端行為

管理頁的 Agent 表格現在有 `設定` 按鈕。

按下後可：

1. 以目前 agent 當模板建立新的遠端 agent
2. 儲存
3. 直接看到：
   - `/etc/backup-agent/env` 內容
   - 一鍵套用 shell 指令
   - 重啟檢查指令

## 8. 目前限制

這版仍有幾個刻意保留的限制：

- 沒有遠端 SSH 套用
- 沒有 secret manager
- `token_hash` 目前仍是明文字串比對，不是真正 hash 驗證
- 沒有交易式「先改 dashboard、再自動改 VM」能力

這版的定位是：

- 先讓維運流程可管理
- 降低手改 `.env` 出錯率
- 讓 dashboard 與 VM 設定能用同一套資料源同步
