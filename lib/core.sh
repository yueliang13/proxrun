#!/usr/bin/env bash
# 配置优先级：环境变量 > 用户配置 > 默认值。运行状态由 runtime.py 统一管理。
sp_resolve_dirs() {
  SP_BIN_DIR="${SP_BIN_DIR:-$HOME/.local/bin}"
  SP_DATA_DIR="${SP_DATA_DIR:-$HOME/.local/share/simple-proxy}"
  SP_CONFIG_DIR="${SP_CONFIG_DIR:-$HOME/.config/simple-proxy}"
  SP_RULES_FILE="${SP_RULES_FILE:-$SP_CONFIG_DIR/rules.yaml}"
  SP_CONF_FILE="${SP_CONF_FILE:-$SP_CONFIG_DIR/proxy.conf}"
  SP_MIHOMO_BIN="${SP_MIHOMO_BIN:-$SP_BIN_DIR/mihomo}"
  SP_PID_FILE="$SP_DATA_DIR/mihomo.pid"
  SP_LOG_FILE="$SP_DATA_DIR/mihomo.log"
  SP_RUNTIME_CONFIG="$SP_DATA_DIR/config.runtime.yaml"
  SP_SUBSCRIPTION_CONFIG="$SP_DATA_DIR/config.yaml"
}

sp_load_config() {
  local name assignment
  local -a overrides=()
  while IFS= read -r name; do
    [[ "$name" == SP_* ]] && overrides+=("$name=${!name}")
  done < <(compgen -e)
  SP_CONF_FILE="${SP_CONF_FILE:-${SP_CONFIG_DIR:-$HOME/.config/simple-proxy}/proxy.conf}"
  if [[ -r "$SP_CONF_FILE" ]]; then
    . "$SP_CONF_FILE"
  fi
  for assignment in "${overrides[@]}"; do export "$assignment"; done
  sp_resolve_dirs
  SP_HTTP_PORT="${SP_HTTP_PORT:-7890}"
  SP_LOG_KEEP_DAYS="${SP_LOG_KEEP_DAYS:-3}"
  SP_MIHOMO_VERSION="${SP_MIHOMO_VERSION:-1.19.33}"
  SP_DNS_FALLBACKS="${SP_DNS_FALLBACKS-https://doh.pub/dns-query https://dns.alidns.com/dns-query}"
  SP_NO_PROXY_EXTRA="${SP_NO_PROXY_EXTRA-localhost,127.0.0.1,::1,.local,.svc,.svc.cluster.local,registry.qz.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16}"
  SP_ENABLE_API="${SP_ENABLE_API:-0}"
  if [[ ! "$SP_HTTP_PORT" =~ ^[0-9]{1,5}$ ]] || (( 10#$SP_HTTP_PORT < 1 || 10#$SP_HTTP_PORT > 65535 )); then
    sp_err "HTTP 端口必须在 1–65535 之间"; return 1
  fi
  SP_HTTP_PORT=$((10#$SP_HTTP_PORT))
  SP_PROXY_URL="http://127.0.0.1:$SP_HTTP_PORT"
  SP_BUILDER="$(_sp_asset lib/build-config.py)" || return 1
  SP_CLEANER="$(_sp_asset scripts/clean-logs.sh)" || return 1
  SP_RUNTIME="$(_sp_asset lib/runtime.py)" || return 1
  export SP_BIN_DIR SP_DATA_DIR SP_CONFIG_DIR SP_RULES_FILE SP_CONF_FILE SP_MIHOMO_BIN
  export SP_HTTP_PORT SP_LOG_KEEP_DAYS SP_MIHOMO_VERSION SP_DNS_FALLBACKS SP_NO_PROXY_EXTRA SP_ENABLE_API
  export SP_BUILDER SP_CLEANER
}

sp_err() { printf '❌ %s\n' "$*" >&2; }
sp_runtime() { python3 "$SP_RUNTIME" "$@"; }
sp_running() { sp_runtime running; }
sp_stop() { sp_runtime off; }
sp_status() { sp_runtime status; }
sp_port_open() {
  python3 - "$1" <<'PORT'
import socket, sys
try:
    with socket.create_connection(('127.0.0.1', int(sys.argv[1])), timeout=.2): pass
except OSError:
    sys.exit(1)
PORT
}

# ── 自检 ─────────────────────────────────────────────────────────────────────
sp_check() {
  local fail=0
  printf '── simple-proxy 自检 ──\n'

  # 内核
  if [[ -x "$SP_MIHOMO_BIN" ]]; then
    printf '  ✓ 内核      : %s (%s)\n' "$SP_MIHOMO_BIN" "$("$SP_MIHOMO_BIN" -v 2>/dev/null | head -1 | cut -d' ' -f3)"
  else
    printf '  ✗ 内核缺失  : %s\n' "$SP_MIHOMO_BIN"; fail=1
  fi

  # 订阅配置
  if [[ -f "$SP_SUBSCRIPTION_CONFIG" ]]; then
    printf '  ✓ 订阅配置  : %s (mode %s)\n' "$SP_SUBSCRIPTION_CONFIG" "$(python3 -c 'import os,sys; print(oct(os.stat(sys.argv[1]).st_mode & 0o777)[2:])' "$SP_SUBSCRIPTION_CONFIG")"
  else
    printf '  ✗ 订阅配置缺失: %s\n' "$SP_SUBSCRIPTION_CONFIG"; fail=1
  fi

  # 规则
  if [[ -f "$SP_RULES_FILE" ]]; then
    printf '  ✓ 规则文件  : %s (%s 条)\n' "$SP_RULES_FILE" "$(grep -c '^[[:space:]]*- ' "$SP_RULES_FILE" 2>/dev/null)"
  else
    printf '  ✗ 规则文件缺失: %s\n' "$SP_RULES_FILE"; fail=1
  fi

  # YAML 依赖
  PYTHONPATH="$SP_DATA_DIR/vendor${PYTHONPATH:+:$PYTHONPATH}" python3 -c "import yaml" 2>/dev/null \
    && printf '  ✓ PyYAML    : 可用\n' \
    || { printf '  ✗ 缺少 PyYAML，请重新安装\n'; fail=1; }

  # python
  command -v python3 >/dev/null 2>&1 \
    && printf '  ✓ python3   : %s\n' "$(command -v python3)" \
    || { printf '  ✗ 缺少 python3（用于生成运行时配置）\n'; fail=1; }

  # terminfo（zsh 在缺少终端描述时会出现重绘错乱：ls 显示成 lss）
  if command -v infocmp >/dev/null 2>&1; then
    if infocmp "${TERM:-xterm}" >/dev/null 2>&1; then
      printf '  ✓ terminfo  : %s 可解析\n' "${TERM:-xterm}"
    else
      printf '  ⚠ terminfo  : 系统不认识 TERM=%s —— 建议手动安装对应 terminfo\n' "${TERM:-xterm}"
    fi
  fi

  # 进程与端口
  if sp_running; then
    printf '  ✓ 进程      : 运行中 (pid %s)\n' "$(cat "$SP_PID_FILE" 2>/dev/null)"
  else
    printf '  ○ 进程      : 未运行\n'
  fi
  if sp_port_open "$SP_HTTP_PORT"; then
    printf '  ✓ 端口      : %s 可连接\n' "$SP_HTTP_PORT"
  else
    printf '  ○ 端口      : %s 未监听\n' "$SP_HTTP_PORT"
  fi

  return $fail
}
