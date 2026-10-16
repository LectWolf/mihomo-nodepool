package pool_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"gopkg.in/yaml.v3"
)

func TestMySQLRuleProfilesBindingAndActiveUsers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "rules_owner", 20, 5, 5)
	other := account(t, s, "rules_other", 20, 5, 5)
	oc, bc := pool.WithUser(ctx, owner), pool.WithUser(ctx, other)
	src := sharedSource(t, s, oc, "profile-source", 2, 0)
	items, e := s.RuleProfiles(oc)
	if e != nil || len(items) != 0 {
		t.Fatalf("profiles auto-created: %v %+v", e, items)
	}
	d := pool.DefaultRuleProfile()
	input := pool.RuleProfileInput{Name: d.Name, RoutingEnabled: d.RoutingEnabled, DNSEnabled: d.DNSEnabled, DNSServers: d.DNSServers, Rules: d.Rules}
	p, e := s.SaveRuleProfile(oc, 0, input)
	if e != nil {
		t.Fatal(e)
	}
	p2, e := s.SaveRuleProfile(oc, 0, pool.RuleProfileInput{Name: "second", Rules: []string{"MATCH,DIRECT"}})
	if e != nil || p.ID == p2.ID {
		t.Fatalf("multiple profiles %v", e)
	}
	foreign, e := s.RuleProfiles(bc)
	if e != nil || len(foreign) != 0 {
		t.Fatal("foreign profile listed")
	}
	for _, foreignCtx := range []context.Context{bc, ctx} {
		if _, e = s.SaveRuleProfile(foreignCtx, p.ID, input); !errors.Is(e, sql.ErrNoRows) {
			t.Fatalf("foreign profile edit %v", e)
		}
		if e = s.DeleteRuleProfile(foreignCtx, p.ID); !errors.Is(e, sql.ErrNoRows) {
			t.Fatalf("foreign profile delete %v", e)
		}
	}
	if _, e = s.SaveSubscription(bc, 0, pool.SubscriptionInput{Name: "foreign binding", SourceIDs: []int64{src.ID}, RuleProfileID: &p.ID}); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("foreign binding %v", e)
	}
	save := func(c context.Context, name string, profile *int64) pool.Subscription {
		t.Helper()
		v, e := s.SaveSubscription(c, 0, pool.SubscriptionInput{Name: name, SourceIDs: []int64{src.ID}, RuleProfileID: profile})
		if e != nil {
			t.Fatal(e)
		}
		return v.Subscription
	}
	first := save(oc, "first", &p.ID)
	second := save(oc, "second", nil)
	check := func(active, claimed int) {
		t.Helper()
		v, e := s.Source(ctx, src.ID)
		if e != nil || v.ActiveUsers != active || v.ClaimedUsers != claimed {
			t.Fatalf("counts want %d/%d got %+v: %v", active, claimed, v, e)
		}
		catalog, e := s.Catalog(bc, pool.CatalogFilter{})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, v := range catalog["items"].([]pool.Source) {
			if v.ID == src.ID {
				found = true
				if v.ActiveUsers != active || v.ClaimedUsers != claimed {
					t.Fatalf("catalog counts %+v", v)
				}
			}
		}
		if !found {
			t.Fatal("catalog source absent")
		}
	}
	check(1, 1)
	if e = s.DeleteRuleProfile(oc, p.ID); !errors.Is(e, pool.ErrProfileInUse) {
		t.Fatalf("referenced delete %v", e)
	}
	omitted := pool.SubscriptionInput{Name: "legacy edit", SourceIDs: []int64{src.ID}}
	updated, e := s.SaveSubscription(oc, first.ID, omitted)
	if e != nil || updated.Subscription.RuleProfileID == nil || *updated.Subscription.RuleProfileID != p.ID {
		t.Fatalf("omitted binding lost %v %+v", e, updated)
	}
	raw, _, _, e := s.ExportSavedSubscription(ctx, first.Token, "clash")
	var exported struct {
		DNS struct {
			Nameservers []string `yaml:"nameserver"`
		} `yaml:"dns"`
		Groups []struct {
			Name string `yaml:"name"`
		} `yaml:"proxy-groups"`
	}
	if e != nil {
		t.Fatal(e)
	}
	if e = yaml.Unmarshal(raw, &exported); e != nil {
		t.Fatal(e)
	}
	cloudflare := false
	for _, group := range exported.Groups {
		cloudflare = cloudflare || group.Name == "🌩️ Cloudflare"
	}
	if len(exported.DNS.Nameservers) != 3 || exported.DNS.Nameservers[0] != "223.5.5.5" || !cloudflare {
		t.Fatal("bound default DNS or strategy group absent")
	}
	input.DNSEnabled = false
	input.Rules = []string{"DOMAIN-SUFFIX,edited.example,DIRECT", "MATCH,REJECT"}
	if _, e = s.SaveRuleProfile(oc, p.ID, input); e != nil {
		t.Fatal(e)
	}
	raw, _, _, e = s.ExportSavedSubscription(ctx, first.Token, "clash")
	if e != nil || bytes.Contains(raw, []byte("dns:")) || !bytes.Contains(raw, []byte("edited.example")) {
		t.Fatalf("profile edits not reflected: %v", e)
	}
	nodes, e := s.SubscriptionNodes(ctx, first.Token)
	if e != nil {
		t.Fatal(e)
	}
	old, _, e := pool.ExportSubscription(nodes, "v2ray")
	if e != nil {
		t.Fatal(e)
	}
	v2, _, _, e := s.ExportSavedSubscription(ctx, first.Token, "v2ray")
	if e != nil || !bytes.Equal(old, v2) {
		t.Fatalf("v2ray changed %v", e)
	}
	third := save(bc, "other", nil)
	check(2, 2)
	enabled := false
	if _, e = s.PatchUser(ctx, other.ID, pool.UserPatch{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	check(1, 2)
	enabled = true
	if _, e = s.PatchUser(ctx, other.ID, pool.UserPatch{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	check(2, 2)
	if _, e = s.MutateSubscription(bc, third.ID, false); e != nil {
		t.Fatal(e)
	}
	check(1, 2)
	if _, e = s.MutateSubscription(oc, second.ID, false); e != nil {
		t.Fatal(e)
	}
	check(1, 2)
	// Explicit JSON null detaches, while an omitted field above retains binding.
	var detached pool.SubscriptionInput
	if e = json.Unmarshal([]byte(`{"name":"detached","source_ids":[`+strings.TrimSpace(jsonNumber(src.ID))+`],"rule_profile_id":null}`), &detached); e != nil {
		t.Fatal(e)
	}
	updated, e = s.SaveSubscription(oc, first.ID, detached)
	if e != nil || updated.Subscription.RuleProfileID != nil {
		t.Fatalf("null did not detach %v", e)
	}
	if e = s.DeleteRuleProfile(oc, p.ID); e != nil {
		t.Fatal(e)
	}
	raw, _, _, e = s.ExportSavedSubscription(ctx, first.Token, "clash")
	if e != nil || bytes.Contains(raw, []byte("dns:")) {
		t.Fatalf("unbound changed %v", e)
	}
	if _, e = s.MutateSubscription(oc, first.ID, false); e != nil {
		t.Fatal(e)
	}
	check(0, 2)
}
func jsonNumber(id int64) string { raw, _ := json.Marshal(id); return string(raw) }

func TestMySQLCachedSourceRoutingUsesSelectedMembership(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "cached_rules", 10, 2, 2)
	uc := pool.WithUser(ctx, u)
	body := `proxies:
 - {name: selected, type: ss, server: selected.example.com, port: 443, cipher: aes-128-gcm, password: selected-secret}
 - {name: hidden, type: ss, server: hidden.example.com, port: 443, cipher: aes-128-gcm, password: hidden-secret}
proxy-groups:
 - {name: origin, type: select, proxies: [selected, hidden]}
rules:
 - DOMAIN-SUFFIX,source-routing.example,origin
 - MATCH,origin
secret: never-copy
`
	src, e := s.AddSource(uc, pool.SourceInput{Name: "cached", Kind: "file", Content: body})
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := pool.Parse(body, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(uc, src, body, parsed); e != nil {
		t.Fatal(e)
	}
	page, e := s.Nodes(uc, pool.Filter{SourceID: src.ID})
	if e != nil {
		t.Fatal(e)
	}
	var id int64
	for _, n := range page.Items {
		if n.Name == "selected" {
			id = n.ID
		}
	}
	if id == 0 {
		t.Fatal("selected node missing")
	}
	p, e := s.SaveRuleProfile(uc, 0, pool.RuleProfileInput{Name: "source routing", RoutingEnabled: false, DNSEnabled: false})
	if e != nil {
		t.Fatal(e)
	}
	sub, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "selected only", Nodes: []pool.Selection{{NodeID: id, SourceID: src.ID}}, RuleProfileID: &p.ID})
	if e != nil {
		t.Fatal(e)
	}
	raw, _, _, e := s.ExportSavedSubscription(ctx, sub.Subscription.Token, "clash")
	if e != nil {
		t.Fatal(e)
	}
	for _, forbidden := range []string{"hidden", "hidden-secret", "never-copy", "dns:"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	if !bytes.Contains(raw, []byte("source-routing.example")) || !bytes.Contains(raw, []byte("[来源")) {
		t.Fatal("cached source routing absent")
	}
}

func TestMySQLSubscriptionDetailsPreserveUnavailableSnapshot(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "preview_owner", 20, 5, 5)
	uc := pool.WithUser(ctx, u)
	src := sharedSource(t, s, uc, "preview-source", 2, 0)
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "preview", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.Subscriptions(uc)
	if err != nil || len(items) != 1 || len(items[0].NodeDetails) != 2 {
		t.Fatalf("details %+v %v", items, err)
	}
	for _, detail := range items[0].NodeDetails {
		if detail.Name == "" || detail.SourceName != src.Name || detail.Server == "" || !detail.Available {
			t.Fatalf("missing display metadata: %+v", detail)
		}
	}
	if err = s.DeleteSource(uc, src.ID); err != nil {
		t.Fatal(err)
	}
	items, err = s.Subscriptions(uc)
	if err != nil || len(items[0].NodeDetails) != len(saved.Subscription.Nodes) {
		t.Fatalf("snapshot lost %+v %v", items, err)
	}
	for i, detail := range items[0].NodeDetails {
		if detail.Available || detail.NodeID != saved.Subscription.Nodes[i].NodeID || detail.SourceID != src.ID {
			t.Fatalf("removed selection changed %+v", detail)
		}
	}
}

func TestMySQLRuleOnlyBindingKeepsUnavailableSnapshot(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "bind_only", 20, 5, 5)
	other := account(t, s, "bind_foreign", 20, 5, 5)
	uc := pool.WithUser(ctx, u)
	src := sharedSource(t, s, uc, "bind-source", 2, 0)
	p, err := s.SaveRuleProfile(uc, 0, pool.RuleProfileInput{Name: "policy", Rules: []string{"MATCH,NodePool"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "snapshot", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSource(uc, src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindSubscriptionRule(pool.WithUser(ctx, other), saved.Subscription.ID, &p.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign subscription accepted: %v", err)
	}
	foreign, err := s.SaveRuleProfile(pool.WithUser(ctx, other), 0, pool.RuleProfileInput{Name: "foreign", Rules: []string{"MATCH,DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindSubscriptionRule(uc, saved.Subscription.ID, &foreign.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign profile accepted: %v", err)
	}
	for _, policy := range []*int64{&p.ID, nil} {
		changed, err := s.BindSubscriptionRule(uc, saved.Subscription.ID, policy)
		if err != nil || changed.URL != saved.Subscription.URL || changed.Name != saved.Subscription.Name || len(changed.Nodes) != len(saved.Subscription.Nodes) {
			t.Fatalf("rule binding changed snapshot: %+v %v", changed, err)
		}
		for i, node := range changed.Nodes {
			if node != saved.Subscription.Nodes[i] {
				t.Fatal("snapshot membership changed")
			}
		}
	}
	if err = s.DeleteRuleProfile(uc, p.ID); err != nil {
		t.Fatalf("cannot delete detached profile: %v", err)
	}
}
