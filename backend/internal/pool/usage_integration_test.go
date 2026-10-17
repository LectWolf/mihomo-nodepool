package pool_test

import (
	"context"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLHourlySnapshotsTrafficAndProviderTrend(t *testing.T) {
	s := testStore(t)
	saveCommunity(t, s, func(c *pool.CommunitySettings) {})
	ctx := context.Background()
	owner := account(t, s, "trendowner", 20, 3, 3)
	oc := pool.WithUser(ctx, owner)
	src := sharedSource(t, s, oc, "trendsrc", 3, 0)
	hour := time.Now().UTC().Truncate(time.Hour)
	for i, used := range []int64{1000, 4000, 500} {
		if _, err := s.DB.Exec(`UPDATE np_sources SET subscription_usage=JSON_OBJECT('upload_bytes',?,'download_bytes',?) WHERE id=?`, used/2, used-used/2, src.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.SnapshotSources(ctx, hour.Add(time.Duration(i-2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	traffic, err := s.TrafficStatistics(ctx)
	if err != nil || len(traffic.Hourly) != 24 || len(traffic.Daily) != 30 {
		t.Fatalf("traffic shape %v %+v", err, traffic)
	}
	var total int64
	for _, p := range traffic.Hourly {
		total += p.Bytes
	}
	// 1000→4000 增量 3000；4000→500 视为流量重置，计 500
	if total != 3500 {
		t.Fatalf("hourly traffic %d", total)
	}
	trend, err := s.ProviderTrend(ctx, owner.ID, 7)
	if err != nil || len(trend) != 7 {
		t.Fatalf("trend %v %d", err, len(trend))
	}
	var bytes int64
	for _, p := range trend {
		bytes += p.Bytes
	}
	if bytes != 3500 || trend[6].Nodes == 0 {
		t.Fatalf("trend %+v", trend)
	}
	// 来源上架前没有快照的日子不应计入健康度平均
	if !trend[6].Sampled || trend[0].Sampled {
		t.Fatalf("sampled flags %+v", trend)
	}
	other := account(t, s, "trendother", 20, 3, 3)
	if v, _ := s.ProviderTrend(ctx, other.ID, 7); v[6].Bytes != 0 || v[6].Nodes != 0 {
		t.Fatal("trend leaked other provider")
	}
}
