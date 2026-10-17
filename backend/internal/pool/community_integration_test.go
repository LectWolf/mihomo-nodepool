package pool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLCommunityCheckinLevelsAndIdempotence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "checkin", 20, 3, 1)
	uc := pool.WithUser(ctx, u)
	if _, err := s.DB.Exec(`UPDATE np_users SET xp=48 WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan pool.CheckinResult, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); v, err := s.CheckIn(uc, false); results <- v; failures <- err }()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	awards := 0
	for v := range results {
		if v.Awarded {
			awards++
		}
	}
	got, err := s.User(ctx, u.ID)
	if err != nil || awards != 1 || got.XP != 53 || got.Points != 2 || got.Level != 1 || got.CheckinStreak != 1 {
		t.Fatalf("idempotent checkin: %+v awards=%d %v", got, awards, err)
	}
	if got.SubscriptionExpiresAt == nil || time.Until(*got.SubscriptionExpiresAt) < 13*24*time.Hour || got.ProbeUUID == "" {
		t.Fatal("checkin did not establish lease/probe")
	}
	yesterday := time.Now().In(time.FixedZone("CST", 8*3600)).AddDate(0, 0, -1).Format("2006-01-02")
	if _, err = s.DB.Exec(`UPDATE np_users SET checkin_on=?,checkin_streak=6 WHERE id=?`, yesterday, u.ID); err != nil {
		t.Fatal(err)
	}
	bonus, err := s.CheckIn(uc, false)
	if err != nil || bonus.XPAdded != 25 || bonus.PointsAdded != 10 || bonus.User.CheckinStreak != 7 {
		t.Fatalf("weekly bonus: %+v %v", bonus, err)
	}
	for xp, level := range map[int64]int{0: 0, 49: 0, 50: 1, 199: 1, 200: 2, 999: 2, 1000: 3, 3500: 4, 10000: 5, 30000: 6} {
		if pool.LevelForXP(xp) != level {
			t.Fatalf("level %d", xp)
		}
	}
}

func TestMySQLCommunityExpiryPartialActivationAndKick(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "leaseowner", 20, 3, 3)
	a := account(t, s, "leasea", 20, 3, 2)
	b := account(t, s, "leaseb", 20, 3, 2)
	oc, ac, bc := pool.WithUser(ctx, owner), pool.WithUser(ctx, a), pool.WithUser(ctx, b)
	limited := sharedSource(t, s, oc, "limitedlease", 1, 1)
	open := sharedSource(t, s, oc, "openlease", 1, 0)
	saved, err := s.SaveSubscription(ac, 0, pool.SubscriptionInput{Name: "lease combination", SourceIDs: []int64{limited.ID, open.ID}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.SubscriptionDeliveryInfo(ctx, saved.Subscription.Token)
	if err != nil || info.ProbeUUID == "" {
		t.Fatal("probe credential", err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	if _, err = s.DB.Exec(`UPDATE np_users SET subscription_expires_at=? WHERE id=?`, past, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE np_source_seats SET expires_at=? WHERE user_id=?`, past, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubscriptionNodes(ctx, saved.Subscription.Token); !errors.Is(err, pool.ErrSubscriptionInactive) {
		t.Fatalf("expired export: %v", err)
	}
	if err = s.RenewByProbe(ctx, info.ProbeUUID); !errors.Is(err, pool.ErrSubscriptionInactive) {
		t.Fatalf("expired probe revived: %v", err)
	}
	if _, err = s.SaveSubscription(bc, 0, pool.SubscriptionInput{Name: "replacement seat", SourceIDs: []int64{limited.ID}}); err != nil {
		t.Fatal("expired capacity not released", err)
	}
	preview, err := s.CheckIn(ac, false)
	if err != nil || !preview.NeedsConfirmation || len(preview.Skipped) != 1 || preview.Skipped[0].SourceID != limited.ID {
		t.Fatalf("partial confirmation: %+v %v", preview, err)
	}
	stillExpired, _ := s.User(ctx, a.ID)
	if stillExpired.XP != 0 || stillExpired.SubscriptionExpiresAt.After(time.Now()) {
		t.Fatal("preview committed state")
	}
	unchanged, _ := s.Subscriptions(ac)
	if len(unchanged[0].SourceIDs) != 2 {
		t.Fatal("preview removed nodes")
	}
	activated, err := s.CheckIn(ac, true)
	if err != nil || activated.NeedsConfirmation || !activated.Awarded || activated.User.ProbeUUID == info.ProbeUUID {
		t.Fatalf("activation %+v %v", activated, err)
	}
	subs, _ := s.Subscriptions(ac)
	if len(subs[0].SourceIDs) != 1 || subs[0].SourceIDs[0] != open.ID || subs[0].URL != saved.Subscription.URL {
		t.Fatal("partial snapshot/link")
	}
	nodes, err := s.SubscriptionNodes(ctx, saved.Subscription.Token)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("partial nodes=%d %v", len(nodes), err)
	}
	list, err := s.SourceSubscribers(oc, open.ID, "current", 1, 20)
	if err != nil || list["total"].(int) != 1 {
		t.Fatal("owner subscriber list", err)
	}
	if _, err = s.SourceSubscribers(bc, open.ID, "all", 1, 20); !errors.Is(err, pool.ErrForbidden) {
		t.Fatalf("foreign subscriber disclosure %v", err)
	}
	if err = s.SetSourceSubscriberBlocked(oc, open.ID, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(ac, 0, pool.SubscriptionInput{Name: "bypass kick", SourceIDs: []int64{open.ID}}); err == nil {
		t.Fatal("kicked account reclaimed")
	}
	nodes, err = s.SubscriptionNodes(ctx, saved.Subscription.Token)
	if err != nil || len(nodes) != 0 {
		t.Fatal("kick left nodes", err)
	}
	if err = s.SetSourceSubscriberBlocked(oc, open.ID, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(ac, 0, pool.SubscriptionInput{Name: "unblocked", SourceIDs: []int64{open.ID}}); err != nil {
		t.Fatal("unblock failed", err)
	}
}

func TestMySQLCommunitySourceLinkMetadataLevelAndHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "historyowner", 20, 3, 2)
	u := account(t, s, "historyuser", 20, 3, 2)
	oc, uc := pool.WithUser(ctx, owner), pool.WithUser(ctx, u)
	src := sharedSource(t, s, oc, "metadatahistory", 1, 1)
	min := 2
	if _, err := s.PatchSource(oc, src.ID, pool.SourcePatch{MinLevel: &min}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "lowlevel", SourceIDs: []int64{src.ID}}); err == nil {
		t.Fatal("level restriction ignored")
	}
	xp := int64(200)
	if _, err := s.PatchUser(ctx, u.ID, pool.UserPatch{XP: &xp}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "historycombo", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	usage := pool.ParseSubscriptionUsage(fmt.Sprintf("upload=5;download=15;total=100;expire=%d", time.Now().Add(48*time.Hour).Unix()))
	raw, _ := json.Marshal(usage)
	if _, err = s.DB.Exec(`UPDATE np_sources SET subscription_usage=? WHERE id=?`, raw, src.ID); err != nil {
		t.Fatal(err)
	}
	sources, err := s.Sources(oc)
	if err != nil {
		t.Fatal(err)
	}
	var link string
	for _, v := range sources {
		if v.ID == src.ID {
			link = v.ShareURL
		}
	}
	if link == "" {
		t.Fatal("no owner link")
	}
	_, skipped, _, meta, err := s.ExportSourceLink(ctx, link[len("/link/"):], "clash")
	if err != nil || skipped != 0 || meta == nil || *meta.RemainingBytes != 80 {
		t.Fatal("source metadata/link", err)
	}
	next, err := s.RotateSourceLink(oc, src.ID)
	if err != nil || next == link {
		t.Fatal("rotate source link", err)
	}
	if _, _, _, _, err = s.ExportSourceLink(ctx, link[len("/link/"):], "clash"); err == nil {
		t.Fatal("old source link survives")
	}
	if _, err = s.MutateSubscription(uc, saved.Subscription.ID, false); err != nil {
		t.Fatal(err)
	}
	archives, err := s.SubscriptionArchives(uc, 1, 20)
	if err != nil || archives["total"].(int) != 1 {
		t.Fatal("archive not saved", err)
	}
	bytes, _ := json.Marshal(archives)
	if string(bytes) == "" || containsToken(bytes, saved.Subscription.Token) {
		t.Fatal("archive leaked old token")
	}
	// A deleted combination releases capacity immediately, while history remains.
	fresh := account(t, s, "historyfresh", 20, 3, 1)
	if _, err = s.SaveSubscription(pool.WithUser(ctx, fresh), 0, pool.SubscriptionInput{Name: "seat reused", SourceIDs: []int64{src.ID}}); err == nil {
		t.Fatal("fresh low level bypass")
	}
	min = 0
	if _, err = s.PatchSource(oc, src.ID, pool.SourcePatch{MinLevel: &min}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(pool.WithUser(ctx, fresh), 0, pool.SubscriptionInput{Name: "seat reused", SourceIDs: []int64{src.ID}}); err != nil {
		t.Fatal("delete did not release", err)
	}
	cutoff := time.Now().Add(-31 * 24 * time.Hour)
	if _, err = s.DB.Exec(`UPDATE np_subscription_archives SET archived_at=? WHERE user_id=?`, cutoff, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE np_source_events SET created_at=? WHERE user_id=?`, cutoff, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SweepCommunity(ctx); err != nil {
		t.Fatal(err)
	}
	archives, err = s.SubscriptionArchives(uc, 1, 20)
	if err != nil || archives["total"].(int) != 0 {
		t.Fatal("history retention", err)
	}
}

func TestMySQLCommunityRetentionRestartAndConfiguredLease(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	settings := testDefaults()
	settings.SubscriptionLeaseDays = 3
	settings.HistoryRetentionDays = 2
	if err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	owner := account(t, s, "retentionowner", 20, 3, 2)
	u := account(t, s, "retentionuser", 20, 3, 2)
	if remaining := time.Until(*u.SubscriptionExpiresAt); remaining < 71*time.Hour || remaining > 73*time.Hour {
		t.Fatal("new account ignored configured lease", remaining)
	}
	oc, uc := pool.WithUser(ctx, owner), pool.WithUser(ctx, u)
	src := sharedSource(t, s, oc, "retention", 1, 0)
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "archived name", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSourceSubscriberBlocked(oc, src.ID, u.ID, true); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-4 * 24 * time.Hour)
	if _, err = s.DB.Exec(`UPDATE np_source_seats SET ended_at=? WHERE user_id=?`, old, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SweepCommunity(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.NewStore(s.DB).Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(uc, saved.Subscription.ID, pool.SubscriptionInput{Name: "blocked after restart", SourceIDs: []int64{src.ID}}); err == nil {
		t.Fatal("retention or migration removed kick")
	}
	archives, err := s.SubscriptionArchives(uc, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	items := archives["items"].([]pool.SubscriptionArchive)
	if len(items) != 1 || len(items[0].NodeDetails) != 1 || items[0].NodeDetails[0].SourceName != "retention" {
		t.Fatal("display snapshot not archived")
	}
	if err = s.DeleteSource(oc, src.ID); err != nil {
		t.Fatal(err)
	}
	archives, err = s.SubscriptionArchives(uc, 1, 20)
	if err != nil || archives["items"].([]pool.SubscriptionArchive)[0].NodeDetails[0].SourceName != "retention" {
		t.Fatal("source deletion erased history metadata", err)
	}
	if _, err = s.DB.Exec(`UPDATE np_subscription_archives SET archived_at=? WHERE user_id=?`, old, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SweepCommunity(ctx); err != nil {
		t.Fatal(err)
	}
	archives, err = s.SubscriptionArchives(uc, 1, 20)
	if err != nil || archives["total"].(int) != 0 {
		t.Fatal("configured retention not applied", err)
	}
}
func containsToken(raw []byte, token string) bool {
	for i := 0; i+len(token) <= len(raw); i++ {
		if string(raw[i:i+len(token)]) == token {
			return true
		}
	}
	return false
}
