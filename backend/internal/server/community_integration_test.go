package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"gopkg.in/yaml.v3"
)

func TestMySQLCommunityDeliveryAndRealMihomoKeepalive(t *testing.T) {
	s := apiStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, pool.UserInput{Username: "leasehttp", Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	uc := pool.WithUser(ctx, u)
	src, err := s.AddSource(uc, pool.SourceInput{Name: "source", Kind: "links", Content: "proxies: [{name: real, type: ss, server: example.com, port: 443, cipher: aes-128-gcm, password: secret}]"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pool.Parse(src.Content, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplySource(ctx, src, src.Content, parsed); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "lease", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(t.TempDir(), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	engine := core.New()
	h := New(Options{Service: pool.NewService(s, engine), Auth: a, Updates: testUpdates{}})
	server := httptest.NewServer(h)
	defer server.Close()
	get := func(path string, want int) ([]byte, http.Header) {
		t.Helper()
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("GET %s status=%d want=%d err=%v body=%s", path, resp.StatusCode, want, err, raw)
		}
		return raw, resp.Header
	}
	before := time.Now().UTC().Add(2 * 24 * time.Hour).Truncate(time.Microsecond)
	if _, err = s.DB.Exec(`UPDATE np_users SET subscription_expires_at=? WHERE id=?`, before, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE np_source_seats SET expires_at=? WHERE user_id=?`, before, u.ID); err != nil {
		t.Fatal(err)
	}
	raw, header := get(saved.Subscription.URL+"?format=clash", 200)
	if header.Get("X-Subscription-State") != "active" || header.Get("Profile-Web-Page-Url") == "" {
		t.Fatal("missing delivery headers")
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []map[string]any `yaml:"proxy-groups"`
	}
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	for _, n := range doc.Proxies {
		if n["type"] == "vless" {
			probe = n
		}
	}
	if probe == nil {
		t.Fatal("keepalive node missing")
	}
	got, _ := s.User(ctx, u.ID)
	if !got.SubscriptionExpiresAt.Equal(before) {
		t.Fatal("ordinary pull renewed lease")
	}
	config, _ := json.Marshal(probe)
	settings := pool.Defaults()
	settings.TestURL = keepaliveURL
	settings.TimeoutMS = 2000
	result := engine.Probe(ctx, pool.Node{Config: config}, settings, false)
	if result.LatencyMS == nil {
		t.Fatalf("real Mihomo VLESS/WS probe failed: %+v", result)
	}
	renewed, _ := s.User(ctx, u.ID)
	if time.Until(*renewed.SubscriptionExpiresAt) < 13*24*time.Hour {
		t.Fatal("successful real delay probe did not renew")
	}
	var method string
	if err = s.DB.QueryRow(`SELECT renew_method FROM np_source_seats WHERE source_id=? AND user_id=?`, src.ID, u.ID).Scan(&method); err != nil || method != "probe" {
		t.Fatal("seat renewal metadata", method, err)
	}
	settings.TestURL = "http://example.org/generate_204"
	result = engine.Probe(ctx, pool.Node{Config: config}, settings, false)
	if result.LatencyMS != nil {
		t.Fatal("probe accepted a general outbound destination")
	}
	raw, _ = get(saved.Subscription.URL+"?format=v2ray", 200)
	decoded, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil || !strings.Contains(string(decoded), "vless://") {
		t.Fatal("v2ray keepalive missing", err)
	}
	sources, _ := s.Sources(uc)
	link := sources[0].ShareURL
	raw, _ = get(link+"?format=clash", 200)
	if strings.Contains(string(raw), "/ws/lease/") {
		t.Fatal("source bearer link contains account probe")
	}
	// 新令牌为 32 位，旧站的 /l/ 与 /give/ 路径同样可用
	token := strings.TrimPrefix(link, "/link/")
	if len(token) != 32 || len(saved.Subscription.Token) != 32 {
		t.Fatalf("token length: %q %q", token, saved.Subscription.Token)
	}
	for _, alias := range []string{"/l/", "/give/"} {
		if alias, _ := get(alias+token+"?format=clash", 200); string(alias) != string(raw) {
			t.Fatal("legacy link alias differs")
		}
	}
	get("/l/"+strings.Repeat("z", 32), 404)
	expired := time.Now().UTC().Add(-time.Hour)
	if _, err = s.DB.Exec(`UPDATE np_users SET subscription_expires_at=? WHERE id=?`, expired, u.ID); err != nil {
		t.Fatal(err)
	}
	raw, header = get(saved.Subscription.URL+"?format=clash", 200)
	if header.Get("X-Subscription-State") != "expired" || !strings.Contains(string(raw), "签到激活") || strings.Contains(string(raw), "password: secret") {
		t.Fatal("expired placeholder")
	}
	settings.TestURL = keepaliveURL
	if result = engine.Probe(ctx, pool.Node{Config: config}, settings, false); result.LatencyMS != nil {
		t.Fatal("expired probe succeeded")
	}
	get(link+"?format=clash", 200)
	if _, err = s.MutateSubscription(uc, saved.Subscription.ID, false); err != nil {
		t.Fatal(err)
	}
	get(saved.Subscription.URL, 404)
}
