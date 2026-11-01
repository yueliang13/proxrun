#!/usr/bin/env bash
# 安装/更新：bash install.sh [--subscription URL] [--no-shell] [--port N] [--no-download]
set -euo pipefail
SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export SP_BIN_DIR="${SP_BIN_DIR:-$HOME/.local/bin}"
export SP_DATA_DIR="${SP_DATA_DIR:-$HOME/.local/share/simple-proxy}"
export SP_CONFIG_DIR="${SP_CONFIG_DIR:-$HOME/.config/simple-proxy}"

_sp_asset() {
  local rel="$1" path
  for path in "$SOURCE_DIR/$rel" "$SOURCE_DIR/${rel##*/}" "$SP_BIN_DIR/${rel##*/}"; do
    [[ -f "$path" ]] && { printf '%s' "$path"; return 0; }
  done
  return 1
}
. "$(_sp_asset lib/core.sh)"
SUB="${SIMPLE_PROXY_SUBSCRIPTION:-}" WANT_SHELL=1 DO_DOWNLOAD=1 PORT=""
while (( $# )); do
  case "$1" in
    --subscription|--port)
      [[ $# -ge 2 && -n "$2" ]] || { sp_err "$1 缺少参数"; exit 1; }
      if [[ "$1" == --subscription ]]; then SUB="$2"; else PORT="$2"; fi
      shift 2;;
    --no-shell) WANT_SHELL=0; shift;;
    --no-download) DO_DOWNLOAD=0; shift;;
    -h|--help) sed -n '2s/^# //p' "${BASH_SOURCE[0]}"; exit 0;;
    *) sp_err "未知选项: $1"; exit 1;;
  esac
done
if [[ -n "$PORT" ]]; then
  [[ "$PORT" =~ ^[0-9]{1,5}$ ]] && (( 10#$PORT >= 1 && 10#$PORT <= 65535 )) \
    || { sp_err "HTTP 端口必须在 1–65535 之间"; exit 1; }
fi
mkdir -p "$SP_BIN_DIR" "$SP_DATA_DIR" "$SP_CONFIG_DIR"
for name in proxy.conf rules.yaml; do
  if [[ ! -f "$SP_CONFIG_DIR/$name" ]]; then
    if [[ -f "$SOURCE_DIR/config/$name" ]]; then
      cp "$SOURCE_DIR/config/$name" "$SP_CONFIG_DIR/$name"
    else
      cp "$SOURCE_DIR/$name.default" "$SP_CONFIG_DIR/$name"
    fi
  fi
done
if [[ -n "$PORT" ]]; then
  python3 - "$SP_CONFIG_DIR/proxy.conf" "$PORT" <<'PY'
from pathlib import Path
import re,sys
p=Path(sys.argv[1]);text=p.read_text()
text=re.sub(r'^SP_HTTP_PORT=.*$', 'SP_HTTP_PORT='+str(int(sys.argv[2])), text, flags=re.M)
p.write_text(text)
PY
  export SP_HTTP_PORT="$PORT"
fi
sp_load_config
# PyYAML 缺失时安装到持久数据目录，不修改系统 Python。
if ! PYTHONPATH="$SP_DATA_DIR/vendor${PYTHONPATH:+:$PYTHONPATH}" python3 -c 'import yaml' 2>/dev/null; then
  python3 -m pip install --disable-pip-version-check --target "$SP_DATA_DIR/vendor" 'PyYAML==6.0.3'
fi
if (( DO_DOWNLOAD )); then
  case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) sp_err '不支持的系统'; exit 1;; esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64-compatible;;
    aarch64|arm64) arch=arm64;;
    *) sp_err '不支持的架构'; exit 1;;
  esac
  url="https://github.com/MetaCubeX/mihomo/releases/download/v$SP_MIHOMO_VERSION/mihomo-$os-$arch-v$SP_MIHOMO_VERSION.gz"
  download_dir="$(mktemp -d "$SP_BIN_DIR/.mihomo-install.XXXXXX")"
  trap 'rm -rf "$download_dir"' EXIT
  printf '→ 下载 mihomo v%s (%s/%s)\n' "$SP_MIHOMO_VERSION" "$os" "$arch" >&2
  curl -fsSL --max-time 300 -o "$download_dir/kernel.gz" "$url"
  gzip -dc "$download_dir/kernel.gz" > "$download_dir/mihomo"
  chmod 755 "$download_dir/mihomo"
  "$download_dir/mihomo" -v >/dev/null
  mv -f "$download_dir/mihomo" "$SP_MIHOMO_BIN"
  rm -rf "$download_dir"
  trap - EXIT
fi
copy_asset() {
  local source target="$2"
  source="$(_sp_asset "$1")"
  [[ "$source" -ef "$target" ]] || install -m 755 "$source" "$target"
}
copy_asset bin/simple-proxy "$SP_BIN_DIR/simple-proxy"
for file in lib/core.sh lib/runtime.py lib/build-config.py lib/shell.sh scripts/clean-logs.sh scripts/update-subscription.sh install.sh uninstall.sh; do
  copy_asset "$file" "$SP_DATA_DIR/${file##*/}"
done
for name in proxy.conf rules.yaml; do
  if [[ -f "$SOURCE_DIR/config/$name" ]]; then
    install -m 644 "$SOURCE_DIR/config/$name" "$SP_DATA_DIR/$name.default"
  fi
done
if [[ -n "$SUB" ]]; then
  bash "$SP_DATA_DIR/update-subscription.sh" "$SUB" "$SP_SUBSCRIPTION_CONFIG"
fi
if (( WANT_SHELL )); then
  case "${SHELL:-}" in */zsh) rc="${ZDOTDIR:-$HOME}/.zshrc";; *) rc="$HOME/.bashrc";; esac
  mkdir -p "$(dirname "$rc")"
  python3 - "$rc" "$SP_DATA_DIR/shell.sh" "$SP_BIN_DIR" "$SP_DATA_DIR" "$SP_CONFIG_DIR" <<'PY'
from pathlib import Path
import shlex,sys,re
p=Path(sys.argv[1]);text=p.read_text() if p.exists() else ''
text=re.sub(r'\n?# ─── simple-proxy ───\nif \[ -r .*?\nfi\n?', '\n', text, flags=re.S)
path=shlex.quote(sys.argv[2])
paths=' '.join(key+'='+shlex.quote(value) for key,value in zip(('SP_BIN_DIR','SP_DATA_DIR','SP_CONFIG_DIR'), sys.argv[3:]))
text+='\n# ─── simple-proxy ───\nif [ -r '+path+' ]; then\n  export '+paths+'; . '+path+'\nfi\n'
p.write_text(text)
PY
fi
printf '✅ 安装完成。运行 simple-proxy check 自检。\n'
