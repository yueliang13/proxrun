package app

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Home, BinDir, DataDir, ConfigDir, ConfFile, RulesFile, MihomoBin string
	Port, KeepDays                                                   int
	MihomoVersion, NoProxy                                           string
	DNSFallbacks                                                     []string
	EnableAPI                                                        bool
	StartTimeout                                                     time.Duration
}

// The configuration is data, never sourced by a shell. Legacy SP_* assignments
// remain valid; PROXRUN_* is the preferred spelling for new configurations.
func LoadConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	values := map[string]string{}
	getEnv := func(name string) (string, bool) {
		if value, ok := os.LookupEnv("PROXRUN_" + name); ok {
			return value, true
		}
		return os.LookupEnv("SP_" + name)
	}
	initial := func(name, fallback string) string {
		if value, ok := getEnv(name); ok {
			return value
		}
		return fallback
	}
	configDir := initial("CONFIG_DIR", filepath.Join(home, ".config/proxrun"))
	confFile := initial("CONF_FILE", filepath.Join(configDir, "proxy.conf"))
	readPath := confFile
	if _, err := os.Stat(readPath); os.IsNotExist(err) && confFile == filepath.Join(home, ".config/proxrun/proxy.conf") {
		readPath = filepath.Join(home, ".config/simple-proxy/proxy.conf")
	}
	if file, err := os.Open(readPath); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for line := 1; scanner.Scan(); line++ {
			text := strings.TrimSpace(scanner.Text())
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}
			text = strings.TrimPrefix(text, "export ")
			key, raw, ok := strings.Cut(text, "=")
			key = strings.TrimSpace(key)
			if !ok || (!strings.HasPrefix(key, "SP_") && !strings.HasPrefix(key, "PROXRUN_")) {
				return Config{}, fmt.Errorf("配置第 %d 行只支持 KEY=value", line)
			}
			parts, err := splitWords(strings.TrimSpace(raw), true)
			if err != nil || len(parts) > 1 {
				return Config{}, fmt.Errorf("配置第 %d 行的值需要正确引用", line)
			}
			value := ""
			if len(parts) == 1 {
				value = parts[0]
			}
			values[key] = value
		}
		if err := scanner.Err(); err != nil {
			return Config{}, err
		}
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	get := func(name, fallback string) string {
		if value, ok := getEnv(name); ok {
			return value
		}
		if value, ok := values["PROXRUN_"+name]; ok {
			return value
		}
		if value, ok := values["SP_"+name]; ok {
			return value
		}
		return fallback
	}
	path := func(value string) string {
		if strings.HasPrefix(value, "~/") {
			value = filepath.Join(home, value[2:])
		}
		value = os.ExpandEnv(value)
		for _, pair := range [][2]string{{".local/share/simple-proxy", ".local/share/proxrun"}, {".config/simple-proxy", ".config/proxrun"}} {
			old, newer := filepath.Join(home, pair[0]), filepath.Join(home, pair[1])
			if value == old || strings.HasPrefix(value, old+string(os.PathSeparator)) {
				if _, err := os.Stat(newer); err == nil {
					value = newer + strings.TrimPrefix(value, old)
				}
			}
		}
		absolute, _ := filepath.Abs(value)
		return absolute
	}
	c := Config{Home: home, BinDir: path(get("BIN_DIR", filepath.Join(home, ".local/bin"))),
		DataDir:   path(get("DATA_DIR", filepath.Join(home, ".local/share/proxrun"))),
		ConfigDir: path(get("CONFIG_DIR", configDir))}
	c.ConfFile = path(get("CONF_FILE", filepath.Join(c.ConfigDir, "proxy.conf")))
	c.RulesFile = path(get("RULES_FILE", filepath.Join(c.ConfigDir, "rules.yaml")))
	c.MihomoBin = path(get("MIHOMO_BIN", filepath.Join(c.BinDir, "mihomo")))
	c.MihomoVersion = get("MIHOMO_VERSION", "1.19.33")
	c.NoProxy = get("NO_PROXY_EXTRA", "localhost,127.0.0.1,::1,.local,.svc,.svc.cluster.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16")
	c.Port, err = strconv.Atoi(get("HTTP_PORT", "7890"))
	if err != nil || c.Port < 1 || c.Port > 65535 {
		return c, fmt.Errorf("HTTP 端口必须在 1–65535 之间")
	}
	c.KeepDays, err = strconv.Atoi(get("LOG_KEEP_DAYS", "3"))
	if err != nil || c.KeepDays < 0 {
		return c, fmt.Errorf("日志保留天数必须是非负整数")
	}
	api := strings.ToLower(get("ENABLE_API", "0"))
	c.EnableAPI = api == "1" || api == "true" || api == "yes"
	c.DNSFallbacks, err = splitWords(get("DNS_FALLBACKS", "https://doh.pub/dns-query https://dns.alidns.com/dns-query"), false)
	if err != nil {
		return c, fmt.Errorf("DNS 备用解析器配置错误: %w", err)
	}
	seconds, err := strconv.ParseFloat(get("START_TIMEOUT", "30"), 64)
	if err != nil || seconds <= 0 || seconds > 3600 {
		return c, fmt.Errorf("启动超时必须在 0–3600 秒之间")
	}
	c.StartTimeout = time.Duration(seconds * float64(time.Second))
	return c, nil
}

func splitWords(text string, comments bool) ([]string, error) {
	var words []string
	var b strings.Builder
	var quote rune
	escaped, present := false, false
	flush := func() {
		if present {
			words = append(words, b.String())
			b.Reset()
			present = false
		}
	}
	for _, r := range text {
		if escaped {
			b.WriteRune(r)
			escaped = false
			present = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			present = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			present = true
			continue
		}
		if comments && r == '#' && !present {
			break
		}
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		b.WriteRune(r)
		present = true
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("引号或转义未闭合")
	}
	flush()
	return words, nil
}
