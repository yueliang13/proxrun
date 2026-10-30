#!/usr/bin/env bash
# simple-proxy · 更新订阅配置
#   用法: update-subscription.sh <订阅URL> [目标文件]
set -euo pipefail

SUB_URL="${1:-${SIMPLE_PROXY_SUBSCRIPTION:-}}"
DATA_DIR="${SIMPLE_PROXY_DATA_DIR:-$HOME/.local/share/simple-proxy}"
TARGET="${2:-$DATA_DIR/config.yaml}"

if [[ -z "$SUB_URL" ]]; then
  echo "用法: update-subscription.sh <订阅URL>" >&2
  echo "  或设置环境变量 SIMPLE_PROXY_SUBSCRIPTION" >&2
  exit 1
fi

mkdir -p "$DATA_DIR"
echo "→ 拉取订阅: ${SUB_URL%%\?*}" >&2   # 不打印 token 部分
if curl -fsSL --max-time 60 -o "$TARGET.tmp" "$SUB_URL"; then
  mv "$TARGET.tmp" "$TARGET"
  chmod 600 "$TARGET"       # 含节点凭据
  echo "✅ 订阅已更新: $TARGET (mode 600)" >&2
else
  rm -f "$TARGET.tmp"
  echo "❌ 订阅拉取失败" >&2
  exit 1
fi
