package pool_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLRuleChangesPreserveNetworkRefresh(t *testing.T) {
	for _, rules := range []string{"source", "global"} {
		for _, work := range []string{"url-change", "queued", "inflight", "due", "cache-only"} {
			t.Run(rules+"/"+work, func(t *testing.T) {
				s := testStore(t)
				ctx := context.Background()
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					generation := requests.Add(1)
					fmt.Fprintf(w, "proxies:\n - {name: node-%d, type: ss, server: node-%d.example.com, port: 443, cipher: aes-128-gcm, password: alpha}\n", generation, generation)
				}))
				defer srv.Close()
				service := pool.NewService(s, core.New())
				src, err := s.AddSource(ctx, pool.SourceInput{Name: "subscription", Kind: "url", URL: srv.URL + "/old"})
				if err != nil {
					t.Fatal(err)
				}
				initial, err := s.ClaimSource(ctx)
				if err != nil {
					t.Fatal(err)
				}
				service.Refresh(ctx, initial)
				var inflight *pool.Source
				switch work {
				case "url-change":
					newURL := srv.URL + "/new"
					if _, err := s.PatchSource(ctx, src.ID, pool.SourcePatch{URL: &newURL}); err != nil {
						t.Fatal(err)
					}
				case "queued", "inflight":
					if err := s.QueueSource(ctx, src.ID); err != nil {
						t.Fatal(err)
					}
					if work == "inflight" {
						claimed, err := s.ClaimSource(ctx)
						if err != nil {
							t.Fatal(err)
						}
						inflight = &claimed
					}
				case "due":
					if _, err := s.DB.Exec(`UPDATE np_sources SET next_refresh_at=DATE_SUB(UTC_TIMESTAMP(),INTERVAL 1 MINUTE) WHERE id=?`, src.ID); err != nil {
						t.Fatal(err)
					}
				}
				pattern := "^skip$"
				if rules == "source" {
					if _, err := s.PatchSource(ctx, src.ID, pool.SourcePatch{ExcludePattern: &pattern}); err != nil {
						t.Fatal(err)
					}
				} else {
					settings := testDefaults()
					settings.ExcludePattern = pattern
					if err := s.SaveSettings(ctx, settings); err != nil {
						t.Fatal(err)
					}
				}
				if inflight != nil {
					parsed, err := pool.Parse(inflight.Content, nil)
					if err != nil {
						t.Fatal(err)
					}
					if err := s.ApplySource(ctx, *inflight, inflight.Content, parsed); err != nil {
						t.Fatal(err)
					}
				}
				claimed, err := s.ClaimSource(ctx)
				if err != nil {
					t.Fatal(err)
				}
				wantCache := work == "cache-only"
				if claimed.Cached != wantCache {
					t.Fatalf("network work overwritten: cached=%v want=%v", claimed.Cached, wantCache)
				}
				if work == "url-change" && claimed.URL != srv.URL+"/new" {
					t.Fatalf("URL change lost: %s", claimed.URL)
				}
				service.Refresh(ctx, claimed)
				wantGeneration := int32(2)
				if wantCache {
					wantGeneration = 1
				}
				page, err := s.Nodes(ctx, pool.Filter{})
				if err != nil {
					t.Fatal(err)
				}
				if requests.Load() != wantGeneration || page.Total != 1 || page.Items[0].Server != fmt.Sprintf("node-%d.example.com", wantGeneration) {
					t.Fatalf("wrong refresh result: requests=%d page=%+v", requests.Load(), page)
				}
			})
		}
	}
}

func TestMySQLPatchWithoutChangesKeepsSource(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "proxies:\n - {name: a, type: ss, server: a.example.com, port: 443, cipher: aes-128-gcm, password: alpha}\n")
	}))
	defer srv.Close()
	service := pool.NewService(s, core.New())
	src, err := s.AddSource(ctx, pool.SourceInput{Name: "subscription", Kind: "url", URL: srv.URL + "/sub"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service.Refresh(ctx, claimed)
	before, _ := s.Source(ctx, src.ID)
	name, same, pattern, enabled := "renamed", srv.URL+"/sub", before.ExcludePattern, true
	after, err := s.PatchSource(ctx, src.ID, pool.SourcePatch{Name: &name, URL: &same, ExcludePattern: &pattern, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if after.Status == "queued" || after.Name != "renamed" {
		t.Fatalf("unchanged edit queued a refresh: %+v", after)
	}
	if _, err = s.ClaimSource(ctx); err == nil {
		t.Fatal("unchanged edit left work in the queue")
	}
	changed := srv.URL + "/sub?v=2"
	if after, err = s.PatchSource(ctx, src.ID, pool.SourcePatch{URL: &changed}); err != nil || after.Status != "queued" {
		t.Fatalf("url change must refresh: %+v %v", after, err)
	}
}

func TestMySQLPruneOrphanNodes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`INSERT INTO np_nodes(config_hash,name,type,server,port,config,last_error,created_at,next_check_at) VALUES('orphan-hash','old','ss','x',1,'{}','',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneOrphanNodes(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("fresh orphan deleted: %d %v", n, err)
	}
	if _, err := s.DB.Exec(`UPDATE np_nodes SET orphaned_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 2 HOUR) WHERE config_hash='orphan-hash'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneOrphanNodes(ctx, time.Hour); err != nil || n != 1 {
		t.Fatalf("old orphan kept: %d %v", n, err)
	}
}
