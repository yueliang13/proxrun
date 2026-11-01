package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yueliang13/proxrun"
)

func TestShellOutputSelection(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/fish")
	for _, tc := range []struct {
		args  []string
		asset string
	}{
		{nil, "lib/shell.fish"},
		{[]string{"fish"}, "lib/shell.fish"},
		{[]string{"bash"}, "lib/shell.sh"},
		{[]string{"zsh"}, "lib/shell.sh"},
	} {
		var out, err bytes.Buffer
		a := New(nil, &out, &err)
		if code := a.shell(tc.args); code != 0 {
			t.Fatal(code, err.String())
		}
		if !bytes.Equal(out.Bytes(), proxrun.Asset(tc.asset)) {
			t.Fatal("incorrect shell script", tc.args)
		}
	}
	var out, err bytes.Buffer
	a := New(nil, &out, &err)
	if a.shell([]string{"unknown"}) == 0 || a.shell([]string{"fish", "extra"}) == 0 {
		t.Fatal("invalid shell arguments accepted")
	}
}

func TestFishInstallRespectsXDGAndExistingConfiguration(t *testing.T) {
	f := newFixture(t)
	f.set("SHELL", "/usr/local/bin/fish")
	xdg := filepath.Join(f.root, "xdg config")
	f.set("XDG_CONFIG_HOME", xdg)
	userConfig := filepath.Join(xdg, "fish/config.fish")
	original := "set -g personal_setting keep-this\n"
	f.write(userConfig, []byte(original), 0600)
	f.require(f.call("install", "--no-download"), 0)
	for _, name := range []string{"shell.sh", "shell.fish"} {
		data, err := os.ReadFile(filepath.Join(f.data, name))
		if err != nil || !bytes.Equal(data, proxrun.Asset("lib/"+name)) {
			t.Fatal(name, err)
		}
	}
	entry := filepath.Join(xdg, "fish/conf.d/proxrun.fish")
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), fishMarker) || !strings.Contains(string(data), "source "+fishQuote(filepath.Join(f.data, "shell.fish"))) {
		t.Fatal(string(data))
	}
	for _, name := range []string{".bashrc", ".zshrc"} {
		if _, err := os.Stat(filepath.Join(f.home, name)); !os.IsNotExist(err) {
			t.Fatal("Fish install modified another shell", name)
		}
	}
	os.Chmod(entry, 0600)
	f.require(f.call("install", "--no-download", "--shell", "fish"), 0)
	info, _ := os.Stat(entry)
	if info.Mode().Perm() != 0600 {
		t.Fatal("Fish loader permission changed")
	}
	actual, _ := os.ReadFile(userConfig)
	if string(actual) != original {
		t.Fatal("user config.fish changed")
	}
	f.require(f.call("uninstall"), 0)
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatal("generated Fish integration not removed")
	}
	actual, _ = os.ReadFile(userConfig)
	if string(actual) != original {
		t.Fatal("uninstall changed user config.fish")
	}
}

func TestFishNoShellAndExplicitShellOverride(t *testing.T) {
	f := newFixture(t)
	f.set("SHELL", "/bin/fish")
	entry := filepath.Join(f.home, ".config/fish/conf.d/proxrun.fish")
	f.require(f.call("install", "--no-download", "--no-shell"), 0)
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatal("--no-shell wrote a Fish loader")
	}
	f.require(f.call("install", "--no-download", "--shell", "bash"), 0)
	if _, err := os.Stat(filepath.Join(f.home, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatal("explicit Bash selection wrote Fish configuration")
	}
	f.require(f.call("install", "--no-download", "--shell", "fish"), 0)
	if _, err := os.Stat(entry); err != nil {
		t.Fatal(err)
	}
	if f.call("install", "--no-download", "--shell", "invalid").code == 0 {
		t.Fatal("unknown shell accepted")
	}
}

func TestFishLoaderQuotesPathsAndProtectsUnownedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	c := Config{Home: home, BinDir: filepath.Join(home, "bin with 'quote' and \\slash"), DataDir: filepath.Join(home, "data $literal"), ConfigDir: filepath.Join(home, "config")}
	if err := writeShellRC(c, "fish"); err != nil {
		t.Fatal(err)
	}
	path := fishRC(c)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "bin with \\'quote\\' and \\\\slash") {
		t.Fatal("Fish path not escaped", string(data))
	}
	if !strings.Contains(string(data), "'"+c.DataDir+"'") {
		t.Fatal("dollar sign path lost quoting")
	}
	original := []byte("# User-managed proxrun Fish configuration\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeShellRC(c, "fish"); err == nil {
		t.Fatal("overwrote an unrelated user file")
	}
	if err := removeFishRC(c); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("uninstall removed an unrelated file")
	}
}
