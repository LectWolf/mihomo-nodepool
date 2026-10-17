package pool_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func reapply(t *testing.T, s *pool.Store, ctx context.Context, src pool.Source, name string, count int) {
	t.Helper()
	var raw strings.Builder
	raw.WriteString("proxies:\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&raw, " - {name: %s-%d, type: ss, server: %s-%d.example.com, port: 443, cipher: aes-128-gcm, password: secret}\n", name, i, name, i)
	}
	parsed, err := pool.Parse(raw.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.Source(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplySource(ctx, fresh, raw.String(), parsed); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLFollowSourceSyncsWithoutNodeQuota(t *testing.T) {
	s := testStore(t)
	saveCommunity(t, s, func(c *pool.CommunitySettings) {
		for i := range c.LevelQuotas {
			c.LevelQuotas[i] = pool.LevelQuota{Nodes: 1, Sources: 2, Subscriptions: 3}
		}
	})
	ctx := context.Background()
	owner := account(t, s, "followowner", 100, 100, 1)
	u := account(t, s, "follower", 1, 2, 3)
	oc, uc := pool.WithUser(ctx, owner), pool.WithUser(ctx, u)
	big := sharedSource(t, s, oc, "followbig", 5, 3)
	second := sharedSource(t, s, oc, "followsecond", 2, 3)
	third := sharedSource(t, s, oc, "followthird", 2, 3)
	free := sharedSource(t, s, oc, "followfree", 3, 0)

	page, err := s.Nodes(uc, pool.Filter{SourceID: big.ID, PageSize: 20, Sort: "id"})
	if err != nil || len(page.Items) != 5 {
		t.Fatal(err)
	}
	// 同源单选节点在跟随整源时被合并掉
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "follow", FollowIDs: []int64{big.ID}, Nodes: []pool.Selection{{NodeID: page.Items[0].ID, SourceID: big.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.SelectedCount != 5 || len(saved.Subscription.Nodes) != 0 || len(saved.Subscription.FollowIDs) != 1 || len(saved.Subscription.Token) != 32 {
		t.Fatalf("follow save: %+v", saved)
	}
	nodes, err := s.SubscriptionNodes(ctx, saved.Subscription.Token)
	if err != nil || len(nodes) != 5 {
		t.Fatalf("follow export ignores node quota: %d %v", len(nodes), err)
	}
	reapply(t, s, oc, big, "followbig", 7)
	if nodes, _ = s.SubscriptionNodes(ctx, saved.Subscription.Token); len(nodes) != 7 {
		t.Fatalf("follow must sync upstream changes, got %d", len(nodes))
	}
	list, err := s.Subscriptions(uc)
	if err != nil || len(list) != 1 || list[0].FollowNodeCount != 7 || len(list[0].FollowDetails) != 1 || list[0].FollowDetails[0].Name != big.Name || !list[0].FollowDetails[0].Available {
		t.Fatalf("follow details: %+v %v", list, err)
	}
	usage, err := s.Usage(ctx, u.ID)
	if err != nil || usage.Sources != 1 || usage.Nodes != 0 {
		t.Fatalf("follow must occupy source quota only: %+v %v", usage, err)
	}
	if _, err = s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "too many", FollowIDs: []int64{second.ID, third.ID}}); !errors.Is(err, pool.ErrQuota) {
		t.Fatalf("source quota not enforced: %v", err)
	}
	other, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "free ok", FollowIDs: []int64{second.ID, free.ID}})
	if err != nil {
		t.Fatal("unlimited source must not count", err)
	}
	if nodes, _ = s.SubscriptionNodes(ctx, other.Subscription.Token); len(nodes) != 5 {
		t.Fatalf("second follow export %d", len(nodes))
	}

	// 被踢出后跟随被移除并存档，解封后可以从历史恢复
	if err = s.SetSourceSubscriberBlocked(oc, big.ID, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if nodes, _ = s.SubscriptionNodes(ctx, saved.Subscription.Token); len(nodes) != 0 {
		t.Fatal("kicked follow still exported")
	}
	archives, err := s.SubscriptionArchives(uc, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	items := archives["items"].([]pool.SubscriptionArchive)
	if len(items) == 0 || len(items[0].FollowIDs) != 1 || items[0].FollowIDs[0] != big.ID || len(items[0].FollowDetails) != 1 {
		t.Fatalf("follow not archived: %+v", items)
	}
	if err = s.SetSourceSubscriberBlocked(oc, big.ID, u.ID, false); err != nil {
		t.Fatal(err)
	}
	restored, err := s.RestoreSubscriptionArchive(uc, items[0].ID, saved.Subscription.ID)
	if err != nil || len(restored.Subscription.FollowIDs) != 1 {
		t.Fatalf("restore follow: %+v %v", restored, err)
	}
	if nodes, _ = s.SubscriptionNodes(ctx, saved.Subscription.Token); len(nodes) != 7 {
		t.Fatalf("restored follow export %d", len(nodes))
	}
	// 来源停用后跟随保留但不再输出
	off := false
	if _, err = s.PatchSource(oc, big.ID, pool.SourcePatch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if nodes, _ = s.SubscriptionNodes(ctx, saved.Subscription.Token); len(nodes) != 0 {
		t.Fatal("disabled source still exported")
	}
}

func TestMySQLFollowSourceRisk(t *testing.T) {
	s := testStore(t)
	saveCommunity(t, s, func(c *pool.CommunitySettings) {
		c.RiskEnabled = true
		c.RiskExposureMultiplier = 2
		c.RiskDailyNewSources = 0
		for i := range c.LevelQuotas {
			c.LevelQuotas[i] = pool.LevelQuota{Nodes: 1, Sources: 1, Subscriptions: 1}
		}
	})
	ctx := context.Background()
	owner := account(t, s, "riskfollowowner", 100, 100, 1)
	u := account(t, s, "riskfollower", 1, 1, 1)
	oc, uc := pool.WithUser(ctx, owner), pool.WithUser(ctx, u)
	srcs := []pool.Source{}
	for i := 0; i < 3; i++ {
		srcs = append(srcs, sharedSource(t, s, oc, fmt.Sprintf("riskfollow%d", i), 2, 5))
	}
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "risk", FollowIDs: []int64{srcs[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	// 每日新增跟随来源默认等于来源额度（1）
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", FollowIDs: []int64{srcs[1].ID}}); !errors.Is(err, pool.ErrRiskLimit) {
		t.Fatalf("daily new sources not limited: %v", err)
	}
	if _, err = s.DB.Exec(`UPDATE np_source_exposures SET first_at=? WHERE user_id=?`, time.Now().UTC().Add(-48*time.Hour), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", FollowIDs: []int64{srcs[1].ID}}); err != nil {
		t.Fatal("next day swap", err)
	}
	if _, err = s.DB.Exec(`UPDATE np_source_exposures SET first_at=? WHERE user_id=?`, time.Now().UTC().Add(-48*time.Hour), u.ID); err != nil {
		t.Fatal(err)
	}
	// 窗口内不同来源上限 = 来源额度 × 倍数 = 2
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", FollowIDs: []int64{srcs[2].ID}}); !errors.Is(err, pool.ErrRiskLimit) {
		t.Fatalf("window not limited: %v", err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", FollowIDs: []int64{srcs[0].ID}}); err != nil {
		t.Fatal("re-following exposed source blocked", err)
	}
	risks, err := s.RiskSummaries(ctx, []int64{u.ID})
	if err != nil || risks[u.ID].WindowSources != 2 || risks[u.ID].SourceWindowLimit != 2 || !strings.Contains(strings.Join(risks[u.ID].Flags, ","), "frequent_source_swaps") {
		t.Fatalf("risk summary: %+v %v", risks[u.ID], err)
	}
	if err = s.ResetExposure(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", FollowIDs: []int64{srcs[2].ID}}); err != nil {
		t.Fatal("reset did not clear source exposure", err)
	}
}

func TestMySQLProviderSourceCap(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "capowner", 100, 100, 1)
	oc := pool.WithUser(ctx, owner)
	for i := 0; i < pool.MaxOwnedSources; i++ {
		if _, err := s.DB.Exec(`INSERT INTO np_sources(owner_id,name,kind,url,content,exclude_pattern,last_error,created_at) VALUES(?,?,'file','','','','',UTC_TIMESTAMP(6))`, owner.ID, fmt.Sprintf("cap%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddSource(oc, pool.SourceInput{Name: "over", Kind: "file", Content: "proxies: []"}); err == nil || !strings.Contains(err.Error(), "100") {
		t.Fatalf("provider cap not enforced: %v", err)
	}
	admin, err := s.User(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE np_sources SET owner_id=1 WHERE owner_id=?`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddSource(pool.WithUser(ctx, admin), pool.SourceInput{Name: "admin extra", Kind: "file", Content: "proxies: []"}); err != nil {
		t.Fatal("admin must not be capped", err)
	}
}
