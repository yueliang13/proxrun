package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Tempfiles start at 0600 before receiving any credential or executable data.
// Rename, rather than truncation, leaves existing running executables intact.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	return atomicStream(path, mode, func(w io.Writer) error { _, err := w.Write(data); return err })
}

func atomicStream(path string, mode os.FileMode, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".proxrun-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := write(file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func copyAtomic(source, target string, mode os.FileMode) error {
	if a, err := os.Stat(source); err == nil {
		if b, err := os.Stat(target); err == nil && os.SameFile(a, b) {
			return nil
		}
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	return atomicStream(target, mode, func(w io.Writer) error { _, err := io.Copy(w, file); return err })
}

func ensureTemplate(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWrite(path, data, 0644)
}

func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	return data, nil
}

func existingMode(path string, fallback os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return fallback
}
