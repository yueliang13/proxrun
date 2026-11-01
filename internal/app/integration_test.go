package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

var testCLI, testExecutable, testRoot string
var testSubreaperEnabled bool

func TestMain(m *testing.M) {
	if mode := os.Getenv("PROXRUN_TEST_HELPER"); mode != "" {
		os.Exit(runHelper(mode))
	}
	var err error
	testExecutable, err = os.Executable()
	if err != nil {
		panic(err)
	}
	testRoot, err = os.MkdirTemp("", "proxrun-go-tests-")
	if err != nil {
		panic(err)
	}
	testCLI = filepath.Join(testRoot, "proxrun")
	root, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	buildArgs := []string{"build", "-o", testCLI}
	if testRace {
		buildArgs = append(buildArgs, "-race")
	}
	buildArgs = append(buildArgs, "./cmd/proxrun")
	build := exec.Command("go", buildArgs...)
	build.Dir = root
	cgo := "0"
	if testRace {
		cgo = "1"
	}
	build.Env = append(os.Environ(), "CGO_ENABLED="+cgo)
	if data, err := build.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(data), err)
		os.RemoveAll(testRoot)
		os.Exit(1)
	}
	testSubreaperEnabled = enableTestSubreaper()
	code := m.Run()
	os.RemoveAll(testRoot)
	os.Exit(code)
}
func runHelper(mode string) int {
	if mode == "kernel" {
		for _, arg := range os.Args[1:] {
			if arg == "-v" {
				fmt.Println("Mihomo fake test")
				return 0
			}
		}
		path := ""
		for i, arg := range os.Args[1:] {
			if arg == "-f" && i+2 < len(os.Args) {
				path = os.Args[i+2]
			}
		}
		if os.Getenv("PROXRUN_TEST_EXIT") != "" {
			return 3
		}
		if os.Getenv("PROXRUN_TEST_NO_LISTEN") != "" {
			time.Sleep(time.Minute)
			return 0
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var config struct {
			Port int `yaml:"port"`
		}
		if err := yaml.Unmarshal(data, &config); err != nil {
			return 1
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(config.Port))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return 0
			}
			conn.Close()
		}
	}
	if mode == "command" {
		args := os.Args[1:]
		if len(args) == 0 {
			return 1
		}
		switch args[0] {
		case "inspect":
			cwd, _ := os.Getwd()
			data, _ := os.ReadFile(filepath.Join(os.Getenv("PROXRUN_DATA_DIR"), "mihomo.pid"))
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			json.NewEncoder(os.Stdout).Encode(map[string]any{"cwd": cwd, "proxy": os.Getenv("https_proxy"), "pid": pid})
			fmt.Fprintln(os.Stderr, "target stderr")
			return 7
		case "block":
			if err := os.WriteFile(args[1], []byte("ready"), 0600); err != nil {
				return 1
			}
			for {
				if _, err := os.Stat(args[2]); err == nil {
					return 0
				}
				time.Sleep(15 * time.Millisecond)
			}
		}
	}
	return 1
}

