# Agent / Dashboard 測試腳本

這些腳本是給 `dashboard VM` 上的獨立 agent 用來做分層診斷。

建議執行順序：

```bash
cd /path/to/backup/scripts/tests
bash run-all.sh
```

如果你想分開跑：

```bash
bash 01-env-and-service.sh
bash 02-agent-direct.sh
bash 03-dashboard-proxy.sh
bash 04-agent-vs-dashboard-headers.sh
```

每支用途：

- `01-env-and-service.sh`：看 `.env`、`/etc/backup-agent/env`、systemd、journal
- `02-agent-direct.sh`：直接打本機 agent，不經 dashboard
- `03-dashboard-proxy.sh`：看 dashboard 自己以及 dashboard 代理 agent 的結果
- `04-agent-vs-dashboard-headers.sh`：確認 agent 是否因 `X-Agent-Code` / `X-Agent-Token` 而拒絕

把完整輸出貼回來，我會依照哪一支先失敗來除錯。
