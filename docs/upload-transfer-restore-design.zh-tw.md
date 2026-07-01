# 上傳中轉備份與還原 — 設計說明

> 在現有「agent 直寫 NAS」之外，新增「agent 上傳到 dashboard、由 dashboard 轉存 NAS」的傳輸模式，並補上還原能力。

---

## 一句話總結

讓 **agent 有兩種備份方式**：

- **direct**：agent 自己把備份檔寫進本機掛載的 NAS（現況）。
- **upload**：agent 把備份檔串流上傳到 dashboard，由 **有掛 NAS 的 dashboard** 落地到 NAS。

還原則是反向：dashboard 從 NAS 取檔 → 串流回 agent → agent 解壓 / import。

**解決的問題**：當 agent 所在 VM 碰不到 NAS（沒掛載 / 跨網段 / 防火牆隔離）時，
改走 dashboard 中轉就能完成備份與還原。

---

## 為什麼可行（現有基礎）

| 已有基礎 | 位置 |
|---|---|
| dashboard 容器**已掛載 `/mnt/nas`**，落地基礎設施現成 | `docker-compose.yml:37` |
| agent 身分驗證（`X-Agent-Code` + `X-Agent-Token`）可直接複用 | `internal/api/agent.go:23` |
| project 歸屬授權檢查 `AgentOwnsProject` 可直接複用 | `internal/api/agent.go` |
| 產檔 + sha256 + `backup_records` 邏輯不用動 | `internal/backup/{files,database,runner}.go` |
| `Runner` 依賴倒置設計可沿用，上傳能力以介面注入 | `internal/backup/runner.go` |

**唯一新增的基礎能力**：agent client 目前只走 JSON，要補串流上傳/下載；還原則是
系統從零新增的一塊。

---

## 決策一覽

| 項目 | 決策 |
|---|---|
| 兩種模式關係 | **並存**，逐 project 用 `transfer_mode` 切換（`direct` / `upload`） |
| 傳輸方式 | HTTP 串流（raw body / multipart）+ sha256 校驗 |
| agent 暫存 | 寫本機暫存 `/var/tmp/backup-agent/staging/`，**上傳完即刪** |
| 還原範圍 | 完整還原（下載 + 解壓 / DB import） |
| DB 還原策略 | **使用者選**：新位置（安全）/ 覆蓋（危險、需二次確認） |
| 暫存 vs 純串流 | 先產到暫存檔再上傳，**穩定優先、可重試** |

---

## 運作流程

### 備份（upload 模式）

```
agent 產檔到暫存 → 建 record(running) → 串流上傳 POST /api/agent/records/{id}/upload
                                              │ (帶 X-Backup-Sha256 / X-Backup-Filename)
                                              ▼
                              dashboard io.Copy 寫入 /mnt/nas/... + 重算 sha256
                                              │
                    sha256 不符 → 刪檔 + 400   │   sha256 相符 → 更新 record.path
                                              ▼
                              agent 刪暫存檔 → 更新 record(success/failed)
```

### 備份（direct 模式）

維持現況：agent 直接把備份檔寫進本機 NAS 路徑，不經過 dashboard。

### 還原

```
UI 選一筆 record + 策略 → POST /api/restore → dashboard 查 project 綁定 agent → 轉發 agent /restore
                                                                                      │
                            agent GET /api/agent/records/{id}/download (dashboard 從 NAS 串流回檔)
                                                                                      │
                            依 type 還原：files 解 tar.gz / database gunzip + import
                                                                                      │
                            依策略：new(新名稱/新路徑) 或 overwrite(覆蓋，需二次確認)
```

---

## 需要改動的部分

### 資料模型

`migrations/021_projects_transfer_mode.sql`（編號接續現有 020）：

```sql
ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS transfer_mode VARCHAR(20) NOT NULL DEFAULT 'direct';
    -- 'direct' = agent 直寫本機 NAS
    -- 'upload' = agent 上傳到 dashboard，由 dashboard 落地 NAS
```

對應調整 `internal/store/models.go`（`Project.TransferMode`）與
`internal/store/projects.go`（CRUD 讀寫）。

### API（皆走既有 `agentMiddleware`）

| 方法 | 路徑 | 說明 |
|---|---|---|
| POST | `/api/agent/records/{id}/upload` | agent 串流上傳備份檔，dashboard 校驗 sha256 後落地 NAS |
| GET | `/api/agent/records/{id}/download` | dashboard 從 NAS 串流回檔（還原用） |
| POST | `/api/restore` | 觸發還原，dashboard 轉發到 project 綁定的 agent |

