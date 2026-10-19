package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
)

// This is a real CONNECT tunnel. No mocked latency result can pass this test.
func TestProbeUsesMihomoTunnelForDelayAndCountry(t *testing.T) {
	var connects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"success","query":"203.0.113.9","country":"美国","countryCode":"US","regionName":"加州","city":"洛杉矶"}`)
			return
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			w.WriteHeader(405)
			return
		}
		conn, e := net.Dial("tcp", r.Host)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		client, buf, e := w.(http.Hijacker).Hijack()
		if e != nil {
			conn.Close()
			return
		}
		connects.Add(1)
		_, _ = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buf.Flush()
		go func() {
			defer client.Close()
			defer conn.Close()
			go io.Copy(conn, client)
			_, _ = io.Copy(client, conn)
		}()
	}))
	defer proxy.Close()
	host, portString, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	config, _ := json.Marshal(map[string]any{"name": "local", "type": "http", "server": host, "port": port})
	engine := New()
	engine.GeoURLs = []string{target.URL + "/geo"}
	settings := pool.Defaults()
	settings.TestURL = target.URL + "/delay"
	before := trafficOf("probe", "http")
	exitBefore := trafficOf("exit", "http")
	result := engine.Probe(context.Background(), pool.Node{Config: config}, settings, true)
	if result.LatencyMS == nil || result.ExitIP != "203.0.113.9" || result.CountryCode != "US" || result.Region != "加州" || result.City != "洛杉矶" || connects.Load() < 2 {
		t.Fatalf("probe did not traverse tunnel: %+v, connects=%d", result, connects.Load())
	}
	// 检测和出口查询分别计数，字节来自经过代理的连接
	if p := trafficOf("probe", "http"); p.Requests != before.Requests+1 || p.Out <= before.Out || p.In <= before.In {
		t.Fatalf("probe traffic not counted: %+v -> %+v", before, p)
	}
	if e := trafficOf("exit", "http"); e.Requests != exitBefore.Requests+1 || e.In-exitBefore.In < 100 {
		t.Fatalf("exit traffic not counted: %+v -> %+v", exitBefore, e)
	}
}

func connectProxy(t *testing.T, counter *atomic.Int32) (string, int) {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			w.WriteHeader(405)
			return
		}
		conn, e := net.Dial("tcp", r.Host)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		client, buf, e := w.(http.Hijacker).Hijack()
		if e != nil {
			conn.Close()
			return
		}
		counter.Add(1)
		_, _ = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buf.Flush()
		go func() {
			defer client.Close()
			defer conn.Close()
			go io.Copy(conn, client)
			_, _ = io.Copy(client, conn)
		}()
	}))
	t.Cleanup(proxy.Close)
	host, portString, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	return host, port
}

// 链式节点：出口代理的连接必须经过内嵌的前置代理
func TestProbeTraversesEmbeddedDialer(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	var relay, exit atomic.Int32
	rh, rp := connectProxy(t, &relay)
	eh, ep := connectProxy(t, &exit)
	cfg := map[string]any{"name": "exit", "type": "http", "server": eh, "port": ep,
		pool.DialerKey: map[string]any{"name": "relay", "type": "http", "server": rh, "port": rp}}
	engine := New()
	if err := engine.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(cfg)
	settings := pool.Defaults()
	settings.TestURL = target.URL + "/delay"
	result := engine.Probe(context.Background(), pool.Node{Config: config}, settings, false)
	if result.LatencyMS == nil || relay.Load() == 0 || exit.Load() == 0 {
		t.Fatalf("chain not used: %+v relay=%d exit=%d", result, relay.Load(), exit.Load())
	}
	if v := trafficOf("probe", "http+chain"); v.Requests == 0 || v.Out == 0 {
		t.Fatalf("chain traffic not counted: %+v", v)
	}
}

func trafficOf(kind, detail string) traffic.Values {
	for _, r := range traffic.Default.Peek() {
		if r.Kind == kind && r.Detail == detail {
			return r.Values
		}
	}
	return traffic.Values{}
}
