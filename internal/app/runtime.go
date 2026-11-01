package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type State struct {
	Kernel  Identity            `json:"kernel"`
	Port    int                 `json:"port"`
	Keep    bool                `json:"keep"`
	Session string              `json:"session"`
	Users   map[string]Identity `json:"users"`
}

type Runtime struct {
	a              *App
	c              Config
	lease, session string
	kernel         *child
	targetMu       sync.Mutex
	target         *child
	interrupted    atomic.Int32
}

func newRuntime(a *App, c Config) *Runtime { return &Runtime{a: a, c: c, lease: token()} }
func token() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
func (r *Runtime) statePath() string { return filepath.Join(r.c.DataDir, "mihomo.state.json") }
func (r *Runtime) locked(fn func() error) error {
	if err := os.MkdirAll(r.c.DataDir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(r.c.DataDir, "runtime.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return fn()
}
func (r *Runtime) readState() (*State, error) {
	data, err := os.ReadFile(r.statePath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("运行状态损坏: %w", err)
	}
	if !alive(state.Kernel) {
		return nil, nil
	}
	if state.Users == nil {
		state.Users = map[string]Identity{}
	}
	return &state, nil
}
func (r *Runtime) writeState(state *State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := atomicWrite(r.statePath(), data, 0600); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(r.c.DataDir, "mihomo.pid"), []byte(strconv.Itoa(state.Kernel.PID)+"\n"), 0600)
}
func (r *Runtime) clear() error {
	for _, name := range []string{r.statePath(), filepath.Join(r.c.DataDir, "mihomo.pid")} {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func pruneUsers(state *State) {
	for key, id := range state.Users {
		if !alive(id) {
			delete(state.Users, key)
		}
	}
}
func (r *Runtime) ready(id Identity) bool {
	if !alive(id) || !ownsListener(id, r.c.Port) {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.c.Port)), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return alive(id)
}
func (r *Runtime) stopKernel(state *State) error {
	if err := signalIdentity(state.Kernel, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for alive(state.Kernel) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(state.Kernel) {
		if err := signalIdentity(state.Kernel, syscall.SIGKILL); err != nil {
			return err
		}
	}
	deadline = time.Now().Add(2 * time.Second)
	for alive(state.Kernel) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(state.Kernel) {
		return fmt.Errorf("停止 mihomo 失败，已保留运行状态")
	}
	return r.clear()
}
func stopOwned(p *child) {
	if p == nil {
		return
	}
	p.signal(syscall.SIGTERM)
	select {
	case <-p.done:
		return
	case <-time.After(2 * time.Second):
		p.signal(syscall.SIGKILL)
		<-p.done
	}
}
func (r *Runtime) prepare() (id Identity, err error) {
	info, err := os.Stat(r.c.MihomoBin)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return id, fmt.Errorf("找不到可执行的 mihomo，请先运行 proxrun install")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.c.Port)))
	if err != nil {
		return id, fmt.Errorf("端口 %d 已被其他进程占用", r.c.Port)
	}
	listener.Close()
	if err = buildConfig(r.c); err != nil {
		return id, err
	}
	if err = cleanLogs(r.c, time.Now()); err != nil {
		return id, err
	}
	log, err := os.OpenFile(filepath.Join(r.c.DataDir, "mihomo.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return id, err
	}
	defer log.Close()
	command := exec.Command(r.c.MihomoBin, "-d", r.c.DataDir, "-f", filepath.Join(r.c.DataDir, "config.runtime.yaml"))
	command.Dir = r.c.DataDir
	command.Stdout = log
	command.Stderr = log
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	r.kernel, err = startChild(command)
	if err != nil {
		return id, err
	}
	defer func() {
		if err != nil {
			stopOwned(r.kernel)
		}
	}()
	id, err = processIdentity(r.kernel.cmd.Process.Pid)
	if err != nil {
		return id, fmt.Errorf("mihomo 启动失败，请查看日志")
	}
	deadline := time.Now().Add(r.c.StartTimeout)
	for !r.kernel.exited() && time.Now().Before(deadline) {
		if r.interrupted.Load() != 0 {
			return id, fmt.Errorf("启动已中断")
		}
		if r.ready(id) {
			return id, nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return id, fmt.Errorf("mihomo 未能监听端口 %d，请查看 %s", r.c.Port, filepath.Join(r.c.DataDir, "mihomo.log"))
}
func (r *Runtime) acquire(keep bool) error {
	return r.locked(func() error {
		state, err := r.readState()
		if err != nil {
			return err
		}
		if state != nil {
			if state.Port != r.c.Port {
				return fmt.Errorf("当前隧道使用其他端口，请先执行 proxrun off")
			}
			if !r.ready(state.Kernel) {
				return fmt.Errorf("代理进程存在但端口不可用，请先执行 proxrun off")
			}
			pruneUsers(state)
			if !state.Keep && len(state.Users) == 0 {
				if err := r.stopKernel(state); err != nil {
					return err
				}
				state = nil
			}
		}
		if state == nil {
			if err := r.clear(); err != nil {
				return err
			}
			id, err := r.prepare()
			if err != nil {
				return err
			}
			state = &State{Kernel: id, Port: r.c.Port, Session: token(), Users: map[string]Identity{}}
		}
		state.Keep = state.Keep || keep
		self, err := processIdentity(os.Getpid())
		if err != nil {
			return err
		}
		state.Users[r.lease] = self
		r.session = state.Session
		return r.writeState(state)
	})
}
func (r *Runtime) release() error {
	return r.locked(func() error {
		state, err := r.readState()
		if err != nil {
			return err
		}
		if state == nil {
			return r.clear()
		}
		if state.Session != r.session {
			return nil
		}
		delete(state.Users, r.lease)
		pruneUsers(state)
		if !state.Keep && len(state.Users) == 0 {
			return r.stopKernel(state)
		}
		return r.writeState(state)
	})
}
func (r *Runtime) off() error {
	return r.locked(func() error {
		state, err := r.readState()
		if err != nil {
			return err
		}
		if state == nil {
			return r.clear()
		}
		return r.stopKernel(state)
	})
}
func (r *Runtime) watchSignals(hold bool) func() {
	signals := make(chan os.Signal, 8)
	done := make(chan struct{})
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-signals:
				if hold && sig == syscall.SIGHUP {
					continue
				}
				number := sig.(syscall.Signal)
				r.interrupted.Store(int32(number))
				r.targetMu.Lock()
				target := r.target
				r.targetMu.Unlock()
				if target != nil {
					target.signal(sig)
				}
			}
		}
	}()
	return func() { signal.Stop(signals); close(done) }
}
func commandEnv(c Config) []string {
	values := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	url := "http://127.0.0.1:" + strconv.Itoa(c.Port)
	for _, key := range []string{"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"} {
		values[key] = url
	}
	noProxy := values["no_proxy"]
	if noProxy == "" {
		noProxy = values["NO_PROXY"]
	}
	if noProxy != "" && c.NoProxy != "" {
		noProxy += ","
	}
	noProxy += c.NoProxy
	values["no_proxy"] = noProxy
	values["NO_PROXY"] = noProxy
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
func (r *Runtime) waitTarget(target *child) int {
	var interruptedAt time.Time
	for {
		select {
		case <-target.done:
			if sig := r.interrupted.Load(); sig != 0 {
				return 128 + int(sig)
			}
			return target.code()
		case <-time.After(30 * time.Millisecond):
			if sig := r.interrupted.Load(); sig != 0 {
				if interruptedAt.IsZero() {
					interruptedAt = time.Now()
					target.signal(syscall.Signal(sig))
				} else if time.Since(interruptedAt) > 2*time.Second {
					target.signal(syscall.SIGKILL)
				}
			}
		}
	}
}
func (r *Runtime) reapOwner() error {
	if r.kernel == nil {
		return nil
	}
	for !r.kernel.exited() {
		err := r.locked(func() error {
			state, err := r.readState()
			if err != nil {
				return err
			}
			if state == nil || state.Session != r.session {
				stopOwned(r.kernel)
				return nil
			}
			pruneUsers(state)
			if !state.Keep && len(state.Users) == 0 {
				return r.stopKernel(state)
			}
			return nil
		})
		if err != nil {
			stopOwned(r.kernel)
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	<-r.kernel.done
	return nil
}
func (r *Runtime) on(args []string, hold bool) (code int, err error) {
	keep := hold
	if !hold && len(args) > 0 && args[0] == "--start" {
		keep = true
		args = args[1:]
	}
	if (!hold && len(args) == 0) || (hold && len(args) != 1) {
		return 1, fmt.Errorf("用法: proxrun on [--start] <cmd> [args...]")
	}
	stopSignals := r.watchSignals(hold)
	defer stopSignals()
	acquired := false
	defer func() {
		if acquired {
			if cleanup := r.release(); cleanup != nil {
				err = errors.Join(err, cleanup)
				stopOwned(r.kernel)
			}
		} else {
			stopOwned(r.kernel)
		}
		if r.kernel != nil && (!keep || hold) {
			if cleanup := r.reapOwner(); cleanup != nil {
				err = errors.Join(err, cleanup)
			}
		}
		if sig := r.interrupted.Load(); sig != 0 {
			code = 128 + int(sig)
			if !acquired {
				err = nil
			}
		}
	}()
	if err = r.acquire(keep); err != nil {
		return 1, err
	}
	acquired = true
	if hold {
		if err = atomicWrite(filepath.Join(args[0], "ready"), nil, 0600); err != nil {
			_ = r.off()
			return 1, err
		}
		for {
			if r.kernel != nil && r.kernel.exited() {
				return 0, nil
			}
			var state *State
			if err = r.locked(func() error { var e error; state, e = r.readState(); return e }); err != nil {
				return 1, err
			}
			if state == nil || state.Session != r.session {
				return 0, nil
			}
			if r.interrupted.Load() != 0 {
				return 0, r.off()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if sig := r.interrupted.Load(); sig != 0 {
		return 128 + int(sig), nil
	}
	command := exec.Command(args[0], args[1:]...)
	command.Env = commandEnv(r.c)
	command.Stdin = r.a.In
	command.Stdout = r.a.Out
	command.Stderr = r.a.Err
	target, startErr := startChild(command)
	if startErr != nil {
		if errors.Is(startErr, os.ErrNotExist) || errors.Is(startErr, exec.ErrNotFound) {
			return 127, startErr
		}
		return 126, startErr
	}
	r.targetMu.Lock()
	r.target = target
	r.targetMu.Unlock()
	return r.waitTarget(target), nil
}
