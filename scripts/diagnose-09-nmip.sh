#!/usr/bin/env bash
# 診斷 09_nmip 備份寫成功但找不到檔案的問題
# 部署到 agent host 執行

set -euo pipefail

PROJECT_NAME="09_nmip"
# 根據 runner.go 路徑規則，調整下方兩個變數來對應 09_nmip 在 DB 裡設定的值
NAS_BASE="${NAS_BASE:-}"       # 對應 projects.nas_base
NAS_SUBPATH="${NAS_SUBPATH:-}" # 對應 projects.nas_subpath（可為空）

echo "======================================"
echo "  09_nmip 備份路徑診斷"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "======================================"
echo ""

# ── 1. 確認 nas_base / nas_subpath ──────────────────────────────────────────
if [[ -z "$NAS_BASE" ]]; then
  echo "[錯誤] 未設定 NAS_BASE 環境變數"
  echo "  使用方式: NAS_BASE=/volume1/debian/backup-new bash $0"
  echo ""
  echo "  請至 dashboard → 專案設定 查詢 09_nmip 的 nas_base 欄位，再帶入執行"
  echo ""
  # 嘗試自動從資料庫取得（若本機有 psql）
  if command -v psql &>/dev/null; then
    DB_URL="${DATABASE_URL:-${POSTGRES_DSN:-}}"
    if [[ -n "$DB_URL" ]]; then
      echo "[自動查詢] 從資料庫取得 09_nmip 設定..."
      psql "$DB_URL" -c "SELECT id, name, nas_base, nas_subpath, executor_type, executor_agent_id FROM projects WHERE name LIKE '%nmip%' OR name LIKE '%09%';" 2>/dev/null || true
    else
      echo "[略過] DATABASE_URL / POSTGRES_DSN 未設定，無法自動查詢"
    fi
  fi
  echo ""
fi

NAS_ROOT="${NAS_BASE}"
if [[ -n "$NAS_SUBPATH" ]]; then
  NAS_ROOT="${NAS_BASE}/${NAS_SUBPATH}"
fi

echo "[設定]"
echo "  NAS_BASE    = ${NAS_BASE:-（未設定）}"
echo "  NAS_SUBPATH = ${NAS_SUBPATH:-（空）}"
echo "  NAS_ROOT    = ${NAS_ROOT:-（未設定）}"
echo ""

# ── 2. 路徑結構說明（目前 runner.go 的規則）───────────────────────────────
echo "[目前備份路徑規則]"
echo "  files    → \$NAS_ROOT/files/\$date/\$name_files_\$timestamp.tar.gz"
echo "  database → \$NAS_ROOT/database/\$date/\$name_database_\$timestamp.sql.gz"
echo ""
echo "  範例（假設今天 2026-06-02）："
echo "  \$NAS_ROOT/files/2026-06-02/${PROJECT_NAME}_files_20260602_020000.tar.gz"
echo "  \$NAS_ROOT/database/2026-06-02/${PROJECT_NAME}_database_20260602_020000.sql.gz"
echo ""

if [[ -z "$NAS_BASE" ]]; then
  echo "[警告] NAS_BASE 未設定，跳過路徑檢查，請帶入環境變數後重新執行"
  echo ""
  exit 1
fi

# ── 3. 確認 NAS 掛載狀態 ────────────────────────────────────────────────────
echo "[NAS 掛載狀態]"
if mountpoint -q "$NAS_BASE" 2>/dev/null; then
  echo "  ✓ $NAS_BASE 是掛載點"
else
  echo "  ✗ $NAS_BASE 不是掛載點（或 mountpoint 指令不可用）"
fi

echo "  mount 相關條目："
mount | grep -i "nas\|nfs\|cifs\|samba\|volume1\|$(echo "$NAS_BASE" | cut -d/ -f2)" 2>/dev/null | sed 's/^/    /' || echo "    （無相符掛載）"
echo ""