type result struct {
	out, err string
	code     int
}
type fixture struct {
	t                                  *testing.T
	root, home, bin, data, config, cli string
	port                               int
	env                                []string
	children                           []*exec.Cmd
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root, home: filepath.Join(root, "home"), bin: filepath.Join(root, "bin"), data: filepath.Join(root, "data"), config: filepath.Join(root, "config")}
	os.MkdirAll(f.home, 0700)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		lower := strings.ToLower(key)
		if strings.HasPrefix(key, "SP_") || strings.HasPrefix(key, "PROXRUN_") || strings.HasPrefix(key, "SIMPLE_PROXY_") || key == "HOME" || key == "ZDOTDIR" || key == "XDG_CONFIG_HOME" || key == "XDG_DATA_HOME" || lower == "http_proxy" || lower == "https_proxy" || lower == "all_proxy" || lower == "no_proxy" {
			continue
		}
		f.env = append(f.env, item)
	}
	f.set("GORACE", "atexit_sleep_ms=0")
	f.set("HOME", f.home)
	f.set("ZDOTDIR", f.home)
	f.set("PROXRUN_BIN_DIR", f.bin)
	f.set("PROXRUN_DATA_DIR", f.data)
	f.set("PROXRUN_CONFIG_DIR", f.config)
	f.set("PROXRUN_START_TIMEOUT", "1")
	f.port = freePort(t)
	f.set("PROXRUN_HTTP_PORT", strconv.Itoa(f.port))
	f.require(f.run(testCLI, "install", "--no-download", "--no-shell"), 0)
	f.cli = filepath.Join(f.bin, "proxrun")
	f.write(filepath.Join(f.data, "config.yaml"), []byte(subscriptionFixture), 0600)
	f.write(filepath.Join(f.bin, "mihomo"), []byte("#!/bin/sh\nPROXRUN_TEST_HELPER=kernel exec "+shellQuote(testExecutable)+" \"$@\"\n"), 0755)
	t.Cleanup(func() {
		if _, err := os.Stat(f.cli); err == nil {
			f.call("off")
		}
		for _, cmd := range f.children {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		}
	})
	return f
}
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}
func (f *fixture) set(key, value string) {
	for i, item := range f.env {
		if strings.HasPrefix(item, key+"=") {
			f.env[i] = key + "=" + value
			return
		}
	}
	f.env = append(f.env, key+"="+value)
}
func (f *fixture) unset(key string) {
	for i, item := range f.env {
		if strings.HasPrefix(item, key+"=") {
			f.env = append(f.env[:i], f.env[i+1:]...)
			return
		}
	}
}
func (f *fixture) write(path string, data []byte, mode os.FileMode) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) run(binary string, args ...string) result {
	f.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = f.env
	var out, err bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &err
	if e := cmd.Start(); e != nil {
		f.t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	code := 0
	select {
	case e := <-done:
		if e != nil {
			if exit, ok := e.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				f.t.Fatal(e)
			}
		}
	case <-time.After(12 * time.Second):
		cmd.Process.Kill()
		<-done
		f.t.Fatalf("command timeout: %s %v\n%s\n%s", binary, args, out.String(), err.String())
	}
	return result{out.String(), err.String(), code}
}
func (f *fixture) call(args ...string) result { return f.run(f.cli, args...) }
func (f *fixture) require(r result, code int) {
	f.t.Helper()
	if r.code != code {
		f.t.Fatalf("exit %d, expected %d; stdout=%s; stderr=%s", r.code, code, r.out, r.err)
	}
}
func (f *fixture) quiet(r result) {
	f.t.Helper()
	f.require(r, 0)
	if r.err != "" {
		f.t.Fatal(r.err)
	}
}
func (f *fixture) state() *State {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.data, "mihomo.state.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		f.t.Fatal(err)
	}
	return &s
}
func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("file timeout:", path)
}
func (f *fixture) block(name string) (*exec.Cmd, string) {
	f.t.Helper()
	ready := filepath.Join(f.root, name+".ready")
	finish := filepath.Join(f.root, name+".finish")
	cmd := exec.Command(f.cli, "on", testExecutable, "block", ready, finish)
	cmd.Env = append(append([]string{}, f.env...), "PROXRUN_TEST_HELPER=command")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	f.children = append(f.children, cmd)
	waitFile(f.t, ready)
	return cmd, finish
}
func finishCommand(t *testing.T, cmd *exec.Cmd, code int) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		actual := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				actual = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if actual != code {
			t.Fatalf("exit %d, want %d", actual, code)
		}
	case <-time.After(6 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatal("command did not exit")
	}
}
func TestOneShotIsolationAndReaping(t *testing.T) {
	f := newFixture(t)
	f.set("PROXRUN_TEST_HELPER", "command")
	r := f.call("on", testExecutable, "inspect")
	f.require(r, 7)
	if r.err != "target stderr\n" {
		t.Fatal(r.err)
	}
	var data struct {
		Cwd, Proxy string
		PID        int
	}
	if err := json.Unmarshal([]byte(r.out), &data); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if data.Cwd != cwd || data.Proxy != "http://127.0.0.1:"+strconv.Itoa(f.port) {
		t.Fatal(data)
	}
	if _, err := processIdentity(data.PID); err == nil {
		t.Fatal("kernel still running")
	}
	for _, name := range []string{"mihomo.pid", "mihomo.state.json"} {
		if _, err := os.Stat(filepath.Join(f.data, name)); !os.IsNotExist(err) {
			t.Fatal("state retained:", name)
		}
	}
	f.quiet(f.call("off"))
}
func TestOverlappingCommands(t *testing.T) {
	f := newFixture(t)
	first, finish1 := f.block("first")
	state := f.state()
	second, finish2 := f.block("second")
	if f.state().Kernel != state.Kernel {
		t.Fatal("multiple kernels")
	}
	f.write(finish1, nil, 0600)
	time.Sleep(100 * time.Millisecond)
	if !alive(state.Kernel) {
		t.Fatal("other command lost its proxy")
	}
	f.write(finish2, nil, 0600)
	finishCommand(t, second, 0)
	finishCommand(t, first, 0)
	if alive(state.Kernel) {
		t.Fatal("kernel retained")
	}
}
func TestSignalsAndSharedUsers(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			f := newFixture(t)
			cmd, _ := f.block("signal")
			kernel := f.state().Kernel
			cmd.Process.Signal(sig)
			finishCommand(t, cmd, 128+int(sig))
			if alive(kernel) {
				t.Fatal("kernel retained")
			}
		})
	}
	t.Run("shared", func(t *testing.T) {
		f := newFixture(t)
		first, _ := f.block("interrupted")
		second, finish := f.block("survivor")
		id := f.state().Kernel
		first.Process.Signal(syscall.SIGTERM)
		time.Sleep(150 * time.Millisecond)
		if !alive(id) {
			t.Fatal("interrupted one user stopped another")
		}
		f.write(finish, nil, 0600)
		finishCommand(t, second, 0)
		finishCommand(t, first, 143)
	})
}
func TestFailedStartupAndOccupiedPort(t *testing.T) {
	for _, setting := range []string{"PROXRUN_TEST_NO_LISTEN", "PROXRUN_TEST_EXIT"} {
		t.Run(setting, func(t *testing.T) {
			f := newFixture(t)
			f.set(setting, "1")
			r := f.call("on", "true")
			if r.code == 0 {
				t.Fatal("startup falsely succeeded")
			}
			if _, err := os.Stat(filepath.Join(f.data, "mihomo.state.json")); !os.IsNotExist(err) {
				t.Fatal("state retained")
			}
		})
	}
	t.Run("occupied", func(t *testing.T) {
		f := newFixture(t)
		listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(f.port))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		r := f.call("on", "printf", "MUST_NOT_RUN")
		if r.code == 0 || r.out != "" {
			t.Fatal(r)
		}
	})
}
func TestStalePIDAndMissingCommand(t *testing.T) {
	f := newFixture(t)
	self, _ := processIdentity(os.Getpid())
	f.write(filepath.Join(f.data, "mihomo.pid"), []byte(strconv.Itoa(self.PID)), 0600)
	f.quiet(f.call("off"))
	if !alive(self) {
		t.Fatal("unrelated process killed")
	}
	fake := State{Kernel: Identity{PID: self.PID, Birth: "wrong"}}
	data, _ := json.Marshal(fake)
	f.write(filepath.Join(f.data, "mihomo.state.json"), data, 0600)
	f.quiet(f.call("off"))
	r := f.call("on", "/nonexistent/proxrun-test-command")
	f.require(r, 127)
	if _, err := os.Stat(filepath.Join(f.data, "mihomo.pid")); !os.IsNotExist(err) {
		t.Fatal("kernel state retained")
	}
}
func TestShellKeepAndPo(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			script := ". " + shellQuote(filepath.Join(f.data, "shell.sh")) + "; po --start true; po true; proxy-status; proxy-off; proxy-status"
			r := f.run(shell, "-c", script)
			f.quiet(r)
			if !strings.Contains(r.out, "常驻") || !strings.Contains(r.out, "未运行") {
				t.Fatal(r.out)
			}
		})
	}
}
func TestStandaloneVersionAndNoPython(t *testing.T) {
	f := newFixture(t)
	f.quiet(f.call("version"))
	f.set("PATH", "/nonexistent")
	// The fake kernel uses /bin/sh and an absolute Go helper, so no Python,
	// curl or user PATH lookup is available anywhere on this execution path.
	f.quiet(f.call("on", "/bin/true"))
	for _, name := range []string{"core.sh", "runtime.py", "build-config.py", "clean-logs.sh", "vendor"} {
		if _, err := os.Stat(filepath.Join(f.data, name)); !os.IsNotExist(err) {
			t.Fatal("external implementation asset installed:", name)
		}
	}
}
func TestInstallAtomicReplacement(t *testing.T) {
	f := newFixture(t)
	running, finish := f.block("old-binary")
	kernel := f.state().Kernel
	before, _ := os.Stat(f.cli)
	f.require(f.run(testCLI, "install", "--no-download", "--no-shell"), 0)
	after, _ := os.Stat(f.cli)
	if os.SameFile(before, after) {
		t.Fatal("binary was not atomically replaced")
	}
	if !alive(kernel) {
		t.Fatal("install interrupted active kernel")
	}
	f.quiet(f.call("on", "true"))
	f.write(finish, nil, 0600)
	finishCommand(t, running, 0)
	if alive(kernel) {
		t.Fatal("kernel retained")
	}
}
func TestDefaultPathsAndOldLayoutMigration(t *testing.T) {
	f := newFixture(t)
	oldData := filepath.Join(f.home, ".local/share/simple-proxy")
	oldConfig := filepath.Join(f.home, ".config/simple-proxy")
	os.MkdirAll(filepath.Dir(oldData), 0700)
	os.MkdirAll(filepath.Dir(oldConfig), 0700)
	if err := os.Rename(f.data, oldData); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.config, oldConfig); err != nil {
		t.Fatal(err)
	}
	f.set("PROXRUN_DATA_DIR", filepath.Join(f.home, ".local/share/proxrun"))
	f.set("PROXRUN_CONFIG_DIR", filepath.Join(f.home, ".config/proxrun"))
	f.require(f.call("install", "--no-download", "--no-shell"), 0)
	newData := filepath.Join(f.home, ".local/share/proxrun")
	data, err := os.ReadFile(filepath.Join(newData, "config.yaml"))
	if err != nil || string(data) != subscriptionFixture {
		t.Fatal(err)
	}
	if info, err := os.Lstat(oldData); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("old path compatibility missing")
	}
	f.quiet(f.call("on", "true"))
}
func TestSignalDuringStartup(t *testing.T) {
	f := newFixture(t)
	f.set("PROXRUN_TEST_NO_LISTEN", "1")
	cmd := exec.Command(f.cli, "on", "/bin/true")
	cmd.Env = f.env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f.children = append(f.children, cmd)
	waitFile(t, filepath.Join(f.data, "config.runtime.yaml"))
	time.Sleep(100 * time.Millisecond)
	cmd.Process.Signal(syscall.SIGTERM)
	finishCommand(t, cmd, 143)
	if _, err := os.Stat(filepath.Join(f.data, "mihomo.pid")); !os.IsNotExist(err) {
		t.Fatal("state retained")
	}
}
func TestReadPythonStateFormat(t *testing.T) {
	id, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	boot := ""
	if runtime.GOOS == "linux" {
		boot = `,"boot":` + strconv.Quote(id.Boot)
	}
	f := newFixture(t)
	raw := fmt.Sprintf(`{"kernel":{"pid":%d,"birth":%q%s},"port":%d,"keep":true,"session":"legacy","users":{}}`, id.PID, id.Birth, boot, f.port)
	t.Cleanup(func() { os.Remove(filepath.Join(f.data, "mihomo.state.json")) })
	f.write(filepath.Join(f.data, "mihomo.state.json"), []byte(raw), 0600)
	c := Config{DataDir: f.data}
	state, err := newRuntime(New(nil, nil, nil), c).readState()
	if err != nil || state == nil || state.Session != "legacy" {
		t.Fatal(state, err)
	}
	// Do not let fixture cleanup interpret this intentionally self-referencing state.
	os.Remove(filepath.Join(f.data, "mihomo.state.json"))
}

