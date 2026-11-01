//go:build darwin

package app

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (Identity, error) {
	if pid <= 1 {
		return Identity{}, fmt.Errorf("无效 PID")
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Identity{}, err
	}
	if info.Proc.P_pid != int32(pid) || info.Proc.P_stat == 5 {
		return Identity{}, fmt.Errorf("进程已退出")
	}
	// Match the legacy ps lstart format without launching a subprocess.
	birth := time.Unix(info.Proc.P_starttime.Sec, int64(info.Proc.P_starttime.Usec)*1000).In(time.Local).Format("Mon Jan _2 15:04:05 2006")
	return Identity{PID: pid, Birth: birth}, nil
}
func signalIdentity(id Identity, sig syscall.Signal) error {
	if !alive(id) {
		return nil
	}
	err := syscall.Kill(id.PID, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
func ownsListener(id Identity, port int) bool {
	if _, err := exec.LookPath("lsof"); err != nil {
		return true
	}
	data, err := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(id.PID), "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	return err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(id.PID)
}
