#!/usr/bin/env bash
# ==============================================================================
# simple-proxy · shell 集成
#
# 提供三个与 CLI 等价的快捷命令：
#   proxy-on  [cmd...]    → simple-proxy on [cmd...]
#   proxy-off             → simple-proxy off
#   proxy-status          → simple-proxy status
#
# 由 `simple-proxy install` 写入 ~/.zshrc 或 ~/.bashrc。
# 改了本文件后需重新 source 一次才能生效。
# ==============================================================================
_simple_proxy_bin() {
  local c
  for c in "$HOME/.local/bin/simple-proxy" "${SP_BIN_DIR:-}/simple-proxy"; do
    [[ -x "$c" ]] && { printf '%s' "$c"; return 0; }
  done
  command -v simple-proxy 2>/dev/null
}

proxy-on() {
  local b; b="$(_simple_proxy_bin)" || { echo "simple-proxy 未安装" >&2; return 127; }
  "$b" on "$@"
}
proxy-off() {
  local b; b="$(_simple_proxy_bin)" || { echo "simple-proxy 未安装" >&2; return 127; }
  "$b" off "$@"
}
proxy-status() {
  local b; b="$(_simple_proxy_bin)" || { echo "simple-proxy 未安装" >&2; return 127; }
  "$b" status "$@"
}
