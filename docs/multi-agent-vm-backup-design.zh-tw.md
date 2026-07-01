# 多 VM Agent 備份擴充設計文件

## 1. 目標與需求

此專案目前已具備 `dashboard + backup-agent` 的基本架構，但整體實作仍是「單一 agent」模型。  
如果要支援同一區網內的另一台 VM 安裝 agent，並由該 VM 直接把備份寫到對應 NAS 路徑，同時將執行狀態與結果回傳到目前這台 dashboard 後端，建議不要重做備份核心，而是把現有 agent 架構改成「多節點可管理」模式。

本文件的設計目標如下：

1. 允許多台 VM 安裝 `backup-agent`。
2. 每個專案可指定由哪一台 VM 執行備份。
3. agent 直接在自己的 VM 上讀檔、dump DB、寫入 NAS。
4. agent 將備份紀錄與執行狀態回傳 dashboard，讓 UI 可顯示。
5. 在新增專案、編輯專案、建立排程、手動觸發備份時，都可以選到對應 VM。
6. 保留現有本機執行能力，作為 agent 不可用或遷移期間的保底機制。

---

## 2. 現況評估

### 2.1 已有可重用能力

這個專案其實已經有可用的 agent 基礎：

- `cmd/agent/main.go`
  - agent 可獨立啟動 HTTP server
  - agent 會向 dashboard 拉排程資料
  - agent 可接收 `/trigger`
  - agent 可把 `backup_records` 寫回 dashboard
- `internal/client/client.go`
  - agent 透過 HTTP API 與 dashboard 溝通，不直連 PostgreSQL
- `internal/backup/runner.go`
  - 備份執行邏輯已和 store 抽象分離，可直接重用
- `scripts/install-agent.sh`
  - 已有 systemd 安裝腳本

這代表「另一台 VM 執行備份」本身不是困難點，真正的限制是目前整個系統只支援一個 agent。

### 2.2 目前的核心限制

目前所有流程都建立在全域單一 agent 假設上：

1. `cmd/dashboard/main.go`
   - 只讀取一組 `AGENT_URL`
   - 若有設定 `AGENT_URL`，dashboard 本機排程器就停用

2. `internal/api/trigger.go`
   - 手動觸發備份時，只會轉發到環境變數中的單一 `AGENT_URL`

3. `internal/api/schedules.go`
   - schedule create/update/delete 只會通知單一 agent reload/remove

4. `internal/api/agent.go`
   - agent API 只有單一 `AGENT_TOKEN`
   - 沒有 agent 身分識別，也沒有多 agent 授權範圍

5. `internal/client/client.go`
   - agent 取得 project / target / retention 時，仍走共用 project API
   - 沒有限制 agent 只能讀取自己負責的專案

6. `internal/store/models.go`
   - `projects`、`schedules`、`backup_records` 都沒有 agent 綁定欄位

7. `cmd/dashboard/web/index.html`
   - UI 沒有 agent 管理頁
   - 專案表單無法選擇要由哪台 VM 執行
   - 備份紀錄也無法顯示是由哪台 agent 執行

---

## 3. 建議的目標架構

### 3.1 設計原則

建議以「dashboard 為控制中心、agent 為執行節點」來擴充：

- dashboard：
  - 管理專案、排程、agent、紀錄
  - 決定哪個專案由哪個 agent 執行
  - 顯示 agent 狀態與備份結果

- agent：
  - 只負責執行被分派到自己的專案
  - 從本機 VM 讀檔與 DB
  - 直接寫入 NAS
  - 把執行結果回傳 dashboard

### 3.2 目前執行模型與建議演進方向

先釐清目前專案的實際行為：

1. `dashboard` 若有設定 `AGENT_URL`，會停用本機 backup scheduler，改由 agent 負責排程執行。
2. 手動觸發備份時，`dashboard` 也會優先把請求轉發給 agent。
3. `backup-agent` 本身會啟動自己的 scheduler，並接收 `dashboard` 轉發的 `/trigger` 請求。
4. `dashboard` 端仍保留本機直接執行能力，但目前角色較接近 fallback；當 agent 轉發失敗時，才會退回本機執行。

所以，現況不是「local 與 agent 並列的雙模式產品設計」，而是：

- 主要模式：`dashboard + 單一 host agent`
- 保底模式：`dashboard 本機直接執行`

也就是說，這個專案目前本來就預設應由 agent 執行備份，只是還沒有把「多台 agent / 多台 VM」做成正式可管理模型。

因此本次需求的正確演進方向，不是從零新增 agent 模式，而是把目前的：

- 單一 `AGENT_URL`
- 單一 `AGENT_TOKEN`
- 單一 host agent

擴充成：

- 多 agent 節點可登錄
- 每個 project 可選擇對應 VM agent
- 每個 agent 只執行自己被分派的 project / schedule
- `dashboard` 統一負責狀態彙整與 UI 顯示

在這個前提下，建議系統概念上仍保留兩種執行來源，但定位要寫清楚：

- `agent`：正式主流程，由指定 VM 執行備份
- `local`：例外保底流程，用於 agent 不可用、遷移期或特定維運情境

這樣的寫法會比較符合目前程式實際行為，也比較能銜接後續多 VM agent 擴充。

### 3.3 建議資料流

#### 手動觸發

1. 使用者在 UI 選專案與備份類型
2. dashboard 查出該專案的執行來源
3. 正常情況下，將請求轉發到該專案綁定的 agent `/trigger`
4. 若該專案被明確標記為 `local`，或指定 agent 不可用且允許 fallback，才由本機 `runner` 執行
5. agent 建立 `running` record
6. agent 執行備份，寫入 NAS
7. agent 更新 record 為 `success` 或 `failed`
8. dashboard UI 透過既有 records API 顯示結果

#### 排程執行

1. dashboard 建立 schedule，並綁定在某個 project
2. dashboard 本機 scheduler 只保留處理例外的 `local` 專案 schedule
3. 每個 agent 啟動時只拉取「分派給自己」的 schedule
4. schedule 到時後，原則上由對應 agent 觸發
5. 執行結果仍統一寫回 dashboard

---

## 4. 資料庫調整建議

## 4.1 新增 `agents` 主表

建議新增資料表 `agents`：

