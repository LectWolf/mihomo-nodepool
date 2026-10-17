package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
	mlog "github.com/metacubex/mihomo/log"
)

// Mihomo embeds the same URLTest implementation used by the reference app.
// Each job owns its adapter; no shared selector or per-node inbound port exists.
type Mihomo struct {
	exitSlots chan struct{}
	GeoURLs   []string
}

func New() *Mihomo {
	adapter.UnifiedDelay.Store(true)
	mlog.SetLevel(mlog.SILENT)
	return &Mihomo{exitSlots: make(chan struct{}, 4), GeoURLs: []string{
		"http://ip-api.com/json/?lang=zh-CN&fields=status,message,country,countryCode,regionName,city,query",
		"https://api.ip.sb/geoip",
		"https://ipwho.is/",
	}}
}
func (*Mihomo) Version() string { return "v1.19.31" }
func (*Mihomo) Validate(config map[string]any) error {
	p, e := adapter.ParseProxy(config)
	if e != nil {
		return e
	}
	return p.Close()
}
func (m *Mihomo) Probe(ctx context.Context, n pool.Node, s pool.Settings, exit bool) pool.ProbeResult {
	result := pool.ProbeResult{}
	var config map[string]any
	if json.Unmarshal(n.Config, &config) != nil {
		result.Error = "节点配置无效"
		return result
	}
	p, e := adapter.ParseProxy(config)
	if e != nil {
		result.Error = "Mihomo 无法加载节点配置"
		return result
	}
	defer p.Close()
	for attempt := 0; attempt < 2; attempt++ {
		testCtx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutMS)*time.Millisecond)
		delay, err := p.URLTest(testCtx, s.TestURL, nil)
		cancel()
		if err == nil {
			v := int(delay)
			result.LatencyMS = &v
			break
		}
		if ctx.Err() != nil {
			result.Error = "检测已中断"
			return result
		}
		if attempt == 0 {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				result.Error = "检测已中断"
				return result
			case <-timer.C:
			}
		}
	}
	if result.LatencyMS == nil {
		result.Error = "连接失败或测试超时（已重试）"
		return result
	}
	if !exit {
		return result
	}
	select {
	case m.exitSlots <- struct{}{}:
		defer func() { <-m.exitSlots }()
	case <-ctx.Done():
		return result
	}
	result.ExitAttempted = true
	geoCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(c context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		pnum, e := strconv.Atoi(port)
		if e != nil {
			return nil, e
		}
		meta := &C.Metadata{NetWork: C.TCP, Host: host, DstPort: uint16(pnum)}
		if ip, e := netip.ParseAddr(host); e == nil {
			meta.DstIP = ip
		}
		return p.DialContext(c, meta)
	}, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 9 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("too many redirects")
		}
		return nil
	}}
	for _, endpoint := range m.GeoURLs {
		req, e := http.NewRequestWithContext(geoCtx, http.MethodGet, endpoint, nil)
		if e != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mihomo-NodePool/1")
		resp, e := client.Do(req)
		if e != nil {
			continue
		}
		body, e := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if e != nil || resp.StatusCode != 200 {
			continue
		}
		location, e := parseLocation(body)
		if e != nil {
			continue
		}
		result.ExitIP = location.IP
		result.Country = location.Country
		result.CountryCode = location.CountryCode
		result.Region = location.Region
		result.City = location.City
		return result
	}
	result.ExitError = "出口国家查询失败，稍后重试"
	return result
}
