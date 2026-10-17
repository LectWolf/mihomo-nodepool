package pool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func account(t *testing.T, s *pool.Store, name string, nodes, sources, subs int) pool.User {
	t.Helper()
	u, e := s.CreateUser(context.Background(), pool.UserInput{Username: fmt.Sprintf("%s_%d", name, time.Now().UnixNano()), Password: "test-password", NodeLimit: &nodes, SourceLimit: &sources, SubscriptionLimit: &subs})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = s.DB.Exec(`DELETE FROM np_users WHERE id=?`, u.ID) })
	return u
}
func sharedSource(t *testing.T, s *pool.Store, ctx context.Context, name string, count, max int) pool.Source {
	t.Helper()
	var raw strings.Builder
	raw.WriteString("proxies:\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&raw, " - {name: %s-%d, type: ss, server: %s-%d.example.com, port: 443, cipher: aes-128-gcm, password: secret}\n", name, i, name, i)
	}
	src, e := s.AddSource(ctx, pool.SourceInput{Name: name, Kind: "file", Content: raw.String(), MaxUsers: max})
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := pool.Parse(raw.String(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(ctx, src, raw.String(), parsed); e != nil {
		t.Fatal(e)
	}
	src, e = s.Source(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	return src
}
func TestMySQLMultiuserSourceOwnershipAndDiscovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "provider", 200, 3, 1)
	b := account(t, s, "viewer", 200, 3, 1)
	ac := pool.WithUser(ctx, a)
	bc := pool.WithUser(ctx, b)
	src := sharedSource(t, s, ac, "catalog-unique", 2, 2)
	if _, e := s.PatchSource(bc, src.ID, pool.SourcePatch{}); !errors.Is(e, pool.ErrForbidden) {
		t.Fatalf("patch ownership: %v", e)
	}
	if e := s.DeleteSource(bc, src.ID); !errors.Is(e, pool.ErrForbidden) {
		t.Fatalf("delete ownership: %v", e)
	}
	if e := s.QueueSource(bc, src.ID); !errors.Is(e, pool.ErrForbidden) {
		t.Fatalf("refresh ownership: %v", e)
	}
	mine, e := s.Sources(bc)
	if e != nil || len(mine) != 0 {
		t.Fatalf("management leak: %v %+v", e, mine)
	}
	mine, e = s.Sources(ac)
	if e != nil || len(mine) != 1 || mine[0].OwnerID != a.ID || mine[0].MaxUsers != 2 {
		t.Fatalf("own metadata: %v %+v", e, mine)
	}
	raw, _ := json.Marshal(mine)
	if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "example.com") {
		t.Fatal("credentials leaked")
	}
	before, e := s.Nodes(bc, pool.Filter{Following: true, UserID: b.ID})
	if e != nil || before.Total != 0 {
		t.Fatalf("unfollowed filter: %v", e)
	}
	if e = s.Follow(bc, a.ID, true); e != nil {
		t.Fatal(e)
	}
	page, e := s.Nodes(bc, pool.Filter{Following: true, UserID: b.ID, ProviderID: a.ID})
	if e != nil || page.Total != 2 || page.Items[0].Sources[0].OwnerID != a.ID || page.Items[0].Sources[0].ProviderName != a.DisplayName {
		t.Fatalf("provider nodes: %v %+v", e, page)
	}
	if e = s.NodePermission(bc, page.Items[0].ID); !errors.Is(e, pool.ErrForbidden) {
		t.Fatalf("foreign test permission: %v", e)
	}
	if e = s.NodePermission(ac, page.Items[0].ID); !errors.Is(e, pool.ErrForbidden) {
		t.Fatalf("owner is not admin: %v", e)
	}
	_, _ = s.DB.Exec(`UPDATE np_nodes SET country_code='JP' WHERE id=?`, page.Items[0].ID)
	_, _ = s.DB.Exec(`UPDATE np_nodes SET country_code='US' WHERE id=?`, page.Items[1].ID)
	page, e = s.Nodes(bc, pool.Filter{Countries: []string{"JP", "US"}, Country: "DE"})
	if e != nil || page.Total != 2 {
		t.Fatalf("multicountry precedence: %v %+v", e, page)
	}
	catalog, e := s.Catalog(bc, pool.CatalogFilter{Q: "catalog-unique", Following: true, PageSize: 1})
	if e != nil || catalog["total"] != 1 || len(catalog["items"].([]pool.Source)) != 1 {
		t.Fatalf("catalog: %v %+v", e, catalog)
	}
	providers, e := s.Providers(bc, pool.CatalogFilter{Q: a.Username, Following: true, PageSize: 1})
	if e != nil || providers["total"] != 1 {
		t.Fatalf("providers: %v %+v", e, providers)
	}
	p := providers["items"].([]pool.Provider)[0]
	if !p.Followed || p.ProviderPath != pool.ProviderPath(a.ID) || p.NodeCount != 2 {
		t.Fatalf("provider metadata %+v", p)
	}
	src, _ = s.Source(ctx, src.ID)
	if src.ClaimedUsers != 0 {
		t.Fatal("follow consumed claim")
	}
	enabled := false
	if _, e = s.PatchUser(ctx, a.ID, pool.UserPatch{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	page, e = s.Nodes(bc, pool.Filter{})
	if e != nil || page.Total != 0 {
		t.Fatal("disabled provider remained visible")
	}
}
func TestMySQLSubscriptionsQuotaSnapshotAndRevocation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "subscriber", 3, 1, 2)
	uc := pool.WithUser(ctx, u)
	src := sharedSource(t, s, ctx, "snapshot", 5, 1)
	saved, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "first", SourceIDs: []int64{src.ID}})
	if e != nil || !saved.Truncated || saved.SelectedCount != 3 {
		t.Fatalf("stable truncation: %+v %v", saved, e)
	}
	sub := saved.Subscription
	for i := 1; i < len(sub.Nodes); i++ {
		if sub.Nodes[i-1].NodeID >= sub.Nodes[i].NodeID {
			t.Fatal("not node ID ordered")
		}
	}
	again, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "same nodes", Nodes: append(sub.Nodes, sub.Nodes[0])})
	if e != nil || again.SelectedCount != 3 {
		t.Fatalf("account union/dedup: %+v %v", again, e)
	}
	usage, e := s.Usage(ctx, u.ID)
	if e != nil || usage.Nodes != 3 || usage.Sources != 1 || usage.Subscriptions != 2 {
		t.Fatalf("usage: %+v %v", usage, e)
	}
	src, _ = s.Source(ctx, src.ID)
	if src.ClaimedUsers != 1 {
		t.Fatalf("claims %+v", src)
	}
	if _, e = s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "third", Nodes: sub.Nodes}); !errors.Is(e, pool.ErrQuota) {
		t.Fatalf("subscription limit: %v", e)
	}
	other := sharedSource(t, s, ctx, "other-source", 1, 0)
	page, _ := s.Nodes(ctx, pool.Filter{SourceID: other.ID})
	forged := []pool.Selection{{NodeID: page.Items[0].ID, SourceID: src.ID}}
	if _, e = s.SaveSubscription(uc, sub.ID, pool.SubscriptionInput{Name: "forged", Nodes: forged}); e == nil {
		t.Fatal("forged source accepted")
	}
	out, e := s.SubscriptionNodes(ctx, sub.Token)
	if e != nil || len(out) != 3 {
		t.Fatalf("token: %d %v", len(out), e)
	}
	one := 1
	if _, e = s.PatchUser(ctx, u.ID, pool.UserPatch{NodeLimit: &one}); e != nil {
		t.Fatal(e)
	}
	out, e = s.SubscriptionNodes(ctx, sub.Token)
	if e != nil || len(out) != 1 {
		t.Fatalf("quota lowering: %d %v", len(out), e)
	}
	out, e = s.SubscriptionNodes(ctx, again.Subscription.Token)
	if e != nil || len(out) != 1 {
		t.Fatalf("account union output: %d %v", len(out), e)
	}
	// Refresh adds a new node but cannot fill the saved selection's removed node.
	_, _ = s.DB.Exec(`DELETE FROM np_source_nodes WHERE source_id=? AND node_id=?`, src.ID, sub.Nodes[0].NodeID)
	_, _ = s.DB.Exec(`UPDATE np_nodes SET blocked=1 WHERE id=?`, sub.Nodes[1].NodeID)
	_, _ = s.DB.Exec(`UPDATE np_source_nodes SET excluded=1 WHERE source_id=? AND node_id=?`, src.ID, sub.Nodes[2].NodeID)
	out, e = s.SubscriptionNodes(ctx, sub.Token)
	if e != nil || len(out) != 0 {
		t.Fatalf("snapshot/excluded/blocked: %d %v", len(out), e)
	}
	rotated, e := s.MutateSubscription(uc, sub.ID, true)
	if e != nil || rotated.Token == sub.Token {
		t.Fatal("rotation failed")
	}
	if _, e = s.SubscriptionNodes(ctx, sub.Token); e == nil {
		t.Fatal("old token accepted")
	}
	if _, e = s.MutateSubscription(uc, rotated.ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubscriptionNodes(ctx, rotated.Token); e == nil {
		t.Fatal("deleted token accepted")
	}
	enabled := false
	if _, e = s.PatchUser(ctx, u.ID, pool.UserPatch{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubscriptionNodes(ctx, again.Subscription.Token); e == nil {
		t.Fatal("disabled token accepted")
	}
	enabled = true
	_, _ = s.PatchUser(ctx, u.ID, pool.UserPatch{Enabled: &enabled})
	if _, e = s.SubscriptionNodes(ctx, again.Subscription.Token); e == nil {
		t.Fatal("reenabling resurrected token")
	}
}
func TestMySQLConcurrentClaimsAndRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "claimA", 20, 3, 3)
	b := account(t, s, "claimB", 20, 3, 3)
	free := sharedSource(t, s, ctx, "free-first", 1, 1)
	limited := sharedSource(t, s, ctx, "one-seat", 2, 1)
	users := []pool.User{a, b}
	type result struct {
		index int
		saved pool.SavedSubscription
		err   error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, u := range users {
		wg.Add(1)
		go func(i int, u pool.User) {
			defer wg.Done()
			<-start
			v, e := s.SaveSubscription(pool.WithUser(ctx, u), 0, pool.SubscriptionInput{Name: "race", SourceIDs: []int64{limited.ID}})
			results <- result{i, v, e}
		}(i, u)
	}
	close(start)
	wg.Wait()
	close(results)
	winner, loser := -1, -1
	var winSub pool.Subscription
	for r := range results {
		if r.err == nil {
			if winner != -1 {
				t.Fatal("oversubscribed")
			}
			winner = r.index
			winSub = r.saved.Subscription
		} else if errors.Is(r.err, pool.ErrQuota) {
			loser = r.index
		} else {
			t.Fatal(r.err)
		}
	}
	if winner < 0 || loser < 0 {
		t.Fatal("race did not enforce one seat")
	}
	src, _ := s.Source(ctx, limited.ID)
	if src.ClaimedUsers != 1 {
		t.Fatalf("claim count %d", src.ClaimedUsers)
	}
	loserCtx := pool.WithUser(ctx, users[loser])
	if _, e := s.SaveSubscription(loserCtx, 0, pool.SubscriptionInput{Name: "rollback", SourceIDs: []int64{free.ID, limited.ID}}); !errors.Is(e, pool.ErrQuota) {
		t.Fatalf("expected failed second source: %v", e)
	}
	free, _ = s.Source(ctx, free.ID)
	if free.ClaimedUsers != 0 {
		t.Fatal("failed transaction consumed earlier source claim")
	}
	usage, _ := s.Usage(ctx, users[loser].ID)
	if usage.Subscriptions != 0 {
		t.Fatal("failed transaction persisted subscription")
	}
	winnerCtx := pool.WithUser(ctx, users[winner])
	if _, e := s.SaveSubscription(winnerCtx, 0, pool.SubscriptionInput{Name: "duplicate account", Nodes: winSub.Nodes}); e != nil {
		t.Fatal(e)
	}
	src, _ = s.Source(ctx, limited.ID)
	if src.ClaimedUsers != 1 {
		t.Fatal("repeat account counted twice")
	}
	if _, e := s.MutateSubscription(winnerCtx, winSub.ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveSubscription(loserCtx, 0, pool.SubscriptionInput{Name: "history", SourceIDs: []int64{limited.ID}}); !errors.Is(e, pool.ErrQuota) {
		t.Fatal("deletion erased historical claim")
	}
}
func TestMySQLSourceURLMigrationPreservesData(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, e := s.AddSource(ctx, pool.SourceInput{Name: "legacy", Kind: "url", URL: "https://example.com/sub?secret=kept"})
	if e != nil {
		t.Fatal(e)
	}
	body := `proxies: [{name: legacy, type: ss, server: legacy.example.com, port: 443, cipher: aes-128-gcm, password: secret}]`
	parsed, _ := pool.Parse(body, nil)
	if e = s.ApplySource(ctx, src, body, parsed); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`ALTER TABLE np_sources DROP INDEX source_owner_url, ADD UNIQUE KEY url_hash(url_hash)`); e != nil {
		t.Fatal(e)
	}
	migrated := pool.NewStore(s.DB)
	if e = migrated.Ensure(ctx); e != nil {
		t.Fatal(e)
	}
	u := account(t, s, "migration", 200, 3, 1)
	copy, e := migrated.AddSource(pool.WithUser(ctx, u), pool.SourceInput{Name: "copy", Kind: "url", URL: src.URL})
	if e != nil || copy.OwnerID != u.ID {
		t.Fatalf("cross-owner URL duplicate blocked: %v", e)
	}
	if _, e = migrated.AddSource(pool.WithUser(ctx, u), pool.SourceInput{Name: "duplicate", Kind: "url", URL: src.URL}); e == nil {
		t.Fatal("same-owner duplicate allowed")
	}
	old, e := migrated.Source(ctx, src.ID)
	if e != nil || old.OwnerID != 1 || old.Content != body || old.URL != src.URL {
		t.Fatalf("legacy data lost: %v %+v", e, old)
	}
	page, e := migrated.Nodes(ctx, pool.Filter{})
	if e != nil || page.Total != 1 {
		t.Fatal("migration lost nodes")
	}
}

func TestMySQLAccountUnionQuotasAndLoweredSourceCapacity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	provider := account(t, s, "quotaprovider", 200, 3, 1)
	pc := pool.WithUser(ctx, provider)
	a := account(t, s, "quotaA", 2, 1, 3)
	b := account(t, s, "quotaB", 2, 1, 3)
	c := account(t, s, "quotaC", 2, 1, 3)
	ac := pool.WithUser(ctx, a)
	bc := pool.WithUser(ctx, b)
	src := sharedSource(t, s, pc, "quota-nodes", 3, 2)
	page, e := s.Nodes(ctx, pool.Filter{SourceID: src.ID})
	if e != nil {
		t.Fatal(e)
	}
	pairs := []pool.Selection{}
	for _, n := range page.Items {
		pairs = append(pairs, pool.Selection{NodeID: n.ID, SourceID: src.ID})
	}
	first, e := s.SaveSubscription(ac, 0, pool.SubscriptionInput{Name: "one", Nodes: pairs[:1]})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveSubscription(ac, 0, pool.SubscriptionInput{Name: "two", Nodes: pairs[1:2]}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveSubscription(ac, 0, pool.SubscriptionInput{Name: "over account nodes", Nodes: pairs[2:]}); !errors.Is(e, pool.ErrQuota) {
		t.Fatalf("aggregate node quota: %v", e)
	}
	other, e := s.AddSource(bc, pool.SourceInput{Name: "same nodes other owner", Kind: "file", Content: src.Content})
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := pool.Parse(src.Content, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(ctx, other, src.Content, parsed); e != nil {
		t.Fatal(e)
	}
	dedup, e := s.Nodes(ctx, pool.Filter{})
	if e != nil || dedup.Total != 3 || len(dedup.Items[0].Sources) != 2 {
		t.Fatal("cross-user global node fingerprint dedup changed")
	}
	if _, e = s.SaveSubscription(ac, 0, pool.SubscriptionInput{Name: "over account sources", Nodes: []pool.Selection{{NodeID: pairs[0].NodeID, SourceID: other.ID}}}); !errors.Is(e, pool.ErrQuota) {
		t.Fatalf("aggregate source quota: %v", e)
	}
	second, e := s.SaveSubscription(bc, 0, pool.SubscriptionInput{Name: "second claimant", Nodes: pairs[:1]})
	if e != nil {
		t.Fatal(e)
	}
	one := 1
	if _, e = s.PatchSource(pc, src.ID, pool.SourcePatch{MaxUsers: &one}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveSubscription(ac, first.Subscription.ID, pool.SubscriptionInput{Name: "existing claimant", Nodes: pairs[:1]}); e != nil {
		t.Fatalf("existing claimant rejected after lowering cap: %v", e)
	}
	if nodes, e := s.SubscriptionNodes(ctx, second.Subscription.Token); e != nil || len(nodes) != 1 {
		t.Fatalf("existing token after lowering cap: %v", e)
	}
	if _, e = s.SaveSubscription(pool.WithUser(ctx, c), 0, pool.SubscriptionInput{Name: "new claimant", Nodes: pairs[:1]}); !errors.Is(e, pool.ErrQuota) {
		t.Fatal("new account bypassed lower cap")
	}
	enabled := false
	if _, e = s.PatchSource(pc, src.ID, pool.SourcePatch{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	if nodes, e := s.SubscriptionNodes(ctx, first.Subscription.Token); e != nil || len(nodes) != 0 {
		t.Fatalf("disabled source output: %v", e)
	}
	enabled = true
	_, _ = s.PatchSource(pc, src.ID, pool.SourcePatch{Enabled: &enabled})
	enabled = false
	if _, e = s.PatchUser(ctx, provider.ID, pool.UserPatch{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	if nodes, e := s.SubscriptionNodes(ctx, first.Subscription.Token); e != nil || len(nodes) != 0 {
		t.Fatalf("disabled provider output: %v", e)
	}
	if e = s.DeleteSource(ctx, src.ID); e != nil {
		t.Fatal(e)
	}
	if nodes, e := s.SubscriptionNodes(ctx, second.Subscription.Token); e != nil || len(nodes) != 0 {
		t.Fatalf("deleted source output: %v", e)
	}
}

func TestMySQLConcurrentAccountSubscriptionLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "accountRace", 200, 3, 1)
	uc := pool.WithUser(ctx, u)
	src := sharedSource(t, s, ctx, "account-race", 1, 0)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "race", SourceIDs: []int64{src.ID}})
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	denied := 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, pool.ErrQuota) {
			denied++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatalf("account oversubscribed: successes=%d denied=%d", success, denied)
	}
}
