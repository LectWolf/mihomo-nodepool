package pool

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func chainConfig(t *testing.T, n ParsedNode) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(n.Config, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseResolvesDialerProxy(t *testing.T) {
	body := `proxies:
 - {name: exit, type: ss, server: exit.example.com, port: 443, cipher: aes-128-gcm, password: a, dialer-proxy: relay}
 - {name: relay, type: ss, server: relay.example.com, port: 8443, cipher: aes-128-gcm, password: b}
 - {name: via-group, type: ss, server: g.example.com, port: 443, cipher: aes-128-gcm, password: c, dialer-proxy: Pick}
 - {name: direct, type: ss, server: d.example.com, port: 443, cipher: aes-128-gcm, password: d, dialer-proxy: DIRECT}
 - {name: missing, type: ss, server: m.example.com, port: 443, cipher: aes-128-gcm, password: e, dialer-proxy: nowhere}
 - {name: loop-a, type: ss, server: la.example.com, port: 443, cipher: aes-128-gcm, password: f, dialer-proxy: loop-b}
 - {name: loop-b, type: ss, server: lb.example.com, port: 443, cipher: aes-128-gcm, password: g, dialer-proxy: loop-a}
proxy-groups:
 - {name: Pick, type: select, proxies: [relay, DIRECT]}
`
	p, err := Parse(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for _, n := range p.Nodes {
		got[n.Name] = chainConfig(t, n)
	}
	for _, name := range []string{"exit", "via-group"} {
		inner, ok := got[name][DialerKey].(map[string]any)
		if !ok || inner["server"] != "relay.example.com" {
			t.Fatalf("%s dialer not embedded: %#v", name, got[name])
		}
		if _, left := got[name]["dialer-proxy"]; left {
			t.Fatalf("%s keeps raw dialer-proxy", name)
		}
	}
	if _, ok := got["relay"]; !ok {
		t.Fatal("relay must stay a normal node")
	}
	if _, ok := got["direct"][DialerKey]; ok {
		t.Fatal("DIRECT dialer must be dropped")
	}
	for _, name := range []string{"missing", "loop-a", "loop-b"} {
		if _, ok := got[name]; ok {
			t.Fatalf("%s must be rejected", name)
		}
	}
	if len(p.Warnings) == 0 {
		t.Fatal("rejected chained nodes should produce warnings")
	}
	// 前置不同的节点指纹不同
	if p.Nodes[0].Hash == func() string {
		q, _ := Parse("proxies:\n - {name: exit, type: ss, server: exit.example.com, port: 443, cipher: aes-128-gcm, password: a}\n", nil)
		return q.Nodes[0].Hash
	}() {
		t.Fatal("chained node must not share fingerprint with the plain one")
	}
}

func TestParseCoercesBoolsPortsAndControlChars(t *testing.T) {
	body := "proxies:\n - {name: \"bad\x01name\", type: hysteria2, server: h.example.com, ports: \"20000-30000\", password: p, skip-cert-verify: \"true\"}\n"
	strict := func(m map[string]any) error {
		if v, ok := m["skip-cert-verify"]; ok {
			if _, isBool := v.(bool); !isBool {
				return errString("skip-cert-verify must be bool")
			}
		}
		return nil
	}
	p, err := Parse(body, strict)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 1 || p.Nodes[0].Port != 20000 || strings.ContainsRune(p.Nodes[0].Name, 1) {
		t.Fatalf("unexpected node: %+v", p.Nodes)
	}
	if chainConfig(t, p.Nodes[0])["skip-cert-verify"] != true {
		t.Fatal("string bool not coerced")
	}
	out, changed := coerceBools(map[string]any{"udp": "False", "nested": map[string]any{"x": "TRUE"}, "keep": "yes"})
	if !changed || out["udp"] != false || out["nested"].(map[string]any)["x"] != true || out["keep"] != "yes" {
		t.Fatalf("coerceBools: %#v", out)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestExportExpandsHiddenDialers(t *testing.T) {
	p, err := Parse(`proxies:
 - {name: a, type: ss, server: a.example.com, port: 443, cipher: aes-128-gcm, password: a, dialer-proxy: r}
 - {name: b, type: ss, server: b.example.com, port: 443, cipher: aes-128-gcm, password: b, dialer-proxy: r}
 - {name: r, type: ss, server: r.example.com, port: 443, cipher: aes-128-gcm, password: r}
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []Node{}
	for i, n := range p.Nodes {
		if n.Name != "r" {
			nodes = append(nodes, Node{ID: int64(i + 1), Name: n.Name, Type: n.Type, Config: n.Config})
		}
	}
	raw, _, err := ExportSubscription(nodes, "clash")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []struct {
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Proxies) != 3 {
		t.Fatalf("want 2 nodes + 1 shared hidden dialer, got %d", len(doc.Proxies))
	}
	hidden := ""
	for _, m := range doc.Proxies {
		if _, ok := m[DialerKey]; ok {
			t.Fatal("internal key leaked")
		}
		if strings.HasPrefix(text(m["name"]), "前置 · ") {
			hidden = text(m["name"])
		}
	}
	for _, m := range doc.Proxies[:2] {
		if m["dialer-proxy"] != hidden {
			t.Fatalf("dialer-proxy not rewritten: %#v", m)
		}
	}
	for _, g := range doc.Groups {
		for _, name := range g.Proxies {
			if name == hidden {
				t.Fatal("hidden dialer must not appear in groups")
			}
		}
	}
	// v2ray URI 无法表达链式代理，全部为链式节点时没有可导出的内容
	if _, _, err = ExportSubscription(nodes, "v2ray"); err == nil {
		t.Fatal("v2ray must skip chained nodes")
	}
}
