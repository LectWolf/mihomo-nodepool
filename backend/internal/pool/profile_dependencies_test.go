package pool

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProfileDialerDependenciesRewriteAndFailClosed(t *testing.T) {
	p := DefaultRuleProfile()
	a, b := profileNode(1, 1, "NodePool"), profileNode(2, 1, "upstream")
	withDialer := func(n Node, target string) Node {
		t.Helper()
		var m map[string]any
		if e := json.Unmarshal(n.Config, &m); e != nil {
			t.Fatal(e)
		}
		m["dialer-proxy"] = target
		n.Config, _ = json.Marshal(m)
		return n
	}
	a = withDialer(a, "upstream")
	raw, _, _, e := ExportSubscriptionWithProfile([]Node{a, b}, "clash", &p, nil)
	if e != nil {
		t.Fatal(e)
	}
	doc := profileDoc(t, raw)
	assertProfileReferences(t, doc)
	proxies := doc["proxies"].([]any)
	if proxies[0].(map[string]any)["dialer-proxy"] != proxies[1].(map[string]any)["name"] {
		t.Fatal("dialer reference not rewritten")
	}
	_, _, _, e = ExportSubscriptionWithProfile([]Node{a}, "clash", &p, nil)
	if !errors.Is(e, ErrUnexportable) || !strings.Contains(e.Error(), "dialer-proxy") {
		t.Fatalf("missing dependency accepted: %v", e)
	}
	b = withDialer(b, "NodePool")
	_, _, _, e = ExportSubscriptionWithProfile([]Node{a, b}, "clash", &p, nil)
	if !errors.Is(e, ErrUnexportable) || !strings.Contains(e.Error(), "循环") {
		t.Fatalf("cycle accepted: %v", e)
	}
	p.RoutingEnabled = false
	b = withDialer(b, "DIRECT")
	raw, _, _, e = ExportSubscriptionWithProfile([]Node{a, b}, "clash", &p, nil)
	if e != nil {
		t.Fatal(e)
	}
	doc = profileDoc(t, raw)
	proxies = doc["proxies"].([]any)
	if proxies[0].(map[string]any)["dialer-proxy"] != proxies[1].(map[string]any)["name"] {
		t.Fatal("namespaced dialer dependency lost")
	}
}
