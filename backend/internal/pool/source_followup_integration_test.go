package pool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func applyNodes(t *testing.T, s *pool.Store, ctx context.Context, src pool.Source, nodes [][2]string) pool.Source {
	t.Helper()
	var raw strings.Builder
	raw.WriteString("proxies:\n")
	for _, n := range nodes {
		fmt.Fprintf(&raw, " - {name: %s, type: ss, server: %s, port: 443, cipher: aes-128-gcm, password: secret}\n", n[0], n[1])
	}
	parsed, e := pool.Parse(raw.String(), nil)
	if e != nil {
		t.Fatal(e)
	}
	src, e = s.Source(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(ctx, src, raw.String(), parsed); e != nil {
		t.Fatal(e)
	}
	return src
}

func nodeIDs(t *testing.T, s *pool.Store, ctx context.Context, sourceID int64) map[string][]int64 {
	t.Helper()
	page, e := s.Nodes(ctx, pool.Filter{SourceID: sourceID, PageSize: 100})
	if e != nil {
		t.Fatal(e)
	}
	out := map[string][]int64{}
	for _, n := range page.Items {
		out[n.Name] = append(out[n.Name], n.ID)
	}
	return out
}

func TestMySQLRefreshRebindsSameNameAndQueuesNodes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "rebind", 10, 2, 2)
	uc := pool.WithUser(ctx, u)
	src := sharedSource(t, s, ctx, "rebind", 1, 0)
	src = applyNodes(t, s, ctx, src, [][2]string{{"dddd", "a1.example.com"}, {"eeee", "b1.example.com"}, {"hhhh", "h1.example.com"}, {"hhhh", "h2.example.com"}, {"keep", "k.example.com"}})
	before := nodeIDs(t, s, ctx, src.ID)
	sel := []pool.Selection{}
	for _, name := range []string{"dddd", "eeee", "hhhh", "keep"} {
		for _, id := range before[name] {
			sel = append(sel, pool.Selection{NodeID: id, SourceID: src.ID})
		}
	}
	saved, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "mine", Nodes: sel})
	if e != nil || saved.SelectedCount != 5 {
		t.Fatalf("save: %+v %v", saved, e)
	}
	// keep 刚检测过，不应被重新排队
	if _, e = s.DB.Exec(`UPDATE np_nodes SET last_checked_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 HOUR),next_check_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 5 HOUR) WHERE id=?`, before["keep"][0]); e != nil {
		t.Fatal(e)
	}
	start := time.Now().UTC()
	applyNodes(t, s, ctx, src, [][2]string{{"dddd", "a2.example.com"}, {"hhhh", "h3.example.com"}, {"hhhh", "h4.example.com"}, {"gggg", "g.example.com"}, {"keep", "k.example.com"}})
	after := nodeIDs(t, s, ctx, src.ID)
	if after["dddd"][0] == before["dddd"][0] {
		t.Fatal("changed config must create a new node")
	}
	var raw []byte
	if e = s.DB.QueryRow(`SELECT nodes FROM np_subscriptions WHERE id=?`, saved.Subscription.ID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var got []pool.Selection
	if e = json.Unmarshal(raw, &got); e != nil {
		t.Fatal(e)
	}
	ids := map[int64]bool{}
	for _, v := range got {
		ids[v.NodeID] = true
	}
	for _, want := range append(append([]int64{after["dddd"][0], after["keep"][0]}, after["hhhh"]...), before["eeee"][0]) {
		if !ids[want] {
			t.Fatalf("selection %d missing after rebind: %s", want, raw)
		}
	}
	if ids[after["gggg"][0]] || len(got) != 5 {
		t.Fatalf("unexpected selection: %s", raw)
	}
	out, e := s.SubscriptionNodes(ctx, saved.Subscription.Token)
	if e != nil || len(out) != 4 {
		t.Fatalf("subscription output: %d %v", len(out), e)
	}
	// 同名节点再次出现（中间一次刷新缺失）也能换回
	applyNodes(t, s, ctx, src, [][2]string{{"dddd", "a2.example.com"}, {"keep", "k.example.com"}})
	applyNodes(t, s, ctx, src, [][2]string{{"dddd", "a2.example.com"}, {"eeee", "b2.example.com"}, {"keep", "k.example.com"}})
	final := nodeIDs(t, s, ctx, src.ID)
	if e = s.DB.QueryRow(`SELECT nodes FROM np_subscriptions WHERE id=?`, saved.Subscription.ID).Scan(&raw); e != nil || !strings.Contains(string(raw), fmt.Sprintf(`"node_id": %d`, final["eeee"][0])) && !strings.Contains(string(raw), fmt.Sprintf(`"node_id":%d`, final["eeee"][0])) {
		t.Fatalf("late rebind: %s %v", raw, e)
	}

	// 排队：未检测过的节点按 3 秒错开，刚检测过的不动
	rows, e := s.DB.Query(`SELECT n.id,n.next_check_at,n.last_checked_at IS NULL FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=? ORDER BY n.next_check_at`, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	var times []time.Time
	for rows.Next() {
		var id int64
		var at time.Time
		var fresh bool
		if e = rows.Scan(&id, &at, &fresh); e != nil {
			t.Fatal(e)
		}
		if id == final["keep"][0] {
			if at.Before(start.Add(4 * time.Hour)) {
				t.Fatalf("recently checked node requeued: %v", at)
			}
			continue
		}
		times = append(times, at)
	}
	rows.Close()
	if len(times) != 2 || times[1].Sub(times[0]) < 2900*time.Millisecond || times[0].After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("staggered queue: %v", times)
	}

	// 成功检测后的兜底间隔为周期加 1 小时
	page, _ := s.Nodes(ctx, pool.Filter{SourceID: src.ID, PageSize: 100})
	ms := 80
	if e = s.SaveProbe(ctx, page.Items[0], pool.ProbeResult{LatencyMS: &ms}); e != nil {
		t.Fatal(e)
	}
	var next time.Time
	if e = s.DB.QueryRow(`SELECT next_check_at FROM np_nodes WHERE id=?`, page.Items[0].ID).Scan(&next); e != nil {
		t.Fatal(e)
	}
	if d := next.Sub(time.Now().UTC()); d < pool.Period+time.Hour-time.Minute || d > pool.Period+time.Hour+time.Minute {
		t.Fatalf("fallback interval %v", d)
	}
}