### Agent client（`internal/client/client.go`）

- `UploadBackup(ctx, recordID, filePath, checksum)`：`os.Open` + `io.Copy` 到 body，
  帶 sha256 / filename header。**需放寬 timeout**（現寫死 60s，大檔會爆）。
- `DownloadBackup(ctx, recordID, destPath)`：串流下載寫檔。

### Runner 分流（`internal/backup/runner.go`）

產檔後依 `proj.TransferMode` 分流；上傳能力以介面注入，不破壞依賴倒置：

```go
type Uploader interface {
    UploadBackup(ctx context.Context, recordID int64, filePath, checksum string) error
}

type Runner struct {
    Store    BackingStore
    Uploader Uploader   // 新增，可為 nil（dashboard 本機執行時走 direct）
    Notifier *notify.Slack
}
```

### Agent restore handler（`cmd/agent/main.go` + 視需要 `internal/backup/restore.go`）

- `POST /restore`（走既有 `auth`）：download → 依 type 還原。
- `files`：解 tar.gz。`database`：gunzip → `psql`/`mysql` 或 `docker exec ... < dump`。
- 策略分支：
  - `new` → 還原到新 database 名（如 `xxx_restored`）/ 新目錄，**不覆蓋現有資料**。
  - `overwrite` → 還原到原位置，**UI 需輸入確認字串**；建議還原前先自動 dump 一份現狀。

### 前端（`cmd/dashboard/web/index.html`）

- project 表單：新增「傳輸模式」選項（direct / upload）。
- records 列表：加傳輸模式欄位 + 「還原」按鈕（含策略選擇與覆蓋確認對話框）。

---

## 工作清單

| # | 項目 | 檔案 |
|---|---|---|
| 1 | Migration：`transfer_mode` | `migrations/021_projects_transfer_mode.sql` |
| 2 | Model / Store CRUD | `internal/store/models.go`、`internal/store/projects.go` |
| 3 | 上傳 API | `internal/api/agent.go` |
| 4 | 下載 API | `internal/api/agent.go` |
| 5 | 還原觸發 API | `internal/api/restore.go`（新增） |
| 6 | client 串流方法 | `internal/client/client.go` |
| 7 | Runner 分流（`Uploader`） | `internal/backup/runner.go` |
| 8 | agent restore handler | `cmd/agent/main.go`、`internal/backup/restore.go`（新增） |
| 9 | 前端：模式選項 + 還原按鈕 | `cmd/dashboard/web/index.html` |

---

## 建議順序

先打通 upload 主幹，再做還原：

1. Migration + Model / Store。
2. 上傳 API（sha256 校驗 + 串流落地 NAS）。
3. client `UploadBackup` + Runner 分流。
4. 端到端驗證 upload 備份；回歸驗證 direct 不變。
5. 下載 API + `DownloadBackup`。
6. restore handler（先 `files` 再 `database`；先 `new` 再 `overwrite`）。
7. 還原觸發 API。
8. 前端模式選項 + 還原對話框。

---

## 風險提醒

1. **DB 覆蓋還原無法復原** — `overwrite` 必須二次確認，建議先自動 dump 現狀保險。
2. **dashboard 是 upload 模式的單點與流量瓶頸** — 大檔上傳要盯記憶體/連線，
   落地一律用 `io.Copy` 串流，切勿整檔讀進記憶體；timeout 要對應放寬。
3. **磁碟空間** — agent 暫存需容納單份備份（上傳完即刪）。
4. **完整性** — 上傳與下載都要 sha256 比對，避免中轉損壞。
5. **路徑一致性** — upload 由 dashboard resolve、direct 由 agent resolve，
   兩者路徑規則要對齊，避免同 project 換模式後路徑不一致。

---

## 完成判定

1. project 可設 `transfer_mode = upload`。
2. upload 模式：agent 產檔 → 上傳 → dashboard 落地 NAS，sha256 通過。
3. direct 模式行為不變（回歸）。
4. UI 可觸發還原，`files` 與 `database` 都能還原。
5. DB 還原可選新位置 / 覆蓋，覆蓋需二次確認。
6. 失敗時 dashboard 看得到 `error_msg`。

---

> 完整版設計（含更詳細的資料流章節）另見 `docs/phase7-upload-transfer-restore.zh-tw.md`。
> 多 agent 架構背景見 `docs/multi-agent-vm-backup-design.zh-tw.md`。
