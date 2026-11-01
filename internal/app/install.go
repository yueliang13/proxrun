package app

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/yueliang13/proxrun"
	"golang.org/x/sys/unix"
)

func kernelAsset(osName, arch, version string) (string, error) {
	if osName != "linux" && osName != "darwin" {
		return "", fmt.Errorf("仅支持 Linux/macOS")
	}
	switch arch {
	case "amd64":
		arch = "amd64-compatible"
	case "arm64":
	default:
		return "", fmt.Errorf("仅支持 amd64/arm64")
	}
	return fmt.Sprintf("mihomo-%s-%s-v%s.gz", osName, arch, version), nil
}
func (a *App) downloadKernel(c Config) error {
	asset, err := kernelAsset(runtime.GOOS, runtime.GOARCH, c.MihomoVersion)
	if err != nil {
		return err
	}
	raw := "https://github.com/MetaCubeX/mihomo/releases/download/v" + c.MihomoVersion + "/" + asset
	fmt.Fprintln(a.Err, "→ 下载 mihomo v"+c.MihomoVersion)
	reader, err := a.openURL(raw)
	if err != nil {
		return err
	}
	defer reader.Close()
	zipped, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("mihomo 压缩文件无效")
	}
	defer zipped.Close()
	if err := os.MkdirAll(filepath.Dir(c.MihomoBin), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(c.MihomoBin), ".mihomo-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := io.Copy(file, io.LimitReader(zipped, 512<<20)); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(0755); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := exec.Command(file.Name(), "-v").Output(); err != nil {
		return fmt.Errorf("下载的 mihomo 无法运行")
	}
	return os.Rename(file.Name(), c.MihomoBin)
}
func (a *App) install(c Config, args []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(a.Err)
	sub := flags.String("subscription", subscriptionEnv(), "Clash/mihomo 订阅 URL")
	noDownload := flags.Bool("no-download", false, "复用 mihomo")
	noShell := flags.Bool("no-shell", false, "不写入 shell 配置")
	port := flags.Int("port", 0, "HTTP 端口")
	shell := flags.String("shell", detectShell(), "bash、zsh 或 fish")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !validShell(*shell) {
		return fmt.Errorf("不支持的 shell: %s（支持 bash、zsh、fish）", *shell)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("install 不接受位置参数")
	}
	if *port < 0 || *port > 65535 {
		return fmt.Errorf("HTTP 端口必须在 1–65535 之间")
	}
	for _, arg := range args {
		if (arg == "--port" || strings.HasPrefix(arg, "--port=")) && *port == 0 {
			return fmt.Errorf("HTTP 端口必须在 1–65535 之间")
		}
	}
	if err := os.MkdirAll(c.BinDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(c.BinDir, ".proxrun-layout.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if err := migrateLayout(c); err != nil {
		return err
	}
	if err := ensureTemplate(c.ConfFile, proxrun.Asset("config/proxy.conf")); err != nil {
		return err
	}
	if err := ensureTemplate(c.RulesFile, proxrun.Asset("config/rules.yaml")); err != nil {
		return err
	}
	if *port != 0 {
		data, err := os.ReadFile(c.ConfFile)
		if err != nil {
			return err
		}
		pattern := regexp.MustCompile(`(?m)^(?:SP|PROXRUN)_HTTP_PORT=.*$`)
		setting := "PROXRUN_HTTP_PORT=" + strconv.Itoa(*port)
		if pattern.Match(data) {
			data = pattern.ReplaceAllLiteral(data, []byte(setting))
		} else {
			data = append(data, []byte("\n"+setting+"\n")...)
		}
		if err := atomicWrite(c.ConfFile, data, existingMode(c.ConfFile, 0644)); err != nil {
			return err
		}
		c.Port = *port
	}
	if !*noDownload {
		if err := a.downloadKernel(c); err != nil {
			return err
		}
	}
	if *sub != "" {
		if err := a.update(c, *sub); err != nil {
			return err
		}
	}
	for _, name := range []string{"shell.sh", "shell.fish"} {
		if err := atomicWrite(filepath.Join(c.DataDir, name), proxrun.Asset("lib/"+name), 0644); err != nil {
			return err
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := copyAtomic(self, filepath.Join(c.BinDir, "proxrun"), 0755); err != nil {
		return err
	}
	if err := compatCommand(c); err != nil {
		return err
	}
	if !*noShell {
		if err := writeShellRC(c, *shell); err != nil {
			return err
		}
	}
	fmt.Fprintln(a.Out, "✅ 安装完成。运行 proxrun check 自检。")
	return nil
}
