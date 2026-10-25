// Package trustedproxy 从请求中取出真实客户端地址，只在直连地址属于可信反向代理时读取 X-Forwarded-For。
package trustedproxy

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP 返回请求发起方的地址。没有配置可信代理时直接使用 RemoteAddr，
// 避免客户端伪造 X-Forwarded-For 绕过按 IP 的风控与限流。
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isTrusted(host, trusted) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		item := strings.TrimSpace(parts[i])
		if item == "" {
			continue
		}
		if !isTrusted(item, trusted) {
			if net.ParseIP(item) == nil {
				return host
			}
			return item
		}
	}
	return host
}

func isTrusted(raw string, trusted []*net.IPNet) bool {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return false
	}
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
