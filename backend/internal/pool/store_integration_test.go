package pool_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/go-sql-driver/mysql"
)

func testStore(t *testing.T) *pool.Store {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set MYSQL_TEST_DSN to run real MySQL integration tests")
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(cfg.DBName, "nodepool_test") {
		t.Fatal("integration tests require a dedicated nodepool_test database")
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	store := pool.NewStore(db)
	if e = store.Ensure(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{`DELETE FROM np_source_nodes`, `DELETE FROM np_sources`, `DELETE FROM np_nodes`} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if e = store.SaveSettings(context.Background(), pool.Defaults()); e != nil {
		t.Fatal(e)
	}
	return store
}
func TestMySQLImportDedupExclusionAndRecovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	engine := core.New()
	content := `proxies:
 - {name: Japan, type: ss, server: node.example.com, port: 443, cipher: aes-128-gcm, password: alpha}
 - {name: 剩余流量, type: ss, server: info.example.com, port: 443, cipher: aes-128-gcm, password: beta}
`
	parsed, e := pool.Parse(content, engine.Validate)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.AddSource(ctx, pool.SourceInput{Name: "A", Kind: "file", Content: content})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(ctx, a, content, parsed); e != nil {
		t.Fatal(e)
	}
	b, e := s.AddSource(ctx, pool.SourceInput{Name: "B", Kind: "links", Content: content})
	if e != nil {
		t.Fatal(e)
	}
	renamed := parsed
	renamed.Nodes = append([]pool.ParsedNode{}, parsed.Nodes...)
	renamed.Nodes[0].Name = "Alias"
	if e = s.ApplySource(ctx, b, content, renamed); e != nil {
		t.Fatal(e)
	}
	page, e := s.Nodes(ctx, pool.Filter{})
	if e != nil {
		t.Fatal(e)
	}
	if page.Total != 1 || len(page.Items[0].Sources) != 2 {
		t.Fatalf("cross-source dedup/exclusion failed: %+v", page)
	}
	node := page.Items[0]
	delay := 42
	if e = s.SaveProbe(ctx, node, pool.ProbeResult{LatencyMS: &delay, ExitAttempted: true, ExitIP: "203.0.113.1", Country: "Japan", CountryCode: "JP"}, false); e != nil {
		t.Fatal(e)
	}
	page, e = s.Nodes(ctx, pool.Filter{Country: "JP", MaxLatency: 50, Q: "Alias", Type: "ss", SourceID: b.ID, Sort: "latency"})
	if e != nil || page.Total != 1 {
		t.Fatalf("filters failed: %v %+v", e, page)
	}
	if e = s.FailSource(ctx, a, "测试刷新失败"); e != nil {
		t.Fatal(e)
	}
	page, e = s.Nodes(ctx, pool.Filter{})
	if e != nil || page.Total != 1 {
		t.Fatal("failed refresh removed nodes")
	}
	// A revision change during fetch must not restore stale content.
	exclude := ".*"
	a2, e := s.PatchSource(ctx, a.ID, pool.SourcePatch{ExcludePattern: &exclude})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(ctx, a, content, parsed); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(ctx, a2, content, parsed); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteSource(ctx, b.ID); e != nil {
		t.Fatal(e)
	}
	page, e = s.Nodes(ctx, pool.Filter{})
	if e != nil || page.Total != 0 {
		t.Fatal("excluded nodes remained visible")
	}
	exclude = ""
	a2, e = s.PatchSource(ctx, a.ID, pool.SourcePatch{ExcludePattern: &exclude})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(ctx, a2, content, parsed); e != nil {
		t.Fatal(e)
	}
	page, e = s.Nodes(ctx, pool.Filter{})
	if e != nil || page.Total != 1 || page.Items[0].ID != node.ID || page.Items[0].LatencyMS == nil || *page.Items[0].LatencyMS != 42 {
		t.Fatalf("restore lost node identity/history: %v %+v", e, page)
	}
	if e = s.QueueNodes(ctx, []int64{node.ID}); e != nil {
		t.Fatal(e)
	}
	claimed, e := s.ClaimNode(ctx)
	if e != nil || claimed.ID != node.ID {
		t.Fatalf("claim failed %v", e)
	}
	if _, e = s.ClaimNode(ctx); e != sql.ErrNoRows {
		t.Fatal("same leased node claimed twice")
	}
	if _, e = s.DB.Exec(`UPDATE np_nodes SET lease_until=DATE_SUB(UTC_TIMESTAMP(),INTERVAL 5 MINUTE),next_check_at=DATE_SUB(UTC_TIMESTAMP(),INTERVAL 5 MINUTE) WHERE id=?`, node.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimNode(ctx); e != nil {
		t.Fatal("expired lease not recovered")
	}
	settings := pool.Defaults()
	settings.ExcludePattern = ".*"
	if e = s.SaveSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	src, e := s.ClaimSource(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if !src.Cached {
		t.Fatal("rule changes must use cache")
	}
	if e = s.ApplySource(ctx, src, content, parsed); e != nil {
		t.Fatal(e)
	}
	page, e = s.Nodes(ctx, pool.Filter{})
	if e != nil || page.Total != 0 {
		t.Fatal("global exclusion failed")
	}
}
func TestMySQLCapacityPagination(t *testing.T) {
	if os.Getenv("NODEPOOL_CAPACITY_TEST") != "1" {
		t.Skip("set NODEPOOL_CAPACITY_TEST=1 for 500-source / 10000-node acceptance")
	}
	s := testStore(t)
	ctx := context.Background()
	start := time.Now()
	for source := 0; source < 500; source++ {
		var content strings.Builder
		content.WriteString("proxies:\n")
		for n := 0; n < 20; n++ {
			fmt.Fprintf(&content, " - {name: node-%d, type: ss, server: node-%d.example.com, port: 443, cipher: aes-128-gcm, password: secret}\n", source*20+n, source*20+n)
		}
		raw := content.String()
		parsed, e := pool.Parse(raw, nil)
		if e != nil {
			t.Fatal(e)
		}
		src, e := s.AddSource(ctx, pool.SourceInput{Name: fmt.Sprintf("source-%d", source), Kind: "file", Content: raw})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.ApplySource(ctx, src, raw, parsed); e != nil {
			t.Fatal(e)
		}
	}
	queryStart := time.Now()
	page, e := s.Nodes(ctx, pool.Filter{Page: 2, PageSize: 50, Sort: "name"})
	if e != nil {
		t.Fatal(e)
	}
	if page.Total != 10000 || len(page.Items) != 50 {
		t.Fatalf("capacity lost rows: total %d items %d", page.Total, len(page.Items))
	}
	t.Logf("500 sources / 10000 nodes: import=%s page_query=%s", queryStart.Sub(start), time.Since(queryStart))
}
