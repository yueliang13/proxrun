#!/usr/bin/env bash
# 原子更新订阅；临时文件在写入前即为 600，失败时保留原订阅。
set -euo pipefail
SUB_URL="${1:-${SIMPLE_PROXY_SUBSCRIPTION:-}}"
DATA_DIR="${SP_DATA_DIR:-${SIMPLE_PROXY_DATA_DIR:-$HOME/.local/share/simple-proxy}}"
TARGET="${2:-$DATA_DIR/config.yaml}"
[[ -n "$SUB_URL" ]] || { echo '用法: simple-proxy update <订阅URL>' >&2; exit 1; }
mkdir -p "$(dirname "$TARGET")"
umask 077
temporary="$(mktemp "$TARGET.XXXXXX.tmp")"
trap 'rm -f "$temporary"' EXIT
# URL 路径也可能含凭据，不输出订阅地址。
curl -fsSL --max-time 60 -o "$temporary" "$SUB_URL"
[[ -s "$temporary" ]] || { echo '❌ 订阅内容为空' >&2; exit 1; }
chmod 600 "$temporary"
mv -f "$temporary" "$TARGET"
echo '✅ 订阅已更新' >&2