func TestStandaloneKeepSurvivesOneShot(t *testing.T) {
	if !testSubreaperEnabled {
		t.Skip("requires Linux subreaper to avoid orphaning test processes")
	}
	f := newFixture(t)
	f.quiet(f.call("on", "--start", "/bin/true"))
	id := f.state().Kernel
	t.Cleanup(func() { f.call("off"); var status syscall.WaitStatus; _, _ = syscall.Wait4(id.PID, &status, 0, nil) })
	f.quiet(f.call("on", "/bin/true"))
	if f.state().Kernel != id || !alive(id) {
		t.Fatal("one-shot stopped persistent kernel")
	}
	f.quiet(f.call("off"))
	if alive(id) {
		t.Fatal("off did not stop kernel")
	}
}
func TestActiveLegacyLayoutAndPurge(t *testing.T) {
	f := newFixture(t)
	oldData := filepath.Join(f.home, ".local/share/simple-proxy")
	oldConfig := filepath.Join(f.home, ".config/simple-proxy")
	os.MkdirAll(filepath.Dir(oldData), 0700)
	os.MkdirAll(filepath.Dir(oldConfig), 0700)
	if err := os.Rename(f.data, oldData); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.config, oldConfig); err != nil {
		t.Fatal(err)
	}
	f.data = oldData
	f.config = oldConfig
	f.set("PROXRUN_DATA_DIR", oldData)
	f.set("PROXRUN_CONFIG_DIR", oldConfig)
	owner, finish := f.block("legacy-active")
	id := f.state().Kernel
	f.data = filepath.Join(f.home, ".local/share/proxrun")
	f.config = filepath.Join(f.home, ".config/proxrun")
	f.set("PROXRUN_DATA_DIR", f.data)
	f.set("PROXRUN_CONFIG_DIR", f.config)
	f.require(f.run(testCLI, "install", "--no-download", "--no-shell"), 0)
	if !alive(id) || f.state().Kernel != id {
		t.Fatal("migration restarted the proxy")
	}
	oldLock, _ := os.Stat(filepath.Join(oldData, "runtime.lock"))
	newLock, _ := os.Stat(filepath.Join(f.data, "runtime.lock"))
	if !os.SameFile(oldLock, newLock) {
		t.Fatal("migration split shared state")
	}
	f.quiet(f.call("on", "/bin/true"))
	f.write(finish, nil, 0600)
	finishCommand(t, owner, 0)
	f.require(f.call("uninstall", "--purge"), 0)
	for _, path := range []string{f.data, f.config, oldData, oldConfig, f.cli} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("purge retained %s: %v", path, err)
		}
	}
}
func TestInstallPreservesShellPrivacyAndReplacesOldBlock(t *testing.T) {
	f := newFixture(t)
	f.set("SHELL", "/bin/zsh")
	path := filepath.Join(f.home, ".zshrc")
	f.write(path, []byte("# before\n# ─── simple-proxy ───\nif [ -r old ]; then\n . old\nfi\n# after\n"), 0600)
	f.require(f.call("install", "--no-download"), 0)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "simple-proxy") || strings.Count(string(data), "# ─── proxrun ───") != 1 || !strings.Contains(string(data), "# before") || !strings.Contains(string(data), "# after") {
		t.Fatal(string(data))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private shell configuration made public")
	}
	f.require(f.call("uninstall"), 0)
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("uninstall changed shell privacy")
	}
}
