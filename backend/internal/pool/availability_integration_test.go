package pool_test

import (
	"context"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLNodeAvailabilityAndCatalogStats(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	provider := account(t, s, "availprovider", 200, 5, 3)
	first := account(t, s, "availfirst", 200, 5, 3)
	late := account(t, s, "availlate", 200, 5, 3)
	pc, fc, lc := pool.WithUser(ctx, provider), pool.WithUser(ctx, first), pool.WithUser(ctx, late)
	full := sharedSource(t, s, pc, "fullsrc", 2, 1)
	open := sharedSource(t, s, pc, "opensrc", 2, 0)
	high := sharedSource(t, s, pc, "highsrc", 1, 0)
	three := 3
	if _, e := s.PatchSource(pc, high.ID, pool.SourcePatch{MinLevel: &three}); e != nil {
		t.Fatal(e)
	}
	page, e := s.Nodes(fc, pool.Filter{SourceID: full.ID})
	if e != nil || len(page.Items) == 0 {
		t.Fatal(e)
	}
	if _, e = s.SaveSubscription(fc, 0, pool.SubscriptionInput{Name: "seat", Nodes: []pool.Selection{{NodeID: page.Items[0].ID, SourceID: full.ID}}}); e != nil {
		t.Fatal(e)
	}
	reasons := func(c context.Context, f pool.Filter) map[int64]string {
		t.Helper()
		f.PageSize = 100
		v, e := s.Nodes(c, f)
		if e != nil {
			t.Fatal(e)
		}
		out := map[int64]string{}
		for _, n := range v.Items {
			out[n.Sources[0].ID] = n.Unavailable
		}
		return out
	}
	// 已占席位的用户仍可选；后来者看到席位已满与等级不足，且排在可选节点之后
	if got := reasons(fc, pool.Filter{}); got[full.ID] != "" {
		t.Fatalf("seated user blocked: %v", got)
	}
	lateAll, e := s.Nodes(lc, pool.Filter{PageSize: 100})
	if e != nil {
		t.Fatal(e)
	}
	got := map[int64]string{}
	seenBlocked := false
	for _, n := range lateAll.Items {
		got[n.Sources[0].ID] = n.Unavailable
		if n.Unavailable != "" {
			seenBlocked = true
		} else if seenBlocked {
			t.Fatal("selectable node listed after unavailable ones")
		}
	}
	if got[full.ID] != "full" || got[high.ID] != "level" || got[open.ID] != "" {
		t.Fatalf("late reasons: %v", got)
	}
	if only := reasons(lc, pool.Filter{Available: true}); len(only) != 1 || only[open.ID] != "" {
		t.Fatalf("available filter: %v", only)
	}
	if p := reasons(pc, pool.Filter{}); p[high.ID] != "" {
		t.Fatalf("owner should bypass level: %v", p)
	}
	// 推荐排序可用，新节点带标记
	rec, e := s.Nodes(lc, pool.Filter{Sort: "recommend", PageSize: 100})
	if e != nil || len(rec.Items) == 0 || !rec.Items[0].Fresh {
		t.Fatalf("recommend sort: %v", e)
	}
	catalog, e := s.Catalog(lc, pool.CatalogFilter{Sort: "recommend", PageSize: 50})
	if e != nil {
		t.Fatal(e)
	}
	items := catalog["items"].([]pool.Source)
	byID := map[int64]pool.Source{}
	for _, v := range items {
		byID[v.ID] = v
	}
	if byID[full.ID].Unavailable != "full" || byID[open.ID].Unavailable != "" || !byID[open.ID].Fresh || len(byID[open.ID].Countries) > 8 {
		t.Fatalf("catalog availability: %+v", byID)
	}
	if items[len(items)-1].Unavailable == "" {
		t.Fatal("unavailable sources should sort last")
	}
	avail, e := s.Catalog(lc, pool.CatalogFilter{Available: true, PageSize: 50})
	if e != nil || avail["total"].(int) != 1 {
		t.Fatalf("catalog available filter: %v %v", avail["total"], e)
	}
}