```sql
CREATE TABLE agents (
    id                SERIAL PRIMARY KEY,
    code              VARCHAR(50) UNIQUE NOT NULL,
    name              VARCHAR(100) NOT NULL,
    base_url          TEXT NOT NULL,
    token_hash        TEXT NOT NULL,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    status            VARCHAR(20) NOT NULL DEFAULT 'offline',
    host_name         TEXT NOT NULL DEFAULT '',
    ip_address        TEXT NOT NULL DEFAULT '',
    version           TEXT NOT NULL DEFAULT '',
    capabilities      JSONB NOT NULL DEFAULT '{}'::jsonb,
    nas_mount_base    TEXT NOT NULL DEFAULT '/mnt/nas/backups',
    last_seen_at      TIMESTAMPTZ,
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

欄位用途：

- `code`：agent 固定識別碼，例如 `vm-app-01`
- `name`：UI 顯示名稱
- `base_url`：dashboard 呼叫 agent 的位址，例如 `http://192.168.1.21:9090`
- `token_hash`：agent 驗證 token 的雜湊值，不建議明文存 DB
- `status`：`online` / `offline` / `disabled`
- `capabilities`：可存 `docker`, `journalctl`, `rsync`, `mounts` 等能力
- `nas_mount_base`：若不同 VM 掛載 NAS 路徑不同時可用；若全數統一可先不使用

## 4.2 `projects` 表新增執行設定

建議在 `projects` 表新增：

```sql
ALTER TABLE projects
    ADD COLUMN executor_type VARCHAR(20) NOT NULL DEFAULT 'agent',
    ADD COLUMN executor_agent_id INTEGER REFERENCES agents(id) ON DELETE SET NULL;
```

用途：

- `executor_type='agent'`：正式主流程，由 `executor_agent_id` 指定的 agent 執行
- `executor_type='local'`：例外保底流程，由 dashboard 本機執行

這是本次需求最重要的欄位，因為它決定新增專案或編輯專案時可以選 VM。

## 4.3 `backup_records` 表新增執行來源資訊

建議新增：

```sql
ALTER TABLE backup_records
    ADD COLUMN executor_type VARCHAR(20) NOT NULL DEFAULT 'agent',
    ADD COLUMN agent_id INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    ADD COLUMN agent_name VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN run_host TEXT NOT NULL DEFAULT '',
    ADD COLUMN started_at TIMESTAMPTZ,
    ADD COLUMN finished_at TIMESTAMPTZ;
```

用途：

- UI 可顯示是哪一台 VM 執行
- 專案之後若改綁其他 agent，舊 record 仍可保留當時執行資訊
- 若未來要做 agent SLA、失敗統計、節點健康度，這些欄位可直接利用

## 4.4 `schedules` 表是否要加 `agent_id`

本次需求的最小可行版本，不一定要在 `schedules` 表直接新增 `agent_id`。

建議先採用：

- schedule 跟著 project 的 `executor_type / executor_agent_id`
- agent 拉 schedule 時，SQL 用 `JOIN projects` 過濾

也就是說，排程的執行節點先「繼承專案設定」。

這樣能減少：

- UI 複雜度
- schedule 與 project agent 不一致的資料風險
- reload/remove 時的分派判斷難度

若未來真的需要「同一專案不同排程跑不同 agent」，再加 schedule override。

---

## 5. API 與後端調整

## 5.1 Agent 驗證不要再使用單一全域 `AGENT_TOKEN`

目前 `internal/api/agent.go` 是：

- 全域環境變數 `AGENT_TOKEN`
- 任何 agent 都用同一把 token

多 agent 模式下，建議改成：

1. agent 每台有自己的 `code` + `token`
2. dashboard middleware 透過 header 辨識 agent
3. dashboard 根據 agent 身分，限制其可讀寫範圍

建議 header：

```http
X-Agent-Code: vm-app-01
X-Agent-Token: <secret>
```

middleware 驗證流程：

1. 讀取 `X-Agent-Code`
2. 以 `code` 查 `agents`
3. 比對 token hash
4. 確認 agent 為 enabled
5. 將 agent 資料放入 request context

## 5.2 新增 agent 管理 API

建議新增以下 API：

### 給 UI / 管理者使用

- `GET /api/agents`
- `POST /api/agents`
- `GET /api/agents/{id}`
- `PUT /api/agents/{id}`
- `PATCH /api/agents/{id}/toggle`
- `DELETE /api/agents/{id}`
- `POST /api/agents/{id}/test`

### 給 agent 自己使用

- `POST /api/agent/heartbeat`
- `GET /api/agent/me`
- `GET /api/agent/schedules/enabled`
- `GET /api/agent/schedules/{id}`
- `POST /api/agent/schedules/{id}/runtime`
- `POST /api/agent/schedules/{id}/status`
- `POST /api/agent/records`
- `PUT /api/agent/records/{id}`
- `GET /api/agent/projects/{id}`
- `GET /api/agent/projects/{id}/targets`
- `GET /api/agent/projects/{id}/retention`

重點是 agent 不應再透過一般 `/api/projects/{id}` 讀取資料，否則權限界線不清楚。

## 5.3 `trigger` 路由改成依 project 分派

`internal/api/trigger.go` 目前是：

- 讀 `AGENT_URL`
- 有值就全部轉發給那一台 agent

應改為：

1. 根據 `project_id` 查 `projects.executor_type`
2. 若 `agent`：
   - 查 `projects.executor_agent_id`
   - 再查 `agents.base_url`
   - 將 trigger 轉發給指定 agent
3. 若該專案被標記為 `local`，或指定 agent 不可用且允許 fallback，再由本機跑 `runner.RunProject`

這會是本次需求中最關鍵的後端邏輯。

## 5.4 `schedule` 變更時要通知對應 agent

`internal/api/schedules.go` 目前只會通知單一 agent reload/remove。

應改為：

1. schedule create/update/delete 時，先查 schedule 所屬 project
2. 若該 project 是 `agent`，只通知該專案所屬 agent
3. 若該 project 被標記為 `local`，才通知本機 scheduler

需要特別處理的情境：

- project 從 `agent` 改成 `local`
- project 從 `agent A` 改成 `agent B`

此時要做兩件事：

1. 舊執行端移除原本載入的 jobs
2. 新執行端重新載入所有該 project 的 schedules

## 5.5 Agent 端拉排程要加過濾

目前 `internal/api/agent.go` 的 `listEnabledSchedules` 會回傳所有 enabled schedules。

多 agent 模式應改成：

- 只回傳這台 agent 需要執行的 schedule
- SQL 建議 `JOIN projects p ON p.id = s.project_id`

篩選條件：

```sql
WHERE s.enabled = true
  AND p.enabled = true
  AND p.executor_type = 'agent'
  AND p.executor_agent_id = $current_agent_id
```

若保留本機 dashboard scheduler，則它只處理明確標記為保底模式的專案：

```sql
WHERE s.enabled = true
  AND p.enabled = true
  AND p.executor_type = 'local'
```

---

## 6. Store / Model 調整建議

