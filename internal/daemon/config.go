package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"frp-tui/internal/model"
	"github.com/pelletier/go-toml/v2"
)

func validateConnection(c model.Connection) error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("连接名称不能为空")
	}
	if strings.TrimSpace(c.ServerAddr) == "" {
		return errors.New("服务端地址不能为空")
	}
	if !port(c.ServerPort) {
		return errors.New("服务端端口必须为 1–65535")
	}
	names := make(map[string]bool)
	for _, p := range c.Proxies {
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("代理名称不能为空")
		}
		if names[p.Name] {
			return fmt.Errorf("代理名称重复：%s", p.Name)
		}
		names[p.Name] = true
		if p.Type != "tcp" && p.Type != "udp" {
			return errors.New("代理类型必须为 tcp 或 udp")
		}
		if strings.TrimSpace(p.LocalIP) == "" {
			return errors.New("本地地址不能为空")
		}
		if !port(p.LocalPort) || !port(p.RemotePort) {
			return errors.New("代理端口必须为 1–65535")
		}
	}
	return nil
}
func port(n int) bool { return n > 0 && n <= 65535 }
func validBinary(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("frpc 路径必须是绝对路径")
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("读取 frpc：%w", err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return errors.New("frpc 必须是可执行文件")
	}
	return nil
}
func frpcConfig(c model.Connection) ([]byte, error) {
	proxies := make([]map[string]any, 0, len(c.Proxies))
	for _, p := range c.Proxies {
		proxies = append(proxies, map[string]any{"name": p.Name, "type": p.Type, "localIP": p.LocalIP, "localPort": p.LocalPort, "remotePort": p.RemotePort})
	}
	doc := map[string]any{
		"serverAddr": c.ServerAddr, "serverPort": c.ServerPort, "loginFailExit": true,
		"transport": map[string]any{"tls": map[string]any{"enable": c.TLS}},
		"log":       map[string]any{"to": "console", "level": "info", "disablePrintColor": true},
		"proxies":   proxies,
	}
	if c.Token != "" {
		doc["auth"] = map[string]any{"method": "token", "token": c.Token}
	}
	return toml.Marshal(doc)
}
func verify(ctx context.Context, dataDir, binary string, c model.Connection) error {
	if err := validateConnection(c); err != nil {
		return err
	}
	if err := validBinary(binary); err != nil {
		return err
	}
	data, err := frpcConfig(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Join(dataDir, "configs"), ".verify-*.toml")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "verify", "-c", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("frpc 配置校验失败：%s (%v)", redact(strings.TrimSpace(string(output)), c.Token), err)
	}
	return nil
}
func redact(s, token string) string {
	if token != "" {
		return strings.ReplaceAll(s, token, "[已隐藏]")
	}
	return s
}
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func loadConfig(dir string) (model.Config, error) {
	cfg := model.Config{Revision: 1, FrpcPath: filepath.Join(dir, "frpc"), Connections: []model.Connection{}}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if os.IsNotExist(err) {
		return cfg, saveConfig(dir, cfg)
	}
	if err != nil {
		return cfg, err
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("读取 config.json：%w", err)
	}
	if !filepath.IsAbs(cfg.FrpcPath) {
		return cfg, errors.New("config.json 中 frpc 路径必须为绝对路径")
	}
	ids := map[string]bool{}
	for _, c := range cfg.Connections {
		if c.ID == "" || strings.ContainsAny(c.ID, "/\\.") || ids[c.ID] {
			return cfg, errors.New("config.json 中连接 ID 无效或重复")
		}
		ids[c.ID] = true
		if err := validateConnection(c); err != nil {
			return cfg, fmt.Errorf("连接 %s：%w", c.Name, err)
		}
	}
	return cfg, nil
}
func saveConfig(dir string, cfg model.Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "config.json"), append(data, '\n'))
}
func cloneConfig(cfg model.Config) model.Config {
	copy := cfg
	copy.Connections = append([]model.Connection{}, cfg.Connections...)
	for i := range copy.Connections {
		copy.Connections[i].Proxies = append([]model.Proxy{}, copy.Connections[i].Proxies...)
	}
	return copy
}
