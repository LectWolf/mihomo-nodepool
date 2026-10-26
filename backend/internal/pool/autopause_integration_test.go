package pool_test

import (
	"context"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// TestSweepDeadSources 长期没有可用节点的来源会在所有者开启后自动停用，重新启用时清空标记
func TestSweepDeadSources(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "deadowner", 50, 10, 5)
	if e := s.SetAutoPause(ctx, owner.ID, true); e != nil {
		t.Fatal(e)
	}
	oc := pool.WithUser(ctx, owner)
	dead := sharedSource(t, s, oc, "deadsource", 2, 0)
	live := sharedSource(t, s, oc, "livesource", 2, 0)
	if _, e := s.DB.Exec(`UPDATE np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id SET n.status='pending' WHERE sn.source_id=?`, dead.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(`UPDATE np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id SET n.status='healthy' WHERE sn.source_id=?`, live.ID); e != nil {
		t.Fatal(e)
	}
	old := time.Now().UTC().AddDate(0, 0, -10)
	if _, e := s.DB.Exec(`UPDATE np_sources SET created_at=? WHERE id IN (?,?)`, old, dead.ID, live.ID); e != nil {
		t.Fatal(e)
	}
	switched, e := s.User(ctx, owner.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !switched.AutoPauseDeadSources {
		t.Fatal("开关没有保存")
	}
	// 第一次只记下 dead_since，不停用
	if n, e := s.SweepDeadSources(ctx, 7); e != nil || n != 0 {
		t.Fatalf("首次扫描 n=%d err=%v", n, e)
	}
	deadSrc, e := s.Source(ctx, dead.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !deadSrc.Enabled || deadSrc.DeadSince == nil {
		t.Fatalf("应记下 dead_since 且保持启用：enabled=%v dead_since=%v", deadSrc.Enabled, deadSrc.DeadSince)
	}
	liveSrc, e := s.Source(ctx, live.ID)
	if e != nil {
		t.Fatal(e)
	}
	if liveSrc.DeadSince != nil {
		t.Fatal("有可用节点的来源不应记 dead_since")
	}
	// 把 dead_since 推到 8 天前，再扫描就该停用
	if _, e := s.DB.Exec(`UPDATE np_sources SET dead_since=? WHERE id=?`, old, dead.ID); e != nil {
		t.Fatal(e)
	}
	if n, e := s.SweepDeadSources(ctx, 7); e != nil || n != 1 {
		t.Fatalf("到期扫描 n=%d err=%v", n, e)
	}
	deadSrc, e = s.Source(ctx, dead.ID)
	if e != nil {
		t.Fatal(e)
	}
	if deadSrc.Enabled || deadSrc.AutoPausedAt == nil || deadSrc.AutoPauseReason != pool.AutoPauseReasonNoHealthy {
		t.Fatalf("未按预期停用：enabled=%v at=%v reason=%q", deadSrc.Enabled, deadSrc.AutoPausedAt, deadSrc.AutoPauseReason)
	}
	if deadSrc.LastError == "" {
		t.Fatal("停用时应写清原因")
	}
	// 所有者重新启用后清空标记，并回到排队状态
	on := true
	if _, e := s.PatchSource(oc, dead.ID, pool.SourcePatch{Enabled: &on}); e != nil {
		t.Fatal(e)
	}
	deadSrc, e = s.Source(ctx, dead.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !deadSrc.Enabled || deadSrc.AutoPausedAt != nil || deadSrc.AutoPauseReason != "" || deadSrc.DeadSince != nil {
		t.Fatalf("重新启用后标记未清空：%+v", deadSrc)
	}
}

// TestSweepDeadSourcesRespectsSwitch 没有开启开关的用户，来源不会被自动停用
func TestSweepDeadSourcesRespectsSwitch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner := account(t, s, "offowner", 50, 10, 5)
	oc := pool.WithUser(ctx, owner)
	src := sharedSource(t, s, oc, "offsource", 1, 0)
	if _, e := s.DB.Exec(`UPDATE np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id SET n.status='pending' WHERE sn.source_id=?`, src.ID); e != nil {
		t.Fatal(e)
	}
	old := time.Now().UTC().AddDate(0, 0, -10)
	if _, e := s.DB.Exec(`UPDATE np_sources SET created_at=?,dead_since=? WHERE id=?`, old, old, src.ID); e != nil {
		t.Fatal(e)
	}
	if n, e := s.SweepDeadSources(ctx, 7); e != nil || n != 0 {
		t.Fatalf("关闭开关时不应停用 n=%d err=%v", n, e)
	}
	got, e := s.Source(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !got.Enabled {
		t.Fatal("关闭开关时来源应保持启用")
	}
}