## 6.1 `internal/store/models.go`

需新增：

- `Agent` struct
- `Project.ExecutorType`
- `Project.ExecutorAgentID`
- `BackupRecord.ExecutorType`
- `BackupRecord.AgentID`
- `BackupRecord.AgentName`
- `BackupRecord.RunHost`
- `BackupRecord.StartedAt`
- `BackupRecord.FinishedAt`

也要補齊：

- `ListAgents`
- `GetAgent`
- `CreateAgent`
- `UpdateAgent`
- `ToggleAgent`
- `DeleteAgent`
- `TouchAgentHeartbeat`
- `GetProjectExecutor`
- `ListEnabledSchedulesForAgent`
- `ListEnabledSchedulesForLocal`

## 6.2 `internal/client/client.go`

目前 agent 端 client 需要補兩件事：

1. 每次 request 都帶 `X-Agent-Code`
2. 讀 project/target/retention 改成呼叫 agent 專用 API

建議 client 初始化改為：

```go
client.New(dashboardURL, agentCode, agentToken)
```

---

## 7. Agent 程式調整

## 7.1 新增環境變數

目前 agent 只有：

- `DASHBOARD_URL`
- `AGENT_TOKEN`
- `AGENT_ADDR`

建議改為：

```env
DASHBOARD_URL=http://192.168.1.10:8080
AGENT_CODE=vm-app-01
AGENT_TOKEN=xxxx
AGENT_ADDR=:9090
HOST_PREFIX=
NAS_BASE=/mnt/nas/backups
```

建議新增 `AGENT_CODE`，不要只靠 token 識別。

## 7.2 啟動時做 heartbeat / 自我登錄

agent 啟動後應立即送：

```json
POST /api/agent/heartbeat
{
  "host_name": "vm-app-01",
  "ip_address": "192.168.1.21",
  "version": "1.0.0",
  "capabilities": {
    "docker": true,
    "journalctl": true,
    "rsync": true
  }
}
```

dashboard 收到後更新：

- `last_seen_at`
- `status=online`
- `host_name`
- `ip_address`
- `version`
- `capabilities`

並建議 agent 每 30~60 秒固定 heartbeat 一次。

## 7.3 執行備份時回傳更多上下文

`internal/backup/runner.go` 建立 record 時，建議把以下欄位一起寫入：

- `executor_type=agent`
- `agent_id`
- `agent_name`
- `run_host`
- `started_at`

完成時再更新：

- `status`
- `finished_at`
- `duration_sec`
- `size_bytes`
- `checksum`
- `error_msg`

## 7.4 agent 本機路徑前提

這個需求能否成功，取決於資料路徑是否真的存在於該 VM：

- `project_path`
- `backup_dirs`
- DB container 名稱或 DB host
- NAS 掛載路徑

因此 agent 被指派前，建議 UI 或後端提供「連線/路徑測試」：

1. dashboard 呼叫 agent test API
2. agent 回報：
   - NAS 路徑是否可寫
   - 專案路徑是否存在
   - DB host / port 是否可達
   - 若用 docker exec，container 是否存在

---

## 8. 前端 UI 調整

目前前端集中在 `cmd/dashboard/web/index.html`，本次變更建議如下。

## 8.1 新增「Agent 管理」頁

建議新增一個管理頁，至少顯示：

- 名稱
- code
- base_url
- 狀態
- 最後心跳時間
- 版本
- capabilities
- 是否啟用

可操作：

- 新增 agent
- 編輯 agent
- 啟用 / 停用
- 測試連線

## 8.2 專案表單新增執行節點選擇

在 `projectForm()` 裡新增欄位：

1. 執行來源
   - 指定 Agent 執行
   - 本機保底執行

2. Agent 選擇
   - 預設顯示並要求選擇 agent
   - 只有當執行來源切成 `local` 時才隱藏
   - 下拉選單資料來自 `/api/agents`

建議欄位如下：

```json
{
  "executor_type": "agent",
  "executor_agent_id": 2
}
```

## 8.3 專案列表與詳情頁顯示執行節點

建議在專案列表新增一欄：

- 執行位置：`本機` / `Agent: vm-app-01`

在專案詳情頁也應顯示目前綁定的 agent。

## 8.4 排程畫面顯示繼承的執行節點

若本版採「schedule 跟著 project」設計，排程畫面不一定要可編輯 agent，  
但至少要顯示：

- 執行節點：`本機` 或 `Agent: vm-app-01`

這可以降低使用者誤解。

## 8.5 備份紀錄顯示執行來源

在備份紀錄列表新增欄位：

- 執行節點
- 執行主機

例如：

- `本機`
- `vm-app-01`
- `vm-db-02`

---

## 9. 建議 API 契約

## 9.1 建立 / 更新專案

```json
{
  "name": "project-a",
  "description": "站台 A",
  "project_path": "/var/www/project-a",
  "backup_dirs": ["storage", "uploads"],
  "nas_base": "/mnt/nas/backups",
  "executor_type": "agent",
  "executor_agent_id": 2,
  "db_type": "postgres",
  "db_host": "127.0.0.1",
  "db_port": 5432,
  "db_name": "project_a",
  "db_user": "postgres",
  "db_password_env": "PROJECT_A_DB_PASSWORD",
  "docker_db_container": ""
}
```

## 9.2 Agent heartbeat

```json
{
  "host_name": "vm-app-01",
  "ip_address": "192.168.1.21",
  "version": "1.0.0",
  "capabilities": {
    "docker": true,
    "journalctl": false,
    "rsync": true
  }
}
```

## 9.3 Trigger 回應

```json
{
  "status": "triggered",
  "project_id": 12,
  "type": "all",
  "executor_type": "agent",
  "agent_id": 2,
  "agent_name": "vm-app-01",
  "message": "備份已交由 vm-app-01 執行"
}
```

---

## 10. 建議實作流程

建議分 4 個階段做，不要一次把所有東西一起改。

### 階段 1：資料模型與 API 基礎

1. 新增 `agents` migration
2. `projects` 增加 `executor_type / executor_agent_id`
3. `backup_records` 增加 agent 相關欄位
4. store / model 補齊 CRUD
5. 新增 `/api/agents` 管理 API

完成後，先能在 DB 與 UI 裡管理 agent。

### 階段 2：Agent 身分化

1. agent 增加 `AGENT_CODE`
2. `internal/api/agent.go` 改成 per-agent auth
3. `internal/client/client.go` 改為攜帶 `X-Agent-Code`
4. agent 加入 heartbeat

完成後，dashboard 可以知道目前有哪些 agent 在線上。

### 階段 3：執行分派

