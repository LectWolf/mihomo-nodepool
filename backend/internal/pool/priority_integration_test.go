package pool_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// 写入一条国内实测：users 个用户各自一批，ok 个能连
func fieldReport(t *testing.T, s *pool.Store, subID, nodeID int64, users []pool.User, ok int, at time.Time) {
	t.Helper()
	for i, u := range users {
		r, e := s.DB.Exec(`INSERT INTO np_beacon_batches(user_id,subscription_id,region,created_at,first_ok_at) VALUES(?,?,'CN',?,?)`, u.ID, subID, at, at)
		if e != nil {
			t.Fatal(e)
		}
		batch, _ := r.LastInsertId()
		var okAt any
		if i < ok {
			okAt = at
		}
		if _, e = s.DB.Exec(`INSERT INTO np_beacon_checks(batch_id,node_id,token,ok_at) VALUES(?,?,?,?)`, batch, nodeID, fmt.Sprintf("%032d", batch*1000+nodeID), okAt); e != nil {
			t.Fatal(e)
		}
	}
}

func TestMySQLFieldTestsRankTiersAndDefer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	viewer := account(t, s, "rank-viewer", 50, 5, 3)
	vc := pool.WithUser(ctx, viewer)
	reporters := []pool.User{}
	for i := 0; i < 3; i++ {
		reporters = append(reporters, account(t, s, fmt.Sprintf("rank-r%d", i), 50, 5, 3))
	}
	src := sharedSource(t, s, ctx, "rank", 1, 0)
	src = applyNodes(t, s, ctx, src, [][2]string{{"fast", "f.example.com"}, {"slow", "s.example.com"}, {"down", "d.example.com"}, {"blocked-cn", "b.example.com"}, {"idle", "i.example.com"}})
	ids := nodeIDs(t, s, ctx, src.ID)
	set := func(name, status string, latency any) {
		if _, e := s.DB.Exec(`UPDATE np_nodes SET status=?,latency_ms=?,last_checked_at=UTC_TIMESTAMP(6) WHERE id=?`, status, latency, ids[name][0]); e != nil {
			t.Fatal(e)
		}
	}
	set("fast", "healthy", 40)
	set("slow", "healthy", 300)
	set("down", "unhealthy", nil)
	set("blocked-cn", "healthy", 20)
	set("idle", "healthy", 100)
	saved, e := s.SaveSubscription(pool.WithUser(ctx, reporters[0]), 0, pool.SubscriptionInput{Name: "r", Nodes: []pool.Selection{{NodeID: ids["slow"][0], SourceID: src.ID}}})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Add(-time.Hour)
	// slow：国内都能连；down：本机不可用但国内能连；blocked-cn：本机最快但国内多数连不上
	fieldReport(t, s, saved.Subscription.ID, ids["slow"][0], reporters, 3, now)
	fieldReport(t, s, saved.Subscription.ID, ids["down"][0], reporters, 3, now)
	fieldReport(t, s, saved.Subscription.ID, ids["blocked-cn"][0], reporters, 0, now)
	s.InvalidateIndex()
	page, e := s.Nodes(vc, pool.Filter{Sort: "latency", SourceID: src.ID})
	if e != nil {
		t.Fatal(e)
	}
	order := []string{}
	for _, n := range page.Items {
		order = append(order, n.Name)
	}
	want := []string{"slow", "down", "fast", "idle", "blocked-cn"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("latency order %v, want %v", order, want)
	}
	for _, n := range page.Items {
		if n.Name == "slow" && (n.FieldTest == nil || n.FieldTest.OK != 3 || n.FieldTest.Days != 3 || !n.FieldTest.Paused) {
			t.Fatalf("slow field test: %+v", n.FieldTest)
		}
		if n.Name == "down" && n.FieldTest.Paused {
			t.Fatal("unhealthy node must keep server checks")
		}
	}
	// 窗口缩到 1 天后，2 天前的回报不再计入
	if _, e = s.DB.Exec(`UPDATE np_beacon_batches SET created_at=DATE_SUB(created_at,INTERVAL 2 DAY)`); e != nil {
		t.Fatal(e)
	}
	settings, _ := s.Settings(ctx)
	settings.FieldTestDays = 1
	if e = s.SaveSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	page, _ = s.Nodes(vc, pool.Filter{Sort: "latency", SourceID: src.ID})
	if page.Items[0].Name != "blocked-cn" || page.Items[0].FieldTest != nil {
		t.Fatalf("expired field tests still ranked: %s %+v", page.Items[0].Name, page.Items[0].FieldTest)
	}
	settings.FieldTestDays = 3
	_ = s.SaveSettings(ctx, settings)
	if _, e = s.DB.Exec(`UPDATE np_beacon_batches SET created_at=DATE_ADD(created_at,INTERVAL 2 DAY)`); e != nil {
		t.Fatal(e)
	}
	s.InvalidateIndex()

	// 分级：被激活订阅选中 Period，来源有人占席位 2×，没人用按闲置间隔（默认 3 天）
	if e = s.RefreshProbeTiers(ctx); e != nil {
		t.Fatal(e)
	}
	if got := s.ProbeInterval(ids["slow"][0]); got != pool.Period {
		t.Fatalf("used node interval %v", got)
	}
	if got := s.ProbeInterval(ids["idle"][0]); got != 2*pool.Period {
		t.Fatalf("seated source node interval %v", got)
	}
	if _, e = s.DB.Exec(`UPDATE np_users SET subscription_expires_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 DAY) WHERE id=?`, reporters[0].ID); e != nil {
		t.Fatal(e)
	}
	if e = s.RefreshProbeTiers(ctx); e != nil {
		t.Fatal(e)
	}
	if got := s.ProbeInterval(ids["slow"][0]); got != 3*24*time.Hour {
		t.Fatalf("inactive user's node interval %v", got)
	}
	// 闲置间隔设为 0：不定期检测
	settings, _ = s.Settings(ctx)
	zero, three := 0, 3
	settings.IdleCheckDays = &zero
	if e = s.SaveSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	if e = s.RefreshProbeTiers(ctx); e != nil {
		t.Fatal(e)
	}
	if got := s.ProbeInterval(ids["slow"][0]); got < 365*24*time.Hour {
		t.Fatalf("idle never interval %v", got)
	}
	// 重新有人使用：按新间隔从上次检测算起，已过期则立即排队
	_, _ = s.DB.Exec(`UPDATE np_nodes SET last_checked_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 2 DAY),next_check_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 5 YEAR) WHERE id=?`, ids["slow"][0])
	_, _ = s.DB.Exec(`UPDATE np_users SET subscription_expires_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 DAY) WHERE id=?`, reporters[0].ID)
	if e = s.RefreshProbeTiers(ctx); e != nil {
		t.Fatal(e)
	}
	var promoted time.Time
	_ = s.DB.QueryRow(`SELECT next_check_at FROM np_nodes WHERE id=?`, ids["slow"][0]).Scan(&promoted)
	if promoted.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("promoted node not queued: %v", promoted)
	}
	// 闲置间隔从不检测改回 3 天：排得过远的健康节点拉回到上次检测 + 3 天
	_, _ = s.DB.Exec(`UPDATE np_nodes SET status='healthy',last_checked_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 5 DAY),next_check_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 5 YEAR) WHERE id=?`, ids["fast"][0])
	settings.IdleCheckDays = &three
	if e = s.SaveSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	if e = s.RefreshProbeTiers(ctx); e != nil {
		t.Fatal(e)
	}
	var pulled time.Time
	_ = s.DB.QueryRow(`SELECT next_check_at FROM np_nodes WHERE id=?`, ids["fast"][0]).Scan(&pulled)
	if pulled.After(time.Now().UTC().Add(-2*24*time.Hour + 2*time.Hour)) {
		t.Fatalf("idle interval shortened but node not pulled back: %v", pulled)
	}
	set("fast", "healthy", 40)

	// 国内近期能连的健康节点：来源刷新不重排，领取时可推迟
	if !s.FieldTested(ctx, ids["slow"][0]) || s.FieldTested(ctx, ids["fast"][0]) {
		t.Fatal("field tested detection")
	}
	if _, e = s.DB.Exec(`UPDATE np_nodes SET last_checked_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 2 DAY),next_check_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 DAY) WHERE id IN (?,?)`, ids["slow"][0], ids["fast"][0]); e != nil {
		t.Fatal(e)
	}
	if e = s.QueueSourceNodes(ctx, src.ID, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	var slowNext, fastNext time.Time
	_ = s.DB.QueryRow(`SELECT next_check_at FROM np_nodes WHERE id=?`, ids["slow"][0]).Scan(&slowNext)
	_ = s.DB.QueryRow(`SELECT next_check_at FROM np_nodes WHERE id=?`, ids["fast"][0]).Scan(&fastNext)
	if slowNext.Before(time.Now().UTC().Add(12*time.Hour)) || fastNext.After(time.Now().UTC().Add(time.Hour)) {
		t.Fatalf("queue skip: slow %v fast %v", slowNext, fastNext)
	}
	until := time.Now().UTC().Add(pool.FieldRecent)
	if e = s.DeferNode(ctx, ids["fast"][0], until); e != nil {
		t.Fatal(e)
	}
	_ = s.DB.QueryRow(`SELECT next_check_at FROM np_nodes WHERE id=?`, ids["fast"][0]).Scan(&fastNext)
	if fastNext.Sub(until).Abs() > time.Second {
		t.Fatalf("defer: %v", fastNext)
	}
	// 暂停到最后一次能连 + 24 小时；之后有人连不上就恢复本机检测
	if paused, ok := s.FieldPause(ctx, ids["slow"][0]); !ok || paused.Sub(now.Add(pool.FieldRecent)).Abs() > time.Second {
		t.Fatalf("pause until %v %v", paused, ok)
	}
	fieldReport(t, s, saved.Subscription.ID, ids["slow"][0], reporters[:1], 0, now.Add(30*time.Minute))
	s.InvalidateIndex()
	if s.FieldTested(ctx, ids["slow"][0]) {
		t.Fatal("latest field failure must resume server checks")
	}
}

