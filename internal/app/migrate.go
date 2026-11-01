package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func legacyActive(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "mihomo.state.json"))
	if err != nil {
		return false
	}
	var state State
	return json.Unmarshal(data, &state) == nil && alive(state.Kernel)
}
func migrateDir(old, new string, preserveRunning bool) error {
	oldInfo, err := os.Lstat(old)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if oldInfo.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if _, err := os.Lstat(new); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(new), 0700); err != nil {
		return err
	}
	if preserveRunning && legacyActive(old) {
		return os.Symlink(old, new)
	}
	if err := os.Rename(old, new); err != nil {
		return err
	}
	if err := os.Symlink(new, old); err != nil {
		_ = os.Rename(new, old)
		return err
	}
	return nil
}
func migrateLayout(c Config) error {
	if c.DataDir == filepath.Join(c.Home, ".local/share/proxrun") {
		if err := migrateDir(filepath.Join(c.Home, ".local/share/simple-proxy"), c.DataDir, true); err != nil {
			return err
		}
	}
	if c.ConfigDir == filepath.Join(c.Home, ".config/proxrun") {
		if err := migrateDir(filepath.Join(c.Home, ".config/simple-proxy"), c.ConfigDir, false); err != nil {
			return err
		}
	}
	for _, name := range []string{"config.yaml", "config.runtime.yaml"} {
		path := filepath.Join(c.DataDir, name)
		if _, err := os.Stat(path); err == nil {
			if err := os.Chmod(path, 0600); err != nil {
				return err
			}
		}
	}
	return nil
}
func compatCommand(c Config) error {
	previous := filepath.Join(c.BinDir, "simple-proxy")
	if _, err := os.Lstat(previous); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	temporary := filepath.Join(c.BinDir, ".proxrun-compat-"+token())
	defer os.Remove(temporary)
	if err := os.Symlink("proxrun", temporary); err != nil {
		return err
	}
	return os.Rename(temporary, previous)
}
