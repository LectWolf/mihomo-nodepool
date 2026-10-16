package pool

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNodeInsightsSecurity(t *testing.T) {
	for _, tc := range []struct {
		name, typ, extra, tls, cipher string
		warnings                      map[string]string
	}{
		{"ss AEAD", "ss", `"cipher":"aes-128-gcm"`, "not_applicable", "aes-128-gcm", nil},
		{"ss2022", "ss", `"cipher":"2022-blake3-aes-256-gcm"`, "not_applicable", "2022-blake3-aes-256-gcm", nil},
		{"ss chacha AEAD", "ss", `"cipher":"chacha20-ietf-poly1305"`, "not_applicable", "chacha20-ietf-poly1305", nil},
		{"ss none", "ss", `"cipher":"none"`, "not_applicable", "none", map[string]string{"unencrypted_transport": "high"}},
		{"ss plain", "ss", `"cipher":"plain"`, "not_applicable", "plain", map[string]string{"unencrypted_transport": "high"}},
		{"ss plugin TLS", "ss", `"cipher":"none","plugin":"v2ray-plugin","plugin-opts":{"tls":true}`, "enabled", "none", nil},
		{"ss plugin insecure", "ss", `"cipher":"aes-128-gcm","plugin":"v2ray-plugin","plugin-opts":{"tls":true,"skip-cert-verify":true}`, "enabled", "aes-128-gcm", map[string]string{"certificate_verification_disabled": "warning"}},
		{"http", "http", `"username":"private"`, "disabled", "", map[string]string{"unencrypted_transport": "high"}},
		{"http TLS", "http", `"tls":true`, "enabled", "", nil},
		{"http false TLS", "http", `"tls":false`, "disabled", "", map[string]string{"unencrypted_transport": "high"}},
		{"socks", "socks", ``, "disabled", "", map[string]string{"unencrypted_transport": "high"}},
		{"socks5", "socks5", ``, "disabled", "", map[string]string{"unencrypted_transport": "high"}},
		{"socks TLS", "socks5", `"tls":true`, "enabled", "", nil},
		{"vless", "vless", ``, "disabled", "", map[string]string{"unencrypted_transport": "high"}},
		{"vless TLS", "vless", `"tls":true`, "enabled", "", nil},
		{"vless reality", "vless", `"tls":true,"reality-opts":{"public-key":"private-value","short-id":"private-id"}`, "enabled", "", nil},
		{"vless reality without flag", "vless", `"reality-opts":{"public-key":"private-value"}`, "enabled", "", nil},
		{"vless invalid reality", "vless", `"reality-opts":{}`, "disabled", "", map[string]string{"unencrypted_transport": "high"}},
		{"vless conflicting reality", "vless", `"tls":false,"reality-opts":{"public-key":"private-value"}`, "unknown", "", nil},
		{"vmess default cipher", "vmess", ``, "disabled", "auto", nil},
		{"vmess AEAD", "vmess", `"cipher":"aes-128-gcm"`, "disabled", "aes-128-gcm", nil},
		{"vmess none", "vmess", `"cipher":"none"`, "disabled", "none", map[string]string{"unencrypted_transport": "high"}},
		{"vmess zero", "vmess", `"cipher":"zero"`, "disabled", "zero", map[string]string{"unencrypted_transport": "high"}},
		{"vmess security none", "vmess", `"security":"none"`, "disabled", "none", map[string]string{"unencrypted_transport": "high"}},
		{"vmess security zero", "vmess", `"security":"zero"`, "disabled", "zero", map[string]string{"unencrypted_transport": "high"}},
		{"vmess none TLS", "vmess", `"cipher":"none","tls":true`, "enabled", "none", nil},
		{"vmess zero TLS", "vmess", `"security":"zero","tls":true`, "enabled", "zero", nil},
		{"vmess deprecated alterId", "vmess", `"alterId":64`, "disabled", "auto", map[string]string{"deprecated_vmess_alter_id": "warning"}},
		{"vmess string alterId", "vmess", `"alterId":"1"`, "disabled", "auto", map[string]string{"deprecated_vmess_alter_id": "warning"}},
		{"vmess zero alterId", "vmess", `"alterId":0`, "disabled", "auto", nil},
		{"vmess combined", "vmess", `"cipher":"none","alterId":1,"skip-cert-verify":true`, "disabled", "none", map[string]string{"unencrypted_transport": "high", "deprecated_vmess_alter_id": "warning", "certificate_verification_disabled": "warning"}},
		{"trojan default", "trojan", ``, "enabled", "", nil},
		{"hy2 default", "hy2", ``, "enabled", "", nil},
		{"hysteria2 default", "hysteria2", ``, "enabled", "", nil},
		{"tuic default", "tuic", ``, "enabled", "", nil},
		{"anytls default", "anytls", ``, "enabled", "", nil},
		{"wireguard", "wireguard", ``, "not_applicable", "", nil},
		{"ssh", "ssh", ``, "not_applicable", "", nil},
		{"disabled cert verification", "trojan", `"skip-cert-verify":true`, "enabled", "", map[string]string{"certificate_verification_disabled": "warning"}},
		{"enabled cert verification", "trojan", `"skip-cert-verify":false`, "enabled", "", nil},
		{"invalid TLS flag", "vless", `"tls":"true"`, "unknown", "", nil},
		{"contradictory native TLS", "trojan", `"tls":false`, "unknown", "", nil},
		{"unknown protocol", "private-protocol", ``, "unknown", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"type":"` + tc.typ + `","server":"private.example","port":443,"password":"private-password","uuid":"private-uuid"`
			if tc.extra != "" {
				raw += "," + tc.extra
			}
			raw += "}"
			got := InspectNode(Node{Config: json.RawMessage(raw)})
			if got.TLS != tc.tls || got.Cipher != tc.cipher || got.Transport != "tcp" {
				t.Fatalf("metadata: %+v; want TLS=%s cipher=%s", got, tc.tls, tc.cipher)
			}
			if len(got.Warnings) != len(tc.warnings) {
				t.Fatalf("warnings: %+v; want %v", got.Warnings, tc.warnings)
			}
			seen := map[string]bool{}
			for _, warning := range got.Warnings {
				if warning.Severity != tc.warnings[warning.Code] || warning.Message == "" || seen[warning.Code] {
					t.Fatalf("unexpected or duplicate warning: %+v", warning)
				}
				seen[warning.Code] = true
			}
		})
	}
}

func TestNodeInsightsLegacyStreamCiphers(t *testing.T) {
	for _, cipher := range []string{"aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "rc4-md5", "salsa20", "chacha20", "chacha20-ietf", "bf-cfb", "camellia-256-cfb"} {
		t.Run(cipher, func(t *testing.T) {
			got := InspectNode(Node{Config: json.RawMessage(`{"type":"ss","cipher":"` + cipher + `"}`)})
			if got.Cipher != cipher || len(got.Warnings) != 1 || got.Warnings[0].Code != "legacy_stream_cipher" || got.Warnings[0].Severity != "high" {
				t.Fatalf("legacy cipher risk missing: %+v", got)
			}
		})
	}
}

func TestNodeInsightsURIFidelity(t *testing.T) {
	for _, tc := range []struct {
		name, raw  string
		compatible bool
	}{
		{"SS", `{"type":"ss","cipher":"aes-128-gcm","password":"secret"}`, true},
		{"SS2022", `{"type":"ss","cipher":"2022-blake3-aes-128-gcm","password":"secret"}`, true},
		{"VMess", `{"type":"vmess","uuid":"secret","tls":true,"network":"ws","ws-opts":{"headers":{"Host":"private.example"},"path":"/private"}}`, true},
		{"VLESS reality", `{"type":"vless","uuid":"secret","tls":true,"network":"grpc","grpc-opts":{"grpc-service-name":"private"},"reality-opts":{"public-key":"secret","short-id":"secret"}}`, true},
		{"Trojan", `{"type":"trojan","password":"secret","skip-cert-verify":true}`, true},
		{"HY2", `{"type":"hysteria2","password":"secret","obfs":"salamander","obfs-password":"secret"}`, true},
		{"TUIC", `{"type":"tuic","uuid":"secret","password":"secret","alpn":["h3"]}`, true},
		{"HTTPS", `{"type":"http","tls":true}`, true},
		{"SOCKS", `{"type":"socks5"}`, true},
		{"unknown field", `{"type":"ss","cipher":"aes-128-gcm","password":"secret","unknown":"private-value"}`, false},
		{"unknown nested header", `{"type":"vless","uuid":"secret","network":"ws","ws-opts":{"headers":{"Authorization":"private-value"}}}`, false},
		{"unknown grpc option", `{"type":"vless","uuid":"secret","network":"grpc","grpc-opts":{"unknown":"private-value"}}`, false},
		{"unknown reality option", `{"type":"vless","uuid":"secret","tls":true,"reality-opts":{"public-key":"secret","unknown":"private-value"}}`, false},
		{"VMess insecure", `{"type":"vmess","uuid":"secret","tls":true,"skip-cert-verify":true}`, false},
		{"plugin options", `{"type":"ss","cipher":"aes-128-gcm","password":"secret","plugin":"v2ray-plugin","plugin-opts":{"mux":true}}`, false},
		{"unknown plugin", `{"type":"ss","cipher":"aes-128-gcm","password":"secret","plugin":"private-plugin"}`, false},
		{"invalid alpn", `{"type":"trojan","password":"secret","alpn":[1]}`, false},
		{"conflicting SNI", `{"type":"trojan","password":"secret","sni":"one.private","servername":"two.private"}`, false},
		{"SOCKS TLS", `{"type":"socks5","tls":true}`, false},
		{"Trojan TLS false", `{"type":"trojan","password":"secret","tls":false}`, false},
		{"TLS wrong type", `{"type":"vless","uuid":"secret","tls":"true"}`, false},
		{"unknown transport", `{"type":"vless","uuid":"secret","network":"private-transport"}`, false},
		{"UDP false", `{"type":"ss","cipher":"aes-128-gcm","password":"secret","udp":false}`, false},
		{"missing password", `{"type":"ss","cipher":"aes-128-gcm"}`, false},
		{"missing UUID", `{"type":"vmess"}`, false},
		{"unsupported protocol", `{"type":"wireguard"}`, false},
		{"invalid server", `{"type":"socks5","server":""}`, false},
		{"invalid port", `{"type":"socks5","port":65536}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := map[string]any{"name": "private-name", "server": "private.example", "port": 443}
			if err := json.Unmarshal([]byte(tc.raw), &config); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			node := Node{Name: "private-name", Config: raw}
			got := InspectNode(node)
			_, uriErr := shareURI(config)
			_, skipped, exportErr := ExportSubscription([]Node{node}, "v2ray")
			if got.V2RayCompatible != tc.compatible || got.V2RayCompatible != (uriErr == nil) || got.V2RayCompatible != (exportErr == nil) || skipped != map[bool]int{true: 0, false: 1}[tc.compatible] {
				t.Fatalf("inspector/export mismatch: %+v uriErr=%v exportErr=%v skipped=%d", got, uriErr, exportErr, skipped)
			}
			if !got.V2RayCompatible && !strings.HasPrefix(got.V2RayReason, "URI导出不支持") {
				t.Fatalf("reason must describe URI export: %s", got.V2RayReason)
			}
			if got.V2RayCompatible && !strings.Contains(got.V2RayReason, "尚未实际测试") {
				t.Fatalf("conversion must not claim tested interoperability: %s", got.V2RayReason)
			}
		})
	}
}

func TestNodeInsightsSafeMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"type":"ss","server":"secret-server","port":443,"password":"secret-password","cipher":"secret-cipher","network":"secret-network","secret-field":"secret-value"}`,
		`{"type":"vless","server":"secret-server","port":443,"uuid":"secret-uuid","network":"ws","ws-opts":{"path":"/secret-path","headers":{"Authorization":"secret-header"}},"reality-opts":{"public-key":"secret-key"}}`,
		`{"type":"trojan","server":"secret-server","port":443,"password":"secret-password","sni":"secret-sni","secret-field":{"value":"secret-value"}}`,
		`{"type":"vless","network":{"secret-network":"secret-value"},"tls":["secret-tls"]}`,
	} {
		n := Node{Type: "secret-type", Name: "secret-name", Server: "secret-server", Config: json.RawMessage(raw)}
		before := append(json.RawMessage(nil), n.Config...)
		got := InspectNode(n)
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "secret-") || !reflect.DeepEqual(n.Config, before) {
			t.Fatalf("inspector leaked or mutated private input: %s", encoded)
		}
		if got.V2RayCompatible || got.Warnings == nil {
			t.Fatalf("unknown fields must fail closed with nonnil warnings: %+v", got)
		}
	}
	for _, network := range []string{"tcp", "ws", "grpc", "h2", "quic", "xhttp"} {
		got := InspectNode(Node{Config: json.RawMessage(`{"type":"vless","network":"` + network + `"}`)})
		if got.Transport != network {
			t.Fatalf("known transport lost: %+v", got)
		}
	}
}

func TestNodeInsightsMissingOrInvalidConfig(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "123", `"secret"`, `{"password":"secret"`, `{}`} {
		got := InspectNode(Node{Type: "trojan", Config: json.RawMessage(raw)})
		if got.V2RayCompatible || got.TLS != "unknown" || got.Warnings == nil || len(got.Warnings) != 0 || !strings.HasPrefix(got.V2RayReason, "URI导出不支持") {
			t.Fatalf("invalid config must remain unknown: %+v", got)
		}
	}
}
