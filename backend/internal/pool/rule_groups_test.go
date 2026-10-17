package pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRuleGroupsValidation(t *testing.T) {
	base := func() []RuleGroup {
		return []RuleGroup{{Name: "NodePool", Type: "select", Groups: []string{"香港"}}, {Name: "香港", Type: "url-test", Countries: []string{"hk"}}}
	}
	valid := RuleProfileInput{Name: "private", Groups: base(), Rules: []string{"MATCH,香港"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if valid.Groups[1].Countries[0] != "HK" || valid.Groups[0].Interval != 300 {
		t.Fatal("normalization missing")
	}
	tests := map[string]func([]RuleGroup) []RuleGroup{
		"cycle":              func(g []RuleGroup) []RuleGroup { g[1].Groups = []string{"NodePool"}; return g },
		"self":               func(g []RuleGroup) []RuleGroup { g[1].Groups = []string{"香港"}; return g },
		"dangling":           func(g []RuleGroup) []RuleGroup { g[1].Groups = []string{"hidden-node"}; return g },
		"reserved":           func(g []RuleGroup) []RuleGroup { g[1].Name = "DIRECT"; return g },
		"global":             func(g []RuleGroup) []RuleGroup { g[1].Name = "GLOBAL"; return g },
		"duplicate":          func(g []RuleGroup) []RuleGroup { g[1].Name = "NodePool"; return g },
		"missing main":       func(g []RuleGroup) []RuleGroup { return g[1:] },
		"comma":              func(g []RuleGroup) []RuleGroup { g[1].Name = "bad,name"; return g },
		"control":            func(g []RuleGroup) []RuleGroup { g[1].Name = "bad\nname"; return g },
		"long name":          func(g []RuleGroup) []RuleGroup { g[1].Name = strings.Repeat("中", 101); return g },
		"bad type":           func(g []RuleGroup) []RuleGroup { g[1].Type = "relay"; return g },
		"too many ids":       func(g []RuleGroup) []RuleGroup { g[1].NodeIDs = make([]int64, 2001); return g },
		"too many countries": func(g []RuleGroup) []RuleGroup { g[1].Countries = make([]string, 65); return g },
		"too many refs":      func(g []RuleGroup) []RuleGroup { g[1].Groups = make([]string, 65); return g },
		"bad country":        func(g []RuleGroup) []RuleGroup { g[1].Countries = []string{"USA"}; return g },
		"cloudflare":         func(g []RuleGroup) []RuleGroup { g[1].Countries = []string{"CF"}; return g },
		"interval low":       func(g []RuleGroup) []RuleGroup { g[1].Interval = 59; return g },
		"interval high":      func(g []RuleGroup) []RuleGroup { g[1].Interval = 86401; return g },
		"tolerance low":      func(g []RuleGroup) []RuleGroup { g[1].Tolerance = -1; return g },
		"tolerance high":     func(g []RuleGroup) []RuleGroup { g[1].Tolerance = 1001; return g },
		"too many groups":    func(g []RuleGroup) []RuleGroup { return make([]RuleGroup, 65) },
		"empty groups":       func(g []RuleGroup) []RuleGroup { return []RuleGroup{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			in := RuleProfileInput{Name: "test", Groups: mutate(base())}
			if in.Validate() == nil {
				t.Fatal("invalid group accepted")
			}
		})
	}
	valid.Rules = []string{"MATCH,♻️ 自动选择"}
	if valid.Validate() == nil {
		t.Fatal("removed default group still allowed as target")
	}
	// Normalization must not mutate shared arrays.
	original := base()
	_, _ = normalizeRuleGroups(original)
	if original[1].Countries[0] != "hk" {
		t.Fatal("validation modified caller")
	}
	var g RuleGroup
	if err := json.Unmarshal([]byte(`{"name":"test","type":"url-test"}`), &g); err != nil || g.Interval != 300 || g.Tolerance != 50 {
		t.Fatalf("defaults %+v %v", g, err)
	}
	if err := json.Unmarshal([]byte(`{"name":"test","type":"url-test","tolerance":0}`), &g); err != nil || g.Tolerance != 0 {
		t.Fatalf("zero tolerance %+v %v", g, err)
	}
	if json.Unmarshal([]byte(`{"name":"test","type":"select","proxies":["secret"]}`), &g) == nil {
		t.Fatal("unknown node-name field accepted")
	}
}

func TestRuleGroupsCountryNestingIsolationAndCollisions(t *testing.T) {
	p := RuleProfile{RoutingEnabled: true, Groups: []RuleGroup{
		{Name: "NodePool", Type: "select", Groups: []string{"自动", "故障"}},
		{Name: "自动", Type: "url-test", Groups: []string{"港澳台"}, Interval: 600, Tolerance: 0},
		{Name: "故障", Type: "fallback", Groups: []string{"港澳台"}},
		{Name: "港澳台", Type: "select", Countries: []string{"HK", "TW", "MO"}, NodeIDs: []int64{4, 999}},
		{Name: "中国", Type: "select", Countries: []string{"CN"}},
		{Name: "空组", Type: "load-balance", NodeIDs: []int64{999}},
	}, Rules: []string{"DOMAIN,example.com,港澳台", "MATCH,NodePool"}}
	nodes := []Node{}
	for i, code := range []string{"HK", "TW", "MO", "US", "CN", "CF"} {
		n := profileNode(int64(i+1), 1, []string{"港澳台", "台湾节点", "澳门节点", "美国节点", "中国节点", "CF节点"}[i])
		n.CountryCode = code
		nodes = append(nodes, n)
	}
	raw, skip, warn, err := ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
	if err != nil || skip != 0 || warn != 0 {
		t.Fatalf("export %d %d %v", skip, warn, err)
	}
	doc := profileDoc(t, raw)
	assertProfileReferences(t, doc)
	groups := map[string]map[string]any{}
	for _, v := range doc["proxy-groups"].([]any) {
		g := v.(map[string]any)
		groups[g["name"].(string)] = g
	}
	if !reflect.DeepEqual(groups["港澳台"]["proxies"], []any{"港澳台 (2)", "台湾节点", "澳门节点"}) {
		t.Fatalf("country only, node IDs ignored: %v", groups["港澳台"])
	}
	if !reflect.DeepEqual(groups["中国"]["proxies"], []any{"中国节点"}) {
		t.Fatalf("CN must not include HK/TW/MO: %v", groups["中国"])
	}
	for _, name := range []string{"自动", "故障"} {
		if !reflect.DeepEqual(groups[name]["proxies"], []any{"港澳台"}) {
			t.Fatalf("country reference missing: %v", groups[name])
		}
	}
	if groups["自动"]["tolerance"] != 0 || groups["自动"]["interval"] != 600 {
		t.Fatal("test parameters lost")
	}
	if !reflect.DeepEqual(groups["空组"]["proxies"], []any{"DIRECT"}) || groups["空组"]["url"] != "https://www.gstatic.com/generate_204" {
		t.Fatalf("empty group unsafe: %v", groups["空组"])
	}
	proxy := doc["proxies"].([]any)[0].(map[string]any)
	if proxy["plugin-opts"].(map[string]any)["host"] != "kept.example.com" {
		t.Fatal("connection settings lost")
	}
	p.Groups[3].Groups = []string{"unselected-node"}
	if _, _, _, err = ExportSubscriptionWithProfile(nodes, "clash", &p, nil); err == nil {
		t.Fatal("node name accepted as group reference")
	}
	p.Groups[3].Groups = nil
	p.Groups[3].IncludeAll = true
	raw, _, _, err = ExportSubscriptionWithProfile(nodes[:1], "clash", &p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(profileDoc(t, raw)["proxies"].([]any)) != 1 || bytes.Contains(raw, []byte("美国节点")) {
		t.Fatal("include_all escaped subscription selection")
	}
}

type ruleProfileScanFixture struct{ groups []byte }

func (f ruleProfileScanFixture) Scan(dest ...any) error {
	if len(dest) != 9 {
		return &json.UnmarshalTypeError{Value: "scan columns", Type: reflect.TypeOf(dest)}
	}
	*dest[0].(*int64) = 7
	*dest[1].(*string) = "legacy"
	*dest[2].(*bool) = true
	*dest[3].(*bool) = false
	*dest[4].(*[]byte) = []byte(`[]`)
	*dest[5].(*[]byte) = []byte(`["MATCH,NodePool"]`)
	*dest[6].(*[]byte) = f.groups
	*dest[7].(*time.Time) = time.Time{}
	*dest[8].(*time.Time) = time.Time{}
	return nil
}
func TestRuleGroupsLegacyNullAndOfficialOutput(t *testing.T) {
	nodes := []Node{profileNode(1, 1, "one"), profileNode(2, 1, "two")}
	old := RuleProfile{RoutingEnabled: true, Rules: []string{"MATCH,NodePool"}}
	want, _, _, err := ExportSubscriptionWithProfile(nodes, "clash", &old, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := referenceProxyGroups([]string{"one", "two"}, nil)
	displayMainGroup(legacy, nil)
	if !reflect.DeepEqual(profileDoc(t, want)["proxy-groups"], profileDoc(t, mustMarshalGroups(t, legacy))["proxy-groups"]) {
		t.Fatal("legacy group order changed")
	}
	for _, raw := range [][]byte{nil, []byte(`null`)} {
		p, err := scanRuleProfile(ruleProfileScanFixture{groups: raw})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p.Groups, defaultRuleGroups()) {
			t.Fatal("NULL did not restore defaults")
		}
		got, _, _, err := ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
		if err != nil || !bytes.Equal(want, got) {
			t.Fatalf("legacy output changed: %v", err)
		}
	}
	for _, raw := range []string{`{"name":"old"}`, `{"name":"old","groups":null}`} {
		var in RuleProfileInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if err := in.Validate(); err != nil || !reflect.DeepEqual(in.Groups, defaultRuleGroups()) {
			t.Fatalf("old input %+v %v", in, err)
		}
	}
	p := DefaultRuleProfile()
	p.Groups[0].Groups[0] = "changed"
	if DefaultRuleProfile().Groups[0].Groups[0] == "changed" {
		t.Fatal("official template shares mutable groups")
	}
	old.RoutingEnabled = false
	old.Groups = []RuleGroup{{Name: "invalid"}}
	if _, _, _, err := ExportSubscriptionWithProfile(nodes, "clash", &old, nil); err != nil {
		t.Fatalf("disabled groups applied: %v", err)
	}
}

func mustMarshalGroups(t *testing.T, groups []map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"proxy-groups": groups})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAutoCountryGroups(t *testing.T) {
	p := DefaultRuleProfile()
	nodes := []Node{}
	for i, code := range []string{"JP", "HK", "JP", "", "CF", "BR"} {
		n := profileNode(int64(i+1), 1, fmt.Sprintf("节点%d", i+1))
		n.CountryCode = code
		nodes = append(nodes, n)
	}
	nodes[5].Name = "🇭🇰 香港"
	raw, _, _, err := ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc := profileDoc(t, raw)
	assertProfileReferences(t, doc)
	groups := map[string]map[string]any{}
	order := []string{}
	for _, v := range doc["proxy-groups"].([]any) {
		g := v.(map[string]any)
		groups[g["name"].(string)] = g
		order = append(order, g["name"].(string))
	}
	main := groups[SubscriptionDisplayName]["proxies"].([]any)
	want := []any{"♻️ 自动选择", "🚑 故障转移", "🇭🇰 香港", "🇯🇵 日本", "🇧🇷 巴西"}
	if !reflect.DeepEqual(main[:len(want)], want) || main[len(main)-1] != "🇭🇰 香港 (2)" {
		t.Fatalf("main group order %v", main)
	}
	if !reflect.DeepEqual(groups["🇯🇵 日本"]["proxies"], []any{"节点1", "节点3"}) || groups["🇯🇵 日本"]["type"] != "url-test" {
		t.Fatalf("japan group %v", groups["🇯🇵 日本"])
	}
	if order[len(order)-1] != "🇧🇷 巴西" {
		t.Fatalf("country groups must follow configured groups: %v", order)
	}
	// 关闭后不再生成
	for i := range p.Groups {
		p.Groups[i].CountryGroups = false
	}
	p.Groups[0].Groups = append(p.Groups[0].Groups, "🛑 拦截广告")
	raw, _, _, err = ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc = profileDoc(t, raw)
	for _, v := range doc["proxy-groups"].([]any) {
		g := v.(map[string]any)
		if strings.HasPrefix(g["name"].(string), "🇯🇵") {
			t.Fatal("country group exported while disabled")
		}
		if g["name"] == SubscriptionDisplayName {
			proxies := g["proxies"].([]any)
			if proxies[2] != "🛑 拦截广告" {
				t.Fatalf("group references must precede nodes: %v", proxies)
			}
		}
	}
}
