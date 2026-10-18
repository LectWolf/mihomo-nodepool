package pool_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// 后台读取跳过证书验证节点的证书，v2ray 订阅附带 pcs，Clash 订阅不变
func TestMySQLTLSPinsInV2raySubscription(t *testing.T) {
	pool.TLSPinPrivateTargets = true
	defer func() { pool.TLSPinPrivateTargets = false }()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	sum := sha256.Sum256(server.TLS.Certificates[0].Certificate[0])
	want := hex.EncodeToString(sum[:])
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())

	s := testStore(t)
	ctx := context.Background()
	u := account(t, s, "pinuser", 200, 5, 3)
	uc := pool.WithUser(ctx, u)
	raw := fmt.Sprintf("proxies:\n - {name: pinned, type: trojan, server: 127.0.0.1, port: %s, password: secret, sni: example.com, skip-cert-verify: true}\n - {name: verified, type: trojan, server: 127.0.0.2, port: %s, password: secret, sni: example.com}\n", port, port)
	src, e := s.AddSource(uc, pool.SourceInput{Name: "pinsrc", Kind: "file", Content: raw})
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := pool.Parse(raw, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplySource(uc, src, raw, parsed); e != nil {
		t.Fatal(e)
	}
	if e = s.RefreshTLSPins(ctx); e != nil {
		t.Fatal(e)
	}
	var needed, pinned int
	var pin string
	if e = s.DB.QueryRow(`SELECT SUM(needed),COUNT(*),MAX(pin) FROM np_node_tls`).Scan(&needed, &pinned, &pin); e != nil || needed != 1 || pinned != 2 || pin != want {
		t.Fatalf("pins: %v needed=%d rows=%d pin=%q want %q", e, needed, pinned, pin, want)
	}
	saved, e := s.SaveSubscription(uc, 0, pool.SubscriptionInput{Name: "pins", SourceIDs: []int64{src.ID}})
	if e != nil {
		t.Fatal(e)
	}
	body, _, _, e := s.ExportSavedSubscription(ctx, saved.Subscription.Token, "v2ray")
	if e != nil {
		t.Fatal(e)
	}
	decoded, _ := base64.StdEncoding.DecodeString(string(body))
	found := 0
	for _, line := range strings.Fields(string(decoded)) {
		link, _ := url.Parse(line)
		switch link.Fragment {
		case "pinned":
			if link.Query().Get("pcs") != want || link.Query().Get("allowInsecure") != "1" {
				t.Fatalf("pinned link: %s", line)
			}
			found++
		case "verified":
			if link.Query().Has("pcs") {
				t.Fatalf("verified link must not carry pcs: %s", line)
			}
			found++
		}
	}
	if found != 2 {
		t.Fatalf("links: %s", decoded)
	}
	clash, _, _, e := s.ExportSavedSubscription(ctx, saved.Subscription.Token, "clash")
	if e != nil || strings.Contains(string(clash), want) {
		t.Fatalf("clash must not change: %v", e)
	}
}
