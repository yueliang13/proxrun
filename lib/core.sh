#!/usr/bin/env bash
# ==============================================================================
# simple-proxy · core
#
# 核心实现：启动/停止 mihomo、注入代理环境、生成运行时配置、状态与自检。
# 所有可调项都来自配置文件（$SP_CONFIG_DIR/proxy.conf），可用环境变量覆盖。
# ==============================================================================

# ── 路径 ─────────────────────────────────────────────────────────────────────
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

# ── 配置 ─────────────────────────────────────────────────────────────────────
sp_load_config() {
  sp_resolve_dirs
  # 默认值（可被配置文件和环境变量覆盖）
  SP_HTTP_PORT="${SP_HTTP_PORT:-7890}"
  SP_LOG_KEEP_DAYS="${SP_LOG_KEEP_DAYS:-3}"
  SP_MIHOMO_VERSION="${SP_MIHOMO_VERSION:-1.19.33}"
  SP_PROXY_URL="http://127.0.0.1:${SP_HTTP_PORT}"
  # DNS 回退列表：系统解析器优先（由 build-config.py 探测），这里是备用
  SP_DNS_FALLBACKS="${SP_DNS_FALLBACKS:-https://1.1.1.1/dns-query https://8.8.8.8/dns-query}"
  # 客户端 NO_PROXY 追加项
  SP_NO_PROXY_EXTRA="${SP_NO_PROXY_EXTRA:-localhost,127.0.0.1,.svc,.svc.cluster.local,10.0.0.0/8,10.42.0.0/16,10.43.0.0/16,10.254.0.0/16,10.255.0.0/16}"
  # 是否开启 mihomo 的控制 API（默认关闭，减少暴露面）
  SP_ENABLE_API="${SP_ENABLE_API:-0}"

  if [[ -r "$SP_CONF_FILE" ]]; then
    # shellcheck disable=SC1090
    . "$SP_CONF_FILE"
    sp_resolve_dirs   # 配置文件可能改了路径
  fi
}

# ── 内部工具 ─────────────────────────────────────────────────────────────────
sp_log()  { printf '%s\n' "$*" >&2; }
sp_err()  { printf '❌ %s\n' "$*" >&2; }
sp_ok()   { printf '✅ %s\n' "$*" >&2; }
sp_warn() { printf '⚠️  %s\n' "$*" >&2; }

sp_running() {
  [[ -f "$SP_PID_FILE" ]] && kill -0 "$(cat "$SP_PID_FILE" 2>/dev/null)" 2>/dev/null
}

# 端口探测：zsh 不支持 /dev/tcp，必须借道 bash
sp_port_open() {
  bash -c "exec 3<>/dev/tcp/127.0.0.1/$1" 2>/dev/null
}

# ── 运行时配置生成 ───────────────────────────────────────────────────────────
sp_build_config() {
  local builder="$SP_DATA_DIR/build-config.py"
  [[ -r "$builder" ]] || builder="$SP_SCRIPT_DIR/lib/build-config.py"
  [[ -r "$builder" ]] || { sp_err "找不到 build-config.py"; return 1; }

  python3 "$builder" \
      --subscription-config "$SP_SUBSCRIPTION_CONFIG" \
      --rules "$SP_RULES_FILE" \
      --output "$SP_RUNTIME_CONFIG" \
      --dns-fallbacks "$SP_DNS_FALLBACKS" \
      --http-port "$SP_HTTP_PORT" \
      --enable-api "$SP_ENABLE_API" \
      --data-dir "$SP_DATA_DIR" \
    || return 1
  chmod 600 "$SP_RUNTIME_CONFIG" 2>/dev/null
}

# ── 日志轮转 ─────────────────────────────────────────────────────────────────
sp_clean_logs() {
  local cleaner="$SP_DATA_DIR/clean-logs.sh"
  [[ -r "$cleaner" ]] || cleaner="$SP_SCRIPT_DIR/scripts/clean-logs.sh"
  [[ -r "$cleaner" ]] && bash "$cleaner" "$SP_DATA_DIR" "$SP_LOG_KEEP_DAYS" 2>/dev/null
  return 0
}

