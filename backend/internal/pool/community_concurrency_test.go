package pool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLCommunityProbeWaitingAcrossExpiryDoesNotReviveSeat(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "probeowner", 20, 3, 1)
	a := account(t, s, "probelate", 20, 3, 1)
	b := account(t, s, "probenext", 20, 3, 1)
	src := sharedSource(t, s, pool.WithUser(ctx, owner), "probeexpiry", 1, 1)
	saved, err := s.SaveSubscription(pool.WithUser(ctx, a), 0, pool.SubscriptionInput{Name: "late probe", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.SubscriptionDeliveryInfo(ctx, saved.Subscription.Token)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(500 * time.Millisecond)
	if _, err = s.DB.Exec(`UPDATE np_users SET subscription_expires_at=? WHERE id=?`, expiry, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE np_source_seats SET expires_at=? WHERE user_id=?`, expiry, a.ID); err != nil {
		t.Fatal(err)
	}
	blocker, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var locked int64
	if err = blocker.QueryRow(`SELECT id FROM np_sources WHERE id=? FOR UPDATE`, src.ID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- s.RenewByProbe(ctx, info.ProbeUUID) }()
	time.Sleep(650 * time.Millisecond)
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, pool.ErrSubscriptionInactive) {
			t.Fatal("waiting probe revived expired capacity", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe lock did not resolve")
	}
	if _, err = s.SaveSubscription(pool.WithUser(ctx, b), 0, pool.SubscriptionInput{Name: "new claimant", SourceIDs: []int64{src.ID}}); err != nil {
		t.Fatal("expired seat was not available", err)
	}
	var active int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM np_source_seats WHERE source_id=? AND state='active' AND expires_at>UTC_TIMESTAMP(6)`, src.ID).Scan(&active); err != nil || active != 1 {
		t.Fatal("capacity exceeded", active, err)
	}
}

func TestMySQLCommunityDisabledAccountReactivationChecksCapacity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "disabledowner", 20, 3, 1)
	a := account(t, s, "disableda", 20, 3, 1)
	b := account(t, s, "disabledb", 20, 3, 1)
	src := sharedSource(t, s, pool.WithUser(ctx, owner), "disabledcapacity", 1, 1)
	saved, err := s.SaveSubscription(pool.WithUser(ctx, a), 0, pool.SubscriptionInput{Name: "a", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.SubscriptionDeliveryInfo(ctx, saved.Subscription.Token)
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	if _, err = s.PatchUser(ctx, a.ID, pool.UserPatch{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSubscription(pool.WithUser(ctx, b), 0, pool.SubscriptionInput{Name: "b", SourceIDs: []int64{src.ID}}); err != nil {
		t.Fatal("disabled seat not released", err)
	}
	enabled = true
	if _, err = s.PatchUser(ctx, a.ID, pool.UserPatch{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	items, err := s.Subscriptions(pool.WithUser(ctx, a))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Nodes) != 1 || items[0].Token == saved.Subscription.Token {
		t.Fatal("reenabling changed snapshot or revived token")
	}
	nodes, err := s.SubscriptionNodes(ctx, items[0].Token)
	if err != nil || len(nodes) != 0 {
		t.Fatal("reenabling restored released seat", err)
	}
	if err = s.RenewByProbe(ctx, info.ProbeUUID); err == nil {
		t.Fatal("old probe survived account suspension")
	}
	preview, err := s.CheckIn(pool.WithUser(ctx, a), false)
	if err != nil || !preview.NeedsConfirmation || len(preview.Skipped) != 1 || preview.Skipped[0].Reason != "来源席位已满" {
		t.Fatal("reactivation bypassed capacity", preview, err)
	}
}
