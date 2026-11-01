package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

var privateRules = []string{
	"DOMAIN-SUFFIX,localhost,DIRECT", "DOMAIN-SUFFIX,local,DIRECT", "DOMAIN-SUFFIX,svc,DIRECT", "DOMAIN-SUFFIX,cluster.local,DIRECT",
	"IP-CIDR,127.0.0.0/8,DIRECT", "IP-CIDR,10.0.0.0/8,DIRECT", "IP-CIDR,172.16.0.0/12,DIRECT", "IP-CIDR,192.168.0.0/16,DIRECT", "IP-CIDR,169.254.0.0/16,DIRECT",
	"IP-CIDR6,::1/128,DIRECT", "IP-CIDR6,fc00::/7,DIRECT", "IP-CIDR6,fe80::/10,DIRECT",
}

func parseYAML(data []byte) (map[string]any, error) {
	var result map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("YAML 格式错误: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("配置必须是 YAML 对象")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("配置必须只包含一个 YAML 文档")
	}
	return result, nil
}

func mergeConfig(subscription, policy []byte, c Config, resolver string) ([]byte, error) {
	sub, err := parseYAML(subscription)
	if err != nil {
		return nil, err
	}
	user, err := parseYAML(policy)
	if err != nil {
		return nil, err
	}
	rawRules, ok := user["rules"].([]any)
	if !ok || len(rawRules) == 0 {
		return nil, fmt.Errorf("用户 rules 必须是非空字符串列表")
	}
	rules := append([]string{}, privateRules...)
	for _, raw := range rawRules {
		value, ok := raw.(string)
		if !ok || value == "" {
			return nil, fmt.Errorf("用户 rules 必须是非空字符串列表")
		}
		rules = append(rules, value)
	}
	result := map[string]any{}
	for _, key := range []string{"proxies", "proxy-providers", "proxy-groups", "rule-providers"} {
		if value, ok := sub[key]; ok {
			result[key] = value
		}
	}
	nodes := []any{}
	providers := map[string]any{}
	groups := []any{}
	if value, exists := sub["proxies"]; exists {
		nodes, ok = value.([]any)
		if !ok {
			return nil, fmt.Errorf("proxies 必须为列表")
		}
	}
	if value, exists := sub["proxy-providers"]; exists {
		providers, ok = value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("proxy-providers 必须为对象")
		}
	}
	if value, exists := sub["proxy-groups"]; exists {
		groups, ok = value.([]any)
		if !ok {
			return nil, fmt.Errorf("proxy-groups 必须为列表")
		}
	}
	var names []string
	for _, raw := range nodes {
		if node, ok := raw.(map[string]any); ok {
			name, _ := node["name"].(string)
			kind, _ := node["type"].(string)
			switch strings.ToLower(kind) {
			case "direct", "reject", "reject-drop":
				continue
			}
			if name != "" {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 && len(providers) == 0 {
		return nil, fmt.Errorf("订阅中没有可用代理节点；需要 Clash/mihomo YAML 格式")
	}
	group := map[string]any{"name": "PROXY", "type": "select"}
	if len(names) > 0 {
		group["proxies"] = names
	}
	if len(providers) > 0 {
		keys := []string{}
		for name := range providers {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		group["use"] = keys
	}
	newGroups := []any{}
	for _, raw := range groups {
		if g, ok := raw.(map[string]any); ok && g["name"] != "PROXY" {
			newGroups = append(newGroups, g)
		}
	}
	result["proxy-groups"] = append(newGroups, group)
	result["port"] = c.Port
	result["allow-lan"] = false
	result["bind-address"] = "127.0.0.1"
	result["mode"] = "rule"
	result["log-level"] = "info"
	result["ipv6"] = false
	result["profile"] = map[string]any{"store-selected": false, "store-fake-ip": false}
	if c.EnableAPI {
		secret, _ := sub["secret"].(string)
		if strings.TrimSpace(secret) == "" {
			return nil, fmt.Errorf("启用控制 API 前必须设置非空 secret")
		}
		result["external-controller"] = "127.0.0.1:9090"
		result["secret"] = secret
	}
	if resolver == "" {
		resolver = "223.5.5.5"
	}
	result["dns"] = map[string]any{"enable": true, "ipv6": false, "enhanced-mode": "redir-host", "default-nameserver": []string{resolver}, "nameserver": unique(append([]string{resolver}, c.DNSFallbacks...))}
	result["rules"] = unique(rules)
	return yaml.Marshal(result)
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
func systemResolver() string {
	data, _ := os.ReadFile("/etc/resolv.conf")
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "nameserver" {
			return fields[1]
		}
	}
	return ""
}
func buildConfig(c Config) error {
	sub, err := readFile(filepath.Join(c.DataDir, "config.yaml"))
	if err != nil {
		return err
	}
	rules, err := readFile(c.RulesFile)
	if err != nil {
		return err
	}
	data, err := mergeConfig(sub, rules, c, systemResolver())
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(c.DataDir, "config.runtime.yaml"), data, 0600)
}
