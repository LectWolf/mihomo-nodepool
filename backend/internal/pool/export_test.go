package pool

import (
	"encoding/base64"
	"encoding/json"
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
		if names[n.Name] || n.Name == "NodePool" || n.Name == SubscriptionDisplayName {
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
	// 自定义请求头只影响伪装，v2ray 链接只保留 Host
	if _, skip, e = ExportSubscription(nodes, "v2ray"); e != nil || skip != 0 {
		t.Fatalf("v2ray export: %d %v", skip, e)
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
func TestURIIgnoresTuningParameters(t *testing.T) {
	for _, extra := range []string{`"smux":{"enabled":true}`, `"tfo":true`, `"plugin":"obfs","plugin-opts":{"mode":"http","nested":{}}`, `"udp":false`, `"ip-version":"ipv4"`, `"client-fingerprint":"chrome"`} {
		raw := `{"name":"SS","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret",` + extra + `}`
		var m map[string]any
		_ = json.Unmarshal([]byte(raw), &m)
		if _, e := shareURI(m); e != nil {
			t.Fatalf("%s blocked export: %v", extra, e)
		}
	}
	good := Node{Config: json.RawMessage(`{"name":"a","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret"}`)}
	bad := Node{Config: json.RawMessage(`{"name":"b","type":"wireguard","server":"example.com","port":443}`)}
	chained := Node{Config: json.RawMessage(`{"name":"c","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret","dialer-proxy":"up"}`)}
	raw, skipped, e := ExportSubscription([]Node{good, bad, chained}, "v2ray")
	if e != nil || skipped != 2 {
		t.Fatalf("skip %d: %v", skipped, e)
	}
	decoded, e := base64.StdEncoding.DecodeString(string(raw))
	if e != nil || !strings.HasPrefix(string(decoded), "ss://") || strings.Count(string(decoded), "\n") != 1 {
		t.Fatal("not base64 URI lines")
	}
}

// 常见机场配置（含 Mihomo 自己转换分享链接时生成的参数）都应能导出，且关键参数能被客户端还原
func TestURIExportsCommonProviderConfigs(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want map[string]any
	}{
		{`{"name":"vmess ws","type":"vmess","server":"a.example","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","alterId":0,"cipher":"auto","udp":true,"xudp":true,"tls":true,"skip-cert-verify":true,"servername":"s.example","network":"ws","ws-opts":{"path":"/ws","headers":{"Host":"h.example","User-Agent":"Mozilla/5.0"},"max-early-data":2048,"early-data-header-name":"Sec-WebSocket-Protocol"}}`,
			map[string]any{"type": "vmess", "network": "ws", "servername": "s.example", "tls": true}},
		{`{"name":"vless reality","type":"vless","server":"b.example","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","udp":true,"tls":true,"flow":"xtls-rprx-vision","client-fingerprint":"chrome","servername":"www.microsoft.com","packet-encoding":"xudp","reality-opts":{"public-key":"pk","short-id":"ab","support-x25519mlkem768":false}}`,
			map[string]any{"type": "vless", "flow": "xtls-rprx-vision", "servername": "www.microsoft.com", "client-fingerprint": "chrome"}},
		{`{"name":"vless h2","type":"vless","server":"b.example","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","tls":true,"network":"h2","h2-opts":{"host":["h.example"],"path":"/h2"}}`,
			map[string]any{"type": "vless", "network": "h2"}},
		{`{"name":"vmess http","type":"vmess","server":"b.example","port":80,"uuid":"00112233-4455-6677-8899-aabbccddeeff","alterId":0,"cipher":"auto","network":"http","http-opts":{"method":"GET","path":["/"],"headers":{"Host":["h.example"]}}}`,
			map[string]any{"type": "vmess", "network": "http"}},
		{`{"name":"vless upgrade","type":"vless","server":"b.example","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","tls":true,"network":"ws","ws-opts":{"path":"/up","v2ray-http-upgrade":true,"v2ray-http-upgrade-fast-open":true}}`,
			map[string]any{"type": "vless", "network": "httpupgrade"}},
		{`{"name":"vless xhttp","type":"vless","server":"b.example","port":443,"uuid":"00112233-4455-6677-8899-aabbccddeeff","tls":true,"network":"xhttp","xhttp-opts":{"path":"/x","mode":"auto"}}`,
			map[string]any{"type": "vless", "network": "xhttp"}},
		{`{"name":"trojan","type":"trojan","server":"c.example","port":443,"password":"p","udp":true,"sni":"c.example","skip-cert-verify":true,"tfo":false}`,
			map[string]any{"type": "trojan", "sni": "c.example", "skip-cert-verify": true}},
		{`{"name":"hy2 ports","type":"hysteria2","server":"d.example","port":443,"ports":"20000-30000","password":"p","up":"50 Mbps","down":"200 Mbps","sni":"d.example","skip-cert-verify":true}`,
			map[string]any{"type": "hysteria2", "sni": "d.example", "skip-cert-verify": true}},
		{`{"name":"ss uot","type":"ss","server":"e.example","port":8388,"cipher":"aes-256-gcm","password":"p","udp":true,"udp-over-tcp":true,"udp-over-tcp-version":2}`,
			map[string]any{"type": "ss", "cipher": "aes-256-gcm"}},
		{`{"name":"anytls","type":"anytls","server":"f.example","port":443,"password":"p","sni":"f.example","skip-cert-verify":true,"client-fingerprint":"chrome","idle-session-check-interval":30}`,
			map[string]any{"type": "anytls", "sni": "f.example", "skip-cert-verify": true}},
	} {
		var m map[string]any
		if e := json.Unmarshal([]byte(tc.raw), &m); e != nil {
			t.Fatal(e)
		}
		uri, e := shareURI(m)
		if e != nil {
			t.Fatalf("%s: %v", m["name"], e)
		}
		parsed, e := Parse(uri, nil)
		if e != nil || len(parsed.Nodes) != 1 {
			t.Fatalf("%s round trip: %v %s", m["name"], e, uri)
		}
		var got map[string]any
		_ = json.Unmarshal(parsed.Nodes[0].Config, &got)
		for k, v := range tc.want {
			if !reflect.DeepEqual(got[k], v) {
				t.Errorf("%s %s: want %#v got %#v (%s)", m["name"], k, v, got[k], uri)
			}
		}
		if got["server"] != m["server"] {
			t.Errorf("%s server lost", m["name"])
		}
	}
}

func TestURIVMessInsecureAndStringTLS(t *testing.T) {
	m := map[string]any{"name": "v", "type": "vmess", "server": "a.example", "port": 443, "uuid": "id", "tls": true, "skip-cert-verify": true}
	uri, e := shareURI(m)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "vmess://"))
	var vm map[string]any
	if json.Unmarshal(raw, &vm) != nil || vm["insecure"] != "1" || vm["tls"] != "tls" {
		t.Fatalf("vmess insecure: %s", raw)
	}
	uri, e = shareURI(map[string]any{"name": "l", "type": "vless", "server": "a.example", "port": "443", "uuid": "id", "tls": "true"})
	if e != nil || !strings.Contains(uri, "security=tls") {
		t.Fatalf("string tls: %s %v", uri, e)
	}
}
