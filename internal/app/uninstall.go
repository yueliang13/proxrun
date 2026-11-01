package app

import (
	"fmt"
	"os"
	"path/filepath"
)

func (a *App) uninstall(c Config, args []string) error {
	purge := len(args) == 1 && args[0] == "--purge"
	if len(args) > 0 && !purge {
		return fmt.Errorf("用法: proxrun uninstall [--purge]")
	}
	if err := newRuntime(a, c).off(); err != nil {
		return err
	}
	zdir := os.Getenv("ZDOTDIR")
	if zdir == "" {
		zdir = c.Home
	}
	for _, rc := range unique([]string{filepath.Join(c.Home, ".bashrc"), filepath.Join(zdir, ".zshrc")}) {
		data, err := os.ReadFile(rc)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		clean := shellBlock.ReplaceAll(data, []byte("\n"))
		if string(data) != string(clean) {
			if err := atomicWrite(rc, clean, existingMode(rc, 0644)); err != nil {
				return err
			}
		}
	}
	if err := removeFishRC(c); err != nil {
		return err
	}
	previous := filepath.Join(c.BinDir, "simple-proxy")
	if target, err := os.Readlink(previous); err == nil && target == "proxrun" {
		if err := os.Remove(previous); err != nil {
			return err
		}
	}
	for _, file := range []string{filepath.Join(c.BinDir, "proxrun"), c.MihomoBin} {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if purge {
		for _, path := range []string{c.DataDir, c.ConfigDir} {
			target, err := filepath.EvalSymlinks(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			home, _ := filepath.EvalSymlinks(c.Home)
			if target == string(os.PathSeparator) || target == home {
				return fmt.Errorf("拒绝删除主目录或根目录")
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
			if filepath.Base(path) == "proxrun" {
				old := filepath.Join(filepath.Dir(path), "simple-proxy")
				if link, err := os.Readlink(old); err == nil && (link == path || link == target) {
					_ = os.Remove(old)
				}
			}
		}
	}
	fmt.Fprintln(a.Out, "✅ 已卸载")
	return nil
}
