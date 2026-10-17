package pool_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// 规模接近线上的热点查询计时；NODEPOOL_LOAD_TEST=源数量 时运行，数据保留供复查
func TestMySQLLoadHotPaths(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("NODEPOOL_LOAD_TEST"))
	if n == 0 {
		t.Skip("set NODEPOOL_LOAD_TEST=<sources> to run")
	}
	s := testStore(t)
	ctx := context.Background()
	perSource := 120
	begin := time.Now()
	srcs := []pool.Source{}
	for i := 0; i < n; i++ {
		var raw strings.Builder
		raw.WriteString("proxies:\n")
		for j := 0; j < perSource; j++ {
			// 约三成节点在两个来源里重复
			k := i*perSource + j
			if j%3 == 0 && i > 0 {
				k = (i-1)*perSource + j + 1
			}
			fmt.Fprintf(&raw, " - {name: n%d, type: ss, server: s%d.example.com, port: 443, cipher: aes-128-gcm, password: p}\n", k, k)
		}
		src, e := s.AddSource(ctx, pool.SourceInput{Name: fmt.Sprintf("src%d", i), Kind: "file", Content: raw.String(), MaxUsers: 20 * (i % 2)})
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
		srcs = append(srcs, src)
	}
	var total int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM np_nodes`).Scan(&total)
	t.Logf("seeded %d sources, %d nodes in %v", n, total, time.Since(begin))
	_, _ = s.DB.Exec(`UPDATE np_nodes SET status=IF(id%4=0,'unhealthy','healthy'),latency_ms=IF(id%4=0,NULL,50+id%400),last_checked_at=UTC_TIMESTAMP(6),next_check_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 HOUR)`)
	s.InvalidateIndex()
	users := []pool.User{}
	for i := 0; i < 50; i++ {
		u := account(t, s, fmt.Sprintf("load%d", i), 300, 10, 3)
		uc := pool.WithUser(ctx, u)
		ids := []int64{}
		for j := 0; j < 5; j++ {
			ids = append(ids, srcs[(i*5+j)%len(srcs)].ID)
		}
		if _, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "s", SourceIDs: ids}); e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
	}
	timeit := func(name string, times int, f func() error) {
		t.Helper()
		start := time.Now()
		for i := 0; i < times; i++ {
			if e := f(); e != nil {
				t.Fatalf("%s: %v", name, e)
			}
		}
		t.Logf("%-28s %8.1f ms/op", name, float64(time.Since(start).Microseconds())/1000/float64(times))
	}
	admin, _ := s.User(ctx, 1)
	ac := pool.WithUser(ctx, admin)
	uc := pool.WithUser(ctx, users[3])
	timeit("Index rebuild", 3, func() error { s.InvalidateIndex(); _, e := s.Nodes(ac, pool.Filter{}); return e })
	timeit("Nodes admin default", 3, func() error { _, e := s.Nodes(ac, pool.Filter{}); return e })
	timeit("Nodes user default", 3, func() error { _, e := s.Nodes(uc, pool.Filter{}); return e })
	timeit("Nodes user recommend", 3, func() error { _, e := s.Nodes(uc, pool.Filter{Sort: "recommend"}); return e })
	timeit("Nodes user available", 3, func() error { _, e := s.Nodes(uc, pool.Filter{Available: true}); return e })
	timeit("Nodes user search", 3, func() error { _, e := s.Nodes(uc, pool.Filter{Q: "n12"}); return e })
	timeit("Nodes user by source", 3, func() error { _, e := s.Nodes(uc, pool.Filter{SourceID: srcs[7].ID}); return e })
	timeit("Catalog user", 3, func() error { _, e := s.Catalog(uc, pool.CatalogFilter{}); return e })
	timeit("Catalog user recommend", 3, func() error { _, e := s.Catalog(uc, pool.CatalogFilter{Sort: "recommend"}); return e })
	subs, _ := s.Subscriptions(uc)
	timeit("SubscriptionNodes", 3, func() error { _, e := s.SubscriptionNodes(ctx, subs[0].Token); return e })
	timeit("ExportSaved clash", 3, func() error {
		_, _, _, _, e := s.ExportSavedSubscriptionNodes(ctx, subs[0].Token, "clash")
		return e
	})
	timeit("Statistics", 2, func() error { _, e := s.Statistics(ac); return e })
	timeit("Sources admin", 3, func() error { _, e := s.Sources(ac); return e })
	timeit("Sources user", 3, func() error { _, e := s.Sources(uc); return e })
	timeit("TaskStatus", 3, func() error { _, e := s.TaskStatus(ctx); return e })
	timeit("Usage user", 3, func() error { _, e := s.Usage(uc, users[3].ID); return e })
	timeit("SweepCommunity", 3, func() error { return s.SweepCommunity(ctx) })
	timeit("AwardDaily", 3, func() error { return s.AwardDaily(ctx) })
	timeit("Settings", 20, func() error { _, e := s.Settings(ctx); return e })
	timeit("Options", 3, func() error { _, e := s.Options(uc); return e })
	timeit("ClaimNode (none due)", 20, func() error {
		_, e := s.ClaimNode(ctx)
		if e != nil && !strings.Contains(e.Error(), "no rows") {
			return e
		}
		return nil
	})
	_, _ = s.DB.Exec(`UPDATE np_nodes SET next_check_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 MINUTE)`)
	timeit("ParkIdleNodes", 2, func() error { return s.ParkIdleNodes(ctx) })
	timeit("RefreshProbeTiers", 2, func() error { return s.RefreshProbeTiers(ctx) })
	timeit("PruneRisk", 1, func() error { return s.PruneRisk(ctx) })
	timeit("ClaimNode (all due)", 20, func() error { _, e := s.ClaimNode(ctx); return e })
	timeit("ClaimSource", 5, func() error {
		_, e := s.ClaimSource(ctx)
		if e != nil && !strings.Contains(e.Error(), "no rows") {
			return e
		}
		return nil
	})
}
