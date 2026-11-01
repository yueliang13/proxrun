package app

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func subscriptionEnv() string {
	if value := os.Getenv("PROXRUN_SUBSCRIPTION"); value != "" {
		return value
	}
	return os.Getenv("SIMPLE_PROXY_SUBSCRIPTION")
}
func (a *App) openURL(raw string) (io.ReadCloser, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("地址格式错误")
	}
	if u.Scheme == "file" {
		if u.Host != "" && u.Host != "localhost" {
			return nil, fmt.Errorf("不支持远程 file 地址")
		}
		return os.Open(u.Path)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("地址必须以 https://、http:// 或 file:// 开头")
	}
	response, err := a.HTTP.Get(raw)
	if err != nil {
		return nil, fmt.Errorf("下载失败，请检查地址和网络")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("下载失败: HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}
func (a *App) update(c Config, raw string) error {
	if raw == "" {
		return fmt.Errorf("用法: proxrun update <订阅URL>")
	}
	reader, err := a.openURL(raw)
	if err != nil {
		return err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 32<<20+1))
	if err != nil {
		return fmt.Errorf("读取订阅失败")
	}
	if len(data) > 32<<20 || len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("订阅为空或超过 32 MiB")
	}
	// Validate before replacing the last usable subscription. Never print credentials.
	if _, err := mergeConfig(data, []byte("rules: ['MATCH,PROXY']\n"), c, systemResolver()); err != nil {
		return fmt.Errorf("订阅不是有效的 Clash/mihomo YAML 配置")
	}
	if err := atomicWrite(filepath.Join(c.DataDir, "config.yaml"), data, 0600); err != nil {
		return err
	}
	fmt.Fprintln(a.Err, "✅ 订阅已更新")
	return nil
}
