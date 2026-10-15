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
)

// This is a real CONNECT tunnel. No mocked latency result can pass this test.
func TestProbeUsesMihomoTunnelForDelayAndCountry(t *testing.T) {
	var connects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ip":"203.0.113.9","country":"Japan","country_code":"JP"}`)
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
	result := engine.Probe(context.Background(), pool.Node{Config: config}, settings, true)
	if result.LatencyMS == nil || result.ExitIP != "203.0.113.9" || result.CountryCode != "JP" || connects.Load() < 2 {
		t.Fatalf("probe did not traverse tunnel: %+v, connects=%d", result, connects.Load())
	}
}