func TestMySQLClaimParkAndPrune(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src := sharedSource(t, s, ctx, "park", 1, 0)
	src = applyNodes(t, s, ctx, src, [][2]string{{"a", "a.example.com"}, {"b", "b.example.com"}, {"c", "c.example.com"}})
	ids := nodeIDs(t, s, ctx, src.ID)
	// b 被排除、c 被屏蔽：都不该被领取，整理后推后一天
	if _, e := s.DB.Exec(`UPDATE np_source_nodes SET excluded=1 WHERE node_id=?`, ids["b"][0]); e != nil {
		t.Fatal(e)
	}
	_, _ = s.DB.Exec(`UPDATE np_nodes SET blocked=1 WHERE id=?`, ids["c"][0])
	_, _ = s.DB.Exec(`UPDATE np_nodes SET pending=0,lease_until=NULL,next_check_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 HOUR)`)
	_, _ = s.DB.Exec(`UPDATE np_nodes SET pending=1 WHERE id=?`, ids["c"][0])
	n, e := s.ClaimNode(ctx)
	if e != nil || n.ID != ids["c"][0] || !n.Manual {
		t.Fatalf("manual claim first: %d %v", n.ID, e)
	}
	n, e = s.ClaimNode(ctx)
	if e != nil || n.ID != ids["a"][0] || n.Manual {
		t.Fatalf("due claim: %d %v", n.ID, e)
	}
	if _, e = s.ClaimNode(ctx); e == nil {
		t.Fatal("ineligible node claimed")
	}
	if e = s.ParkIdleNodes(ctx); e != nil {
		t.Fatal(e)
	}
	var parked time.Time
	_ = s.DB.QueryRow(`SELECT next_check_at FROM np_nodes WHERE id=?`, ids["b"][0]).Scan(&parked)
	if parked.Before(time.Now().UTC().Add(23 * time.Hour)) {
		t.Fatalf("ineligible node not parked: %v", parked)
	}

	// 清理：一年前的普通流水删除，成就保留
	u := account(t, s, "prune", 5, 1, 1)
	old := time.Now().UTC().AddDate(-2, 0, 0)
	for _, kind := range []string{"checkin", "achievement"} {
		if _, e = s.DB.Exec(`INSERT INTO np_ledger(user_id,kind,points,xp,note,uniq,created_at) VALUES(?,?,1,1,'old',?,?)`, u.ID, kind, kind+"-old", old); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.PruneRisk(ctx); e != nil {
		t.Fatal(e)
	}
	var left []string
	rows, _ := s.DB.Query(`SELECT kind FROM np_ledger WHERE user_id=? AND note='old'`, u.ID)
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		left = append(left, k)
	}
	rows.Close()
	if fmt.Sprint(left) != "[achievement]" {
		t.Fatalf("ledger prune kept %v", left)
	}
}
