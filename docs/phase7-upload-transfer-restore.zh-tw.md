# Phase 7：上傳中轉備份與還原設計文件

> 需求：在現有「agent 直寫 NAS」之外，新增一種「agent 上傳備份檔到 dashboard，由 dashboard 轉存到 NAS」的傳輸模式；並補上還原（restore）能力。

---

## 目錄

1. [需求摘要](#1-需求摘要)
2. [現況評估](#2-現況評估)
3. [新架構與資料流](#3-新架構與資料流)
4. [核心設計決策](#4-核心設計決策)
5. [資料模型調整](#5-資料模型調整)
6. [API 設計](#6-api-設計)
7. [Agent Client 串流](#7-agent-client-串流)
8. [Runner 分流設計](#8-runner-分流設計)
9. [還原流程](#9-還原流程)
10. [前端調整](#10-前端調整)
11. [實作工作項目](#11-實作工作項目)
12. [風險與注意事項](#12-風險與注意事項)
13. [建議實作順序](#13-建議實作順序)

---

## 1. 需求摘要

目前系統的備份主流程是：`backup-agent`（systemd, root）在自己的 VM 上執行
`docker exec pg_dump` / `tar.gz`，然後**直接把備份檔寫入本機掛載的 NAS 路徑**
（`/mnt/nas/backups`）。Agent 只把備份紀錄（metadata）透過 HTTP 回傳 dashboard，
**檔案本身從不經過 dashboard**。

本次需求要新增第二種傳輸模式：

1. Agent 不直接寫 NAS，改為把備份檔**上傳到 dashboard 的 API**。
2. 由**有掛載 NAS 的 dashboard** 把檔案落地到 NAS，完成備份。
3. 還原（restore）也要：dashboard 從 NAS 取檔，串流回 agent，由 agent 還原。

這個模式解決的核心問題：**當 agent 所在的 VM「碰不到 NAS」（沒掛載 / 跨網段 /
防火牆隔離）時，改用「經由 dashboard 中轉」的方式完成備份與還原。**

---

## 2. 現況評估

### 2.1 目前的備份方案（確認結果）

使用者原本的認知「只有一種：透過 agent 複製到同一台 VM 的 NAS」— **基本正確，
但要補充兩點**：

1. **主流程確實是 agent → NAS**：agent 在 host 上執行備份，直接寫入本機掛載的
   NAS 路徑（`internal/backup/files.go:33`、`internal/backup/runner.go:62`）。
   Agent 只把備份紀錄回傳 dashboard。
2. **架構其實已經是「多 agent 版」**：README 描述的是舊的單 agent 模型，但實際
   程式碼（`internal/api/agent.go` 的 `X-Agent-Code`、`agent_nas_targets`、
   `ResolveProjectNASForAgent`、migrations 014–020）已演進成**多 VM agent** 模型
   —— 每台 VM 各自掛 NAS、各自寫入。詳見 `docs/multi-agent-vm-backup-design.zh-tw.md`。

精確描述：**「每個 project 指派給一台 agent，該 agent 在自己的 VM 上執行備份並
寫入自己掛載的 NAS」**。關鍵前提是 —— **執行備份的那台 VM 必須自己掛載得到 NAS**。

### 2.2 現有可重用能力

本次需求對現有架構是**低侵入擴充**，因為以下基礎設施已經存在：

- **dashboard 容器已掛載 `/mnt/nas`**（`docker-compose.yml:37`），
  「dashboard 落地 NAS」的基礎設施現成，**不需改 compose**。
- 上傳/下載 API 可**直接複用**既有的 `agentMiddleware` 身分驗證
  （`internal/api/agent.go:23`）與 `AgentOwnsProject` 授權檢查。
- 核心產檔邏輯（`BackupFiles` / `BackupDatabase`、sha256 計算、`backup_records`
  寫入）**不用改**，只在「檔案落地」這一步做分流。
- `Runner` 依賴倒置設計（`BackingStore` 介面）可沿用，上傳能力另以介面注入。

### 2.3 需要補的能力

- Agent client 目前只走 JSON（`internal/client/client.go` 的 `do()`），
  需新增 streaming 上傳/下載方法，且現有寫死的 60s timeout 對大檔會爆。
- 系統**目前完全沒有 restore 功能**，這是全新的一塊。

---

## 3. 新架構與資料流

### 3.1 核心設計：在「檔案落地」這一步做分流

現有 `Runner.RunTargetWithOptions`（`internal/backup/runner.go:50`）的流程是：
產檔 → 直接寫到 `destPath`（NAS 本機路徑）。新架構在產檔之後、落地時分兩條路：

```
                        ┌─ transfer_mode = "direct" → 寫本機 NAS 掛載路徑（現況，不變）
產出 .tar.gz/.sql.gz ──┤
   (含 sha256)          └─ transfer_mode = "upload" → 串流 POST 到 dashboard → dashboard 寫 /mnt/nas
```

### 3.2 備份資料流（upload 模式）

1. Agent 依 project 設定判斷 `transfer_mode = upload`。
2. Agent 執行備份，**先產出到本機暫存目錄**（`/var/tmp/backup-agent/staging/`），
   同步計算 sha256。
3. Agent 建立 `backup_record`（status=running）。
4. Agent 串流上傳暫存檔到 `POST /api/agent/records/{id}/upload`，
   header 帶 `X-Backup-Sha256`、`X-Backup-Filename`。
5. Dashboard 邊 `io.Copy` 寫入 `/mnt/nas/backups/<project 的 NAS 路徑>/<type>/<filename>`，
   邊重算 sha256。
6. Dashboard 寫完比對 sha256，不符 → 刪檔 + 回 400；相符 → 更新該 record 的 `path`。
7. Agent 上傳成功後**刪除本機暫存檔**，更新 record 為 success / failed。

### 3.3 還原資料流

1. 使用者在 UI 選一筆備份紀錄，按「還原」，選擇還原策略（新位置 / 覆蓋）。
2. Dashboard 收到 `POST /api/restore`，查 project 綁定的 agent，轉發到 agent `/restore`。
3. Agent 呼叫 `GET /api/agent/records/{id}/download`，dashboard 從 NAS 串流回檔案。
4. Agent 依 target type 還原：
   - `files`：解 tar.gz 到目標路徑。
   - `database`：`gunzip` → `psql` / `mysql` 或 `docker exec ... < dump`。
5. 依使用者選的策略決定還原到「新名稱 / 新路徑」或「覆蓋原位置」。
6. Agent 回報還原結果，dashboard 記錄還原歷史。

---

## 4. 核心設計決策

以下為與需求方確認後拍板的決策：

| 項目 | 決策 |
|---|---|
| **模式關係** | 兩種並存，agent 可選「直寫 NAS (direct)」或「上傳中轉 (upload)」，**逐 project 切換** |
| **傳輸方式** | HTTP 串流（raw body / multipart）+ sha256 校驗 |
| **agent 暫存** | 寫本機暫存目錄（`/var/tmp/backup-agent/staging/`），**上傳完即刪** |
| **還原範圍** | 完整還原（下載 API + agent 端解壓 / DB import） |
| **DB 還原策略** | **兩種都支援，使用者選**：還原到新名稱/新路徑，或覆蓋原位置（覆蓋需二次確認） |
| **暫存 vs 純串流** | 採「先產到暫存檔再上傳」，MVP **穩定優先、可重試**（非純串流 pipe） |

---

## 5. 資料模型調整

### 5.1 Migration

新增 `migrations/021_projects_transfer_mode.sql`（編號接續現有到 020）：

```sql
ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS transfer_mode VARCHAR(20) NOT NULL DEFAULT 'direct';
    -- 'direct' = agent 直寫本機 NAS（現況）
    -- 'upload' = agent 上傳到 dashboard，由 dashboard 落地 NAS
```

若要保留還原歷史，可另建 `restore_records` 表或複用 `backup_records` 記錄還原事件
（實作時決定；MVP 可先用簡單 log + record）。

### 5.2 Store / Model

- `internal/store/models.go`：`Project` struct 新增 `TransferMode string`。
- `internal/store/projects.go`：projects CRUD 讀寫 `transfer_mode`。

---

## 6. API 設計

所有 agent 端 API 沿用既有 `agentMiddleware`（`X-Agent-Code` + `X-Agent-Token`），
並複用 `AgentOwnsProject` 授權檢查。

### 6.1 上傳 API（dashboard 端）

```
POST /api/agent/records/{id}/upload
Headers:
  X-Agent-Code:    <agent code>
  X-Agent-Token:   <agent token>
  X-Backup-Sha256: <hex sha256>
  X-Backup-Filename: <filename>
Body: raw stream（或 multipart/form-data）
```

行為：
1. 驗證 agent 身分與該 record 所屬 project 是否歸該 agent。
2. `io.Copy` 串流寫到 `/mnt/nas/backups/<project NAS 路徑>/<type>/<filename>`
   （複用 `projectNASRoot` 的路徑邏輯，在 dashboard 端 resolve）。
3. 邊寫邊算 sha256，寫完比對 `X-Backup-Sha256`，不符 → 刪檔 + 回 400。
4. 成功 → 更新該 `backup_record` 的 `path` → 回 200。

### 6.2 下載 API（還原用，dashboard 端）

```
GET /api/agent/records/{id}/download
```

行為：授權同上，`http.ServeContent` / `io.Copy` 從 NAS 串流回 agent。

### 6.3 還原觸發 API（dashboard 端）

```
POST /api/restore
Body: { "record_id": 123, "project_id": 12, "strategy": "new" | "overwrite", "target": "..." }
```

行為：查 project 綁定的 agent → 轉發到該 agent 的 `/restore`。

---

## 7. Agent Client 串流

`internal/client/client.go` 目前 `do()` 只走 JSON marshal，需新增串流方法：

- `UploadBackup(ctx, recordID, filePath, checksum)`：
  `os.Open` + `io.Copy` 到 request body，帶 sha256 / filename header。
  **需放寬此 client 的 timeout**（現在寫死 60s，大檔會爆）—— 上傳用獨立
  `http.Client` 或 per-request context timeout。
- `DownloadBackup(ctx, recordID, destPath)`：
  串流下載並寫到目標檔。

---

## 8. Runner 分流設計

`internal/backup/runner.go`：

- 產檔後判斷 `proj.TransferMode`：
  - `upload` → 產到暫存目錄 → `client.UploadBackup` → 刪暫存。
  - `direct` → 現況不變（直接產到 NAS `destPath`）。
- `Runner` 目前依賴 `BackingStore` 介面。上傳能力**另外用介面注入**：

```go
// 只有 agent 端的 DashboardClient 實作它；
// dashboard 本機執行時為 nil，自動走 direct。
type Uploader interface {
    UploadBackup(ctx context.Context, recordID int64, filePath, checksum string) error
}

type Runner struct {
    Store    BackingStore
    Uploader Uploader        // 新增，可為 nil
    Notifier *notify.Slack
}
```

這樣不破壞現有依賴倒置設計，dashboard 與 agent 共用同一個 `Runner`。

---

## 9. 還原流程

### 9.1 Agent restore handler

`cmd/agent/main.go` 新增 `POST /restore`（走既有 `auth` middleware）：

1. `DownloadBackup` 從 dashboard 取檔到暫存目錄。
2. 依 target type 還原：
   - `files`：解 tar.gz 到目標路徑。
   - `database`：`gunzip` → `psql` / `mysql`，或 `docker exec <container> ... < dump`。
3. 依 `strategy` 分支：
   - `new`：還原到新 database 名（如 `xxx_restored`）或新目錄（**安全，不覆蓋現有資料**）。
   - `overwrite`：還原到原 database / 原路徑（**危險，覆蓋後無法復原**）。

### 9.2 DB 還原策略（兩種都支援）

| 策略 | 行為 | 風險 |
|---|---|---|
| `new`（新位置） | DB 還原到新 database 名（如 `xxx_restored`），檔案解壓到新目錄。驗證後手動切換。 | 低，不損壞現有資料 |
| `overwrite`（覆蓋） | 還原到原 database / 原路徑，UI 需輸入確認字串才執行。 | 高，覆蓋後無法復原 |

**建議**：`overwrite` 分支在還原前**先自動 dump 一份現狀**作為保險。

---

## 10. 前端調整

`cmd/dashboard/web/index.html`：

- **Project 表單**：新增「傳輸模式」選項（`direct` / `upload`）。
- **Records 列表**：
  - 顯示欄位加傳輸模式。
  - 每筆加「還原」按鈕。
  - 還原時彈出對話框：選策略（新位置 / 覆蓋），覆蓋需輸入確認字串。

---

## 11. 實作工作項目

依既有 phase 慣例，本次可命名為 **Phase 7**。

| # | 項目 | 主要檔案 |
|---|---|---|
| 1 | Migration：`transfer_mode` 欄位 | `migrations/021_projects_transfer_mode.sql` |
| 2 | Model / Store：`Project.TransferMode` + CRUD | `internal/store/models.go`、`internal/store/projects.go` |
| 3 | Dashboard 上傳 API | `internal/api/agent.go` |
| 4 | Dashboard 下載 API | `internal/api/agent.go` |
| 5 | Dashboard 還原觸發 API | 新增 `internal/api/restore.go` |
| 6 | Agent client 串流方法 | `internal/client/client.go` |
| 7 | Runner 分流（`Uploader` 介面） | `internal/backup/runner.go` |
| 8 | Agent restore handler | `cmd/agent/main.go`、視需要 `internal/backup/restore.go` |
| 9 | 前端：傳輸模式選項 + 還原按鈕 | `cmd/dashboard/web/index.html` |

---

## 12. 風險與注意事項

1. **DB 覆蓋還原無法復原**：`overwrite` 分支一定要二次確認，並建議還原前自動
   dump 一份現狀作為保險。
2. **dashboard 成為 upload 模式的流量瓶頸 / 單點**：大檔上傳期間 dashboard 的
   記憶體、連線數要盯。`WriteTimeout` 目前是 10 分鐘（`cmd/agent/main.go:224`），
   但超大檔仍可能不夠，dashboard 端上傳 handler 也要設對應 timeout。
3. **磁碟空間**：
   - agent 暫存目錄（`/var/tmp/backup-agent/staging/`）需容納單份備份，
     上傳完即刪。
   - 大檔上傳時要用純 `io.Copy` 串流落地 NAS，避免 dashboard 把整檔讀進記憶體。
4. **完整性校驗**：上傳與下載都要 sha256 比對，避免中轉損壞。
5. **路徑一致性**：upload 模式下路徑由 dashboard 端 resolve，direct 模式由 agent
   端 resolve，兩者要對齊，避免同一 project 換模式後路徑不一致。

---

## 13. 建議實作順序

先打通「upload 備份」主幹，再做還原：

1. Migration + Model / Store（`transfer_mode`）。
2. Dashboard 上傳 API（含 sha256 校驗、串流落地 NAS）。
3. Agent client `UploadBackup` + Runner 分流。
4. 端到端驗證：upload 模式備份可寫入 NAS，紀錄正確。
5. Dashboard 下載 API + Agent `DownloadBackup`。
6. Agent restore handler（先 `files` 再 `database`；先 `new` 再 `overwrite`）。
7. Dashboard 還原觸發 API。
8. 前端：傳輸模式選項 + 還原按鈕與確認對話框。

### 完成判定

1. project 可設定 `transfer_mode = upload`。
2. upload 模式下 agent 產檔 → 上傳 → dashboard 落地 NAS，sha256 校驗通過。
3. direct 模式行為不變（回歸測試）。
4. 可從 UI 觸發還原，`files` 與 `database` 都能還原。
5. DB 還原可選「新位置」或「覆蓋」，覆蓋需二次確認。
6. 失敗時 dashboard 看得到 `error_msg`。
```
