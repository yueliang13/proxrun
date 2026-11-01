package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func cleanLogs(c Config, now time.Time) error {
	log := filepath.Join(c.DataDir, "mihomo.log")
	archive := log + "." + now.Format("2006-01-02")
	if _, err := os.Stat(log); err == nil {
		if _, err := os.Stat(archive); os.IsNotExist(err) {
			if err := os.Rename(log, archive); err != nil {
				return err
			}
		}
	}
	files, err := filepath.Glob(log + ".*")
	if err != nil {
		return err
	}
	for _, file := range files {
		stamp := strings.TrimPrefix(file, log+".")
		date, err := time.ParseInLocation("2006-01-02", stamp, now.Location())
		if err != nil {
			continue
		}
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		if now.Sub(date) > time.Duration(c.KeepDays+1)*24*time.Hour || (info.Size() > 20<<20 && now.Sub(info.ModTime()) > 24*time.Hour) {
			if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	return nil
}
func tailLogs(reader io.Reader, writer io.Writer, n int) error {
	all, routes := make([]string, 0, n), make([]string, 0, n)
	appendLine := func(lines []string, line string) []string {
		if len(lines) == n {
			copy(lines, lines[1:])
			lines = lines[:n-1]
		}
		return append(lines, line)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		all = appendLine(all, line)
		if strings.Contains(line, "[TCP]") {
			routes = appendLine(routes, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(routes) > 0 {
		all = routes
	}
	for _, line := range all {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}
