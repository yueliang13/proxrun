//go:build linux

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (Identity, error) {
	if pid <= 1 {
		return Identity{}, fmt.Errorf("无效 PID")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return Identity{}, err
	}
	index := strings.LastIndexByte(string(data), ')')
	if index < 0 {
		return Identity{}, fmt.Errorf("无效进程状态")
	}
	fields := strings.Fields(string(data[index+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return Identity{}, fmt.Errorf("进程已退出")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, Birth: fields[19], Boot: strings.TrimSpace(string(boot))}, nil
}

func signalIdentity(id Identity, sig syscall.Signal) error {
	if !alive(id) {
		return nil
	}
	fd, err := unix.PidfdOpen(id.PID, 0)
	if err == nil {
		defer unix.Close(fd)
		if !alive(id) {
			return nil
		}
		err = unix.PidfdSendSignal(fd, unix.Signal(sig), nil, 0)
		if err == unix.ESRCH {
			return nil
		}
		return err
	}
	if err == unix.ESRCH {
		return nil
	}
	if err != unix.ENOSYS && err != unix.EINVAL {
		return err
	}
	if !alive(id) {
		return nil
	}
	err = syscall.Kill(id.PID, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func ownsListener(id Identity, port int) bool {
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", id.PID))
	if err != nil {
		return false
	}
	inodes := map[string]bool{}
	for _, entry := range fds {
		target, err := os.Readlink(filepath.Join(fmt.Sprintf("/proc/%d/fd", id.PID), entry.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}
	for _, table := range []string{"tcp", "tcp6"} {
		data, _ := os.ReadFile("/proc/net/" + table)
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] {
				continue
			}
			_, hex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			p, err := strconv.ParseInt(hex, 16, 32)
			if err == nil && int(p) == port {
				return true
			}
		}
	}
	return false
}
