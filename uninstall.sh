#!/usr/bin/env bash
# simple-proxy · 卸载
#   bash uninstall.sh [--purge]
#     --purge  连同数据与配置一起删除
set -uo pipefail

SP_BIN_DIR="${SP_BIN_DIR:-$HOME/.local/bin}"
SP_DATA_DIR="${SP_DATA_DIR:-$HOME/.local/share/simple-proxy}"
SP_CONFIG_DIR="${SP_CONFIG_DIR:-$HOME/.config/simple-proxy}"
PURGE=0
[[ "${1:-}" == "--purge" ]] && PURGE=1

# 停止进程
if [[ -f "$SP_DATA_DIR/mihomo.pid" ]]; then
  kill "$(cat "$SP_DATA_DIR/mihomo.pid")" 2>/dev/null && echo "✅ 已停止 mihomo"
  rm -f "$SP_DATA_DIR/mihomo.pid"
fi

rm -f "$SP_BIN_DIR/simple-proxy" "$SP_BIN_DIR/mihomo"
echo "✅ 已移除命令与内核"

# 清理 shell 集成
for rc in "${ZDOTDIR:-$HOME}/.zshrc" "$HOME/.bashrc"; do
  [[ -f "$rc" ]] || continue
  if grep -q 'simple-proxy/shell.sh' "$rc"; then
    sed -i '/# ─── simple-proxy ───/,+3d' "$rc"
    echo "✅ 已清理 shell 集成: $rc"
  fi
done

if (( PURGE )); then
  rm -rf "$SP_DATA_DIR" "$SP_CONFIG_DIR"
  echo "✅ 已删除数据与配置"
else
  echo "• 数据与配置保留: $SP_DATA_DIR / $SP_CONFIG_DIR（加 --purge 可一并删除）"
fi