# ── 启动 ─────────────────────────────────────────────────────────────────────
sp_start() {
  sp_running && return 0

  if [[ ! -x "$SP_MIHOMO_BIN" ]]; then
    sp_err "找不到 mihomo: $SP_MIHOMO_BIN"
    sp_err "请先运行: simple-proxy install"
    return 1
  fi
  if [[ ! -f "$SP_SUBSCRIPTION_CONFIG" ]]; then
    sp_err "缺少订阅配置: $SP_SUBSCRIPTION_CONFIG"
    sp_err "请先运行: simple-proxy update --subscription '<URL>'"
    return 1
  fi

  sp_build_config || { sp_err "生成运行时配置失败"; return 1; }
  sp_clean_logs

  # ① 在当前 shell 启动（保持为子进程 → 停止时可 wait 回收，不留僵尸）
  # ② 在 mihomo 家目录启动（geo 缓存写这里），但立刻还原用户的工作目录
  # ③ 关闭 zsh/bash 的作业通知，避免输出 [2] pid / + done 噪音
  local oldpwd="$PWD"
  cd "$SP_DATA_DIR" || return 1
  setopt no_monitor 2>/dev/null || true
  nohup "$SP_MIHOMO_BIN" -d "$SP_DATA_DIR" -f "$SP_RUNTIME_CONFIG" \
        >>"$SP_LOG_FILE" 2>&1 &
  SP_MIHOMO_PID=$!
  cd "$oldpwd" || true
  echo "$SP_MIHOMO_PID" > "$SP_PID_FILE"

  local i
  for i in $(seq 1 60); do
    if sp_port_open "$SP_HTTP_PORT"; then
      return 0
    fi
    if ! kill -0 "$SP_MIHOMO_PID" 2>/dev/null; then
      sp_err "mihomo 启动失败，日志尾部："
      tail -n 15 "$SP_LOG_FILE" >&2
      rm -f "$SP_PID_FILE"
      return 1
    fi
    sleep 0.5
  done
  sp_err "等待端口 $SP_HTTP_PORT 超时"
  return 1
}

# ── 停止 ─────────────────────────────────────────────────────────────────────
sp_stop() {
  [[ -f "$SP_PID_FILE" ]] || return 0
  local pid; pid="$(cat "$SP_PID_FILE")"
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null
    local i
    for i in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
    kill -9 "$pid" 2>/dev/null
    # 若是本 shell 的子进程 → 立即回收，避免僵尸
    if [[ "${SP_MIHOMO_PID:-}" == "$pid" ]]; then
      wait "$pid" 2>/dev/null
      unset SP_MIHOMO_PID
    fi
  fi
  rm -f "$SP_PID_FILE"
  setopt monitor 2>/dev/null || true
  return 0
}

# ── 代理环境注入（仅子进程）───────────────────────────────────────────────────
sp_env() {
  export http_proxy="$SP_PROXY_URL"  https_proxy="$SP_PROXY_URL"
  export HTTP_PROXY="$SP_PROXY_URL"  HTTPS_PROXY="$SP_PROXY_URL"
  export no_proxy="${no_proxy:+$no_proxy,}$SP_NO_PROXY_EXTRA"
  export NO_PROXY="$no_proxy"
}

# ── 状态 ─────────────────────────────────────────────────────────────────────
sp_status() {
  if sp_running; then
    printf '● mihomo: 运行中 (pid %s)  HTTP 代理 %s\n' "$(cat "$SP_PID_FILE")" "$SP_PROXY_URL"
  else
    printf '○ mihomo: 未运行\n'
  fi
  printf '  端口      : %s\n' "$SP_HTTP_PORT"
  printf '  数据目录  : %s\n' "$SP_DATA_DIR"
  printf '  配置文件  : %s\n' "$SP_RUNTIME_CONFIG"
  printf '  规则文件  : %s\n' "$SP_RULES_FILE"
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
    printf '  ✓ 订阅配置  : %s (mode %s)\n' "$SP_SUBSCRIPTION_CONFIG" "$(stat -c '%a' "$SP_SUBSCRIPTION_CONFIG" 2>/dev/null)"
  else
    printf '  ✗ 订阅配置缺失: %s\n' "$SP_SUBSCRIPTION_CONFIG"; fail=1
  fi

  # 规则
  if [[ -f "$SP_RULES_FILE" ]]; then
    printf '  ✓ 规则文件  : %s (%s 条)\n' "$SP_RULES_FILE" "$(grep -c '^[[:space:]]*- ' "$SP_RULES_FILE" 2>/dev/null)"
  else
    printf '  ✗ 规则文件缺失: %s\n' "$SP_RULES_FILE"; fail=1
  fi

  # python
  command -v python3 >/dev/null 2>&1 \
    && printf '  ✓ python3   : %s\n' "$(command -v python3)" \
    || { printf '  ✗ 缺少 python3（用于生成运行时配置）\n'; fail=1; }

  # terminfo（zsh 在缺少终端描述时会出现重绘错乱：ls 显示成 lss）
  if command -v infocmp >/dev/null 2>&1; then
    if infocmp "${TERM:-xterm}" >/dev/null 2>&1; then
      printf '  ✓ terminfo  : %s 可解析\n' "${TERM:-xterm}"
    else
      printf '  ⚠ terminfo  : 系统不认识 TERM=%s —— 建议 simple-proxy check --fix-term 或手动安装对应 terminfo\n' "${TERM:-xterm}"
    fi
  fi

  # 进程与端口
  if sp_running; then
    printf '  ✓ 进程      : 运行中 (pid %s)\n' "$(cat "$SP_PID_FILE")"
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
