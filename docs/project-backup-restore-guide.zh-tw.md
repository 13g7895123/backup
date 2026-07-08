# 專案備份與還原指南

> 本文件是寫給「使用 Backup Manager 管理某個應用專案」的人，不是寫給開發 Backup Manager 本身的人。重點是：怎麼建立專案、怎麼備份、怎麼還原、怎麼搬移到新位置。

---

## 目錄

1. [這份文件在講什麼](#這份文件在講什麼)
2. [一個專案在系統裡包含什麼](#一個專案在系統裡包含什麼)
3. [常見使用情境](#常見使用情境)
4. [建立專案前要準備什麼](#建立專案前要準備什麼)
5. [新增專案的建議填法](#新增專案的建議填法)
6. [備份目標怎麼決定](#備份目標怎麼決定)
7. [手動備份怎麼做](#手動備份怎麼做)
8. [排程備份怎麼做](#排程備份怎麼做)
9. [怎麼確認備份真的成功](#怎麼確認備份真的成功)
10. [還原前先判斷要用哪種策略](#還原前先判斷要用哪種策略)
11. [檔案還原](#檔案還原)
12. [資料庫還原](#資料庫還原)
13. [整個專案搬到新位置](#整個專案搬到新位置)
14. [建議的實戰流程](#建議的實戰流程)
15. [常見錯誤](#常見錯誤)

---

## 這份文件在講什麼

這份文件討論的是「你的應用專案」。

例如：

- `/var/www/myapp`
- `/srv/projects/rootadviser-api`
- 一個 Docker Compose 專案
- 一個有上傳檔與 PostgreSQL 的 Web 專案

Backup Manager 會替這個專案管理：

- 檔案備份
- 資料庫備份
- 系統層備份（可選）
- 排程
- 還原

---

## 一個專案在系統裡包含什麼

一個 project 通常會有三塊資料：

### 1. 專案檔案

例如：

- 程式碼目錄
- `.env`
- `storage`
- `uploads`
- `config`

### 2. 專案資料庫

例如：

- PostgreSQL
- MySQL
- Docker container 內的資料庫

### 3. 備份設定

例如：

- 備份要寫去哪個 NAS
- 是由 dashboard 跑還是 agent 跑
- agent 是直接寫 NAS 還是先上傳到 dashboard

---

## 常見使用情境

### 情境 A：同機備份

- dashboard 跟要備份的專案在同一台或同一個可直接存取的環境
- 適合 `executor_type=local`

### 情境 B：遠端 VM 備份

- 專案在另一台 VM
- 要由 agent 在該 VM 上執行
- 適合 `executor_type=agent`

### 情境 C：agent VM 看不到 NAS

- 專案在遠端 VM
- agent 可以碰到專案，但碰不到 NAS
- 這時用：
  - `executor_type=agent`
  - `transfer_mode=upload`

---

## 建立專案前要準備什麼

至少先確認以下資訊：

### 檔案備份

- 專案根路徑在哪裡
- 真的要備份哪些子目錄
- 哪些目錄不要備份

常見會備份：

- `storage`
- `uploads`
- `public/uploads`
- `config`

通常不建議直接備份整個 `.git`

### 資料庫備份

要先知道你是下面哪一種：

1. DB 在 Docker container 內
2. DB 是獨立主機或獨立 service

至少要知道：

- DB type
- DB name
- DB user
- DB password 或 password env
- container name 或 host/port

### NAS 目標

要知道：

- 備份檔最後要放哪裡
- agent 主機能不能直接寫 NAS

---

## 新增專案的建議填法

### 基本欄位

- `name`
  - 建議用穩定、可辨識名稱
  - 例如：`rootadviser-api-prod`
- `description`
  - 可寫環境、主機、用途
- `project_path`
  - 專案根目錄

### 備份目錄

`backup_dirs` 建議只放真正需要還原的資料。

例如：

```text
storage,uploads,.env
```

如果是前後端 repo，通常不要把整個 source code 全備份當成唯一來源。程式碼應以 Git repo 為主，Backup Manager 應主要保護：

- 使用者上傳檔
- 執行期設定
- 產出檔
- DB

### 執行方式

- `local`
  - dashboard 自己跑
- `agent`
  - 指定某台 agent 跑

### 傳輸模式

- `direct`
  - 執行端直接寫 NAS
- `upload`
  - agent 先上傳到 dashboard 再寫 NAS

判斷方式很簡單：

- agent 看得到 NAS：`direct`
- agent 看不到 NAS：`upload`

### 資料庫設定

#### 如果 DB 在 Docker container

- `docker_db_container`
- `db_type`
- `db_name`
- `db_user`
- `db_password_env` 或 `db_password`

#### 如果 DB 是獨立主機

- `db_host`
- `db_port`
- `db_type`
- `db_name`
- `db_user`
- `db_password_env` 或 `db_password`

---

## 備份目標怎麼決定

系統裡的 `backup_targets` 代表「實際要備份什麼」。

### files target

適合：

- `storage`
- `uploads`
- `config`

每個 target 應該盡量單一職責，不要把所有目錄都塞成一個大 target。這樣還原時比較精準。

### database target

每個專案通常至少要有一個 DB target。

### system target

只有當你真的想保留主機層狀態時才加，不要把它當成應用資料還原的主流程。

---

## 手動備份怎麼做

### UI

在專案頁或備份頁觸發「立即備份」。

### API

```http
POST /api/backups/trigger
Content-Type: application/json

{
  "project_id": 1,
  "target_type": "all"
}
```

你也可以只備份單一類型：

- `files`
- `database`
- `system`

---

## 排程備份怎麼做

建議至少拆成兩種節奏：

### 檔案

- 高頻率
- 例如每 1 小時或每 6 小時

### 資料庫

- 視資料變動量
- 例如每天凌晨、或每 4 小時

如果資料很重要，不要只做每天一次。

---

## 怎麼確認備份真的成功

不要只看「按下去沒報錯」。

至少檢查：

### 1. `backup_records.status=success`

### 2. 有 `path`

表示系統知道備份檔落在哪裡。

### 3. 有 `checksum`

表示有做完整性紀錄。

### 4. 檔案大小合理

如果檔案突然小很多，通常有問題。

### 5. 定期做 restore drill

沒有還原驗證的備份，不算真的可用。

---

## 還原前先判斷要用哪種策略

系統支援兩種：

### `new`

還原到新位置或新資料庫。

優點：

- 安全
- 不會破壞現場
- 適合驗證

缺點：

- 需要手動切換

### `overwrite`

直接覆蓋原位置或原 DB。

優點：

- 快
- 適合緊急直接回復

缺點：

- 風險高
- 一旦覆蓋，回頭成本高

建議：

- 平常演練一律用 `new`
- 真正事故才考慮 `overwrite`

---

## 檔案還原

### 適合什麼情況

- 使用者上傳檔遺失
- storage 內容壞掉
- 想把舊版本檔案解到新目錄比對

### `new` 範例

把某筆 files 備份解到新目錄：

```json
{
  "record_id": 123,
  "strategy": "new",
  "target": "/tmp/myapp-restore-check"
}
```

### `overwrite` 範例

```json
{
  "record_id": 123,
  "strategy": "overwrite",
  "target": "",
  "confirm": "RESTORE"
}
```

說明：

- `target` 留空時，系統會嘗試還原回原始 source 路徑
- overwrite 前系統會先做 snapshot，但你不能把它當成萬無一失

### 檔案還原建議

1. 先用 `new`
2. 比對內容
3. 確認檔案權限、目錄結構、大小
4. 最後再人工切換

---

## 資料庫還原

### 適合什麼情況

- 資料被誤刪
- migration 出錯
- 要把資料拉到新 DB 驗證

### `new` 範例

把資料還原到新 DB 名稱：

```json
{
  "record_id": 456,
  "strategy": "new",
  "target": "myapp_restore_check"
}
```

### `overwrite` 範例

```json
{
  "record_id": 456,
  "strategy": "overwrite",
  "target": "",
  "confirm": "RESTORE"
}
```

### DB 還原建議

1. 優先還原到新 DB 名稱
2. 用應用程式或 SQL 驗證內容
3. 再決定要不要切正式流量

不要把第一次 restore 驗證就做成直接覆蓋正式 DB。

---

## 整個專案搬到新位置

這是你最常見的實戰場景之一。

例如：

- 舊專案在 `/var/www/app-old`
- 新專案要改到 `/srv/projects/app-new`

這時建議流程不是直接 overwrite，而是：

### 做法 A：先還原到新位置

1. 用 Git clone 新 repo
2. files backup 用 `new` 還原到新路徑
3. database backup 用 `new` 還原到新 DB 名稱
4. 調整新專案 `.env`
5. 啟動新專案驗證
6. 驗證完成後切流量

這是最穩的做法。

### 做法 B：只搬資料，不搬程式碼

如果 repo 已經從 Git clone 完成，Backup Manager 主要還原：

- `.env`
- `storage`
- `uploads`
- database

這通常比把整個 source tree 都當成備份來源更合理。

---

## 建議的實戰流程

### 新專案第一次納管

1. 建立 project
2. 設定 files target
3. 設定 database target
4. 跑一次 smoke backup
5. 跑一次正式手動 backup
6. 做一次 `new` restore drill

### 事故回復

1. 找到最後一筆成功 record
2. 判斷是檔案問題、DB 問題、還是兩者都有
3. 先 `new` restore 驗證
4. 若時間壓力很高，才做 `overwrite`

### 搬機或改路徑

1. 新 repo / 新環境先建好
2. files restore 到新目錄
3. database restore 到新 DB
4. 新環境驗證
5. 切流量

---

## 常見錯誤

### 1. 把 Git repo 當成唯一備份

Git 只能保護程式碼，不能保護：

- DB
- uploads
- storage
- 執行期 `.env`

### 2. 只做備份，不做還原演練

這是最常見問題。真正出事時才發現：

- DB 帳密不對
- target 路徑不對
- agent 沒權限
- NAS 路徑其實錯了

### 3. direct / upload 選錯

如果 agent VM 根本碰不到 NAS，卻硬設 `direct`，備份就一定失敗。

### 4. files target 設太大

把整個專案根目錄全部打包，通常會讓：

- 備份太慢
- 還原太笨重
- 難以針對單一資料類型回復

### 5. overwrite 用得太隨便

這應該是最後手段，不是日常操作。

---

## 最後建議

如果你是要保護「應用專案」而不是「程式碼倉庫」，建議最少做到：

1. `files`：備份 `storage` / `uploads` / 必要 `.env`
2. `database`：固定排程 dump
3. 每個重要專案至少做過一次 `new` restore drill
4. 搬遷時優先用「新位置 / 新 DB」還原，不要直接覆蓋

如果你後續要，我可以再補第二份文件，專門寫：

- Laravel 專案怎麼設
- Docker Compose 專案怎麼設
- Node / Go API 專案怎麼設
