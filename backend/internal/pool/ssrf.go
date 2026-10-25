package pool

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
)

// ErrBlockedTarget 表示订阅地址或重定向目标落在内网、本机或保留网段。
// 来源 URL 与提供方用量地址都由用户填写，不能信任。
var ErrBlockedTarget = errors.New("目标地址不是公网地址，已拒绝访问")

// AllowPrivateTargets 仅供浏览器端 e2e 与集成测试：让回环地址上的测试服务器
// 可以当订阅源。生产环境必须保持关闭，否则来源地址能指向内网。
var AllowPrivateTargets = false

// targetBlocked 判断是否拒绝该目标，测试开关打开时一律放行。
func targetBlocked(ip netip.Addr) bool {
	return !AllowPrivateTargets && blockedAddress(ip)
}

// blockedPrefixes 覆盖 net 包没有判定的保留网段。
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // 本网络
	netip.MustParsePrefix("100.64.0.0/10"),   // 运营商级 NAT，含阿里云元数据 100.100.100.200
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF 协议分配
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // 基准测试
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // 保留与广播
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64
	netip.MustParsePrefix("100::/64"),        // 丢弃前缀
	netip.MustParsePrefix("2001:db8::/32"),   // 文档
	netip.MustParsePrefix("fc00::/7"),        // 唯一本地地址
	netip.MustParsePrefix("fe80::/10"),       // 链路本地
	netip.MustParsePrefix("ff00::/8"),        // 组播
}

// blockedAddress 判断地址是否不允许出站访问。只允许公网地址。
func blockedAddress(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// guardedTransport 在 traffic.Transport 的基础上拒绝内网目标。
// 连接建立前先解析主机名并校验解析结果，再按解析出的 IP 拨号，避免 DNS rebinding。
func guardedTransport(m *traffic.Meter, kind string, keepAlive bool) *http.Transport {
	t := traffic.Transport(m, kind, keepAlive)
	// 走环境变量里的代理会绕过地址校验，订阅与用量请求不允许
	t.Proxy = nil
	inner := t.DialContext
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialPublic(ctx, network, addr, inner)
	}
	return t
}

func dialPublic(ctx context.Context, network, addr string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		if targetBlocked(ip) {
			return nil, ErrBlockedTarget
		}
		return dial(ctx, network, addr)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", strings.TrimSuffix(host, "."))
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		if targetBlocked(ip) {
			lastErr = ErrBlockedTarget
			continue
		}
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = ErrBlockedTarget
	}
	return nil, lastErr
}
