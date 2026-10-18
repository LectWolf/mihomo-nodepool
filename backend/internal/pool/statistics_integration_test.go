package pool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLStatisticsQueuesAndUsage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := sharedSource(t, s, ctx, "stats-active", 1, 0)
	b := sharedSource(t, s, ctx, "stats-queued", 1, 0)
	c := sharedSource(t, s, ctx, "stats-disabled", 1, 0)
	for _, query := range []string{
		`UPDATE np_sources SET pending=0,lease_until=NULL,status='ready',next_refresh_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 HOUR)`,
		`UPDATE np_nodes SET pending=0,lease_until=NULL,next_check_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 HOUR)`,
	} {
		if _, err := s.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`UPDATE np_sources SET kind='url',pending=1,status='processing',lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 5 MINUTE),next_refresh_at=UTC_TIMESTAMP(6) WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE np_sources SET pending=0,status='processing',lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 MINUTE) WHERE id=?`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE np_sources SET enabled=0,pending=1 WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE np_nodes SET pending=1,lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 2 MINUTE),next_check_at=UTC_TIMESTAMP(6) WHERE id IN (SELECT node_id FROM np_source_nodes WHERE source_id=?)`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE np_nodes SET pending=1,blocked=1 WHERE id IN (SELECT node_id FROM np_source_nodes WHERE source_id=?)`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE np_nodes SET pending=1 WHERE id IN (SELECT node_id FROM np_source_nodes WHERE source_id=?)`, c.ID); err != nil {
		t.Fatal(err)
	}
	usage := pool.ParseSubscriptionUsage("upload=5;download=15;total=100")
	raw, _ := json.Marshal(usage)
	if _, err := s.DB.Exec(`UPDATE np_sources SET subscription_usage=? WHERE id=?`, raw, a.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.Statistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Tasks != (pool.Tasks{ActiveSources: 1, PendingSources: 1, ActiveNodes: 1, PendingNodes: 1}) {
		t.Fatalf("overlapping or ineligible queues: %+v", view.Tasks)
	}
	if len(view.Sources) != 2 || view.Sources[0].ID != a.ID || view.Sources[0].Status != "processing" || view.Sources[1].Status != "queued" {
		t.Fatalf("source tasks: %+v", view.Sources)
	}
	if len(view.Nodes) != 2 || view.Nodes[0].Status != "processing" || view.Nodes[1].Status != "queued" {
		t.Fatalf("node tasks: %+v", view.Nodes)
	}
	if view.Overview.NodesTotal != 3 || view.Overview.NodesVisible != 1 || view.Overview.NodesBlocked != 1 {
		t.Fatalf("node overview: %+v", view.Overview)
	}
	if view.Usage.SourcesKnown != 1 || view.Usage.SourcesTotal != 3 || view.Usage.UploadBytes == nil || *view.Usage.UploadBytes != 5 || view.Usage.RemainingBytes == nil || *view.Usage.RemainingBytes != 80 || view.Usage.UploadSources != 1 {
		t.Fatalf("usage: %+v", view.Usage)
	}
	encoded, _ := json.Marshal(view)
	for _, secret := range []string{"secret", "aes-128-gcm", "config", "share_token"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("statistics leaked %s", secret)
		}
	}
}

func TestMySQLSourceDownloadProcessCounters(t *testing.T) {
	s := testStore(t)
	body := `proxies:
 - {name: stats-probe, type: ss, server: stat.example.com, port: 443, cipher: aes-128-gcm, password: secret}
`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer upstream.Close()
	ctx := context.Background()
	src, err := s.AddSource(ctx, pool.SourceInput{Name: "stats download", Kind: "url", URL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	service := pool.NewService(s, core.New())
	service.Refresh(ctx, src)
	metrics := service.RuntimeStatistics()
	if metrics.SourceBodyBytes != int64(len(body)) || metrics.SourceRuns != 1 || metrics.SourceErrors != 0 || metrics.StartedAt.After(time.Now()) {
		t.Fatalf("runtime: %+v", metrics)
	}
	src, err = s.Source(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	src.Cached = true
	service.Refresh(ctx, src)
	metrics = service.RuntimeStatistics()
	if metrics.SourceBodyBytes != int64(len(body)) || metrics.SourceRuns != 2 {
		t.Fatal("cache reparse counted as network download")
	}
}

// 有来源只返回部分套餐字段时，整点快照仍要成功，流量差值按小时归档
func TestMySQLHourlySnapshotWithPartialUsage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	full := sharedSource(t, s, ctx, "snap-full", 1, 0)
	partial := sharedSource(t, s, ctx, "snap-partial", 1, 0)
	setUsage := func(id int64, header string) {
		t.Helper()
		raw, _ := json.Marshal(pool.ParseSubscriptionUsage(header))
		if _, err := s.DB.Exec(`UPDATE np_sources SET subscription_usage=? WHERE id=?`, raw, id); err != nil {
			t.Fatal(err)
		}
	}
	expire := time.Now().Add(72 * time.Hour).Unix()
	setUsage(full.ID, fmt.Sprintf("upload=100;download=900;total=10000;expire=%d", expire))
	setUsage(partial.ID, fmt.Sprintf("expire=%d", expire))
	now := time.Now().UTC().Truncate(time.Hour)
	first, second := now.Add(-2*time.Hour), now.Add(-time.Hour)
	if err := s.SnapshotSources(ctx, first); err != nil {
		t.Fatalf("snapshot with partial usage: %v", err)
	}
	setUsage(full.ID, fmt.Sprintf("upload=300;download=1700;total=10000;expire=%d", expire))
	if err := s.SnapshotSources(ctx, second); err != nil {
		t.Fatal(err)
	}
	var used *int64
	if err := s.DB.QueryRow(`SELECT used_bytes FROM np_source_hourly WHERE source_id=? AND hour=?`, partial.ID, second).Scan(&used); err != nil || used != nil {
		t.Fatalf("partial source used bytes: %v %v", err, used)
	}
	if err := s.DB.QueryRow(`SELECT used_bytes FROM np_source_hourly WHERE source_id=? AND hour=?`, full.ID, second).Scan(&used); err != nil || used == nil || *used != 2000 {
		t.Fatalf("full source used bytes: %v %v", err, used)
	}
	traffic, err := s.TrafficStatistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	for _, p := range traffic.Hourly {
		if p.At.Equal(first) {
			got = p.Bytes
		}
	}
	if got < 1000 {
		t.Fatalf("hourly delta for %v: %d (%+v)", first, got, traffic.Hourly)
	}
}
