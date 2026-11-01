package app

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type Identity struct {
	PID   int    `json:"pid"`
	Birth string `json:"birth"`
	Boot  string `json:"boot,omitempty"`
}

func alive(id Identity) bool {
	current, err := processIdentity(id.PID)
	return err == nil && id.PID > 1 && current == id
}

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func startChild(cmd *exec.Cmd) (*child, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &child{cmd: cmd, done: make(chan struct{})}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}
func (p *child) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
func (p *child) signal(sig os.Signal) {
	if p != nil && !p.exited() {
		_ = p.cmd.Process.Signal(sig)
	}
}
func (p *child) code() int {
	<-p.done
	if p.err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(p.err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exit.ExitCode()
	}
	return 1
}
