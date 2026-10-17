package pool_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func saveCommunity(t *testing.T, s *pool.Store, edit func(*pool.CommunitySettings)) {
	t.Helper()
	v := testDefaults()
	v.Community = pool.DefaultCommunity()
	v.Community.RiskEnabled = false
	edit(&v.Community)
	if err := s.SaveSettings(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.SaveSettings(context.Background(), testDefaults()) })
}

func setPoints(t *testing.T, s *pool.Store, id int64, xp, points int) {
	t.Helper()
	if _, err := s.DB.Exec(`UPDATE np_users SET xp=?,points=? WHERE id=?`, xp, points, id); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLDataEncryptionMigrationAndKeyChecks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "cryptoowner", 20, 3, 3)
	oc := pool.WithUser(ctx, owner)
	plain := sharedSource(t, s, oc, "plainsrc", 2, 0)
	key := base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
	if err := pool.SetDataKey(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(`DELETE FROM np_sources`)
		_, _ = s.DB.Exec(`DELETE FROM np_nodes`)
		_ = pool.SetDataKey("")
	})
	// 新实例启动时迁移旧明文
	migrated := pool.NewStore(s.DB)
	if err := migrated.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	var rawContent, rawServer, rawConfig string
	if err := s.DB.QueryRow(`SELECT content FROM np_sources WHERE id=?`, plain.ID).Scan(&rawContent); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT n.server,n.config FROM np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id WHERE sn.source_id=? LIMIT 1`, plain.ID).Scan(&rawServer, &rawConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rawContent, "enc1:") || !strings.HasPrefix(rawServer, "enc1:") || strings.Contains(rawConfig, "example.com") {
		t.Fatalf("plaintext left: %q %q %q", rawContent[:10], rawServer, rawConfig[:20])
	}
	fresh := sharedSource(t, s, oc, "sealedsrc", 2, 0)
	if err := s.DB.QueryRow(`SELECT content FROM np_sources WHERE id=?`, fresh.ID).Scan(&rawContent); err != nil || !strings.HasPrefix(rawContent, "enc1:") {
		t.Fatal("new content not sealed", err)
	}
	full, err := migrated.Source(ctx, fresh.ID)
	if err != nil || !strings.Contains(full.Content, "sealedsrc-0.example.com") {
		t.Fatalf("decrypt source: %v", err)
	}
	page, err := migrated.Nodes(oc, pool.Filter{SourceID: fresh.ID, PageSize: 10})
	if err != nil || len(page.Items) != 2 || !strings.HasSuffix(page.Items[0].Server, ".example.com") {
		t.Fatalf("decrypt nodes %+v %v", page.Items, err)
	}
	user := account(t, s, "cryptouser", 20, 3, 3)
	saved, err := migrated.SaveSubscription(pool.WithUser(ctx, user), 0, pool.SubscriptionInput{Name: "crypto", SourceIDs: []int64{fresh.ID}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := migrated.ExportSavedSubscription(ctx, saved.Subscription.Token, "clash")
	if err != nil || !strings.Contains(string(raw), "sealedsrc-1.example.com") {
		t.Fatalf("export with sealed config: %v", err)
	}
	if err = pool.SetDataKey(""); err != nil {
		t.Fatal(err)
	}
	if err = pool.NewStore(s.DB).Ensure(ctx); !errors.Is(err, pool.ErrDataKey) {
		t.Fatalf("missing key accepted: %v", err)
	}
	if err = pool.SetDataKey(strings.Repeat("cd", 32)); err != nil {
		t.Fatal(err)
	}
	if err = pool.NewStore(s.DB).Ensure(ctx); !errors.Is(err, pool.ErrDataKey) {
		t.Fatalf("wrong key accepted: %v", err)
	}
	_ = pool.SetDataKey(key)
}

func TestMySQLEconomyLimitsShopAndDailyRewards(t *testing.T) {
	s := testStore(t)
	saveCommunity(t, s, func(c *pool.CommunitySettings) {})
	ctx := context.Background()
	u := account(t, s, "walletuser", 1, 1, 1)
	uc := pool.WithUser(ctx, u)
	w, err := s.Wallet(uc)
	if err != nil || w.Limits.Nodes != 200 || w.Limits.Sources != 3 || w.Limits.Subscriptions != 1 {
		t.Fatalf("level limits %+v %v", w.Limits, err)
	}
	if _, err = s.Boost(uc, "node"); !errors.Is(err, pool.ErrPoints) {
		t.Fatalf("boost without points: %v", err)
	}
	setPoints(t, s, u.ID, 50, 200)
	if w, err = s.Boost(uc, "node"); err != nil || w.Limits.Nodes != 200+100 || w.User.Points != 170 {
		t.Fatalf("boost %+v %d %v", w.Limits, w.User.Points, err)
	}
	if w.Limits.Subscriptions != 2 {
		t.Fatalf("Lv1 subscriptions %+v", w.Limits)
	}
	// 补签：最后签到为三天前，需要补两天
	three := time.Now().In(time.FixedZone("CST", 8*3600)).AddDate(0, 0, -3).Format("2006-01-02")
	if _, err = s.DB.Exec(`UPDATE np_users SET checkin_on=?,checkin_streak=5 WHERE id=?`, three, u.ID); err != nil {
		t.Fatal(err)
	}
	if w, err = s.Makeup(uc); err != nil || w.User.Points != 150 || w.User.CheckinStreak != 7 || w.MakeupUsed != 2 {
		t.Fatalf("makeup %+v %v", w.User, err)
	}
	if _, err = s.Makeup(uc); err == nil {
		t.Fatal("repeated makeup")
	}
	before, _ := s.User(ctx, u.ID)
	if w, err = s.Leave(uc); err != nil || !w.User.SubscriptionExpiresAt.After(before.SubscriptionExpiresAt.Add(6*24*time.Hour)) || w.LeaveUsed != 1 {
		t.Fatalf("leave %+v %v", w.User.SubscriptionExpiresAt, err)
	}
	if _, err = s.Leave(uc); err == nil {
		t.Fatal("second leave this month")
	}
	// 签到不缩短请假后的到期时间
	if _, err = s.DB.Exec(`UPDATE np_users SET checkin_on='' WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckIn(uc, true); err != nil {
		t.Fatal(err)
	}
	after, _ := s.User(ctx, u.ID)
	if after.SubscriptionExpiresAt.Before(*w.User.SubscriptionExpiresAt) {
		t.Fatal("checkin shortened leave")
	}
	setPoints(t, s, u.ID, 50, 400)
	if w, err = s.BuyTitle(uc, "共享先锋"); err != nil || w.User.Title != "共享先锋" || w.User.Points != 100 {
		t.Fatalf("title %+v %v", w.User, err)
	}
	if _, err = s.BuyTitle(uc, "共享先锋"); err == nil {
		t.Fatal("bought twice")
	}
	if w, err = s.EquipTitle(uc, ""); err != nil || w.User.Title != "" {
		t.Fatal("unequip", err)
	}
	if _, err = s.EquipTitle(uc, "彼岸摆渡人"); err == nil {
		t.Fatal("equipped unowned title")
	}

	owner := account(t, s, "rewardowner", 20, 3, 3)
	oc := pool.WithUser(ctx, owner)
	src := sharedSource(t, s, oc, "rewardsrc", 4, 0)
	if _, err = s.Tip(uc, owner.ID, 30); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Tip(uc, u.ID, 1); err == nil {
		t.Fatal("self tip")
	}
	if _, err = s.Tip(oc, u.ID, 1); err == nil {
		t.Fatal("tip to non-provider")
	}
	ownerNow, _ := s.User(ctx, owner.ID)
	if ownerNow.Points != 30 {
		t.Fatalf("tip received %d", ownerNow.Points)
	}
	for _, name := range []string{"rewarda", "rewardb"} {
		a := account(t, s, name, 20, 3, 3)
		if _, err = s.SaveSubscription(pool.WithUser(ctx, a), 0, pool.SubscriptionInput{Name: name, SourceIDs: []int64{src.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.Exec(`UPDATE np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id SET n.last_success_at=UTC_TIMESTAMP(6) WHERE sn.source_id=?`, src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE np_sources SET healthy_since=? WHERE id=?`, time.Now().UTC().Add(-31*24*time.Hour), src.ID); err != nil {
		t.Fatal(err)
	}
	ownerBefore, _ := s.User(ctx, owner.ID)
	for i := 0; i < 2; i++ {
		if err = s.AwardDaily(ctx); err != nil {
			t.Fatal(err)
		}
		_, _ = s.DB.Exec(`DELETE FROM np_daily_jobs`)
	}
	ownerAfter, _ := s.User(ctx, owner.ID)
	// 2 位使用者 ×1.5 独特加成 = 3，另有 30 天里程碑 30 与首个使用者成就 15
	if got := ownerAfter.XP - ownerBefore.XP; got != 3+30 {
		t.Fatalf("daily share xp %d", got)
	}
	if ownerAfter.Points-ownerBefore.Points != 1 {
		t.Fatalf("daily share points %d", ownerAfter.Points-ownerBefore.Points)
	}
	if ownerBefore.XP < 15+15 {
		t.Fatalf("achievements missing: %d", ownerBefore.XP)
	}
	ledger, err := s.Ledger(oc, 1, 50)
	if err != nil || ledger.Total < 4 {
		t.Fatalf("ledger %+v %v", ledger, err)
	}
}

func TestMySQLRiskExposureMaskingAndPullLimit(t *testing.T) {
	s := testStore(t)
	saveCommunity(t, s, func(c *pool.CommunitySettings) {
		c.RiskEnabled = true
		for i := range c.LevelQuotas {
			c.LevelQuotas[i] = pool.LevelQuota{Nodes: 2, Sources: 3, Subscriptions: 2}
		}
		c.RiskPullsPerHour = 2
	})
	ctx := context.Background()
	owner := account(t, s, "riskowner", 1, 1, 1)
	oc := pool.WithUser(ctx, owner)
	src := sharedSource(t, s, oc, "risksrc", 10, 0)
	u := account(t, s, "riskuser", 1, 1, 1)
	uc := pool.WithUser(ctx, u)
	page, err := s.Nodes(uc, pool.Filter{SourceID: src.ID, PageSize: 20, Sort: "id"})
	if err != nil || len(page.Items) != 10 {
		t.Fatal(err)
	}
	for _, n := range page.Items {
		if n.Server != "" || n.Port != 0 || !n.ServerHidden {
			t.Fatalf("server leaked: %+v", n)
		}
	}
	if own, _ := s.Nodes(oc, pool.Filter{SourceID: src.ID, PageSize: 1}); own.Items[0].Server == "" {
		t.Fatal("owner cannot see server")
	}
	ids := []int64{}
	for _, n := range page.Items {
		ids = append(ids, n.ID)
	}
	pick := func(a, b int) []pool.Selection {
		return []pool.Selection{{NodeID: ids[a], SourceID: src.ID}, {NodeID: ids[b], SourceID: src.ID}}
	}
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "risk", Nodes: pick(0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", Nodes: pick(0, 1)}); err != nil {
		t.Fatal("unchanged save blocked", err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", Nodes: pick(2, 3)}); !errors.Is(err, pool.ErrRiskLimit) {
		t.Fatalf("daily new nodes not limited: %v", err)
	}
	saveCommunity(t, s, func(c *pool.CommunitySettings) {
		c.RiskEnabled = true
		for i := range c.LevelQuotas {
			c.LevelQuotas[i] = pool.LevelQuota{Nodes: 2, Sources: 3, Subscriptions: 2}
		}
		c.RiskDailyNewNodes = 100
		c.RiskPullsPerHour = 2
	})
	for _, p := range [][2]int{{2, 3}, {4, 5}} {
		if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", Nodes: pick(p[0], p[1])}); err != nil {
			t.Fatal("window swap", err)
		}
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", Nodes: pick(6, 7)}); !errors.Is(err, pool.ErrRiskLimit) {
		t.Fatalf("window not limited: %v", err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", Nodes: pick(0, 2)}); err != nil {
		t.Fatal("re-using exposed nodes blocked", err)
	}
	risks, err := s.RiskSummaries(ctx, []int64{u.ID})
	if err != nil || risks[u.ID].WindowNodes != 6 || len(risks[u.ID].Flags) == 0 {
		t.Fatalf("risk summary %+v %v", risks[u.ID], err)
	}
	if err = s.ResetExposure(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FreezeUser(ctx, u.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", Nodes: pick(8, 9)}); !errors.Is(err, pool.ErrRiskLimit) {
		t.Fatalf("frozen user swapped: %v", err)
	}
	if _, err = s.FreezeUser(ctx, u.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "risk", Nodes: pick(8, 9)}); err != nil {
		t.Fatal("after reset", err)
	}
	token := saved.Subscription.Token
	for i := 0; i < 2; i++ {
		if err = s.RecordPull(ctx, token, "203.0.113.9"); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.RecordPull(ctx, token, "203.0.113.9"); !errors.Is(err, pool.ErrRateLimited) {
		t.Fatalf("pull limit: %v", err)
	}
	risks, _ = s.RiskSummaries(ctx, []int64{u.ID})
	if risks[u.ID].TodayIPs != 1 || risks[u.ID].TodayPulls != 2 {
		t.Fatalf("pull stats %+v", risks[u.ID])
	}
}

func TestMySQLFeedbackEligibilityAnonymityAndSummary(t *testing.T) {
	s := testStore(t)
	saveCommunity(t, s, func(c *pool.CommunitySettings) {})
	ctx := context.Background()
	owner := account(t, s, "fbowner", 20, 3, 3)
	oc := pool.WithUser(ctx, owner)
	src := sharedSource(t, s, oc, "fbsrc", 3, 0)
	u := account(t, s, "fbuser", 20, 3, 3)
	uc := pool.WithUser(ctx, u)
	other := account(t, s, "fbother", 20, 3, 3)
	xc := pool.WithUser(ctx, other)
	page, _ := s.Nodes(uc, pool.Filter{SourceID: src.ID, PageSize: 10, Sort: "id"})
	node := page.Items[0].ID
	if _, err := s.SubmitFeedback(uc, pool.FeedbackInput{Target: "node", NodeID: node, SourceID: src.ID, Kind: "unusable"}); !errors.Is(err, pool.ErrForbidden) {
		t.Fatalf("unsubscribed feedback: %v", err)
	}
	if _, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "fb", Nodes: []pool.Selection{{NodeID: node, SourceID: src.ID}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitFeedback(oc, pool.FeedbackInput{Target: "source", SourceID: src.ID, Kind: "ok"}); err == nil {
		t.Fatal("owner reviewed own source")
	}
	if _, err := s.SubmitFeedback(uc, pool.FeedbackInput{Target: "node", NodeID: node, SourceID: src.ID, Kind: "bogus"}); err == nil {
		t.Fatal("bad kind accepted")
	}
	first, err := s.SubmitFeedback(uc, pool.FeedbackInput{Target: "node", NodeID: node, SourceID: src.ID, Kind: "slow", Comment: "晚上很慢"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SubmitFeedback(uc, pool.FeedbackInput{Target: "node", NodeID: node, SourceID: src.ID, Kind: "unusable", Comment: strings.Repeat("断", 10)})
	if err != nil || second.ID != first.ID || second.Kind != "unusable" {
		t.Fatalf("24h replace %+v %v", second, err)
	}
	if _, err = s.SubmitFeedback(uc, pool.FeedbackInput{Target: "source", SourceID: src.ID, Kind: "many_down", Comment: strings.Repeat("长", 201)}); err == nil {
		t.Fatal("long comment accepted")
	}
	if _, err = s.SubmitFeedback(uc, pool.FeedbackInput{Target: "source", SourceID: src.ID, Kind: "many_down"}); err != nil {
		t.Fatal(err)
	}
	seen, err := s.Feedback(xc, pool.FeedbackFilter{Target: "node", NodeID: node})
	if err != nil || seen.Total != 1 || seen.Items[0].Reporter != "Lv0 用户" || seen.Items[0].ReporterID != 0 || seen.Items[0].CanResolve {
		t.Fatalf("anonymous view %+v %v", seen.Items, err)
	}
	byOwner, _ := s.Feedback(oc, pool.FeedbackFilter{Target: "all", SourceID: src.ID})
	if byOwner.Total != 2 || byOwner.Items[0].Reporter == "Lv0 用户" || !byOwner.Items[0].CanResolve {
		t.Fatalf("owner view %+v", byOwner.Items)
	}
	listed, _ := s.Nodes(xc, pool.Filter{SourceID: src.ID, PageSize: 10, Sort: "id"})
	var summary *pool.FeedbackSummary
	for _, n := range listed.Items {
		if n.ID == node {
			summary = n.Feedback
			if n.Sources[0].Feedback == nil || n.Sources[0].Feedback.Bad != 1 {
				t.Fatalf("source summary %+v", n.Sources[0].Feedback)
			}
		}
	}
	if summary == nil || summary.Bad != 1 || summary.Kinds["unusable"] != 1 {
		t.Fatalf("node summary %+v", summary)
	}
	if err = s.ResolveFeedback(xc, first.ID, true); !errors.Is(err, pool.ErrForbidden) {
		t.Fatal("stranger resolved")
	}
	if err = s.ResolveFeedback(oc, first.ID, true); err != nil {
		t.Fatal(err)
	}
	// 确认后奖励反馈人，重复确认不重复奖励
	_ = s.ResolveFeedback(oc, first.ID, false)
	_ = s.ResolveFeedback(oc, first.ID, true)
	var rewards int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM np_ledger WHERE user_id=? AND kind='feedback'`, u.ID).Scan(&rewards); err != nil || rewards != 1 {
		t.Fatalf("feedback reward %d %v", rewards, err)
	}
	listed, _ = s.Nodes(xc, pool.Filter{SourceID: src.ID, PageSize: 10})
	for _, n := range listed.Items {
		if n.ID == node && n.Feedback != nil {
			t.Fatal("resolved feedback still summarized")
		}
	}
	if err = s.DeleteFeedback(oc, first.ID); !errors.Is(err, pool.ErrForbidden) {
		t.Fatal("owner deleted feedback")
	}
	if err = s.DeleteFeedback(uc, first.ID); err != nil {
		t.Fatal(err)
	}
}
