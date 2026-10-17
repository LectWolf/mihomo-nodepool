package pool_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLPrivateSourceSharing(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "privowner", 200, 5, 3)
	friend := account(t, s, "privfriend", 200, 5, 3)
	stranger := account(t, s, "privstranger", 200, 5, 3)
	oc, fc, sc := pool.WithUser(ctx, owner), pool.WithUser(ctx, friend), pool.WithUser(ctx, stranger)
	src := sharedSource(t, s, oc, "privsrc", 2, 1)
	yes, three := true, 3
	if _, e := s.PatchSource(oc, src.ID, pool.SourcePatch{Private: &yes, MinLevel: &three}); e != nil {
		t.Fatal(e)
	}
	nodes := func(c context.Context, f pool.Filter) []pool.Node {
		t.Helper()
		f.PageSize = 100
		v, e := s.Nodes(c, f)
		if e != nil {
			t.Fatal(e)
		}
		return v.Items
	}
	inCatalog := func(c context.Context, f pool.CatalogFilter) *pool.Source {
		t.Helper()
		f.PageSize = 50
		v, e := s.Catalog(c, f)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range v["items"].([]pool.Source) {
			if item.ID == src.ID {
				return &item
			}
		}
		return nil
	}
	ownerNodes := nodes(oc, pool.Filter{SourceID: src.ID})
	if len(ownerNodes) != 2 {
		t.Fatalf("owner should see private nodes: %d", len(ownerNodes))
	}
	pick := []pool.Selection{{NodeID: ownerNodes[0].ID, SourceID: src.ID}}
	// 未分享：节点、目录和选用都不可见
	if len(nodes(sc, pool.Filter{SourceID: src.ID})) != 0 || inCatalog(sc, pool.CatalogFilter{}) != nil {
		t.Fatal("private source leaked to stranger")
	}
	if _, e := s.SaveSubscription(sc, 0, pool.SubscriptionInput{Name: "x", Nodes: pick}); e == nil {
		t.Fatal("stranger used private source")
	}
	if _, e := s.AddSourceShare(sc, src.ID, strconv.FormatInt(friend.ID, 10)); !errors.Is(e, pool.ErrForbidden) {
		t.Fatalf("non-owner share: %v", e)
	}
	if _, e := s.AddSourceShare(oc, src.ID, strconv.FormatInt(owner.ID, 10)); e == nil {
		t.Fatal("shared to owner")
	}
	if _, e := s.AddSourceShare(oc, src.ID, strconv.FormatInt(friend.ID, 10)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AddSourceShare(oc, src.ID, friend.Username); e == nil {
		t.Fatal("duplicate share accepted")
	}
	// 已分享：专属视图可见，忽略等级要求，可以选用并导出
	got := nodes(fc, pool.Filter{Exclusive: true})
	if len(got) != 2 || got[0].Unavailable != "" {
		t.Fatalf("friend exclusive nodes: %+v", got)
	}
	if v := inCatalog(fc, pool.CatalogFilter{Exclusive: true}); v == nil || !v.SharedWithMe || !v.Private || v.ShareCount != 0 || v.Unavailable != "" {
		t.Fatalf("friend exclusive catalog: %+v", v)
	}
	if inCatalog(fc, pool.CatalogFilter{}) == nil {
		t.Fatal("shared source missing from friend catalog")
	}
	if len(nodes(sc, pool.Filter{})) != 0 {
		t.Fatal("stranger sees private nodes in full list")
	}
	saved, e := s.SaveSubscription(fc, 0, pool.SubscriptionInput{Name: "friend", Nodes: pick})
	if e != nil {
		t.Fatal(e)
	}
	// 私有来源不占来源额度，也不受人数上限影响以外的公开规则
	if out, e := s.SubscriptionNodes(ctx, saved.Subscription.Token); e != nil || len(out) != 1 {
		t.Fatalf("friend export: %v %d", e, len(out))
	}
	shares, e := s.SourceShares(oc, src.ID)
	if e != nil || len(shares) != 1 || shares[0].UserID != friend.ID || !shares[0].Seated {
		t.Fatalf("shares: %v %+v", e, shares)
	}
	if mine, e := s.Sources(oc, false); e != nil || len(mine) != 1 || !mine[0].Private || mine[0].ShareCount != 1 {
		t.Fatalf("owner sources: %v %+v", e, mine)
	}
	if _, e := s.SourceShares(fc, src.ID); !errors.Is(e, pool.ErrForbidden) {
		t.Fatalf("friend listed shares: %v", e)
	}
	// 取消分享：立即停止输出并释放席位
	if e = s.RemoveSourceShare(oc, src.ID, friend.ID); e != nil {
		t.Fatal(e)
	}
	if out, e := s.SubscriptionNodes(ctx, saved.Subscription.Token); e != nil || len(out) != 0 {
		t.Fatalf("revoked export: %v %d", e, len(out))
	}
	if len(nodes(fc, pool.Filter{SourceID: src.ID})) != 0 {
		t.Fatal("revoked friend still sees nodes")
	}
	// 公开来源改为私有：未分享的使用者失去席位与输出
	public := sharedSource(t, s, oc, "privlater", 1, 0)
	pubNodes := nodes(sc, pool.Filter{SourceID: public.ID})
	if len(pubNodes) != 1 {
		t.Fatal("public source hidden")
	}
	later, e := s.SaveSubscription(sc, 0, pool.SubscriptionInput{Name: "later", Nodes: []pool.Selection{{NodeID: pubNodes[0].ID, SourceID: public.ID}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PatchSource(oc, public.ID, pool.SourcePatch{Private: &yes}); e != nil {
		t.Fatal(e)
	}
	if out, e := s.SubscriptionNodes(ctx, later.Subscription.Token); e != nil || len(out) != 0 {
		t.Fatalf("made private export: %v %d", e, len(out))
	}
	var seated int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM np_source_seats WHERE source_id=? AND user_id=? AND state='active'`, public.ID, stranger.ID).Scan(&seated); e != nil || seated != 0 {
		t.Fatalf("seat kept after private: %v %d", e, seated)
	}
	// 管理员默认只看自己的来源，scope=all 才看全部（含私有）
	admin, e := s.CreateUser(ctx, pool.UserInput{Username: "privadmin_" + strconv.FormatInt(owner.ID, 10), Password: "test-password", Role: "admin"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = s.DB.Exec(`DELETE FROM np_users WHERE id=?`, admin.ID) })
	adm := pool.WithUser(ctx, admin)
	if mine, e := s.Sources(adm, false); e != nil || len(mine) != 0 {
		t.Fatalf("admin own sources: %v %d", e, len(mine))
	}
	all, e := s.Sources(adm, true)
	found := false
	for _, v := range all {
		found = found || v.ID == src.ID
	}
	if e != nil || !found {
		t.Fatalf("admin all sources: %v", e)
	}
	if len(nodes(adm, pool.Filter{SourceID: src.ID})) != 0 {
		t.Fatal("admin browses unshared private source")
	}
	// 提供方列表不统计私有来源
	providers, e := s.Providers(sc, pool.CatalogFilter{Q: owner.Username, PageSize: 10})
	if e != nil || providers["total"].(int) != 0 {
		t.Fatalf("private-only provider listed: %v %v", e, providers["total"])
	}
}
