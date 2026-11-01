# Fish 快捷命令；常驻模式保留可由当前 shell 等待回收的内核父进程。
if not set -q _PROXRUN_HOLDERS
    set -g _PROXRUN_HOLDERS
end

function _proxrun_bin
    set -l binary_dir "$HOME/.local/bin"
    if set -q PROXRUN_BIN_DIR[1]; and test -n "$PROXRUN_BIN_DIR"
        set binary_dir "$PROXRUN_BIN_DIR"
    else if set -q SP_BIN_DIR[1]; and test -n "$SP_BIN_DIR"
        set binary_dir "$SP_BIN_DIR"
    end
    for candidate in "$binary_dir/proxrun" "$HOME/.local/bin/proxrun"
        if test -x "$candidate"
            printf '%s' "$candidate"
            return 0
        end
    end
    command -s proxrun
end

function proxy-on --description '让指定命令走代理，用完自动关闭'
    set -l binary (_proxrun_bin); or return 127
    if test (count $argv) -gt 1; and test "$argv[1]" = --start; and not "$binary" _kept
        set -l ready (mktemp -d); or return 1
        set -l holder
        begin
            "$binary" _hold "$ready" >"$ready/output" 2>&1 &
            set holder $last_pid
        end 2>/dev/null
        set -ga _PROXRUN_HOLDERS $holder
        set -l attempts 0
        while not test -f "$ready/ready"
            if not kill -0 $holder 2>/dev/null; or test $attempts -ge 650
                kill $holder 2>/dev/null
                wait $holder 2>/dev/null
                cat "$ready/output" >&2
                rm -rf "$ready"
                return 1
            end
            sleep .05
            set attempts (math $attempts + 1)
        end
        rm -rf "$ready"
    end
    "$binary" on $argv
end

function po --description 'proxy-on 的简写'
    proxy-on $argv
end

function proxy-off --description '关闭共享代理'
    set -l binary (_proxrun_bin); or return 127
    "$binary" off; or return $status
    for holder in $_PROXRUN_HOLDERS
        wait $holder 2>/dev/null
    end
    set -g _PROXRUN_HOLDERS
    return 0
end

function proxy-status --description '查看代理状态'
    set -l binary (_proxrun_bin); or return 127
    "$binary" status $argv
end
