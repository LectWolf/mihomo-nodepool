package pool

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// fetchFailure 把下载失败归纳成用户能看懂的原因，不暴露订阅地址与令牌
func fetchFailure(e error) string {
	var netErr net.Error
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var opErr *net.OpError
	switch {
	case errors.Is(e, ErrBlockedTarget):
		return "订阅地址指向内网或本机，已拒绝访问"
	case errors.Is(e, context.DeadlineExceeded) || (errors.As(e, &netErr) && netErr.Timeout()):
		return "订阅请求超时，保留上次节点"
	case errors.As(e, &dnsErr):
		return "订阅域名解析失败，保留上次节点"
	case errors.As(e, &certErr):
		return "订阅站点证书校验失败，保留上次节点"
	case errors.As(e, &opErr):
		return "订阅站点无法连接，保留上次节点"
	}
	return "订阅请求失败，保留上次节点"
}

// upstreamStatus 把上游状态码翻译成订阅者能看懂的原因
func upstreamStatus(code int) string {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Sprintf("订阅链接已失效或无权限（HTTP %d），保留上次节点", code)
	case code == http.StatusNotFound:
		return "订阅链接不存在（HTTP 404），保留上次节点"
	case code == http.StatusTooManyRequests:
		return "上游限流（HTTP 429），保留上次节点"
	case code >= 500:
		return fmt.Sprintf("上游服务出错（HTTP %d），保留上次节点", code)
	}
	return fmt.Sprintf("上游返回 HTTP %d，保留上次节点", code)
}
