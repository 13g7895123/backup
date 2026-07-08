# Repo 路徑遷移

當舊專案與新專案的程式內容相同，只是 clone 到不同路徑時，建議不要直接搬整個 `pgdata` volume。
比較穩的做法是：

1. 舊 repo 匯出 migration bundle
2. 新 repo 匯入 bundle
3. 啟動新 repo 的 dashboard / agent

這樣會一起搬：

- PostgreSQL schema + data
- `.env`
- `secrets/pg_password.txt`
- `/etc/backup-agent/env`（若匯出機器上存在，且匯入時指定還原）

## 1. 舊 repo 匯出

```bash
bash scripts/export-instance-bundle.sh
```

預設輸出：

```text
migration_bundles/backup-manager-migration_<timestamp>_<commit>.tar.gz
```

## 2. 新 repo 匯入

先把 bundle 複製到新 repo 所在機器，再執行：

```bash
FORCE=1 bash scripts/import-instance-bundle.sh /path/to/backup-manager-migration_*.tar.gz
```

如果連 agent 的 `/etc/backup-agent/env` 也要一起還原：

```bash
FORCE=1 RESTORE_AGENT_ENV=1 bash scripts/import-instance-bundle.sh /path/to/bundle.tar.gz
```

如果新 repo 內已經有 `.env` 或 `secrets/pg_password.txt`，但你要用 bundle 內版本強制覆蓋：

```bash
FORCE=1 FORCE_CONFIG=1 bash scripts/import-instance-bundle.sh /path/to/bundle.tar.gz
```

## 3. 匯入腳本會做什麼

`scripts/import-instance-bundle.sh` 會依序：

1. 解開 bundle
2. 還原 `.env` / `secrets/pg_password.txt`（可選擇是否覆蓋）
3. 先備份新 repo 目前的 DB 到 safety backup
4. 停止 `dashboard`
5. 啟動 `postgres`
6. 將 bundle 內的 SQL dump restore 進去
7. 重建並啟動 `dashboard`

## 4. 驗證

```bash
docker compose ps
curl -fsS http://127.0.0.1:${DASHBOARD_PORT:-8080}/healthz
```

如果有 agent：

```bash
systemctl status backup-agent --no-pager
journalctl -u backup-agent -n 50 --no-pager
```

## 5. 風險與限制

- 這套流程適合「同一套服務，只換 repo 路徑」。
- 若新 repo 的 schema 已與舊 repo 不相容，restore 後仍可能需要額外 migration。
- 這套流程不搬 Docker volume 本體，而是搬「可重建的資料與設定」，比較安全。
- 匯入前腳本會先做一份當前 DB safety backup，但仍建議先在測試機驗證一次。
