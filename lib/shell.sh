# shell 快捷命令。常驻模式由当前 shell 保留一个可 wait 的内核父进程。
typeset -ga _PROXRUN_HOLDERS
if typeset -p _SIMPLE_PROXY_HOLDERS >/dev/null 2>&1; then
  _PROXRUN_HOLDERS+=("${_SIMPLE_PROXY_HOLDERS[@]}")
  unset _SIMPLE_PROXY_HOLDERS
fi
_proxrun_bin() {
  local candidate
  for candidate in "${PROXRUN_BIN_DIR:-${SP_BIN_DIR:-$HOME/.local/bin}}/proxrun" "$HOME/.local/bin/proxrun"; do
    [[ -x "$candidate" ]] && { printf '%s' "$candidate"; return 0; }
  done
  command -v proxrun 2>/dev/null
}

proxy-on() {
  local binary rc holder ready count monitor_was_on=0
  binary="$(_proxrun_bin)" || return 127
  if [[ "${1:-}" == --start && $# -gt 1 ]] && ! "$binary" _kept; then
    ready="$(mktemp -d)" || return 1
    if [[ -n "${ZSH_VERSION:-}" ]]; then
      setopt local_options no_monitor
    else
      [[ "$-" == *m* ]] && monitor_was_on=1
      set +m
    fi
    {
      "$binary" _hold "$ready" >"$ready/output" 2>&1 &
      holder=$!
    } 2>/dev/null
    _PROXRUN_HOLDERS+=("$holder")
    count=0
    while [[ ! -f "$ready/ready" ]]; do
      if ! kill -0 "$holder" 2>/dev/null || (( count >= 650 )); then
        kill "$holder" 2>/dev/null || true
        wait "$holder" 2>/dev/null || true
        cat "$ready/output" >&2
        rm -rf "$ready"
        (( monitor_was_on )) && set -m
        return 1
      fi
      sleep .05
      count=$((count + 1))
    done
    rm -rf "$ready"
    (( monitor_was_on )) && set -m
  fi
  "$binary" on "$@"
}

# 保留 proxy-on 的全部参数及常驻模式的回收逻辑。
po() {
  proxy-on "$@"
}

proxy-off() {
  local binary holder
  binary="$(_proxrun_bin)" || return 127
  "$binary" off || return $?
  [[ -z "${ZSH_VERSION:-}" ]] || setopt local_options no_monitor
  for holder in "${_PROXRUN_HOLDERS[@]}"; do
    wait "$holder" 2>/dev/null || true
  done
  _PROXRUN_HOLDERS=()
}

proxy-status() {
  local binary
  binary="$(_proxrun_bin)" || return 127
  "$binary" status "$@"
}
