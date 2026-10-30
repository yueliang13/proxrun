#!/usr/bin/env bash
# simple-proxy · 日志轮转
#   用法: clean-logs.sh [数据目录] [保留天数]
# 策略: mihomo.log → mihomo.log.YYYY-MM-DD；删除超过保留天数的（按文件名日期判断）
set -uo pipefail

DATA_DIR="${1:-$HOME/.local/share/simple-proxy}"
KEEP_DAYS="${2:-3}"
LOG="$DATA_DIR/mihomo.log"

[[ -f "$LOG" ]] || exit 0
mkdir -p "$DATA_DIR"

stamp="$(date +%F)"
if [[ ! -f "$DATA_DIR/mihomo.log.$stamp" ]]; then
  mv "$LOG" "$DATA_DIR/mihomo.log.$stamp"
fi

today_ts="$(date +%s)"
for f in "$DATA_DIR"/mihomo.log.*; do
  [[ -f "$f" ]] || continue
  ts="${f##*.}"
  [[ "$ts" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || continue
  f_ts="$(date -d "$ts" +%s 2>/dev/null || date -j -f "%Y-%m-%d" "$ts" +%s 2>/dev/null || echo "$today_ts")"
  age=$(( (today_ts - f_ts) / 86400 ))
  (( age > KEEP_DAYS )) && rm -f "$f"
done

# 兜底：超大旧日志也清掉
find "$DATA_DIR" -maxdepth 1 -name 'mihomo.log.*' -type f -size +20M -mtime +1 -delete 2>/dev/null
exit 0
