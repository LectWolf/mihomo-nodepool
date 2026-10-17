package pool_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLRiskUserFilterBatch(t *testing.T) {
	s := testStore(t)
	saveCommunity(t, s, func(c *pool.CommunitySettings) {
		for i := range c.LevelQuotas {
			c.LevelQuotas[i] = pool.LevelQuota{Nodes: 2, Sources: 3, Subscriptions: 2}
		}
		c.RiskExposureMultiplier, c.RiskWindowDays, c.RiskDistinctIPs = 3, 30, 2
	})
	ctx := context.Background()
	idle := account(t, s, "riskidle", 0, 0, 0)
	swapper := account(t, s, "riskswapper", 0, 0, 0)
	puller := account(t, s, "riskpuller", 0, 0, 0)
	frozen := account(t, s, "riskfrozen", 0, 0, 0)
	calm := account(t, s, "riskcalm", 0, 0, 0)
	now := time.Now().UTC()
	// 窗口上限 3×2=6，达到 80% 即标记；calm 只有 1 个曝光
	for i := 1; i <= 6; i++ {
		if _, e := s.DB.Exec(`INSERT INTO np_node_exposures(user_id,node_id,first_at,last_at) VALUES(?,?,?,?)`, swapper.ID, 900000+i, now.Add(-48*time.Hour), now); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.DB.Exec(`INSERT INTO np_node_exposures(user_id,node_id,first_at,last_at) VALUES(?,?,?,?)`, calm.ID, 900100, now.Add(-48*time.Hour), now); e != nil {
		t.Fatal(e)
	}
	day := now.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	for _, ip := range []string{"a", "b", "c"} {
		if _, e := s.DB.Exec(`INSERT INTO np_pull_stats(user_id,day,ip_hash,pulls,last_at) VALUES(?,?,?,1,?)`, puller.ID, day, ip, now); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.FreezeUser(ctx, frozen.ID, 2); e != nil {
		t.Fatal(e)
	}
	ours := []int64{idle.ID, swapper.ID, puller.ID, frozen.ID, calm.ID}
	f := pool.UserFilter{Q: "risk", IDs: ours}
	flagged, _, e := s.RiskUserIDs(ctx, f, "flagged")
	if e != nil {
		t.Fatal(e)
	}
	slices.Sort(flagged)
	want := []int64{swapper.ID, frozen.ID, puller.ID}
	slices.Sort(want)
	if !slices.Equal(flagged, want) {
		t.Fatalf("flagged %v want %v", flagged, want)
	}
	onlyFrozen, _, e := s.RiskUserIDs(ctx, f, "frozen")
	if e != nil || !slices.Equal(onlyFrozen, []int64{frozen.ID}) {
		t.Fatalf("frozen %v %v", onlyFrozen, e)
	}
	_, exclude, e := s.RiskUserIDs(ctx, f, "clean")
	if e != nil {
		t.Fatal(e)
	}
	f.ExcludeIDs = exclude
	page, e := s.Users(ctx, f, 1, 50)
	if e != nil {
		t.Fatal(e)
	}
	clean := []int64{}
	for _, u := range page["items"].([]pool.User) {
		clean = append(clean, u.ID)
	}
	if !slices.Equal(clean, []int64{idle.ID, calm.ID}) {
		t.Fatalf("clean %v", clean)
	}
	// 批量摘要与逐个计算结果一致
	all, e := s.RiskSummaries(ctx, ours)
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range ours {
		one, e := s.RiskSummaries(ctx, []int64{id})
		if e != nil || len(one[id].Flags) != len(all[id].Flags) || one[id].WindowNodes != all[id].WindowNodes {
			t.Fatalf("batch mismatch for %d: %+v vs %+v", id, one[id], all[id])
		}
	}
	if all[swapper.ID].WindowNodes != 6 || all[puller.ID].TodayIPs != 3 || all[idle.ID].NodeLimit != 2 {
		t.Fatalf("stats: %+v %+v %+v", all[swapper.ID], all[puller.ID], all[idle.ID])
	}
}
