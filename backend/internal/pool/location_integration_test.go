package pool_test

import (
	"context"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLLocationUpgradePreservesExistingData(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	raw := `proxies:
 - {name: existing-node, type: ss, server: node.example.com, port: 443, cipher: aes-128-gcm, password: alpha}
`
	parsed, err := pool.Parse(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	src, err := s.AddSource(ctx, pool.SourceInput{Name: "existing-source", Kind: "file", Content: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplySource(ctx, src, raw, parsed); err != nil {
		t.Fatal(err)
	}
	page, err := s.Nodes(ctx, pool.Filter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("initial page: %+v %v", page, err)
	}
	node := page.Items[0]
	delay := 35
	if err := s.SaveProbe(ctx, node, pool.ProbeResult{LatencyMS: &delay, ExitAttempted: true, ExitIP: "203.0.113.1", Country: "美国", CountryCode: "US"}, false); err != nil {
		t.Fatal(err)
	}
	// Reproduce the first version's table shape without dropping its records.
	if _, err := s.DB.Exec(`ALTER TABLE np_nodes DROP COLUMN region, DROP COLUMN city`); err != nil {
		t.Fatal(err)
	}
	upgraded := pool.NewStore(s.DB)
	if err := upgraded.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.NewStore(s.DB).Ensure(ctx); err != nil {
		t.Fatalf("repeated startup failed: %v", err)
	}
	page, err = upgraded.Nodes(ctx, pool.Filter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("upgraded page: %+v %v", page, err)
	}
	got := page.Items[0]
	if got.ID != node.ID || got.LatencyMS == nil || *got.LatencyMS != delay || got.ExitIP != "203.0.113.1" || len(got.Sources) != 1 || got.Region != "" || got.City != "" {
		t.Fatalf("upgrade changed existing data: %+v", got)
	}
	if err := upgraded.SaveProbe(ctx, got, pool.ProbeResult{LatencyMS: &delay, ExitAttempted: true, ExitIP: "203.0.113.1", Country: "美国", CountryCode: "US", Region: "加州", City: "洛杉矶"}, false); err != nil {
		t.Fatal(err)
	}
	page, err = upgraded.Nodes(ctx, pool.Filter{Country: "US"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Region != "加州" || page.Items[0].City != "洛杉矶" {
		t.Fatalf("location round trip failed: %+v %v", page, err)
	}
	if err := upgraded.SaveProbe(ctx, page.Items[0], pool.ProbeResult{LatencyMS: &delay, ExitAttempted: true, ExitError: "lookup failed"}, false); err != nil {
		t.Fatal(err)
	}
	page, err = upgraded.Nodes(ctx, pool.Filter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].City != "洛杉矶" {
		t.Fatalf("failed lookup lost old region: %+v %v", page, err)
	}
	if err := upgraded.SaveProbe(ctx, page.Items[0], pool.ProbeResult{LatencyMS: &delay, ExitAttempted: true, ExitIP: "203.0.113.1"}, false); err != nil {
		t.Fatal(err)
	}
	page, err = upgraded.Nodes(ctx, pool.Filter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].City != "洛杉矶" || page.Items[0].CountryCode != "US" {
		t.Fatalf("partial fallback erased known geography: %+v %v", page, err)
	}
	if err := upgraded.SaveProbe(ctx, page.Items[0], pool.ProbeResult{LatencyMS: &delay, ExitAttempted: true, ExitIP: "203.0.113.2"}, false); err != nil {
		t.Fatal(err)
	}
	page, err = upgraded.Nodes(ctx, pool.Filter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].City != "" || page.Items[0].Region != "" || page.Items[0].CountryCode != "" {
		t.Fatalf("new exit kept stale geography: %+v %v", page, err)
	}
}
