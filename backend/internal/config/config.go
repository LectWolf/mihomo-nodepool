package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config contains runtime configuration. Passwords must never be logged.
type Config struct {
	HTTPAddr       string
	DataDir        string
	MySQL          MySQL
	DataKey        string
	TrustedProxies []*net.IPNet
}

type MySQL struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	TLS      bool
}

func (m MySQL) Configured() bool {
	return m.Host != "" && m.User != "" && m.Database != ""
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr: envOr("HTTP_ADDR", ":8080"),
		DataDir:  envOr("DATA_DIR", "./data"),
		MySQL: MySQL{
			Host:     strings.TrimSpace(os.Getenv("MYSQL_HOST")),
			User:     strings.TrimSpace(os.Getenv("MYSQL_USER")),
			Password: os.Getenv("MYSQL_PASSWORD"),
			Database: strings.TrimSpace(os.Getenv("MYSQL_DATABASE")),
		},
	}
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR 必须是 host:port 格式")
	}
	if _, err := parsePort(port); err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR 端口无效")
	}
	cfg.MySQL.Port, err = parsePort(envOr("MYSQL_PORT", "3306"))
	if err != nil {
		return Config{}, fmt.Errorf("MYSQL_PORT 必须在 1 到 65535 之间")
	}
	switch strings.ToLower(envOr("MYSQL_TLS", "false")) {
	case "true":
		cfg.MySQL.TLS = true
	case "false":
	default:
		return Config{}, fmt.Errorf("MYSQL_TLS 必须为 true 或 false")
	}
	cfg.DataKey = strings.TrimSpace(os.Getenv("NODEPOOL_DATA_KEY"))
	for _, item := range strings.Split(os.Getenv("NODEPOOL_TRUSTED_PROXIES"), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if ip := net.ParseIP(item); ip != nil && ip.To4() != nil {
				item += "/32"
			} else {
				item += "/128"
			}
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return Config{}, fmt.Errorf("NODEPOOL_TRUSTED_PROXIES 须为逗号分隔的 IP 或 CIDR")
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, network)
	}
	partial := cfg.MySQL.Host != "" || cfg.MySQL.User != "" || cfg.MySQL.Database != "" || cfg.MySQL.Password != ""
	if partial && !cfg.MySQL.Configured() {
		return Config{}, fmt.Errorf("MYSQL_HOST、MYSQL_USER、MYSQL_DATABASE 必须一起配置")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port")
	}
	return port, nil
}
