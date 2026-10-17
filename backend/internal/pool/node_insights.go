package pool

import (
	"encoding/json"
	"strconv"
	"strings"
)

// NodeInsights is static configuration metadata, not a live connectivity or
// client interoperability test. Only allowlisted labels and static text leave
// this inspector; credentials, addresses, headers and raw export errors do not.
type NodeInsights struct {
	V2RayCompatible bool          `json:"v2ray_compatible"`
	V2RayReason     string        `json:"v2ray_reason"`
	Cipher          string        `json:"cipher,omitempty"`
	Transport       string        `json:"transport,omitempty"`
	TLS             string        `json:"tls"`
	Warnings        []NodeWarning `json:"warnings"`
}

type NodeWarning struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// InspectNode delegates the fail-closed URI fidelity decision to the actual
// exporter, including its nested option checks. Do not expose its error text:
// unknown configuration keys may themselves contain secrets supplied by users.
func InspectNode(n Node) NodeInsights {
	out := NodeInsights{
		V2RayReason: "URI导出不支持该配置或无法保真表示全部参数",
		TLS:         "unknown",
		Warnings:    []NodeWarning{},
	}
	var config map[string]any
	if json.Unmarshal(n.Config, &config) != nil || config == nil {
		out.V2RayReason = "URI导出不支持缺失或无法解析的节点配置"
		return out
	}
	if _, err := shareURI(config); err == nil {
		out.V2RayCompatible = true
		out.V2RayReason = "可保真导出分享URI；客户端兼容性尚未实际测试"
	}

	typ, _ := config["type"].(string)
	out.TLS = insightsTLS(typ, config)
	out.Transport = "tcp"
	if value, exists := config["network"]; exists {
		switch value {
		case "", "tcp":
		case "ws", "grpc", "h2", "http", "quic", "kcp", "httpupgrade", "xhttp":
			out.Transport = value.(string)
		default:
			out.Transport = "unknown"
		}
	}

	cipher := insightsString(config["cipher"])
	security := insightsString(config["security"])
	if cipher == "" && typ == "vmess" {
		cipher = security
		if cipher == "" {
			cipher = "auto"
		}
	}
	if typ == "ss" || typ == "vmess" {
		if insightsLegacyCipher(cipher) {
			out.Cipher = cipher
		} else {
			switch cipher {
			case "auto", "none", "plain", "zero", "aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
				"chacha20-poly1305", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
				"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
				out.Cipher = cipher
			case "":
			default:
				out.Cipher = "unknown"
			}
		}
	}

	warn := func(code, severity, message string) {
		out.Warnings = append(out.Warnings, NodeWarning{Code: code, Severity: severity, Message: message})
	}
	if typ == "ss" && insightsLegacyCipher(cipher) {
		warn("legacy_stream_cipher", "high", "Shadowsocks使用已弃用且缺少完整性保护的流加密算法")
	}
	unencrypted := false
	switch typ {
	case "ss":
		unencrypted = (cipher == "none" || cipher == "plain") && out.TLS != "enabled"
	case "http", "socks", "socks5", "socks4":
		unencrypted = out.TLS == "disabled"
	case "vless":
		unencrypted = out.TLS == "disabled"
	case "vmess":
		unencrypted = out.TLS == "disabled" && (cipher == "none" || cipher == "zero" || security == "none" || security == "zero")
	}
	if unencrypted {
		warn("unencrypted_transport", "high", "节点配置未启用传输加密，流量可能被读取或篡改")
	}
	insecure, _ := config["skip-cert-verify"].(bool)
	if typ == "ss" && config["plugin"] == "v2ray-plugin" {
		if opts, ok := config["plugin-opts"].(map[string]any); ok && opts["skip-cert-verify"] == true {
			insecure = true
		}
	}
	if insecure {
		warn("certificate_verification_disabled", "warning", "节点配置关闭了TLS证书验证，存在中间人攻击风险")
	}
	if typ == "vmess" {
		positiveAlterID := false
		switch value := config["alterId"].(type) {
		case float64:
			positiveAlterID = value > 0
		case string:
			n, err := strconv.ParseInt(value, 10, 64)
			positiveAlterID = err == nil && n > 0
		}
		if positiveAlterID {
			warn("deprecated_vmess_alter_id", "warning", "VMess使用已弃用的非零alterId配置")
		}
	}
	return out
}

func insightsString(v any) string {
	s, _ := v.(string)
	return strings.ToLower(strings.TrimSpace(s))
}

// TLS describes TLS only, not the protocol's overall encryption. SS AEAD,
// SS2022, WireGuard and SSH remain encrypted when TLS is not applicable.
// TLS-native protocols enable it by default; contradictory flags are unknown.
func insightsTLS(typ string, config map[string]any) string {
	switch typ {
	case "ss":
		if config["plugin"] == "v2ray-plugin" {
			if opts, ok := config["plugin-opts"].(map[string]any); ok {
				if tls, ok := opts["tls"].(bool); ok && tls {
					return "enabled"
				}
			}
		}
		return "not_applicable"
	case "wireguard", "ssh":
		return "not_applicable"
	case "trojan", "hysteria", "hysteria2", "hy2", "tuic", "anytls", "https":
		if tls, exists := config["tls"]; exists && tls != true {
			return "unknown"
		}
		return "enabled"
	case "vless", "vmess", "http", "socks", "socks5", "socks4":
		if typ == "vless" {
			if opts, ok := config["reality-opts"].(map[string]any); ok {
				if key, ok := opts["public-key"].(string); ok && strings.TrimSpace(key) != "" {
					if tls, exists := config["tls"]; exists && tls != true {
						return "unknown"
					}
					return "enabled"
				}
			}
		}
		if tls, exists := config["tls"]; exists {
			if enabled, ok := tls.(bool); ok {
				if enabled {
					return "enabled"
				}
				return "disabled"
			}
			return "unknown"
		}
		return "disabled"
	default:
		return "unknown"
	}
}

func insightsLegacyCipher(cipher string) bool {
	switch cipher {
	case "aes-128-cfb", "aes-192-cfb", "aes-256-cfb",
		"aes-128-cfb1", "aes-192-cfb1", "aes-256-cfb1",
		"aes-128-cfb8", "aes-192-cfb8", "aes-256-cfb8",
		"aes-128-cfb128", "aes-192-cfb128", "aes-256-cfb128",
		"aes-128-ctr", "aes-192-ctr", "aes-256-ctr",
		"aes-128-ofb", "aes-192-ofb", "aes-256-ofb",
		"camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb",
		"rc4", "rc4-md5", "rc4-md5-6", "salsa20", "xsalsa20", "chacha20", "chacha20-ietf",
		"bf-cfb", "cast5-cfb", "des-cfb", "idea-cfb", "seed-cfb", "table":
		return true
	default:
		return false
	}
}
