package pool_test

import (
	"context"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
)

func TestSiteTrafficFlushAndRead(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if _, err := store.DB.ExecContext(ctx, `DELETE FROM np_site_traffic`); err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	rows := []traffic.Row{{Key: traffic.Key{Kind: "probe", Detail: "vmess"}, Values: traffic.Values{Requests: 3, Failures: 1, In: 1000, Out: 500}}}
	for i := 0; i < 2; i++ {
		if err := store.FlushSiteTraffic(ctx, hour, rows); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.FlushSiteTraffic(ctx, hour.Add(-48*time.Hour), rows); err != nil {
		t.Fatal(err)
	}
	traffic.Default.Counter("probe", "vmess").AddIn(7)
	defer traffic.Default.Drain()
	st, err := store.SiteTraffic(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hourly *struct{ req, in int64 }
	for _, r := range st.Hourly {
		if r.Kind == "probe" && r.Detail == "vmess" && r.At.Equal(hour) {
			hourly = &struct{ req, in int64 }{r.Requests, r.In}
		}
		if r.At.Before(hour.Add(-23 * time.Hour)) {
			t.Fatalf("old hour leaked: %+v", r)
		}
	}
	if hourly == nil || hourly.req != 6 || hourly.in != 2007 {
		t.Fatalf("hourly = %+v, all %+v", hourly, st.Hourly)
	}
	var daily int64
	for _, r := range st.Daily {
		if r.Kind == "probe" {
			daily += r.In
		}
	}
	if daily != 3007 {
		t.Fatalf("daily in = %d, %+v", daily, st.Daily)
	}
}
