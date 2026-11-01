package app

import (
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestInteractiveShellKeepsOutputQuiet(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			f.set("TERM", "xterm")
			f.set("PS1", "proxrun-test> ")
			args := []string{"-f", "-i"}
			if shell == "bash" {
				args = []string{"--noprofile", "--norc", "-i"}
			}
			command := exec.Command(shell, args...)
			command.Env = f.env
			terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: 100})
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			output := make(chan []byte, 1)
			go func() { data, _ := io.ReadAll(terminal); output <- data }()
			script := ". " + shellQuote(filepath.Join(f.data, "shell.sh")) + "; po --start true; proxy-off; jobs -p; exit\n"
			if _, err := io.WriteString(terminal, script); err != nil {
				command.Process.Kill()
				command.Wait()
				t.Fatal(err)
			}
			select {
			case data := <-output:
				if err := command.Wait(); err != nil {
					t.Fatal(string(data), err)
				}
				text := string(data)
				if regexp.MustCompile(`\[\d+\]\s+\d+`).MatchString(text) || strings.Contains(text, "proxrun:") {
					t.Fatal("unexpected shell noise:", text)
				}
			case <-time.After(8 * time.Second):
				command.Process.Kill()
				command.Wait()
				terminal.Close()
				t.Fatal("interactive shell timeout:", string(<-output))
			}
		})
	}
}
