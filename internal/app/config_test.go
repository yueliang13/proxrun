package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yueliang13/proxrun"
)

const subscriptionFixture = `rules: [MATCH,DIRECT]
port: 8080
mixed-port: 8081
socks-port: 8082
redir-port: 8083
mode: direct
allow-lan: true
bind-address: '*'
external-controller: 0.0.0.0:9090
external-controller-unix: /tmp/undesired.sock
external-controller-tls: 0.0.0.0:9443
tun: {enable: true}
listeners: [{name: extra, type: http, port: 8888}]
dns: {enable: true, listen: '0.0.0.0:53', nameserver: [8.8.8.8]}
profile: {store-selected: true}
proxies:
  - {name: TEST, type: socks5, server: 127.0.0.1, port: 9}
proxy-groups:
  - {name: PROXY, type: select, proxies: [DIRECT, TEST]}
`

func builderConfig() Config {
	return Config{Port: 34567, DNSFallbacks: []string{"https://doh.pub/dns-query"}}
}
func TestMergeOrderScopeAndPrivateRules(t *testing.T) {
	data, err := mergeConfig([]byte(subscriptionFixture), []byte("rules: ['MATCH,PROXY']\n"), builderConfig(), "127.0.0.53")
	if err != nil {
		t.Fatal(err)
	}
	out, err := parseYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	if out["mode"] != "rule" || out["port"] != 34567 || out["allow-lan"] != false || out["bind-address"] != "127.0.0.1" {
		t.Fatalf("incorrect scope: %s", data)
	}
	for _, key := range []string{"mixed-port", "socks-port", "redir-port", "listeners", "tun", "external-controller", "external-controller-unix", "external-controller-tls"} {
		if _, ok := out[key]; ok {
			t.Errorf("subscription scope leaked: %s", key)
		}
	}
	groups := out["proxy-groups"].([]any)
	group := groups[len(groups)-1].(map[string]any)
	if nodes := group["proxies"].([]any); len(nodes) != 1 || nodes[0] != "TEST" {
		t.Fatal(group)
	}
	if out["profile"].(map[string]any)["store-selected"] != false {
		t.Fatal("selected node cache enabled")
	}
	rules := out["rules"].([]any)
	if rules[len(rules)-1] != "MATCH,PROXY" {
		t.Fatal(rules)
	}
	for i, rule := range privateRules {
		if rules[i] != rule {
			t.Fatalf("private rule missing: %s", rule)
		}
	}
	if strings.Contains(string(data), "no-resolve") {
		t.Fatal("private domains must be resolved")
	}
	if _, ok := out["dns"].(map[string]any)["listen"]; ok {
		t.Fatal("unexpected DNS listener")
	}
}
func TestRejectInvalidYAMLAndPolicy(t *testing.T) {
	for _, data := range []string{"rules: []\nrules: []\n", "[]", "a: 1\n---\nb: 2\n"} {
		if _, err := parseYAML([]byte(data)); err == nil {
			t.Errorf("accepted invalid YAML %q", data)
		}
	}
	for _, policy := range []string{"rules: MATCH,PROXY", "rules: []", "rules: [true]"} {
		if _, err := mergeConfig([]byte(subscriptionFixture), []byte(policy), builderConfig(), ""); err == nil {
			t.Error("accepted", policy)
		}
	}
	if _, err := mergeConfig([]byte("proxies: []"), []byte("rules: ['MATCH,PROXY']"), builderConfig(), ""); err == nil {
		t.Fatal("empty nodes accepted")
	}
}
func TestAPIRequiresSecret(t *testing.T) {
	c := builderConfig()
	c.EnableAPI = true
	if _, err := mergeConfig([]byte(subscriptionFixture), []byte("rules: ['MATCH,PROXY']"), c, ""); err == nil {
		t.Fatal("API enabled without secret")
	}
	data, err := mergeConfig([]byte(subscriptionFixture+"secret: local-test\n"), []byte("rules: ['MATCH,PROXY']"), c, "")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := parseYAML(data)
	if out["external-controller"] != "127.0.0.1:9090" {
		t.Fatal(out)
	}
}
func TestAtomicCredentialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := atomicWrite(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	err := atomicStream(path, 0600, func(w io.Writer) error {
		file := w.(*os.File)
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal("credentials writable before chmod", info.Mode())
		}
		_, err = w.Write([]byte("secret"))
		if err != nil {
			return err
		}
		return fmt.Errorf("interrupted write")
	})
	if err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "old" {
		t.Fatal("old credentials overwritten")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".proxrun-*.tmp"))
	if len(files) != 0 {
		t.Fatal("temporary credentials retained")
	}
}
func TestConfigPrecedenceAndNoShellEvaluation(t *testing.T) {
	home := t.TempDir()
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "SP_") || strings.HasPrefix(key, "PROXRUN_") {
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".config/proxrun")
	os.MkdirAll(directory, 0700)
	config := []byte("SP_HTTP_PORT=12456\nPROXRUN_HTTP_PORT=23456\nPROXRUN_DNS_FALLBACKS=\"https://one.invalid/dns https://two.invalid/dns\"\nPROXRUN_NO_PROXY_EXTRA='$(touch bad)'\n")
	if err := os.WriteFile(filepath.Join(directory, "proxy.conf"), config, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SP_HTTP_PORT", "34567")
	t.Setenv("PROXRUN_HTTP_PORT", "45678")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 45678 || len(c.DNSFallbacks) != 2 || c.NoProxy != "$(touch bad)" {
		t.Fatalf("incorrect configuration: %+v", c)
	}
	if _, err := os.Stat("bad"); err == nil {
		t.Fatal("configuration executed shell code")
	}
}
func TestEmbeddedDefaultsAndLogRotation(t *testing.T) {
	if len(proxrun.Asset("config/rules.yaml")) == 0 || len(proxrun.Asset("lib/shell.sh")) == 0 {
		t.Fatal("embedded defaults missing")
	}
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	os.WriteFile(filepath.Join(dir, "mihomo.log"), []byte("current"), 0600)
	expired := filepath.Join(dir, "mihomo.log.2026-10-01")
	os.WriteFile(expired, []byte("expired"), 0600)
	if err := cleanLogs(Config{DataDir: dir, KeepDays: 3}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatal("expired archive retained")
	}
	if _, err := os.Stat(filepath.Join(dir, "mihomo.log.2026-11-01")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tailLogs(strings.NewReader("noise\n[TCP] first\nnoise\n[TCP] last\n"), &out, 1); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[TCP] last\n" {
		t.Fatal(out.String())
	}
}
func TestKernelAssets(t *testing.T) {
	for _, target := range [][3]string{{"linux", "amd64", "mihomo-linux-amd64-compatible-v1.19.33.gz"}, {"linux", "arm64", "mihomo-linux-arm64-v1.19.33.gz"}, {"darwin", "amd64", "mihomo-darwin-amd64-compatible-v1.19.33.gz"}, {"darwin", "arm64", "mihomo-darwin-arm64-v1.19.33.gz"}} {
		actual, err := kernelAsset(target[0], target[1], "1.19.33")
		if err != nil || actual != target[2] {
			t.Fatal(actual, err)
		}
	}
}
