package app

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yueliang13/proxrun"
)

type App struct {
	In       io.Reader
	Out, Err io.Writer
	HTTP     *http.Client
}

func New(in io.Reader, out, err io.Writer) *App {
	return &App{In: in, Out: out, Err: err, HTTP: &http.Client{Timeout: 60 * time.Second}}
}
func (a *App) Run(args []string) int {
	if len(args) == 0 {
		a.usage()
		return 0
	}
	action := args[0]
	args = args[1:]
	switch action {
	case "help", "-h", "--help":
		a.usage()
		return 0
	case "version", "--version", "-v":
		fmt.Fprintln(a.Out, "proxrun "+proxrun.Version())
		return 0
	case "shell":
		return a.shell(args)
	}
	c, err := LoadConfig()
	if err != nil {
		return a.fail(1, err)
	}
	r := newRuntime(a, c)
	code := 0
	switch action {
	case "on":
		code, err = r.on(args, false)
	case "_hold":
		code, err = r.on(args, true)
	case "off":
		err = r.off()
	case "status":
		err = a.status(r)
	case "_kept":
		var state *State
		err = r.locked(func() error { var e error; state, e = r.readState(); return e })
		if state == nil || !state.Keep {
			code = 1
		}
	case "check":
		code = a.check(r)
	case "logs":
		err = a.logs(c, args)
	case "update":
		url := subscriptionEnv()
		if len(args) == 1 {
			url = args[0]
		} else if len(args) > 1 {
			err = fmt.Errorf("用法: proxrun update <订阅URL>")
			break
		}
		err = a.update(c, url)
	case "install":
		err = a.install(c, args)
	case "uninstall":
		err = a.uninstall(c, args)
	default:
		err = fmt.Errorf("未知命令: %s", action)
	}
	if err != nil {
		if code == 0 {
			code = 1
		}
		return a.fail(code, err)
	}
	return code
}
func (a *App) fail(code int, err error) int { fmt.Fprintln(a.Err, "proxrun:", err); return code }
func (a *App) usage() {
	fmt.Fprint(a.Out, `proxrun — 一条命令走代理，用完自动关闭。

用法:
  proxrun on <cmd> [args...]          一次性使用代理
  proxrun on --start <cmd> [args...]  保持代理常驻，直到 off
  proxrun off                        关闭共享代理
  proxrun status                     查看状态
  proxrun check                      自检
  proxrun logs [N]                   最近 N 行路由日志，默认 50
  proxrun update <URL>               更新 Clash/mihomo YAML 订阅
  proxrun install [选项]             显式安装当前二进制
  proxrun uninstall [--purge]        卸载，--purge 同时删除配置和数据
  proxrun shell [bash|zsh|fish]       输出指定 shell 的快捷命令
  proxrun version                    查看版本

安装选项:
  --subscription <URL>  订阅地址，也可设置 PROXRUN_SUBSCRIPTION
  --no-download         复用已有的 mihomo
  --no-shell            不修改 shell 配置
  --shell <name>        指定 bash、zsh 或 fish（默认根据 SHELL 检测）
  --port <N>            HTTP 代理端口，默认 7890

快捷命令: po / proxy-on、proxy-off、proxy-status
`)
}
func (a *App) status(r *Runtime) error {
	return r.locked(func() error {
		state, err := r.readState()
		if err != nil {
			return err
		}
		if state == nil {
			fmt.Fprintln(a.Out, "○ mihomo: 未运行")
		} else {
			fmt.Fprintf(a.Out, "● mihomo: 运行中 (pid %d)  HTTP 代理 http://127.0.0.1:%d\n", state.Kernel.PID, state.Port)
			ready := "不可用"
			if r.ready(state.Kernel) {
				ready = "可用"
			}
			fmt.Fprintln(a.Out, "  端口状态  :", ready)
			mode := "一次性，共享至最后一个命令完成"
			if state.Keep {
				mode = "常驻，直到 off"
			}
			fmt.Fprintln(a.Out, "  生命周期  :", mode)
		}
		fmt.Fprintf(a.Out, "  端口      : %d\n  数据目录  : %s\n  配置文件  : %s\n  规则文件  : %s\n", r.c.Port, r.c.DataDir, filepath.Join(r.c.DataDir, "config.runtime.yaml"), r.c.RulesFile)
		return nil
	})
}
func (a *App) check(r *Runtime) int {
	fmt.Fprintln(a.Out, "── proxrun 自检 ──")
	failed := false
	if data, err := exec.Command(r.c.MihomoBin, "-v").Output(); err != nil {
		fmt.Fprintln(a.Out, "  ✗ 内核不可执行:", r.c.MihomoBin)
		failed = true
	} else {
		fmt.Fprintln(a.Out, "  ✓ 内核:", strings.Split(strings.TrimSpace(string(data)), "\n")[0])
	}
	for _, item := range []struct {
		name, path string
		secret     bool
	}{{"订阅配置", filepath.Join(r.c.DataDir, "config.yaml"), true}, {"规则文件", r.c.RulesFile, false}} {
		info, err := os.Stat(item.path)
		if err != nil {
			fmt.Fprintln(a.Out, "  ✗ "+item.name+":", item.path)
			failed = true
			continue
		}
		if item.secret && info.Mode().Perm() != 0600 {
			fmt.Fprintf(a.Out, "  ✗ %s权限: %o，应为 600\n", item.name, info.Mode().Perm())
			failed = true
		} else {
			fmt.Fprintln(a.Out, "  ✓ "+item.name+":", item.path)
		}
		if data, err := os.ReadFile(item.path); err != nil {
			failed = true
		} else if _, err := parseYAML(data); err != nil {
			fmt.Fprintln(a.Out, "  ✗ "+item.name+": YAML 格式错误")
			failed = true
		}
	}
	if err := a.status(r); err != nil {
		fmt.Fprintln(a.Out, "  ✗ 运行状态:", err)
		failed = true
	}
	if failed {
		return 1
	}
	return 0
}
func (a *App) logs(c Config, args []string) error {
	n := 50
	if len(args) > 1 {
		return fmt.Errorf("用法: proxrun logs [N]")
	}
	if len(args) == 1 {
		value, err := strconv.Atoi(args[0])
		if err != nil || value < 1 {
			return fmt.Errorf("日志行数必须为正整数")
		}
		n = value
	}
	file, err := os.Open(filepath.Join(c.DataDir, "mihomo.log"))
	if os.IsNotExist(err) {
		fmt.Fprintln(a.Out, "（还没有日志）")
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	return tailLogs(file, a.Out, n)
}