# ── 4. 確認目錄是否存在、可寫 ───────────────────────────────────────────────
echo "[目錄存在性與權限]"
for TYPE in files database; do
  DIR="${NAS_ROOT}/${TYPE}"
  if [[ -d "$DIR" ]]; then
    echo "  ✓ 目錄存在: $DIR"
    if [[ -w "$DIR" ]]; then
      echo "    ✓ 可寫入"
    else
      echo "    ✗ 無寫入權限 (ls -la):"
      ls -la "$DIR" 2>/dev/null | sed 's/^/      /'
    fi

    echo "    最近 5 個日期子目錄："
    ls -1 "$DIR" 2>/dev/null | sort -r | head -5 | sed 's/^/      /'

    echo "    最近 3 個備份檔案（遞迴）："
    find "$DIR" -type f \( -name "*.tar.gz" -o -name "*.sql.gz" \) \
      -printf '%T@ %p\n' 2>/dev/null | sort -rn | head -3 | awk '{print "      "$2}' || \
    find "$DIR" -type f \( -name "*.tar.gz" -o -name "*.sql.gz" \) 2>/dev/null | \
      xargs ls -t 2>/dev/null | head -3 | sed 's/^/      /'
  else
    echo "  ✗ 目錄不存在: $DIR"
    # 往上找存在的父目錄
    PARENT="$NAS_ROOT"
    if [[ -d "$PARENT" ]]; then
      echo "    父目錄 $PARENT 存在，內容："
      ls -la "$PARENT" 2>/dev/null | sed 's/^/      /'
    else
      echo "    父目錄也不存在: $PARENT"
      if [[ -d "$NAS_BASE" ]]; then
        echo "    NAS_BASE $NAS_BASE 存在，內容："
        ls -la "$NAS_BASE" 2>/dev/null | head -20 | sed 's/^/      /'
      else
        echo "    ✗ NAS_BASE $NAS_BASE 也不存在！"
      fi
    fi
  fi
  echo ""
done

# ── 5. 磁碟空間 ─────────────────────────────────────────────────────────────
echo "[磁碟空間]"
df -h "$NAS_BASE" 2>/dev/null | sed 's/^/  /' || echo "  （無法取得）"
echo ""

# ── 6. 嘗試寫入測試檔案 ────────────────────────────────────────────────────
echo "[寫入測試]"
TEST_FILE="${NAS_ROOT}/database/$(date +%Y-%m-%d)/.write_test_$$"
if mkdir -p "$(dirname "$TEST_FILE")" && touch "$TEST_FILE" 2>/dev/null; then
  echo "  ✓ 成功建立測試檔案: $TEST_FILE"
  rm -f "$TEST_FILE"
  echo "  ✓ 清除測試檔案"
else
  echo "  ✗ 無法在目標路徑建立檔案"
  echo "    確認使用者: $(whoami)"
  echo "    目標路徑: $(dirname "$TEST_FILE")"
fi
echo ""

# ── 7. 找近期所有 nmip 相關備份檔案 ────────────────────────────────────────
echo "[尋找近期 nmip 備份檔案（全域搜尋 $NAS_BASE）]"
find "$NAS_BASE" -type f \( -name "*nmip*" -o -name "*09_*" \) 2>/dev/null | \
  sort | head -20 | sed 's/^/  /' || echo "  （找不到）"
echo ""

# ── 8. agent log 參考 ────────────────────────────────────────────────────────
echo "[Agent 執行 Log 位置]"
LOG_DIR="${BACKUP_RUN_LOG_DIR:-/var/log/backup-agent/runs}"
echo "  預設 log 目錄: $LOG_DIR"
if [[ -d "$LOG_DIR" ]]; then
  echo "  最近 5 個 log："
  ls -t "$LOG_DIR"/*.log 2>/dev/null | head -5 | sed 's/^/    /'
  echo ""
  echo "  最近 nmip 相關 log："
  ls -t "$LOG_DIR"/*nmip*.log "$LOG_DIR"/*09*.log 2>/dev/null | head -3 | while read f; do
    echo "    --- $f ---"
    tail -20 "$f" 2>/dev/null | sed 's/^/      /'
  done
else
  echo "  目錄不存在: $LOG_DIR"
  echo "  （備份 log 可能在 /tmp/backup-agent/runs/）"
  find /tmp/backup-agent 2>/dev/null -name "*nmip*" -o -name "*09*" | head -5 | sed 's/^/  /'
fi
echo ""

echo "======================================"
echo "  診斷完成"
echo "======================================"
