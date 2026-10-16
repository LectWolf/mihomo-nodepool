package pool

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSubscriptionFormatsAndCompleteClash(t *testing.T) {
	for _, tc := range []struct{ format, ua, want string }{{"clash", "v2rayN", "clash"}, {"v2ray", "mihomo", "v2ray"}, {"auto", "Stash", "clash"}, {"auto", "v2rayNG", "v2ray"}, {"", "NekoBox", "v2ray"}, {"auto", "Shadowrocket", "v2ray"}, {"auto", "Hiddify", "v2ray"}, {"auto", "sing-box", "clash"}, {"", "", "clash"}} {
		got, e := ExportFormat(tc.format, tc.ua)
		if e != nil || got != tc.want {
			t.Fatalf("%+v: %s %v", tc, got, e)
		}
	}
	if _, e := ExportFormat("sing-box", ""); e == nil {
		t.Fatal("unsupported format accepted")
	}
	config := `{"name":"NodePool","type":"vless","server":"example.com","port":443,"uuid":"123","tls":true,"ws-opts":{"path":"/ws","headers":{"X-Custom":"kept"}},"network":"ws"}`
	nodes := []Node{{ID: 1, Name: "NodePool", Config: json.RawMessage(config)}, {ID: 2, Name: "NodePool", Config: json.RawMessage(config)}}
	raw, skip, e := ExportSubscription(nodes, "clash")
	if e != nil || skip != 0 {
		t.Fatalf("export: %v %d", e, skip)
	}
	var doc map[string]any
	if e = yaml.Unmarshal(raw, &doc); e != nil {
		t.Fatal(e)
	}
	if doc["proxy-groups"] == nil || doc["rules"] == nil {
		t.Fatal("incomplete clash document")
	}
	parsed, e := Parse(string(raw), nil)
	if e != nil || len(parsed.Nodes) != 2 {
		t.Fatalf("clash import: %v", e)
	}
	names := map[string]bool{}
	for _, n := range parsed.Nodes {
		if names[n.Name] || n.Name == "NodePool" {
			t.Fatal("duplicate/reserved name")
		}
		names[n.Name] = true
		var m map[string]any
		_ = json.Unmarshal(n.Config, &m)
		opts := m["ws-opts"].(map[string]any)
		if opts["headers"].(map[string]any)["X-Custom"] != "kept" {
			t.Fatal("clash dropped config")
		}
	}
	if _, skip, e = ExportSubscription(nodes, "v2ray"); !errors.Is(e, ErrUnexportable) || skip != 2 {
		t.Fatalf("complex headers silently lost: %d %v", skip, e)
	}
}
func TestShareURIRoundTrips(t *testing.T) {
	configs := []string{
		`{"name":"日本 #&","type":"ss","server":"2001:db8::1","port":443,"cipher":"aes-128-gcm","password":"秘密:@/?#%"}`,
		`{"name":"2022","type":"ss","server":"example.com","port":443,"cipher":"2022-blake3-aes-128-gcm","password":"YWJjZGVmZ2hpamtsbW5vcA=="}`,
		`{"name":"VMess","type":"vmess","server":"example.com","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","alterId":0,"cipher":"auto","tls":true,"servername":"tls.example.com","network":"ws","ws-opts":{"path":"/路径?x=1&y=2","headers":{"Host":"ws.example.com"}}}`,
		`{"name":"VMess TLS options","type":"vmess","server":"example.com","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","alterId":0,"cipher":"auto","tls":true,"servername":"tls.example.com","skip-cert-verify":false,"client-fingerprint":"chrome","alpn":["h2","http/1.1"],"network":"grpc","grpc-opts":{"grpc-service-name":"svc"}}`,
		`{"name":"SS plugin","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret","plugin":"obfs","plugin-opts":{"mode":"http","host":"obfs.example.com"}}`,
		`{"name":"SS websocket","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret","plugin":"v2ray-plugin","plugin-opts":{"mode":"websocket","host":"ws.example.com","path":"/路径","tls":true}}`,
		`{"name":"VLESS","type":"vless","server":"example.com","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","tls":true,"servername":"tls.example.com","client-fingerprint":"chrome","network":"grpc","grpc-opts":{"grpc-service-name":"a&b /c"},"reality-opts":{"public-key":"abcd_-","short-id":"aabb"}}`,
		`{"name":"Trojan","type":"trojan","server":"2001:db8::2","port":443,"password":"秘密:@/?#%","sni":"tls.example.com","skip-cert-verify":true,"alpn":["h2","http/1.1"]}`,
		`{"name":"HY2","type":"hysteria2","server":"example.com","port":443,"password":"秘密:@/?#%","sni":"tls.example.com","skip-cert-verify":true,"obfs":"salamander","obfs-password":"a&b=?你好"}`,
		`{"name":"TUIC","type":"tuic","server":"example.com","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","password":"秘密:@/?#%","sni":"tls.example.com","alpn":["h3"],"congestion-controller":"bbr","udp-relay-mode":"native"}`,
		`{"name":"HTTP","type":"http","server":"example.com","port":8080,"username":"用户","password":"秘密:@/?#%"}`,
		`{"name":"SOCKS","type":"socks5","server":"example.com","port":1080,"username":"用户","password":"秘密:@/?#%"}`,
	}
	for _, raw := range configs {
		var m map[string]any
		if e := json.Unmarshal([]byte(raw), &m); e != nil {
			t.Fatal(e)
		}
		t.Run(m["name"].(string), func(t *testing.T) {
			uri, e := shareURI(m)
			if e != nil {
				t.Fatal(e)
			}
			if m["type"] == "ss" && strings.HasPrefix(text(m["cipher"]), "2022-") {
				u, e := url.Parse(uri)
				if e != nil || u.User.Username() != m["cipher"] {
					t.Fatal("SS2022 userinfo must not be base64")
				}
				password, _ := u.User.Password()
				if password != m["password"] {
					t.Fatal("SS2022 password lost")
				}
			}
			if m["type"] == "hysteria2" {
				u, _ := url.Parse(uri)
				if u.User.Username() != m["password"] {
					t.Fatal("HY2 password must be in userinfo")
				}
			}
			// HTTP/SOCKS URI syntax is checked directly: Mihomo's import converter only
			// accepts a subset of the share formats used by other subscription clients.
			if m["type"] == "http" || m["type"] == "socks5" {
				u, e := url.Parse(uri)
				if e != nil {
					t.Fatal(e)
				}
				password, _ := u.User.Password()
				if u.User.Username() != m["username"] || password != m["password"] || u.Hostname() != m["server"] {
					t.Fatal("proxy URI credentials/address lost")
				}
				return
			}
			parsed, e := Parse(uri, nil)
			if e != nil || len(parsed.Nodes) != 1 {
				t.Fatalf("round trip: %v, URI %s", e, uri)
			}
			var got map[string]any
			_ = json.Unmarshal(parsed.Nodes[0].Config, &got)
			for k, v := range m {
				if k == "alterId" && text(got[k]) == text(v) {
					continue
				}
				if !reflect.DeepEqual(got[k], v) {
					t.Errorf("%s lost: want %#v got %#v (uri %s)", k, v, got[k], uri)
				}
			}
		})
	}
}
func TestURIRejectsUnrepresentedParameters(t *testing.T) {
	for _, extra := range []string{`"smux":{"enabled":true}`, `"tfo":true`, `"plugin-opts":{"nested":{}}`, `"udp":false`, `"dialer-proxy":"upstream"`} {
		raw := `{"name":"SS","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret",` + extra + `}`
		var m map[string]any
		_ = json.Unmarshal([]byte(raw), &m)
		if uri, e := shareURI(m); e == nil {
			t.Fatalf("silently lost %s: %s", extra, uri)
		}
	}
	good := Node{Config: json.RawMessage(`{"name":"a","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret"}`)}
	bad := Node{Config: json.RawMessage(`{"name":"b","type":"wireguard","server":"example.com","port":443}`)}
	raw, skipped, e := ExportSubscription([]Node{good, bad}, "v2ray")
	if e != nil || skipped != 1 {
		t.Fatalf("skip %d: %v", skipped, e)
	}
	decoded, e := base64.StdEncoding.DecodeString(string(raw))
	if e != nil || !strings.HasPrefix(string(decoded), "ss://") || strings.Count(string(decoded), "\n") != 1 {
		t.Fatal("not base64 URI lines")
	}
}

func TestURISkipsNonportableProtocolOptions(t *testing.T) {
	for _, raw := range []string{
		`{"name":"VMess","type":"vmess","server":"example.com","port":443,"uuid":"uuid","tls":true,"skip-cert-verify":true}`,
		`{"name":"SS","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret","plugin":"v2ray-plugin","plugin-opts":{"mux":false}}`,
		`{"name":"HY2","type":"hysteria2","server":"example.com","port":443,"password":"secret","client-fingerprint":"chrome"}`,
		`{"name":"VLESS","type":"vless","server":"example.com","port":443,"uuid":"uuid","tls":"true"}`,
	} {
		var m map[string]any
		_ = json.Unmarshal([]byte(raw), &m)
		if uri, e := shareURI(m); e == nil {
			t.Fatalf("nonportable options exported: %s", uri)
		}
	}
}