1. `trigger` 改成依 project 分派，預設轉發到指定 agent
2. schedule API 改成通知對應 agent
3. dashboard scheduler 只保留處理 `local` 專案
4. agent scheduler 只拉自己負責的 schedule

完成後，核心需求就成立了。

### 階段 4：UI 與可觀測性

1. 新增 agent 管理頁
2. project form 增加 agent 選擇
3. records 顯示執行節點
4. 加入 agent 測試與路徑驗證

完成後，使用者體驗才算完整。

---

## 11. `sadmin` 產生與下載 Agent 執行檔流程

這一段是補足目前文件尚未明確定義的「如何從管理端產生新的 VM agent 執行檔，並讓維運人員下載部署」流程。

### 11.1 建議定位

建議把 `sadmin` 視為：

- 管理 agent 發版
- 產生可下載的 agent 安裝包
- 顯示版本、checksum、建置時間
- 提供安裝指令與部署說明

也就是說，`sadmin` 不直接 SSH 進 VM 幫你安裝，而是負責：

1. 產生 agent release artifact
2. 提供下載
3. 提供對應版本的安裝與診斷指令
4. 回收安裝後的測試結果與 log

### 11.2 建議產出物格式

每次在 `sadmin` 產生新版本時，建議至少產出：

1. `backup-agent-linux-amd64`
2. `backup-agent-linux-arm64`（若環境需要）
3. `backup-agent_<version>_linux_amd64.tar.gz`
4. `backup-agent_<version>_checksums.txt`
5. `install-agent.sh`
6. `diagnose-agent.sh`

建議目錄：

```text
/sadmin/artifacts/backup-agent/<version>/
```

例如：

```text
/sadmin/artifacts/backup-agent/1.2.3/
  backup-agent-linux-amd64
  backup-agent_1.2.3_linux_amd64.tar.gz
  backup-agent_1.2.3_checksums.txt
  install-agent.sh
  diagnose-agent.sh
  manifest.json
```

`manifest.json` 建議內容：

```json
{
  "version": "1.2.3",
  "built_at": "2026-05-31T10:00:00Z",
  "commit": "abc1234",
  "files": [
    {
      "name": "backup-agent-linux-amd64",
      "os": "linux",
      "arch": "amd64",
      "sha256": "..."
    }
  ]
}
```

### 11.3 `sadmin` 需要提供的功能

建議新增：

- `建立新版本`：輸入 version 或從 git tag/commit 產生
- `查看版本列表`
- `下載 binary / tar.gz / checksum`
- `複製安裝指令`
- `複製升級指令`
- `查看該版本對應 release note`

建議 API：

- `POST /api/admin/agent-releases/build`
- `GET /api/admin/agent-releases`
- `GET /api/admin/agent-releases/{version}`
- `GET /api/admin/agent-releases/{version}/download/{file}`

### 11.4 VM 安裝與升級流程

建議實際維運流程如下：

1. 在 `sadmin` 建立新 agent 版本
2. 下載對應平台的安裝包
3. 將檔案放到目標 VM
4. 執行 `install-agent.sh`
5. 安裝腳本寫入：
   - agent binary
   - systemd service
   - `.env` 或對應設定檔
6. 腳本執行 `systemctl daemon-reload`
7. 腳本啟動或重啟 agent
8. agent 啟動後立即 heartbeat 到 dashboard
9. dashboard 將 agent 標記為 `online`
10. 自動執行 post-install 測試

安裝腳本建議支援：

```bash
./install-agent.sh \
  --binary ./backup-agent-linux-amd64 \
  --dashboard-url http://dashboard:8080 \
  --agent-code vm-app-01 \
  --agent-token 'xxxx' \
  --agent-addr :9090 \
  --nas-base /mnt/nas/backups
```

### 11.5 安裝成功判定條件

安裝完成不應只看 `systemctl start` 成功，還要同時滿足：

1. systemd service 狀態為 `active`
2. `/api/agent/heartbeat` 已成功回傳
3. dashboard 上可看到正確 version
4. dashboard 上可看到最後心跳時間更新
5. post-install 測試通過

若任一條件不成立，`sadmin` 應標記本次部署為失敗或待處理。

---

## 12. 自動測試與備份測試流程

文件原本提到「測試連線」與「路徑驗證」，但若要支援實際維運，需要再明確區分成安裝後測試、執行前測試與備份測試。

### 12.1 測試分層建議

建議至少拆成三層：

1. `connectivity test`
   - agent process 是否存活
   - dashboard 與 agent 是否互通
   - token / agent code 是否正確

2. `preflight test`
   - 專案路徑是否存在
   - NAS 路徑是否存在且可寫
   - DB host/port 是否可達
   - 若採 `docker exec`，container 是否存在

3. `backup smoke test`
   - 實際建立一份測試備份
   - 驗證產出檔案是否存在
   - 驗證檔案大小是否大於 0
   - 驗證 checksum 或壓縮檔可正常讀取

### 12.2 建議新增測試 API

建議新增：

- `POST /api/agents/{id}/test/connectivity`
- `POST /api/agents/{id}/test/preflight`
- `POST /api/projects/{id}/test-backup`
- `GET /api/test-runs/{id}`

回傳格式建議包含：

- `status`
- `started_at`
- `finished_at`
- `steps`
- `error_msg`
- `log_ref`

範例：

```json
{
  "status": "failed",
  "started_at": "2026-05-31T10:10:00Z",
  "finished_at": "2026-05-31T10:10:12Z",
  "steps": [
    {"name": "check_nas_write", "status": "success"},
    {"name": "check_project_path", "status": "success"},
    {"name": "check_db_connectivity", "status": "failed"}
  ],
  "error_msg": "dial tcp 10.0.0.5:5432: connect: connection refused",
  "log_ref": "agent-test-20260531-101012-vm-app-01.log"
}
```

### 12.3 備份測試模式建議

建議不要只有正式備份，還要明確支援：

1. `preflight-only`
   - 只驗證環境，不產出正式備份檔

2. `smoke-backup`
   - 產出一份測試備份
   - 寫到測試目錄，不寫正式目錄

3. `full-backup`
   - 正式備份流程

若要避免污染正式資料，測試備份建議統一寫到：

```text
<nas_base>/_tests/<project_slug>/<yyyyMMddHHmmss>/
```

### 12.4 測試完成條件

`smoke-backup` 建議至少驗證：

1. 檔案備份可完成
2. DB dump 可完成
3. 壓縮檔存在
4. 壓縮檔大小合理
5. manifest/checksum 可產生
6. dashboard 有正確紀錄結果

若之後要更完整，可再加 `restore drill`，但不建議放在第一版 MVP。

---

