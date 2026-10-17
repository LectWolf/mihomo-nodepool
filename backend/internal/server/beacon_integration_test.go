package server

import (
	"context"
	"fmt"
	"io"
	"net"
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

func TestMySQLBeaconFieldTests(t *testing.T) {
	s := apiStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, pool.UserInput{Username: "beaconuser", Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	if !u.BeaconEnabled {
		t.Fatal("beacon must default on")
	}
	uc := pool.WithUser(ctx, u)
	content := "proxies:\n"
	for i := 1; i <= 3; i++ {
		content += fmt.Sprintf("  - {name: n%d, type: ss, server: example.com, port: %d, cipher: aes-128-gcm, password: secret}\n", i, 8000+i)
	}
	src, err := s.AddSource(uc, pool.SourceInput{Name: "beacon", Kind: "links", Content: content})
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
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "beacon", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(t.TempDir(), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	_, local, _ := net.ParseCIDR("127.0.0.1/32")
	server := httptest.NewServer(New(Options{Service: pool.NewService(s, core.New()), Auth: a, Updates: testUpdates{}, TrustedProxies: []*net.IPNet{local}}))
	defer server.Close()
	call := func(method, path string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, nil)
		req.Header.Set("X-Forwarded-For", "114.114.114.114")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
		return raw
	}
	// 返回 端口→回报路径
	beacons := func() map[int]string {
		t.Helper()
		var doc struct {
			Proxies []map[string]any `yaml:"proxies"`
			Groups  []map[string]any `yaml:"proxy-groups"`
		}
		if err := yaml.Unmarshal(call("GET", saved.Subscription.URL+"?format=clash", 200), &doc); err != nil {
			t.Fatal(err)
		}
		ports := map[string]int{}
		for _, p := range doc.Proxies {
			if v, ok := p["port"].(int); ok {
				ports[fmt.Sprint(p["name"])] = v
			}
		}
		out := map[int]string{}
		for _, g := range doc.Groups {
			if !strings.HasPrefix(fmt.Sprint(g["name"]), "实测回报") {
				continue
			}
			list, _ := g["proxies"].([]any)
			link := fmt.Sprint(g["url"])
			if g["hidden"] != true || g["lazy"] != false || g["type"] != "url-test" || len(list) != 1 || !strings.HasPrefix(link, server.URL+"/b/") {
				t.Fatalf("bad beacon group: %v", g)
			}
			out[ports[fmt.Sprint(list[0])]] = strings.TrimPrefix(link, server.URL)
		}
		return out
	}
	first := beacons()
	if len(first) != 3 || first[8001] == "" || first[8003] == "" {
		t.Fatalf("beacon groups: %v", first)
	}
	if again := beacons(); fmt.Sprint(again) != fmt.Sprint(first) {
		t.Fatal("batch must be reused within rotation window")
	}
	var region string
	if err = s.DB.QueryRow(`SELECT region FROM np_beacon_batches WHERE user_id=?`, u.ID).Scan(&region); err != nil || region != "CN" {
		t.Fatalf("region %q %v", region, err)
	}
	call("HEAD", first[8002], 204)
	call("GET", first[8003], 204)
	call("GET", first[8003], 204)
	call("GET", "/b/"+strings.Repeat("0", 32), 204)
	// 刚回报时不推断失败
	if tail := subscriptionTail(t, s, saved.Subscription.Token); tail != 8003 {
		t.Fatal("failure inferred before settle window")
	}
	if _, err = s.DB.Exec(`UPDATE np_beacon_batches SET first_ok_at=? WHERE user_id=?`, time.Now().UTC().Add(-time.Hour), u.ID); err != nil {
		t.Fatal(err)
	}
	if tail := subscriptionTail(t, s, saved.Subscription.Token); tail != 8001 {
		t.Fatalf("failed node must be last, got port %d", tail)
	}
	page, err := s.Nodes(uc, pool.Filter{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	stats := map[int]pool.FieldTest{}
	for _, n := range page.Items {
		if n.FieldTest != nil {
			stats[n.Port] = *n.FieldTest
		}
	}
	if stats[8001] != (pool.FieldTest{Fail: 1}) || stats[8002] != (pool.FieldTest{OK: 1}) || stats[8003] != (pool.FieldTest{OK: 1}) {
		t.Fatalf("field tests: %v", stats)
	}
	if err = s.SetBeacon(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if off := beacons(); len(off) != 0 {
		t.Fatal("disabled user still gets beacon groups")
	}
	if got, _ := s.User(ctx, u.ID); got.BeaconEnabled {
		t.Fatal("beacon flag not persisted")
	}
}

func subscriptionTail(t *testing.T, s *pool.Store, token string) int {
	t.Helper()
	nodes, err := s.SubscriptionNodes(context.Background(), token)
	if err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	return nodes[len(nodes)-1].Port
}

func TestBeaconBase(t *testing.T) {
	for origin, want := range map[string]string{
		"https://proxy.mcii.cc":      "http://proxy.mcii.cc",
		"https://proxy.mcii.cc:8443": "http://proxy.mcii.cc",
		"http://127.0.0.1:8080":      "http://127.0.0.1:8080",
		"http://[::1]:8080":          "http://[::1]:8080",
	} {
		if got, ok := beaconBase(origin); !ok || got != want {
			t.Fatalf("%s: %s", origin, got)
		}
	}
}

// 用真实 Mihomo 经 HTTP 代理节点访问回报地址，确认内核的检测请求能被记录
func TestMySQLBeaconRealMihomo(t *testing.T) {
	s := apiStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, pool.UserInput{Username: "beaconreal", Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	uc := pool.WithUser(ctx, u)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "connect only", 405)
			return
		}
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.WriteHeader(200)
		conn, buf, _ := w.(http.Hijacker).Hijack()
		go func() { _, _ = io.Copy(up, buf); up.Close() }()
		_, _ = io.Copy(conn, up)
		conn.Close()
	})}
	go proxy.Serve(ln)
	defer proxy.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	src, err := s.AddSource(uc, pool.SourceInput{Name: "real", Kind: "links", Content: fmt.Sprintf("proxies: [{name: local, type: http, server: 127.0.0.1, port: %d}]", port)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := pool.Parse(src.Content, nil)
	if err = s.ApplySource(ctx, src, src.Content, parsed); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "real", SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := auth.New(t.TempDir(), "test-password")
	engine := core.New()
	server := httptest.NewServer(New(Options{Service: pool.NewService(s, engine), Auth: a, Updates: testUpdates{}}))
	defer server.Close()
	nodes, err := s.SubscriptionNodes(ctx, saved.Subscription.Token)
	if err != nil || len(nodes) != 1 {
		t.Fatal(nodes, err)
	}
	tokens, err := s.BeaconTokens(ctx, saved.Subscription.Token, nodes, false)
	if err != nil || len(tokens) != 1 {
		t.Fatal(tokens, err)
	}
	settings := pool.Defaults()
	settings.TestURL = server.URL + "/b/" + tokens[nodes[0].ID]
	settings.TimeoutMS = 3000
	if r := engine.Probe(ctx, nodes[0], settings, false); r.LatencyMS == nil {
		t.Fatalf("mihomo probe failed: %+v", r)
	}
	var ok *time.Time
	if err = s.DB.QueryRow(`SELECT ok_at FROM np_beacon_checks WHERE token=?`, tokens[nodes[0].ID]).Scan(&ok); err != nil || ok == nil {
		t.Fatal("real mihomo beacon not recorded", err)
	}
}
