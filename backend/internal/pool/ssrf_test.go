package pool

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
)

// privateTargets 固定内网开关，避免受其它测试（集成测试会临时打开）影响。
func privateTargets(t *testing.T, on bool) {
	t.Helper()
	old := AllowPrivateTargets
	AllowPrivateTargets = on
	t.Cleanup(func() { AllowPrivateTargets = old })
}

func TestBlockedAddress(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.8.9.10", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.100.100.200", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"::1", true},
		{"::", true},
		{"fe80::1", true},
		{"fd00::1", true},
		{"64:ff9b::1", true},
		{"::ffff:127.0.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.114.1", false},
		{"2001:4860:4860::8888", false},
	}
	for _, c := range cases {
		ip, e := netip.ParseAddr(c.addr)
		if e != nil {
			t.Fatalf("parse %s: %v", c.addr, e)
		}
		if got := blockedAddress(ip); got != c.want {
			t.Fatalf("blockedAddress(%s) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestCheckSourceRejectsPrivateLiteral(t *testing.T) {
	privateTargets(t, false)
	for _, raw := range []string{"http://127.0.0.1/sub", "http://192.168.0.2/sub", "http://[::1]/sub", "http://169.254.169.254/latest", "http://100.100.100.200/sub"} {
		in := SourceInput{Name: "内网", Kind: "url", URL: raw}
		if e := checkSource(in); e == nil {
			t.Fatalf("checkSource(%s) accepted private target", raw)
		}
	}
	if e := checkSource(SourceInput{Name: "公网", Kind: "url", URL: "https://example.com/sub"}); e != nil {
		t.Fatalf("checkSource rejected public target: %v", e)
	}
}

func TestGuardedTransportRejectsLoopback(t *testing.T) {
	privateTargets(t, false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	tr := guardedTransport(traffic.Default, traffic.KindSource, false)
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	// 字面量地址与经 DNS 解析出的回环地址都要拒绝，后者顺带覆盖域名路径
	targets := []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)}
	for _, target := range targets {
		if _, e := client.Get(target); !errors.Is(e, ErrBlockedTarget) {
			t.Fatalf("GET %s: want ErrBlockedTarget, got %v", target, e)
		}
	}
}

// 集成测试与浏览器 e2e 依赖这个开关访问回环模拟源，改动校验逻辑时不能顺手删掉。
func TestPrivateTargetsSwitchAllowsLoopback(t *testing.T) {
	privateTargets(t, true)
	if e := checkSource(SourceInput{Name: "本机", Kind: "url", URL: "http://127.0.0.1:18080/sub"}); e != nil {
		t.Fatalf("checkSource rejected loopback with switch on: %v", e)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tr := guardedTransport(traffic.Default, traffic.KindUsage, false)
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, e := client.Get(srv.URL)
	if e != nil {
		t.Fatalf("GET %s with switch on: %v", srv.URL, e)
	}
	resp.Body.Close()
}
