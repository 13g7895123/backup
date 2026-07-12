# 本地建置與上傳 Agent Release

這份文件適用於 dashboard 無法直接建置 agent release，或需要先在本地驗證原始碼與產物，再上傳正式站的情境。

流程分成兩個階段：

1. 在本地專案建置並打包 agent release
2. 將 release 上傳 dashboard，再由指定 Agent VM 執行升級

## 1. 前置檢查

在 repo 根目錄確認版本、工作樹與測試：

```bash
git status --short
git log -1 --oneline
go test ./...
```

release manifest 的 `commit` 來自目前 `HEAD`。建置 agent 的 Go 原始碼若尚未 commit，manifest 將無法完整代表 binary 內容，因此正式 release 應從已確認的 commit 建置。

版本名稱不可與 dashboard 或本地既有 release 重複。先查正式站：

```bash
DASHBOARD_URL=https://nas.rootadviserplaner.com
curl -fsS "$DASHBOARD_URL/api/admin/agent-releases" \
  | jq '.[] | {version, built_at, commit}'
```

## 2. 本地建置 Release

指定新版本並執行 package script：

```bash
VERSION=1.0.6-manual
bash scripts/package-agent-release.sh "$VERSION"
```

預設產物位於：

```text
artifacts/backup-agent/<version>/
```

至少應包含：

```text
backup-agent-linux-amd64
backup-agent_<version>_linux_amd64.tar.gz
backup-agent_<version>_checksums.txt
install-agent.sh
diagnose-agent.sh
backup-agent.service
manifest.json
```

確認 binary 類型、版本與來源 commit：

```bash
file "artifacts/backup-agent/$VERSION/backup-agent-linux-amd64"
jq '{version, built_at, commit, files}' \
  "artifacts/backup-agent/$VERSION/manifest.json"
git rev-parse --short HEAD
```

`manifest.json` 的 `version` 必須等於 `$VERSION`，`commit` 必須等於預期部署的 commit。binary 必須是 Linux x86-64 靜態執行檔。

## 3. 上傳正式站

使用 import API 上傳整份 release：

```bash
DASHBOARD_URL=https://nas.rootadviserplaner.com \
  bash scripts/upload-agent-release.sh "$VERSION"
```

上傳成功後，從 API 重新讀取正式站 manifest：

```bash
curl -fsS "$DASHBOARD_URL/api/admin/agent-releases/$VERSION" \
  | jq '{version, built_at, commit, log_ref, files}'
```

確認：

- `version` 是剛上傳的版本
- `commit` 是預期 commit
- `files` 包含 binary、tarball、checksum、安裝腳本與 service
- 該版本成為 `/api/admin/agent-releases` 中建置時間最新的 release

## 4. 升級指定 Agent

上傳 release 不會自動更新遠端 VM。先從 dashboard 取得指定 agent 的升級資料：

```bash
AGENT_ID=3
curl -fsS "$DASHBOARD_URL/api/agents/$AGENT_ID/installer?version=$VERSION" \
  | jq '{current_version, selected_version, version_state, version_note, upgrade_command}'
```

在該 Agent VM 上執行 API 回傳的 `upgrade_command`。也可以直接使用：

```bash
curl -fsSL \
  "$DASHBOARD_URL/api/agents/$AGENT_ID/upgrade-script?version=$VERSION" \
  -o /tmp/backup-agent-upgrade.sh
bash /tmp/backup-agent-upgrade.sh
```

升級腳本會下載 release、驗證 checksum、安裝 binary、重啟 `backup-agent`，並顯示 service status 與最近日誌。

## 5. 升級後驗收

等待下一次 heartbeat，再從 dashboard 確認版本：

```bash
curl -fsS "$DASHBOARD_URL/api/agents/$AGENT_ID" \
  | jq '{id, code, status, version, latest_release_version, version_state, last_seen_at, last_error}'
```

成功標準：

- `status` 是 `online`
- `version` 與 `latest_release_version` 都是 `$VERSION`
- `version_state` 是 `up_to_date`
- `last_seen_at` 已更新
- `last_error` 為空

若這次 release 修正 agent command，應先執行低風險測試，再重試正式操作。可透過以下 API 查 command 結果：

```bash
curl -fsS \
  "$DASHBOARD_URL/api/admin/agent-commands?agent_id=$AGENT_ID&limit=10" \
  | jq '.commands[] | {id, type, status, error_msg, created_at, finished_at}'
```

## 6. 常見問題

### dashboard 顯示 agent 已是最新版本，但程式仍是舊的

dashboard 是用 agent heartbeat 的版本字串與最新 release version 比較，不會直接比對 binary commit。每次改動 agent 原始碼都必須建立新版本，不能重用舊版本名稱。

### dashboard capability 顯示無法建置

這不影響本地建置與 upload。使用本文件的 `package-agent-release.sh` 與 `upload-agent-release.sh` 流程即可。

### 執行正式站 deploy 後 agent 沒有更新

`scripts/deploy.sh production` 預設只更新 dashboard 與 PostgreSQL。`--with-agent` 也只更新部署主機上的本機 agent，不會自動更新其他 VM；遠端 agent 必須執行該版本的 upgrade script。

### 上傳回覆 release 已存在

release version 不可覆寫。確認內容後使用下一個新版本重新建置，不要刪除或覆蓋已發佈版本。
