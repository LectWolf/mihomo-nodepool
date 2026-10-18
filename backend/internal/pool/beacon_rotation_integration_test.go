package pool_test

import (
	"context"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// 每批最多 15 个节点，下一批优先给本用户还没测过的节点
func TestMySQLBeaconRotatesUntestedNodes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "beaconrotate", 200, 5, 3)
	uc := pool.WithUser(ctx, u)
	src := sharedSource(t, s, uc, "rotatesrc", 20, 0)
	saved, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "rotate", SourceIDs: []int64{src.ID}})
	if e != nil {
		t.Fatal(e)
	}
	page, e := s.Nodes(uc, pool.Filter{PageSize: 50})
	if e != nil || len(page.Items) != 20 {
		t.Fatalf("nodes: %v %d", e, len(page.Items))
	}
	first, e := s.BeaconTokens(ctx, saved.Subscription.Token, page.Items, true)
	if e != nil || len(first) != pool.BeaconMaxNodes {
		t.Fatalf("first batch: %v %d", e, len(first))
	}
	same, e := s.BeaconTokens(ctx, saved.Subscription.Token, page.Items, true)
	if e != nil || len(same) != len(first) {
		t.Fatalf("reuse: %v", e)
	}
	for id, token := range first {
		if same[id] != token {
			t.Fatal("batch must be reused within rotation window")
		}
	}
	if _, e = s.DB.Exec(`UPDATE np_beacon_batches SET created_at=? WHERE user_id=?`, time.Now().UTC().Add(-pool.BeaconRotate-time.Minute), u.ID); e != nil {
		t.Fatal(e)
	}
	second, e := s.BeaconTokens(ctx, saved.Subscription.Token, page.Items, true)
	if e != nil || len(second) != pool.BeaconMaxNodes {
		t.Fatalf("second batch: %v %d", e, len(second))
	}
	fresh := 0
	for id := range second {
		if _, ok := first[id]; !ok {
			fresh++
		}
	}
	if fresh != 20-pool.BeaconMaxNodes {
		t.Fatalf("second batch must include all %d untested nodes, got %d", 20-pool.BeaconMaxNodes, fresh)
	}
}
