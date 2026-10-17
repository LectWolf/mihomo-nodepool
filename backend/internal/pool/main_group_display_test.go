package pool

import (
	"strings"
	"testing"
)

func TestExportShowsMainGroupDisplayName(t *testing.T) {
	p := DefaultRuleProfile()
	nodes := []Node{profileNode(1, 1, SubscriptionDisplayName), profileNode(2, 1, "普通节点")}
	raw, _, _, err := ExportSubscriptionWithProfile(nodes, "clash", &p, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc := profileDoc(t, raw)
	assertProfileReferences(t, doc)
	groups := doc["proxy-groups"].([]any)
	if groups[0].(map[string]any)["name"] != SubscriptionDisplayName {
		t.Fatalf("main group %v", groups[0])
	}
	for _, v := range groups {
		g := v.(map[string]any)
		for _, member := range g["proxies"].([]any) {
			if member == "NodePool" {
				t.Fatalf("internal name leaked in %v", g["name"])
			}
		}
	}
	for _, v := range doc["proxies"].([]any) {
		if v.(map[string]any)["name"] == SubscriptionDisplayName {
			t.Fatal("node name collides with main group")
		}
	}
	renamed := 0
	for _, r := range doc["rules"].([]any) {
		rule := r.(string)
		if strings.HasSuffix(rule, ",NodePool") || strings.Contains(rule, ",NodePool,") {
			t.Fatalf("rule target not renamed: %v", r)
		}
		if strings.HasSuffix(rule, ","+SubscriptionDisplayName) {
			renamed++
		}
	}
	if renamed == 0 {
		t.Fatal("no rule targets main group")
	}
	plain, _, err := ExportSubscription(nodes, "clash")
	if err != nil || !strings.Contains(string(plain), "MATCH,"+SubscriptionDisplayName) || strings.Contains(string(plain), "NodePool") {
		t.Fatalf("plain export: %v\n%s", err, plain)
	}
}

func TestRuleGroupsRejectDisplayName(t *testing.T) {
	groups := []RuleGroup{{Name: "NodePool", Type: "select"}, {Name: SubscriptionDisplayName, Type: "select"}}
	if _, err := normalizeRuleGroups(groups); err == nil {
		t.Fatal("display name accepted as custom group")
	}
}