## 13. 錯誤 Log、診斷與除錯資料

目前文件提到 `error_msg` 與 `scripts/diagnose-agent.sh`，但若要真正可除錯，需要更明確的 log 設計。

### 13.1 建議區分三種除錯資料

1. `execution log`
   - 單次 trigger / schedule 執行的詳細輸出

2. `diagnostic bundle`
   - 安裝失敗、路徑異常、環境異常時的機器診斷資料

3. `service log`
   - agent 常駐程序的 systemd / journal 記錄

### 13.2 單次執行 Log 建議

每次備份或測試執行時，agent 應產出一份獨立 log：

```text
/var/log/backup-agent/runs/<run_id>.log
```

log 至少應包含：

- project id / project name
- agent code / host name
- executor type
- backup type
- nas target / nas path
- 每個步驟的開始與結束時間
- 失敗步驟
- 原始錯誤訊息

`backup_records` 建議再增加：

- `log_path`
- `log_download_token` 或 `log_ref`
- `test_mode`

### 13.3 診斷包建議

`scripts/diagnose-agent.sh` 建議輸出：

- `systemctl status backup-agent`
- `journalctl -u backup-agent -n 300`
- 掛載資訊 `mount`
- 磁碟資訊 `df -h`
- NAS 目錄測試結果
- project path 檢查結果
- DB 連線檢查結果
- agent 設定檔遮罩後內容
- agent version / checksum

並打包成：

```text
/tmp/backup-agent-diagnose-<timestamp>.tar.gz
```

### 13.4 Dashboard / `sadmin` 應提供的除錯能力

建議在 UI 上增加：

- 查看最近一次測試 log
- 查看最近一次備份 log
- 下載診斷包
- 顯示 `error_msg`
- 顯示失敗步驟
- 顯示 systemd service 狀態

建議 API：

- `GET /api/records/{id}/log`
- `GET /api/agents/{id}/diagnostics/latest`
- `POST /api/agents/{id}/diagnostics/collect`

---

## 14. NAS 選擇與路徑模型

原文件已有 `projects.nas_base` 與 `agents.nas_mount_base` 的概念，但若需求是「可選擇備份到哪一台串接 NAS，以及路徑是哪裡」，建議再補一層具名 NAS 模型。

### 14.1 建議新增 `nas_targets` 主表

```sql
CREATE TABLE nas_targets (
    id                SERIAL PRIMARY KEY,
    code              VARCHAR(50) UNIQUE NOT NULL,
    name              VARCHAR(100) NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    mount_type        VARCHAR(20) NOT NULL DEFAULT 'nfs',
    remote_path       TEXT NOT NULL DEFAULT '',
    default_subpath   TEXT NOT NULL DEFAULT '',
    enabled           BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

用途：

- `code`：例如 `nas-office-a`
- `name`：UI 顯示名稱
- `remote_path`：例如 `10.0.0.20:/volume1/backup`
- `default_subpath`：預設備份子路徑

### 14.2 Agent 與 NAS 的可用性關係

因為不是每台 VM 都一定掛了所有 NAS，建議再建立對應表：

```sql
CREATE TABLE agent_nas_targets (
    agent_id          INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    nas_target_id     INTEGER NOT NULL REFERENCES nas_targets(id) ON DELETE CASCADE,
    mount_base        TEXT NOT NULL,
    writable          BOOLEAN NOT NULL DEFAULT true,
    last_checked_at   TIMESTAMPTZ,
    PRIMARY KEY (agent_id, nas_target_id)
);
```

這樣 UI 在選某台 agent 時，就能只顯示該 VM 真正可用的 NAS 清單。

### 14.3 Project 層設定建議

建議 `projects` 追加：

```sql
ALTER TABLE projects
    ADD COLUMN nas_target_id INTEGER REFERENCES nas_targets(id) ON DELETE SET NULL,
    ADD COLUMN nas_subpath TEXT NOT NULL DEFAULT '';
