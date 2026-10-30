#!/usr/bin/env bash
# ==============================================================================
# simple-proxy · 安装脚本
#
#   bash install.sh [--subscription <URL>] [--no-shell] [--port N] [--no-download]
#
# 安装内容:
#   ~/.local/bin/mihomo                 代理内核
#   ~/.local/bin/simple-proxy           主命令
#   ~/.local/share/simple-proxy/        数据（订阅配置 / 运行时配置 / 日志 / 规则库）
#   ~/.config/simple-proxy/             配置（proxy.conf / rules.yaml）
#   ~/.zshrc 或 ~/.bashrc               shell 集成（可选，提供 proxy-on/off/status）
# ==============================================================================
set -uo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SP_BIN_DIR="${SP_BIN_DIR:-$HOME/.local/bin}"
SP_DATA_DIR="${SP_DATA_DIR:-$HOME/.local/share/simple-proxy}"
SP_CONFIG_DIR="${SP_CONFIG_DIR:-$HOME/.config/simple-proxy}"

SUB="" WANT_SHELL=1 DO_DOWNLOAD=1 PORT=""
while (( $# )); do
  case "$1" in
    --subscription) SUB="$2"; shift 2;;
    --no-shell) WANT_SHELL=0; shift;;
    --no-download) DO_DOWNLOAD=0; shift;;
    --port) PORT="$2"; shift 2;;
    -h|--help)
      sed -n '2,14p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0;;
    *) echo "未知选项: $1" >&2; exit 1;;
  esac
done

info() { printf '  %s\n' "$*"; }
ok()   { printf '✅ %s\n' "$*"; }
err()  { printf '❌ %s\n' "$*" >&2; }

echo "── simple-proxy 安装 ──"
mkdir -p "$SP_BIN_DIR" "$SP_DATA_DIR" "$SP_CONFIG_DIR"
info "bin   : $SP_BIN_DIR"
info "data  : $SP_DATA_DIR"
info "config: $SP_CONFIG_DIR"

# ── 1. 配置与规则 ─────────────────────────────────────────────────────────────
for f in proxy.conf rules.yaml; do
  [[ -f "$SP_CONFIG_DIR/$f" ]] || cp "$REPO_DIR/config/$f" "$SP_CONFIG_DIR/$f"
done
info "配置与规则就绪"

# ── 2. 内核 ──────────────────────────────────────────────────────────────────
if (( DO_DOWNLOAD )); then
  MIHOMO_VERSION="$(grep -oE '^SP_MIHOMO_VERSION=.*' "$SP_CONFIG_DIR/proxy.conf" | cut -d= -f2 | tr -d '"')"
  MIHOMO_VERSION="${MIHOMO_VERSION:-1.19.33}"
  case "$(uname -s)" in
    Linux)  os=linux;;
    Darwin) os=darwin;;
    *) err "暂不支持的系统: $(uname -s)"; exit 1;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=x86_64;;
    aarch64|arm64) arch=aarch64;;
    *) err "暂不支持的架构: $(uname -m)"; exit 1;;
  esac
  url="https://github.com/MetaCubeX/mihomo/releases/download/v${MIHOMO_VERSION}/mihomo-${os}-${arch}-compatible-v${MIHOMO_VERSION}.gz"
  info "下载 mihomo v${MIHOMO_VERSION} (${os}/${arch}) ..."
  if curl -fsSL --max-time 300 -o /tmp/sp-mihomo.gz "$url"; then
    gunzip -f /tmp/sp-mihomo.gz && install -m 755 /tmp/sp-mihomo.gz "$SP_BIN_DIR/mihomo" && rm -f /tmp/sp-mihomo.gz
    ok "mihomo 已安装"
  else
    rm -f /tmp/sp-mihomo.gz
    err "mihomo 下载失败，可稍后重试或用 --no-download 自行安装"
  fi
fi

# ── 3. 主命令与内部文件 ──────────────────────────────────────────────────────
install -m 755 "$REPO_DIR/bin/simple-proxy" "$SP_BIN_DIR/simple-proxy"
install -m 644 "$REPO_DIR/lib/core.sh"            "$SP_DATA_DIR/core.sh"
install -m 755 "$REPO_DIR/lib/build-config.py"   "$SP_DATA_DIR/build-config.py"
install -m 644 "$REPO_DIR/lib/shell.sh"           "$SP_DATA_DIR/shell.sh"
install -m 755 "$REPO_DIR/scripts/clean-logs.sh" "$SP_DATA_DIR/clean-logs.sh"
install -m 755 "$REPO_DIR/scripts/update-subscription.sh" "$SP_DATA_DIR/update-subscription.sh"
ok "命令已安装: $SP_BIN_DIR/simple-proxy"

# ── 4. 端口 ──────────────────────────────────────────────────────────────────
if [[ -n "$PORT" ]]; then
  if grep -q '^SP_HTTP_PORT=' "$SP_CONFIG_DIR/proxy.conf"; then
    sed -i "s/^SP_HTTP_PORT=.*/SP_HTTP_PORT=$PORT/" "$SP_CONFIG_DIR/proxy.conf"
  else
    echo "SP_HTTP_PORT=$PORT" >> "$SP_CONFIG_DIR/proxy.conf"
  fi
  info "HTTP 端口设为 $PORT"
fi

# ── 5. 订阅 ──────────────────────────────────────────────────────────────────
if [[ -n "$SUB" ]]; then
  bash "$SP_DATA_DIR/update-subscription.sh" "$SUB" "$SP_DATA_DIR/config.yaml" || true
fi

# ── 6. shell 集成 ────────────────────────────────────────────────────────────
if (( WANT_SHELL )); then
  case "${SHELL:-}" in
    */zsh) rc="${ZDOTDIR:-$HOME}/.zshrc";;
    *)     rc="$HOME/.bashrc";;
  esac
  if ! grep -q 'simple-proxy/shell.sh' "$rc" 2>/dev/null; then
    cat >> "$rc" <<'EOF'

# ─── simple-proxy ───
if [ -r "$HOME/.local/share/simple-proxy/shell.sh" ]; then
  . "$HOME/.local/share/simple-proxy/shell.sh"
fi
EOF
    ok "shell 集成已写入: $rc"
  else
    info "shell 集成已存在: $rc"
  fi
fi

echo ""
echo "── 下一步 ──"
echo "  1) 填订阅:   simple-proxy update '<URL>'"
echo "  2) 自检:     simple-proxy check"
echo "  3) 使用:     simple-proxy on curl https://api.ipify.org"
echo "  4) 快捷命令: proxy-on / proxy-off / proxy-status"
