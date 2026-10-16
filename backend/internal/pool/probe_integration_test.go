package pool_test

import (
	"context"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLProbeBackoffStaleBlockAndRecovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	raw := `proxies:
 - {name: probe-node, type: ss, server: probe.example.com, port: 443, cipher: aes-128-gcm, password: secret}
`
	parsed, err := pool.Parse(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	src, err := s.AddSource(ctx, pool.SourceInput{Name: "probe-source", Kind: "file", Content: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplySource(ctx, src, raw, parsed); err != nil {
		t.Fatal(err)
	}
	page, err := s.Nodes(ctx, pool.Filter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("initial node: %+v %v", page, err)
	}
	node := page.Items[0]
	for failures, want := range []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour} {
		node.Failures = failures
		if err = s.SaveProbe(ctx, node, pool.ProbeResult{Error: "timeout"}); err != nil {
			t.Fatal(err)
		}
		var gotFailures int
		var next time.Time
		if err = s.DB.QueryRow(`SELECT failures,next_check_at FROM np_nodes WHERE id=?`, node.ID).Scan(&gotFailures, &next); err != nil {
			t.Fatal(err)
		}
		if gotFailures != failures+1 || next.Before(time.Now().UTC().Add(want-time.Minute)) {
			t.Fatalf("failure %d: failures=%d next=%s want about %s", failures+1, gotFailures, next, want)
		}
	}
	if _, err = s.DB.Exec(`UPDATE np_nodes SET last_success_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 7 DAY),next_check_at=UTC_TIMESTAMP(6),blocked=0 WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	node.LastSuccessAt = func() *time.Time { v := time.Now().UTC().Add(-7 * 24 * time.Hour); return &v }()
	node.Failures = 6
	if err = s.SaveProbe(ctx, node, pool.ProbeResult{Error: "timeout"}); err != nil {
		t.Fatal(err)
	}
	page, err = s.Nodes(ctx, pool.Filter{})
	if err != nil || page.Total != 0 {
		t.Fatalf("stale node remained visible: %+v %v", page, err)
	}
	if err = s.QueueNodes(ctx, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNode(ctx)
	if err != nil || claimed.ID != node.ID {
		t.Fatalf("manual blocked-node claim failed: %v %+v", err, claimed)
	}
	delay := 42
	if err = s.SaveProbe(ctx, claimed, pool.ProbeResult{LatencyMS: &delay}); err != nil {
		t.Fatal(err)
	}
	page, err = s.Nodes(ctx, pool.Filter{})
	if err != nil || page.Total != 1 || page.Items[0].Status != "healthy" || page.Items[0].LatencyMS == nil || *page.Items[0].LatencyMS != delay {
		t.Fatalf("successful recovery failed: %+v %v", page, err)
	}
	var blocked bool
	var failures int
	var lastSuccess *time.Time
	if err = s.DB.QueryRow(`SELECT blocked,failures,last_success_at FROM np_nodes WHERE id=?`, node.ID).Scan(&blocked, &failures, &lastSuccess); err != nil {
		t.Fatal(err)
	}
	if blocked || failures != 0 || lastSuccess == nil {
		t.Fatalf("recovery fields incorrect: blocked=%v failures=%d last_success=%v", blocked, failures, lastSuccess)
	}
}
