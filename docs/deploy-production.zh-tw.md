# 正式站部署說明

這份文件對應 [scripts/deploy.sh](/home/jarvis/project/bonus/05_rootadviser/07_server/backup/scripts/deploy.sh)。

## 1. 指令

預設正式部署：

```bash
./scripts/deploy.sh production
```

若這次也要更新 host agent：

```bash
./scripts/deploy.sh production --with-agent
```

## 2. 預設行為

`./scripts/deploy.sh production` 只會部署：

1. `postgres`
2. `dashboard`

不會更新：

- `/usr/local/bin/backup-agent`
- `systemctl restart backup-agent`

這是刻意的預設，因為正式站常見情境是：

- 只更新 dashboard / DB schema / 前端
- 不想每次都碰 host agent

## 3. --with-agent 行為

只有在你明確加上：

```bash
--with-agent
```

才會額外做：

1. `bash scripts/build-agent.sh`
2. `sudo cp backup-agent /usr/local/bin/backup-agent`
3. `sudo systemctl restart backup-agent`

前提是本機真的有：

- `backup-agent.service`
- `scripts/build-agent.sh`

否則會略過並顯示訊息。

## 4. 部署流程

無論是否更新 agent，`./scripts/deploy.sh production` 都會先做：

1. `scripts/pre-deploy-snapshot.sh`
2. 啟動 / 更新 `postgres`
3. `docker compose up -d --build dashboard`
4. 驗證 `docker compose ps`
5. 驗證 `dashboard /healthz`

也就是說，資料庫備份與快照會先發生，再進入正式部署。

## 5. 前置條件

正式站至少要有：

- `.env`
- `docker-compose.yml`
- `secrets/pg_password.txt`
- Docker / Docker Compose
- `curl`

如果要用 `--with-agent`，還要有：

- `backup-agent.service`
- `sudo` 權限
- 可成功執行 `scripts/build-agent.sh`

## 6. 部署產物

部署時會額外留下：

- `deploy_logs/`
- `deploy_snapshots/`

其中：

- `deploy_logs/` 會存每次部署輸出
- `deploy_snapshots/` 會存部署前的 DB 備份、設定與 rollback 說明

## 7. 建議用法

### 只更新 dashboard / migration

```bash
./scripts/deploy.sh production
```

### 更新 dashboard，且這次 agent 也一起升級

```bash
./scripts/deploy.sh production --with-agent
```

## 8. 驗收建議

部署後至少檢查：

```bash
docker compose ps
docker compose logs --tail=50 dashboard
curl http://127.0.0.1:${DASHBOARD_PORT}/healthz
```

如果有更新 agent，再加檢查：

```bash
systemctl status backup-agent --no-pager
journalctl -u backup-agent -n 50 --no-pager
```

## 9. 失敗時回復

若部署失敗，可先到最近一次快照目錄查看：

```text
deploy_snapshots/<timestamp>_<commit>/
```

裡面會有：

- DB 備份
- `.env`
- `secrets`
- rollback 腳本
- `ROLLBACK_README.txt`

若要回到正式站 baseline commit + 指定 DB 備份：

```bash
bash scripts/rollback-to-prod.sh /path/to/backup.sql.gz
```

## 10. 限制

這版 `scripts/deploy.sh` 有幾個刻意保留的限制：

- 不會自動 `git pull`
- 不會自動切 branch
- 不會遠端更新其他 VM 的 agent
- 不會自動 restore DB，只做部署與驗證

它的定位是：

- 部署「目前 working tree」
- 預設只碰 `postgres + dashboard`
- 在需要時才加 `--with-agent`