```

欄位語意：

- `nas_target_id`：要備份到哪一台 NAS
- `nas_subpath`：該專案在此 NAS 下的子路徑

實際寫入路徑建議組成方式：

```text
<agent_nas_targets.mount_base>/<projects.nas_subpath>
```

例如：

```text
/mnt/nas-office-a/backups/project-a
```

### 14.4 UI 操作建議

在 project form 補兩個欄位：

1. NAS 目標
   - 下拉選單顯示 `nas_targets`
   - 並依 `executor_agent_id` 過濾

2. NAS 子路徑
   - 例如 `backups/project-a`

此外，agent 管理頁建議可顯示：

- 這台 VM 掛了哪些 NAS
- 掛載點在哪裡
- 最近一次寫入測試結果

### 14.5 寫入前驗證

在正式備份與 `smoke-backup` 前，都應先驗證：

1. project 指定的 NAS 是否存在於該 agent 可用清單
2. mount base 是否存在
3. 目標子路徑是否可建立
4. 寫入測試是否成功

若失敗，應直接中止並回傳明確錯誤，例如：

- `agent vm-app-01 does not support nas_target nas-office-b`
- `mount base /mnt/nas-office-a not found`
- `target path is not writable`

---

## 15. 建議修改檔案清單

### Migration

- `migrations/014_agents.sql`
- `migrations/015_projects_executor.sql`
- `migrations/016_backup_records_agent_columns.sql`
- `migrations/017_nas_targets.sql`
- `migrations/018_agent_nas_targets.sql`
- `migrations/019_projects_nas_target.sql`

### Store / Model

- `internal/store/models.go`
- 若有拆檔，可新增 `internal/store/agents.go`

### Dashboard API

- `internal/api/agent.go`
- `internal/api/projects.go`
- `internal/api/schedules.go`
- `internal/api/trigger.go`
- 新增 `internal/api/agents.go`
- 視需要新增 `internal/api/agent_releases.go`
- 視需要新增 `internal/api/test_runs.go`
- 視需要新增 `internal/api/nas_targets.go`

### Agent Client / Runtime

- `internal/client/client.go`
- `cmd/agent/main.go`
- 視需要新增 `internal/agent/identity.go`
- 視需要新增 `internal/agent/logging.go`
- 視需要新增 `internal/agent/diagnostics.go`

### Scheduler

- `internal/scheduler/scheduler.go`
- `cmd/dashboard/main.go`

### Backup Record Metadata

- `internal/backup/runner.go`

### Frontend

- `cmd/dashboard/web/index.html`
- 若 `sadmin` 為獨立前端，需補對應 release / log / diagnostics 頁面

### Scripts

- `scripts/install-agent.sh`
- `scripts/diagnose-agent.sh`
- `scripts/backup-agent.service`
- 視需要新增 `scripts/upgrade-agent.sh`
- 視需要新增 `scripts/smoke-backup.sh`

---

## 16. 關鍵設計決策

## 16.1 建議以 project 綁 agent，不要先做 target 綁 agent

原因：

1. 路徑與 DB 存取通常屬於同一台 VM
2. UI 比較簡單
3. schedule 跟著 project 即可，不會出現資料不一致
4. 目前需求已足夠滿足

若一開始就做到 target 綁 agent，複雜度會多很多：

- `files` 在 VM-A
- `database` 在 VM-B
- 同一 project 變成跨節點協調

這不適合作為第一版。

## 16.2 NAS 掛載路徑建議統一

最佳做法是所有 agent VM 都把 NAS 掛在相同路徑，例如：

```text
/mnt/nas/backups
```

這樣現有 `projects.nas_base` 幾乎不用改。

如果不同 VM 掛載路徑不同，才需要額外做：

- agent 層 path mapping
- 或 `agents.nas_mount_base`
- 或更完整的 `agent_nas_targets.mount_base`

但這會讓除錯與遷移都更麻煩。

## 16.3 建議保留 local 保底模式

不要直接移除 dashboard 本機執行能力，但它應該被定位成保底能力，而不是主要模式。原因是：

1. 舊專案或臨時任務在遷移期間可先保留既有行為
2. agent 故障或網路異常時，維運上仍有回退手段
3. 緊急情況下可快速切回本機執行

---

## 17. 風險與注意事項

### 17.1 路徑不一致

若 project 設定的是 `/var/www/app`，但該 agent VM 上根本沒有這個路徑，備份一定失敗。  
所以指派 agent 前，必須驗證該 VM 是否真的擁有該專案路徑。

### 17.2 DB 連線方式要符合 VM 環境

若目前 DB 備份是靠 `docker exec`，則該 container 必須在對應 agent VM 上存在。  
若改走 direct host/port，則要確認該 VM 能連到 DB。

### 17.3 Token 不可共用

多 agent 環境若還共用同一把 `AGENT_TOKEN`，後續很難做權限控制、稽核與故障排查。  
建議每台 agent 都有獨立 token。

### 17.4 排程轉移要處理舊 job 清理

當 project 從 VM-A 改到 VM-B 時，必須：

1. 通知 VM-A remove jobs
2. 通知 VM-B reload jobs

否則可能發生同一排程被兩邊都執行。

### 17.5 測試備份不可污染正式備份目錄

若 `smoke-backup` 直接寫入正式目錄，容易讓 retention、容量計算與使用者辨識全部混亂。  
因此測試備份必須寫入獨立測試目錄，並在 UI 上明確標示為 `test run`。

### 17.6 log 與診斷資料可能包含敏感資訊

診斷輸出可能包含：

- 掛載資訊
- IP / hostname
- DB host
- service env

因此：

1. UI 顯示前要遮罩敏感欄位
2. 診斷包下載應有權限控管
3. 不應把明文 token 或密碼寫入 log

---

## 18. 最小可行版本建議

如果要先做一版可上線的 MVP，我建議只做以下範圍：

1. 新增 `agents` 表與管理 API
2. `projects` 增加 `executor_type / executor_agent_id`
3. `trigger` 改成依 project 指定 agent
4. agent 改成有 `AGENT_CODE`
5. schedule 改成 agent 只拉自己的 jobs
6. `backup_records` 增加 agent 顯示欄位
7. `sadmin` 可產生與下載 agent release artifact
8. `install-agent.sh` 支援 version / checksum / post-install test
9. 至少提供 `connectivity test` 與 `preflight test`
10. UI 專案表單可選擇 agent
11. UI 可選 NAS 目標與 NAS 子路徑
12. UI 備份紀錄顯示執行節點與錯誤摘要

這樣就已經能完整滿足：

- 另一台 VM 安裝 agent
- 備份直接從該 VM 寫 NAS
- dashboard 可看到結果
- 新增 / 編輯專案時可指定哪台 VM 執行
- 可選擇備份到哪個 NAS 與路徑
- 失敗時能取得基本除錯資訊

---

## 19. 結論

這個專案其實不需要重做 agent。  
現有 `backup-agent`、`runner`、`DashboardClient`、`record` 回傳機制都可以沿用，真正要補的是：

- 多 agent 的資料模型
- agent 身分識別與分派
- `sadmin` 發版與下載流程
- 自動測試與備份測試
- log / diagnostics 除錯能力
- NAS 目標與路徑模型

最合理的實作方向是：

1. 新增 `agents` 主表
2. project 綁定執行來源與指定 agent
3. `sadmin` 能產生與下載對應版本的 agent artifact
4. trigger / schedule 改成依 project 分派
5. agent 只讀取自己的專案與排程
6. record 與 test run 補上執行節點、錯誤與 log 資訊
7. project 可選擇 NAS 目標與子路徑
8. 前端提供 agent 管理、測試、log 與 project 綁定操作

這樣做可以最小幅度地延伸現有架構，同時讓系統真正支援同區網多台 VM 的分散式備份執行，並補齊你要的部署、測試、除錯與 NAS 選擇閉環。

---

## 20. 實作收斂建議

前面章節已把完整能力攤開，但若直接全部一起做，範圍會過大。  
下面是建議的收斂版本，目標是先做出「可用、可部署、可測試、可除錯」的一版。

### 20.1 第一版 MVP 必做範圍

第一版只做以下能力：

1. 多 agent 註冊與指派
2. project 可選執行 VM
3. project 可選 NAS 與備份子路徑
4. `sadmin` 可產生並下載 agent 安裝包
5. agent 安裝後會 heartbeat
6. dashboard 可手動 trigger 到指定 agent
7. agent 可執行 `preflight test`
8. agent 可執行 `smoke-backup`
9. 失敗時可回傳 `error_msg` 與單次執行 log
10. UI 可看到 agent 狀態、測試結果、備份結果

### 20.2 第一版先不要做的項目

以下功能先延後，不要進第一版：

1. dashboard 主動 SSH / 遠端安裝 VM
2. 自動升級整批 agent
3. restore drill
4. schedule override 到不同 agent
5. target 級別綁 agent
6. 跨 VM 協調同一 project 的檔案與 DB
7. 複雜的 log 權限 token 機制
8. 多平台 artifact 細分到過多變體

### 20.3 第一版資料模型最小集合

第一版資料表只需要先補：

1. `agents`
2. `projects.executor_type`
3. `projects.executor_agent_id`
4. `projects.nas_target_id`
5. `projects.nas_subpath`
6. `backup_records.agent_id`
7. `backup_records.agent_name`
8. `backup_records.run_host`
9. `backup_records.error_msg`
10. `backup_records.log_ref`
11. `nas_targets`
12. `agent_nas_targets`

可先不做：

- `log_download_token`
- 太細的 capabilities schema
- 額外 deployment history 表

### 20.4 第一版 API 最小集合

管理端：

- `GET /api/agents`
- `POST /api/agents`
- `PUT /api/agents/{id}`
- `POST /api/agents/{id}/test/preflight`
- `POST /api/projects/{id}/test-backup`
- `GET /api/nas-targets`
- `POST /api/admin/agent-releases/build`
- `GET /api/admin/agent-releases`
- `GET /api/admin/agent-releases/{version}/download/{file}`

agent 端：

- `POST /api/agent/heartbeat`
- `GET /api/agent/schedules/enabled`
- `POST /api/agent/records`
- `PUT /api/agent/records/{id}`
- `GET /api/agent/projects/{id}`

可第二階段再補：

- diagnostics collect/download API
- test-runs 獨立查詢 API
- agent me API

### 20.5 第一版 UI 最小集合

只保留三個重點畫面：

1. Agent 管理頁
   - agent 清單
   - online/offline
   - 最後心跳
   - preflight test

2. Project 表單
   - executor type
   - executor agent
   - nas target
   - nas subpath

3. Backup records / test records
   - 執行節點
   - NAS 目標
   - 狀態
   - `error_msg`
   - `log_ref`

### 20.6 第一版 artifact 與安裝流程

第一版 `sadmin` 只需要做到：

1. build 一個 `linux-amd64` agent binary
2. 打包 `tar.gz`
3. 產出 `checksums.txt`
4. 提供下載連結
5. 附上固定格式安裝指令

先不要做：

- arm64
- 差分升級
- UI 內直接推送到 VM

### 20.7 第一版測試收斂

第一版只做兩種測試：

1. `preflight test`
   - 檢查 project path
   - 檢查 NAS 可寫
   - 檢查 DB 可達或 container 存在

2. `smoke-backup`
   - 寫到 `_tests/` 目錄
   - 建立最小備份檔
   - 回傳結果與 log

先不要做：

- restore test
- retention 驗證
- 長時間壓力測試

### 20.8 第一版 log 收斂

第一版不做複雜 log 平台整合，只要：

1. 每次執行產出 `/var/log/backup-agent/runs/<run_id>.log`
2. `backup_records` 記錄 `log_ref`
3. UI 可顯示 `error_msg`
4. 維運人員可透過 VM 或後續 API 取回 log

若時間不足，`GET /api/records/{id}/log` 甚至可放到第二版。

### 20.9 建議實作順序

請依下面順序做，不要並行發散：

1. Migration 與 store model
2. agent auth 與 heartbeat
3. project -> agent 分派 trigger
4. nas target 資料模型與 project 綁定
5. `preflight test`
6. `smoke-backup`
7. `backup_records` 補 agent / log 欄位
8. Agent 管理頁與 project form
9. `sadmin` artifact build/download
10. log 查詢或 diagnostics 補強

### 20.10 完成判定

這份需求可以視為完成，至少要滿足以下驗收條件：

1. 可在 `sadmin` 產生一版新的 agent 安裝包
2. 可下載並安裝到指定 VM
3. agent 安裝後會在 dashboard 顯示 `online`
4. project 可指定執行 VM
5. project 可指定 NAS 目標與路徑
6. 可手動執行 `preflight test`
7. 可手動執行 `smoke-backup`
8. 備份檔會寫入指定 NAS 路徑
9. 失敗時 dashboard 至少看得到 `error_msg`
10. 維運人員能取得單次執行 log 進行除錯

---

## 21. 開發 Backlog

以下 backlog 以「可直接開工」為目標，盡量避免抽象任務名稱。  
每個 task 都附上依賴、主要修改檔案與驗收點。

### 21.1 Phase 1：資料模型與基礎 API

#### BG-001 建立 `agents` migration

- 目標：新增 agent 主表，支援多 VM 註冊與心跳狀態保存。
- 依賴：無
- 主要檔案：
  - `migrations/014_agents.sql`
- 驗收：
  - migration 可成功執行
  - `agents` 表包含 `code`、`name`、`base_url`、`token_hash`、`status`、`last_seen_at`

#### BG-002 建立 `nas_targets` 與 `agent_nas_targets` migration

- 目標：讓 project 可以選具名 NAS，且 agent 只綁定自己可用的 NAS。
- 依賴：BG-001
- 主要檔案：
  - `migrations/017_nas_targets.sql`
  - `migrations/018_agent_nas_targets.sql`
- 驗收：
  - migration 可成功執行
  - 可建立 NAS 主資料
  - 可建立 agent 與 NAS 的對應關係

#### BG-003 擴充 `projects` 與 `backup_records` migration

- 目標：把執行 VM、NAS 目標、log 與錯誤資訊接進既有資料模型。
- 依賴：BG-001、BG-002
- 主要檔案：
  - `migrations/015_projects_executor.sql`
  - `migrations/016_backup_records_agent_columns.sql`
  - `migrations/019_projects_nas_target.sql`
- 驗收：
  - `projects` 具有 `executor_type`、`executor_agent_id`、`nas_target_id`、`nas_subpath`
  - `backup_records` 具有 `agent_id`、`agent_name`、`run_host`、`error_msg`、`log_ref`

#### BG-004 更新 store model 與 CRUD

- 目標：讓後端程式可以讀寫新的 agent / NAS / project executor 資料。
- 依賴：BG-001、BG-002、BG-003
- 主要檔案：
  - `internal/store/models.go`
  - `internal/store/agents.go`
  - `internal/store/projects.go`
  - `internal/store/records.go`
- 驗收：
  - 可列出 / 建立 / 更新 agent
  - 可列出 NAS targets
  - 可正確讀取 project 的 executor 與 NAS 設定

#### BG-005 新增管理端基礎 API

- 目標：先把最小管理介面所需的 API 補齊。
- 依賴：BG-004
- 主要檔案：
  - `internal/api/agents.go`
  - `internal/api/projects.go`
  - 視需要調整 `cmd/dashboard/main.go`
- 驗收：
  - `GET /api/agents` 可用
  - `POST /api/agents` 可用
  - `PUT /api/agents/{id}` 可用
  - `GET /api/nas-targets` 可用
  - project create/update 可接受 executor 與 NAS 欄位

### 21.2 Phase 2：Agent 身分化與分派

#### BG-006 改成 per-agent 驗證

- 目標：移除單一全域 `AGENT_TOKEN` 模式，改成 `AGENT_CODE + AGENT_TOKEN`。
- 依賴：BG-005
- 主要檔案：
  - `internal/api/agent.go`
  - `internal/client/client.go`
  - `cmd/agent/main.go`
- 驗收：
  - agent request 需攜帶 `X-Agent-Code`
  - dashboard 可依 `code` 查 agent 並驗證 token
  - 未授權 agent 無法呼叫 agent API

#### BG-007 實作 heartbeat

- 目標：讓 dashboard 知道 agent 是否在線、版本是什麼。
- 依賴：BG-006
- 主要檔案：
  - `internal/api/agent.go`
  - `cmd/agent/main.go`
  - `internal/store/agents.go`
- 驗收：
  - agent 啟動後會送 heartbeat
  - dashboard 會更新 `last_seen_at`、`status`、`version`

#### BG-008 `trigger` 改成依 project 分派

- 目標：手動備份時，能依 project 綁定的 VM 轉發到正確 agent。
- 依賴：BG-006、BG-007
- 主要檔案：
  - `internal/api/trigger.go`
  - `internal/store/projects.go`
- 驗收：
  - 指定 `executor_type=agent` 的 project 會被轉發到對應 agent
  - 指定 `executor_type=local` 的 project 仍可本機執行

#### BG-009 排程只分派給對應 agent

- 目標：避免所有 agent 都拉到全部 schedule。
- 依賴：BG-008
- 主要檔案：
  - `internal/api/agent.go`
  - `internal/scheduler/scheduler.go`
  - `cmd/dashboard/main.go`
- 驗收：
  - agent 只會拉到自己負責的 schedules
  - dashboard local scheduler 只處理 `local` project

### 21.3 Phase 3：NAS 綁定與環境測試

#### BG-010 Project 綁定 NAS target 與 subpath

- 目標：讓每個 project 可選哪一台 NAS 與寫入哪個子路徑。
- 依賴：BG-005
- 主要檔案：
  - `internal/api/projects.go`
  - `internal/store/projects.go`
- 驗收：
  - project create/update 可保存 `nas_target_id` 與 `nas_subpath`
  - project read API 會回傳 NAS 設定

#### BG-011 實作 `preflight test`

- 目標：在正式備份前，先檢查 project path、NAS 與 DB 條件。
- 依賴：BG-007、BG-010
- 主要檔案：
  - `internal/api/agents.go`
  - `cmd/agent/main.go`
  - 視需要新增 `internal/agent/diagnostics.go`
- 驗收：
  - `POST /api/agents/{id}/test/preflight` 可用
  - 回傳至少包含 path、NAS、DB 三類檢查結果
  - 失敗時有明確 `error_msg`

### 21.4 Phase 4：備份測試與 log

#### BG-012 實作 `smoke-backup`

- 目標：在 `_tests/` 路徑產出測試備份，驗證整條鏈路可用。
- 依賴：BG-008、BG-010、BG-011
- 主要檔案：
  - `internal/api/trigger.go`
  - `internal/backup/runner.go`
  - 視需要新增 `scripts/smoke-backup.sh`
- 驗收：
  - `POST /api/projects/{id}/test-backup` 可用
  - 測試備份寫入 `_tests/` 子路徑
  - dashboard 可看到成功或失敗結果

#### BG-013 實作單次執行 log

- 目標：讓每次測試或備份都有對應 log 可追查。
- 依賴：BG-012
- 主要檔案：
  - `internal/backup/runner.go`
  - 視需要新增 `internal/agent/logging.go`
  - `internal/store/records.go`
- 驗收：
  - 執行時會產生 `/var/log/backup-agent/runs/<run_id>.log`
  - `backup_records.log_ref` 會被寫入
  - 失敗時 `backup_records.error_msg` 會被寫入

### 21.5 Phase 5：前端最小閉環

#### BG-014 Agent 管理頁

- 目標：能看到 agent 狀態並手動執行 preflight test。
- 依賴：BG-005、BG-007、BG-011
- 主要檔案：
  - `cmd/dashboard/web/index.html`
- 驗收：
  - 可列出 agent
  - 可顯示 online/offline、version、last_seen_at
  - 可從 UI 觸發 preflight test

#### BG-015 Project 表單補 executor 與 NAS 欄位

- 目標：讓使用者在 UI 指定 VM 與 NAS。
- 依賴：BG-005、BG-010
- 主要檔案：
  - `cmd/dashboard/web/index.html`
- 驗收：
  - project form 可選 executor type
  - 當 executor 為 agent 時可選 agent
  - 可選 NAS target 與輸入 NAS subpath

#### BG-016 Records 畫面補執行節點與錯誤摘要

- 目標：讓使用者能在 UI 上看到是哪台 VM 執行、失敗原因是什麼。
- 依賴：BG-013
- 主要檔案：
  - `cmd/dashboard/web/index.html`
- 驗收：
  - records 畫面顯示 agent / host / NAS / `error_msg` / `log_ref`

### 21.6 Phase 6：`sadmin` Artifact Build / Download

#### BG-017 建立 agent artifact build 流程

- 目標：可從 `sadmin` 產生 `linux-amd64` agent binary、tarball 與 checksum。
- 依賴：無
- 主要檔案：
  - 視實際結構新增 `internal/api/agent_releases.go`
  - 視需要新增 build script
- 驗收：
  - 可建立一版 release artifact
  - 產出 binary、`tar.gz`、`checksums.txt`、`manifest.json`

#### BG-018 提供 artifact download 與安裝指令

- 目標：讓維運人員可下載指定版本並照指令安裝到 VM。
- 依賴：BG-017
- 主要檔案：
  - `internal/api/agent_releases.go`
  - `scripts/install-agent.sh`
- 驗收：
  - 可下載指定版本 artifact
  - UI 或 API 可取得固定格式安裝指令
  - 安裝後 agent 可成功 heartbeat

### 21.7 建議開工順序

建議照下面順序建立工作單：

1. BG-001
2. BG-002
3. BG-003
4. BG-004
5. BG-005
6. BG-006
7. BG-007
8. BG-008
9. BG-009
10. BG-010
11. BG-011
12. BG-012
13. BG-013
14. BG-014
15. BG-015
16. BG-016
17. BG-017
18. BG-018

### 21.8 第一輪驗收組合

若要做第一輪整體驗收，建議至少等以下 task 完成後一起驗：

1. BG-001 ~ BG-013
2. BG-015
3. BG-017 ~ BG-018

這樣就能驗證：

- agent 可註冊
- project 可綁 VM 與 NAS
- 可做 preflight test
- 可做 smoke-backup
- 可產生與下載 agent 安裝包
- 失敗時可透過 record 與 log 除錯
