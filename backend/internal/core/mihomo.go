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
	"strings"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/proxydialer"
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
	return &Mihomo{exitSlots: make(chan struct{}, 8), GeoURLs: []string{
		"http://ip-api.com/json/?lang=zh-CN&fields=status,message,country,countryCode,regionName,city,query",
		"https://api.ip.sb/geoip",
		"https://ipwho.is/",
	}}
}
func (*Mihomo) Version() string { return "v1.19.31" }
func (*Mihomo) Validate(config map[string]any) error {
	_, release, e := parseProxy(config, nil)
	if e != nil {
		return e
	}
	release()
	return nil
}

// parseProxy 解析节点；内嵌前置代理时以前置代理作为拨号器，release 关闭整条链。
// tag 非空时最内层节点的连接计入流量统计
func parseProxy(config map[string]any, tag *traffic.Tag) (C.Proxy, func(), error) {
	var chain []C.Proxy
	release := func() {
		for _, p := range chain {
			_ = p.Close()
		}
	}
	var build func(cfg map[string]any, depth int) (C.Proxy, error)
	build = func(cfg map[string]any, depth int) (C.Proxy, error) {
		m := make(map[string]any, len(cfg))
		for k, v := range cfg {
			m[k] = v
		}
		var opts []adapter.ProxyOption
		var metered *meteredDialer
		if raw, ok := m[pool.DialerKey]; ok {
			delete(m, pool.DialerKey)
			inner, ok := raw.(map[string]any)
			if !ok || depth >= 3 {
				return nil, errors.New("前置代理配置无效")
			}
			d, err := build(inner, depth+1)
			if err != nil {
				return nil, err
			}
			opts = append(opts, adapter.WithDialerForAPI(proxydialer.New(d, false)))
		} else if tag != nil {
			metered = &meteredDialer{tag: tag}
			opts = append(opts, adapter.WithDialerForAPI(metered))
		}
		p, err := adapter.ParseProxy(m, opts...)
		if err != nil {
			return nil, err
		}
		// 自定义拨号器会跳过节点的 ip-version、tfo 等选项，这里补回
		if metered != nil {
			if o, ok := p.Adapter().(interface{ DialOptions() []dialer.Option }); ok {
				metered.opts = o.DialOptions()
			}
		}
		chain = append(chain, p)
		return p, nil
	}
	p, err := build(config, 0)
	if err != nil {
		release()
		return nil, nil, err
	}
	return p, release, nil
}
func (m *Mihomo) Probe(ctx context.Context, n pool.Node, s pool.Settings, exit bool) pool.ProbeResult {
	result := pool.ProbeResult{}
	var config map[string]any
	if json.Unmarshal(n.Config, &config) != nil {
		result.Error = "节点配置无效"
		return result
	}
	kind := nodeType(config)
	probeCounter := traffic.Default.Counter(traffic.KindProbe, kind)
	tag := traffic.NewTag(probeCounter)
	p, release, e := parseProxy(config, tag)
	if e != nil {
		result.Error = "Mihomo 无法加载节点配置"
		return result
	}
	defer release()
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
	probeCounter.Hit(result.LatencyMS == nil)
	if result.LatencyMS == nil {
		result.Error = "连接失败或测试超时（已重试）"
		return result
	}
	if !exit {
		return result
	}
	// 出口查询名额已满时先返回延迟结果，下轮检测再补查出口
	select {
	case m.exitSlots <- struct{}{}:
		defer func() { <-m.exitSlots }()
	default:
		return result
	}
	result.ExitAttempted = true
	exitCounter := traffic.Default.Counter(traffic.KindExit, kind)
	tag.Set(exitCounter)
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
		exitCounter.Hit(false)
		return result
	}
	exitCounter.Hit(true)
	result.ExitError = "出口国家查询失败，稍后重试"
	return result
}

// nodeType 流量明细按协议区分；带前置代理时标注 +chain
func nodeType(config map[string]any) string {
	t, _ := config["type"].(string)
	if t == "" {
		t = "unknown"
	}
	if _, ok := config[pool.DialerKey]; ok {
		t += "+chain"
	}
	return strings.ToLower(t)
}
