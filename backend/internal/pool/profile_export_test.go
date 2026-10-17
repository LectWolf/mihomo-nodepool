package pool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func profileNode(id, sid int64, name string) Node {
	return Node{ID: id, Name: name, Sources: []Origin{{ID: sid, NodeName: name}}, Config: json.RawMessage(`{"name":"original","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret","udp":true,"plugin":"obfs","plugin-opts":{"mode":"http","host":"kept.example.com"}}`)}
}
func profileDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if e := yaml.Unmarshal(raw, &doc); e != nil {
		t.Fatal(e)
	}
	return doc
}
func assertProfileReferences(t *testing.T, doc map[string]any) {
	t.Helper()
	names := map[string]bool{"DIRECT": true, "REJECT": true}
	groups := doc["proxy-groups"].([]any)
	for _, p := range doc["proxies"].([]any) {
		n := p.(map[string]any)["name"].(string)
		if names[n] {
			t.Fatalf("duplicate proxy %s", n)
		}
		names[n] = true
	}
	for _, g := range groups {
		n := g.(map[string]any)["name"].(string)
		if names[n] {
			t.Fatalf("group collision %s", n)
		}
		names[n] = true
	}
	for _, g := range groups {
		for _, p := range g.(map[string]any)["proxies"].([]any) {
			if !names[p.(string)] {
				t.Fatalf("dangling proxy %v", p)
			}
		}
	}
	for _, r := range doc["rules"].([]any) {
		p, i, e := splitLocalRule(r.(string))
		if e != nil {
			t.Fatal(e)
		}
		if !names[p[i]] {
			t.Fatalf("dangling rule %s", r)
		}
	}
}
func TestDefaultProfileCompleteRulesDNSAndReservedNames(t *testing.T) {
	p := DefaultRuleProfile()
	if p.ID != 0 || p.Name != "官方默认规则" || !p.Official || !p.Readonly || len(p.Rules) != 9735 || !p.RoutingEnabled || !p.DNSEnabled || len(p.DNSServers) != 0 {
		t.Fatalf("default %+v rules=%d", p, len(p.Rules))
	}
	in := RuleProfileInput{Name: p.Name, RoutingEnabled: p.RoutingEnabled, DNSEnabled: p.DNSEnabled, DNSServers: p.DNSServers, Rules: p.Rules}
	if e := in.Validate(); e != nil {
		t.Fatal(e)
	}
	nodes := []Node{}
	for i, name := range referenceGroups {
		nodes = append(nodes, profileNode(int64(i+1), 1, name))
	}
	raw, n, w, e := ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
	if e != nil || n != 0 || w != 0 {
		t.Fatalf("export %d %d %v", n, w, e)
	}
	doc := profileDoc(t, raw)
	assertProfileReferences(t, doc)
	if len(doc["rules"].([]any)) != 9735 || len(doc["proxy-groups"].([]any)) != 7 {
		t.Fatal("reference rules/groups lost")
	}
	dns := doc["dns"].(map[string]any)
	want := []any{"223.5.5.5", "119.29.29.29", "8.8.8.8"}
	if !reflect.DeepEqual(dns["nameserver"], want) || !reflect.DeepEqual(dns["default-nameserver"], want) {
		t.Fatalf("DNS %+v", dns)
	}
	proxy := doc["proxies"].([]any)[0].(map[string]any)
	if proxy["plugin-opts"].(map[string]any)["host"] != "kept.example.com" || proxy["udp"] != true {
		t.Fatal("node parameters lost")
	}
	p.DNSEnabled = false
	p.Rules = nil
	raw, _, _, e = ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
	if e != nil {
		t.Fatal(e)
	}
	doc = profileDoc(t, raw)
	if _, ok := doc["dns"]; ok {
		t.Fatal("disabled DNS emitted")
	}
	if !reflect.DeepEqual(doc["rules"], []any{"MATCH," + SubscriptionDisplayName}) {
		t.Fatal("empty rules missing fallback")
	}
	p.DNSEnabled = true
	p.DNSServers = []string{"https://dns.example.com/dns-query"}
	raw, _, _, e = ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(raw, []byte(p.DNSServers[0])) {
		t.Fatal("custom DNS missing")
	}
}
func TestProfileSourceRoutingNamespacesAndExternalIsolation(t *testing.T) {
	p := RuleProfile{RoutingEnabled: false, DNSEnabled: false, Rules: []string{"MATCH,REJECT"}}
	nodes := []Node{profileNode(1, 2, "same"), profileNode(2, 1, "same"), profileNode(3, 1, "策略 1")}
	makeSource := func(id int64, domain string) Source {
		return Source{ID: id, URL: "https://upstream.example/secret", Content: `external-controller: 0.0.0.0:9090
secret: admin-secret
proxy-providers:
 remote: {type: http, url: "https://upstream.example/credential"}
rule-providers:
 remote: {type: http, url: "https://upstream.example/rule-secret"}
proxies:
 - {name: unselected-secret, type: ss, server: never-export.example.com, password: no-leak}
proxy-groups:
 - {name: shared, type: select, proxies: [same, unselected-secret, DIRECT], use: [remote]}
 - {name: nested, type: fallback, proxies: [shared], url: "https://upstream.example/probe-secret"}
 - {name: empty, type: select, use: [remote]}
 - {name: cycle-a, type: select, proxies: [cycle-b]}
 - {name: cycle-b, type: select, proxies: [cycle-a]}
rules:
 - DOMAIN-SUFFIX,` + domain + `,nested
 - DOMAIN,empty.example,empty
 - RULE-SET,remote,shared
 - DOMAIN,missing.example,unselected-secret
 - MATCH,shared
`}
	}
	sources := []Source{makeSource(2, "two.example"), makeSource(1, "one.example")}
	raw, n, w, e := ExportSubscriptionWithProfile(nodes, "clash", &p, sources)
	if e != nil || n != 0 || w == 0 {
		t.Fatalf("routing %d %d %v", n, w, e)
	}
	doc := profileDoc(t, raw)
	assertProfileReferences(t, doc)
	if len(doc["proxies"].([]any)) != 3 {
		t.Fatal("selected nodes lost")
	}
	rules := doc["rules"].([]any)
	if !strings.Contains(rules[0].(string), "one.example,[来源 1]") || !strings.Contains(rules[1].(string), "two.example,[来源 2]") {
		t.Fatalf("unstable source ordering: %v", rules)
	}
	for _, secret := range []string{"upstream.example", "credential", "unselected-secret", "no-leak", "never-export", "admin-secret", "RULE-SET", "proxy-providers", "rule-providers", "probe-secret", "empty.example", "missing.example", "MATCH,REJECT"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("leaked %s", secret)
		}
	}
	raw2, _, _, e := ExportSubscriptionWithProfile(nodes, "clash", &p, []Source{makeSource(1, "one.example"), makeSource(2, "two.example")})
	if e != nil || !bytes.Equal(raw, raw2) {
		t.Fatal("merge depends on input source order")
	}
	raw, _, _, e = ExportSubscriptionWithProfile(nodes, "clash", &p, []Source{{ID: 1, Content: "proxies: []"}, {ID: 2, Content: "bad: ["}})
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(profileDoc(t, raw)["rules"], []any{"MATCH," + SubscriptionDisplayName}) {
		t.Fatal("source fallback missing")
	}
}
func TestProfileV2rayAndUnboundExportsUnchanged(t *testing.T) {
	n := profileNode(1, 1, "node")
	n.Config = json.RawMessage(`{"name":"node","type":"ss","server":"example.com","port":443,"cipher":"aes-128-gcm","password":"secret"}`)
	p := DefaultRuleProfile()
	for _, format := range []string{"v2ray", "clash"} {
		old, skip, e := ExportSubscription([]Node{n}, format)
		if e != nil {
			t.Fatal(e)
		}
		var profile *RuleProfile
		if format == "v2ray" {
			profile = &p
		}
		raw, n, w, e := ExportSubscriptionWithProfile([]Node{n}, format, profile, nil)
		if e != nil || !bytes.Equal(old, raw) || n != skip || w != 0 {
			t.Fatalf("changed %s: %v", format, e)
		}
	}
}
func TestProfileInputValidationAndBindingPresence(t *testing.T) {
	for _, dns := range []string{"223.5.5.5", "2001:4860:4860::8888", "tls://dns.example.com:853", "https://dns.example.com/dns-query", "udp://1.1.1.1:53", "tcp://[2001:db8::1]:53"} {
		if !validDNS(dns) {
			t.Fatalf("valid DNS rejected: %s", dns)
		}
	}
	for _, dns := range []string{"x\nsecret: injected", "https://user:password@dns.example.com/dns-query", "file:///etc/passwd", "http://dns.example.com", "https://bad_host/dns-query", "udp://1.1.1.1:0", "tls://dns.example.com/path", "https://dns.example.com/#proxy", "1.1.1.1\t"} {
		if validDNS(dns) {
			t.Fatalf("bad DNS accepted: %q", dns)
		}
	}
	for _, r := range []string{"MATCH,arbitrary", "RULE-SET,external,NodePool", "IP-CIDR,nope,DIRECT", "DST-PORT,99999,DIRECT", "DOMAIN,x,NodePool\nsecret: x", "DOMAIN,https://secret,DIRECT", "DOMAIN,x,DIRECT,invalid", "GEOIP,anything,DIRECT", "MATCH,DIRECT,extra"} {
		in := RuleProfileInput{Name: "test", Rules: []string{r}}
		if in.Validate() == nil {
			t.Fatalf("invalid rule accepted %q", r)
		}
	}
	for _, name := range []string{"", strings.Repeat("x", 201), "x\nname"} {
		in := RuleProfileInput{Name: name}
		if in.Validate() == nil {
			t.Fatal("invalid name accepted")
		}
	}
	var in RuleProfileInput
	if e := json.Unmarshal([]byte(`{"name":"defaults"}`), &in); e != nil || !in.DNSEnabled || !in.RoutingEnabled {
		t.Fatalf("default switches: %+v %v", in, e)
	}
	for _, tc := range []struct {
		raw   string
		set   bool
		bound bool
		id    int64
	}{{`{"name":"old"}`, false, false, 0}, {`{"name":"new","rule_profile_id":null}`, true, false, 0}, {`{"name":"official","rule_profile_id":0}`, true, true, 0}, {`{"name":"bound","rule_profile_id":12}`, true, true, 12}} {
		var in SubscriptionInput
		if e := json.Unmarshal([]byte(tc.raw), &in); e != nil {
			t.Fatal(e)
		}
		if in.RuleProfileSet != tc.set || (in.RuleProfileID != nil) != tc.bound || (tc.bound && *in.RuleProfileID != tc.id) {
			t.Fatalf("presence %s %+v", tc.raw, in)
		}
	}
	for _, raw := range []string{`{"name":"x","rule_profile_id":-1}`, `{"name":"x","unknown":true}`} {
		var in SubscriptionInput
		if json.Unmarshal([]byte(raw), &in) == nil {
			t.Fatalf("invalid request accepted %s", raw)
		}
	}
	s := NewStore(nil)
	negative := int64(-1)
	if _, err := s.SaveSubscription(context.Background(), 0, SubscriptionInput{Name: "negative", RuleProfileID: &negative}); err == nil || !strings.Contains(err.Error(), "0（官方默认规则）") {
		t.Fatalf("negative save binding: %v", err)
	}
	if _, err := s.BindSubscriptionRule(context.Background(), 1, &negative); err == nil || !strings.Contains(err.Error(), "0（官方默认规则）") {
		t.Fatalf("negative rule-only binding: %v", err)
	}
	if err := s.DeleteRuleProfile(context.Background(), 0); !errors.Is(err, ErrOfficialProfileReadOnly) || !errors.Is(err, ErrForbidden) {
		t.Fatalf("official deletion: %v", err)
	}
	ctx := WithUser(context.Background(), User{ID: 2, Role: "user", Enabled: true})
	if !errors.Is(s.NodePermission(ctx, 1), ErrForbidden) || !errors.Is(s.QueueNodes(ctx, []int64{1}), ErrForbidden) {
		t.Fatal("non-admin can probe")
	}
}
