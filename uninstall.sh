#!/usr/bin/env bash
# bash uninstall.sh [--purge]
set -euo pipefail
SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SP_BIN_DIR="${SP_BIN_DIR:-$HOME/.local/bin}"
SP_DATA_DIR="${SP_DATA_DIR:-$HOME/.local/share/simple-proxy}"
SP_CONFIG_DIR="${SP_CONFIG_DIR:-$HOME/.config/simple-proxy}"
PURGE=0
case "${1:-}" in --purge) PURGE=1;; '') ;; *) echo '用法: uninstall.sh [--purge]' >&2; exit 1;; esac
# 统一使用带进程身份验证的停止逻辑。
if [[ -x "$SP_BIN_DIR/simple-proxy" ]]; then
  "$SP_BIN_DIR/simple-proxy" off
elif [[ -f "$SOURCE_DIR/bin/simple-proxy" ]]; then
  bash "$SOURCE_DIR/bin/simple-proxy" off
fi
for rc in "${ZDOTDIR:-$HOME}/.zshrc" "$HOME/.bashrc"; do
  [[ -f "$rc" ]] || continue
  python3 - "$rc" <<'PY'
from pathlib import Path
import re,sys
p=Path(sys.argv[1]);s=p.read_text()
s=re.sub(r'\n?# ─── simple-proxy ───\nif \[ -r .*?\nfi\n?', '\n', s, flags=re.S)
p.write_text(s)
PY
done
rm -f "$SP_BIN_DIR/simple-proxy" "$SP_BIN_DIR/mihomo"
if (( PURGE )); then
  [[ -n "$SP_DATA_DIR" && "$SP_DATA_DIR" != / && "$SP_DATA_DIR" != "$HOME" ]] || exit 1
  [[ -n "$SP_CONFIG_DIR" && "$SP_CONFIG_DIR" != / && "$SP_CONFIG_DIR" != "$HOME" ]] || exit 1
  rm -rf "$SP_DATA_DIR" "$SP_CONFIG_DIR"
fi
echo '✅ 已卸载'
