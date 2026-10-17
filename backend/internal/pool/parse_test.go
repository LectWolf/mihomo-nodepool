package pool

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestParseFormatsAndFingerprint(t *testing.T) {
	yaml := `proxies:
 - {name: first, type: ss, server: EXAMPLE.COM, port: 443, cipher: aes-128-gcm, password: secret}
 - {name: second, type: ss, server: example.com, port: 443, cipher: aes-128-gcm, password: secret}
 - {name: third, type: ss, server: example.com, port: 443, cipher: aes-128-gcm, password: different}
`
	p, e := Parse(yaml, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Nodes) != 3 || p.Nodes[0].Hash != p.Nodes[1].Hash || p.Nodes[1].Hash == p.Nodes[2].Hash {
		t.Fatal("fingerprint must ignore only display identity")
	}
	link := "ss://" + base64.RawStdEncoding.EncodeToString([]byte("aes-128-gcm:secret")) + "@example.com:443#hello"
	for _, body := range []string{link, base64.StdEncoding.EncodeToString([]byte(link)), `[{"name":"one","type":"socks5","server":"127.0.0.1","port":1080}]`} {
		p, e := Parse(body, nil)
		if e != nil || len(p.Nodes) != 1 {
			t.Fatalf("format failed: %v", e)
		}
	}
	if _, e := Parse("this is not a subscription", nil); e == nil {
		t.Fatal("invalid source accepted")
	}
}
func TestNextRunIsStableStaggeredAndSkipsMissedCycles(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := created.Add(3 * Period)
	seen := map[time.Time]bool{}
	for i := 0; i < 500; i++ {
		key := strings.Repeat("x", i+1)
		next := NextRun(created, key, now)
		if !next.After(now) || next.After(now.Add(Period)) {
			t.Fatal("next must be inside the next period")
		}
		if next != NextRun(created, key, now) {
			t.Fatal("schedule changed")
		}
		if NextRun(created, key, next) != next.Add(Period) {
			t.Fatal("not periodic")
		}
		seen[next] = true
	}
	if len(seen) < 490 {
		t.Fatal("same-time imports were not staggered")
	}
}
