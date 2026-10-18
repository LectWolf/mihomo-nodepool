package pool

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/metacubex/quic-go"
	mtls "github.com/metacubex/tls"
)

const leafPin = "e8e2d387fdbffeb38e9c9065cf30a97ee23c0e3d32ee6f78ffae40966befccc9"
const issuerPin = "1111111111111111111111111111111111111111111111111111111111111111"

func v2rayLines(t *testing.T, nodes []Node) []string {
	t.Helper()
	raw, _, e := ExportSubscription(nodes, "v2ray")
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := base64.StdEncoding.DecodeString(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	return strings.Fields(string(decoded))
}

// 跳过证书验证的节点：保留 allowInsecure 兼容旧内核，同时附带 pcs 供新版 Xray 校验
func TestV2rayExportAddsCertPins(t *testing.T) {
	pin := leafPin + "," + issuerPin
	nodes := []Node{
		{ID: 1, Name: "vless", TLSPin: pin, Config: json.RawMessage(`{"type":"vless","server":"a.example.com","port":443,"uuid":"u","tls":true,"skip-cert-verify":true,"servername":"a.example.com"}`)},
		{ID: 2, Name: "trojan", TLSPin: pin, Config: json.RawMessage(`{"type":"trojan","server":"b.example.com","port":443,"password":"p","skip-cert-verify":true}`)},
		{ID: 3, Name: "vmess", TLSPin: pin, Config: json.RawMessage(`{"type":"vmess","server":"c.example.com","port":443,"uuid":"u","alterId":0,"cipher":"auto","tls":true,"skip-cert-verify":true}`)},
		{ID: 4, Name: "hy2", TLSPin: pin, Config: json.RawMessage(`{"type":"hysteria2","server":"d.example.com","port":443,"password":"p","skip-cert-verify":true}`)},
		{ID: 5, Name: "own", Config: json.RawMessage(`{"type":"trojan","server":"e.example.com","port":443,"password":"p","skip-cert-verify":true,"fingerprint":"E8:E2:D3:87:FD:BF:FE:B3:8E:9C:90:65:CF:30:A9:7E:E2:3C:0E:3D:32:EE:6F:78:FF:AE:40:96:6B:EF:CC:C9"}`)},
		{ID: 6, Name: "plain", Config: json.RawMessage(`{"type":"trojan","server":"f.example.com","port":443,"password":"p"}`)},
	}
	lines := v2rayLines(t, nodes)
	if len(lines) != 6 {
		t.Fatalf("lines: %v", lines)
	}
	for _, i := range []int{0, 1} {
		u, _ := url.Parse(lines[i])
		if q := u.Query(); q.Get("pcs") != pin || q.Get("allowInsecure") != "1" {
			t.Fatalf("%s query: %v", u.Scheme, q)
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(lines[2], "vmess://"))
	var vm map[string]any
	if json.Unmarshal(raw, &vm) != nil || vm["pcs"] != pin || vm["insecure"] != "1" {
		t.Fatalf("vmess: %s", raw)
	}
	if u, _ := url.Parse(lines[3]); u.Query().Get("pinSHA256") != leafPin || u.Query().Get("insecure") != "1" {
		t.Fatalf("hy2: %s", lines[3])
	}
	if u, _ := url.Parse(lines[4]); u.Query().Get("pcs") != leafPin {
		t.Fatalf("own fingerprint: %s", lines[4])
	}
	if u, _ := url.Parse(lines[5]); u.Query().Has("pcs") {
		t.Fatalf("plain: %s", lines[5])
	}
	clash, _, e := ExportSubscription(nodes, "clash")
	if e != nil || strings.Contains(string(clash), pinKey) || strings.Contains(string(clash), issuerPin) {
		t.Fatalf("clash must not carry fetched pins: %v", e)
	}
}

func TestParseKeepsSharePins(t *testing.T) {
	vmess, _ := json.Marshal(map[string]any{"v": "2", "ps": "vm", "add": "c.example.com", "port": "443", "id": "11111111-1111-1111-1111-111111111111", "aid": "0", "net": "tcp", "tls": "tls", "pcs": leafPin})
	links := "vless://11111111-1111-1111-1111-111111111111@a.example.com:443?security=tls&allowInsecure=1&pcs=" + leafPin + "," + issuerPin + "#vl\n" +
		"trojan://p@b.example.com:443?allowInsecure=1&pcs=" + strings.ToUpper(leafPin) + "#tr\n" +
		"vmess://" + base64.StdEncoding.EncodeToString(vmess) + "\n"
	parsed, e := Parse(links, nil)
	if e != nil || len(parsed.Nodes) != 3 {
		t.Fatalf("parse: %v %d", e, len(parsed.Nodes))
	}
	for _, n := range parsed.Nodes {
		var m map[string]any
		_ = json.Unmarshal(n.Config, &m)
		// mihomo 转换器自己会把 vless 的 pcs 整串写入 fingerprint
		if f := strings.ToLower(text(m["fingerprint"])); f != leafPin && f != leafPin+","+issuerPin {
			t.Fatalf("%s fingerprint: %v", m["type"], m["fingerprint"])
		}
	}
}

func TestPinTargetSkipsUnneededNodes(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   bool
		sni    string
	}{
		{`{"type":"vless","server":"a.example.com","port":443,"tls":true,"skip-cert-verify":true}`, true, "a.example.com"},
		{`{"type":"vless","server":"1.2.3.4","port":443,"tls":true,"skip-cert-verify":true,"network":"ws","ws-opts":{"headers":{"Host":"cdn.example.com"}}}`, true, "cdn.example.com"},
		{`{"type":"vless","server":"a.example.com","port":443,"tls":true,"skip-cert-verify":true,"reality-opts":{"public-key":"k"}}`, false, ""},
		{`{"type":"vless","server":"a.example.com","port":443,"tls":true}`, false, ""},
		{`{"type":"vmess","server":"a.example.com","port":443,"skip-cert-verify":true}`, false, ""},
		{`{"type":"trojan","server":"a.example.com","port":443,"skip-cert-verify":true,"fingerprint":"` + leafPin + `"}`, false, ""},
		{`{"type":"hysteria2","server":"a.example.com","port":443,"skip-cert-verify":true,"obfs":"salamander"}`, false, ""},
		{`{"type":"ss","server":"a.example.com","port":443,"skip-cert-verify":true}`, false, ""},
	} {
		got, ok := pinTarget(json.RawMessage(tc.config))
		if ok != tc.want || (ok && got.sni != tc.sni) {
			t.Fatalf("%s: %v %+v", tc.config, ok, got)
		}
	}
}

// 从本机 TLS（TCP）和 QUIC 服务器读取证书指纹
func TestFetchPinOverTLSAndQUIC(t *testing.T) {
	TLSPinPrivateTargets = true
	defer func() { TLSPinPrivateTargets = false }()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	cert := server.TLS.Certificates[0]
	sum := sha256.Sum256(cert.Certificate[0])
	want := hex.EncodeToString(sum[:])
	host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	got, e := fetchPin(context.Background(), tlsTarget{host: host, port: port, sni: "example.com"})
	if e != nil || got != want {
		t.Fatalf("tcp pin: %v %q want %q", e, got, want)
	}
	udp, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	listener, e := quic.Listen(udp, &mtls.Config{Certificates: []mtls.Certificate{{Certificate: cert.Certificate, PrivateKey: cert.PrivateKey}}, NextProtos: []string{"h3"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			go func() { <-c.Context().Done() }()
		}
	}()
	got, e = fetchPin(context.Background(), tlsTarget{host: "127.0.0.1", port: strconv.Itoa(udp.LocalAddr().(*net.UDPAddr).Port), sni: "example.com", alpn: []string{"h3"}, quic: true})
	if e != nil || got != want {
		t.Fatalf("quic pin: %v %q want %q", e, got, want)
	}
	TLSPinPrivateTargets = false
	if _, e = fetchPin(context.Background(), tlsTarget{host: host, port: port}); e == nil {
		t.Fatal("private address must be refused")
	}
	_ = tls.VersionTLS13
}
