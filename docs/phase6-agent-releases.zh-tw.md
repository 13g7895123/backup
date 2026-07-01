# Phase 6 Agent Release Artifact

這份文件對應 `phase6` 的第一版實作，目標是：

1. build `linux-amd64` agent binary
2. 產出 `tar.gz`
3. 產出 `checksums.txt`
4. 提供下載
5. 提供固定格式安裝指令

## 1. API

可用 API：

- `GET /api/admin/agent-releases/capability`
- `POST /api/admin/agent-releases/build`
- `GET /api/admin/agent-releases`
- `GET /api/admin/agent-releases/{version}`
- `GET /api/admin/agent-releases/{version}/download/{file}`

## 2. Build Request

```http
POST /api/admin/agent-releases/build
Content-Type: application/json

{
  "version": "1.2.3"
}
```

## 3. 產出目錄

預設 release 目錄：

```text
artifacts/backup-agent/<version>/
```

例如：

```text
artifacts/backup-agent/1.2.3/
  backup-agent-linux-amd64
  backup-agent_1.2.3_linux_amd64.tar.gz
  backup-agent_1.2.3_checksums.txt
  install-agent.sh
  diagnose-agent.sh
  backup-agent.service
  manifest.json
```

## 4. Build 能力檢查

這版改成先透過 capability API 明確宣告目前環境是否可建置：

```http
GET /api/admin/agent-releases/capability
```

回傳範例：

```json
{
  "available": false,
  "reason": "找不到 build workspace：/workspace/backup",
  "workdir": "/workspace/backup",
  "script": "scripts/build-agent.sh"
}
```

管理頁會先讀這個結果：

- `available=true` 才允許按「建立新版本」
- `available=false` 時會直接顯示原因，不再讓使用者按下去才看到 `scripts/build-agent.sh: No such file or directory`

## 5. 正式站建議做法

正式站不要依賴 dashboard container 目前的工作目錄。

比較乾淨的做法是：

1. 在 host 準備一份專案 source tree，例如 `/opt/backup-manager`
2. 把這個目錄掛載進 dashboard container，例如 `/workspace/backup`
3. 用環境變數明確指定 build workspace 與 script

例如：

```yaml
services:
  dashboard:
    volumes:
      - /opt/backup-manager:/workspace/backup:ro
      - ./artifacts:/app/artifacts
    environment:
      AGENT_RELEASES_DIR: /app/artifacts/backup-agent
      AGENT_BUILD_WORKDIR: /workspace/backup
      AGENT_BUILD_SCRIPT: scripts/build-agent.sh
```

這樣 dashboard 只負責：

- 檢查 build workspace 是否存在
- 呼叫指定的 build script
- 把 artifact 寫到 `AGENT_RELEASES_DIR`

而不是假設 container image 內一定有整份原始碼。

## 6. 相關環境變數

可用環境變數：

```env
AGENT_RELEASES_DIR=artifacts/backup-agent
AGENT_BUILD_WORKDIR=.
AGENT_BUILD_SCRIPT=scripts/build-agent.sh
AGENT_RELEASE_LOG_DIR=/var/log/backup-agent/releases
```

說明：

- `AGENT_RELEASES_DIR`
  - artifact 輸出位置
- `AGENT_BUILD_WORKDIR`
  - 專案原始碼所在目錄
- `AGENT_BUILD_SCRIPT`
  - 相對於 `AGENT_BUILD_WORKDIR` 的 build script，或絕對路徑
- `AGENT_RELEASE_LOG_DIR`
  - agent release build 的實體 log 目錄
  - 預設為 `/var/log/backup-agent/releases`
  - 若無法寫入，會退回 `/tmp/backup-agent/releases`

## 7. Build Log

每次按「建立新版本」都會產生一份獨立的實體 log 檔。

命名格式類似：

```text
/var/log/backup-agent/releases/1.2.3-20260531-153045.log
```

log 內容會包含：

- build request 基本資訊
- capability 檢查結果
- build script 執行路徑
- `scripts/build-agent.sh` 的 stdout / stderr
- artifact 複製、打包、checksum、manifest 寫入步驟

如果 build 失敗：

- API error response 會回傳 `log_ref`
- 管理頁會直接跳出 `實體 Log 路徑`
- release detail 也會顯示 `log_ref`

## 8. 安裝指令

release detail API 會回傳固定格式安裝指令，形式類似：

```bash
curl -fsSLO http://host/api/admin/agent-releases/1.2.3/download/backup-agent_1.2.3_linux_amd64.tar.gz \
  && tar -xzf backup-agent_1.2.3_linux_amd64.tar.gz \
  && cd backup-agent_1.2.3_linux_amd64 \
  && sudo AGENT_BINARY_SRC=./backup-agent-linux-amd64 ./install-agent.sh
```

## 9. install-agent.sh 相容性

`install-agent.sh` 現在支援兩種模式：

1. repo 內直接執行
2. 從 release artifact 解壓後直接執行

它會優先使用：

- `AGENT_BINARY_SRC`
- 同目錄下的 `backup-agent-linux-amd64`
- 同目錄下的 `backup-agent`
- repo 根目錄下的 `backup-agent`

## 10. 第一版限制

這版刻意維持最小範圍，只做：

- `linux-amd64`
- 單機 build
- 靜態 artifact download

尚未做：

- `arm64`
- UI 內直接推送到 VM
- 差分升級
- release note 管理
