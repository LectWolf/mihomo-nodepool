package pool

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
)

func TestFetchFailureMessages(t *testing.T) {
	certErr := &tls.CertificateVerificationError{Err: errors.New("x509: certificate signed by unknown authority")}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"inner target", ErrBlockedTarget, "订阅地址指向内网或本机，已拒绝访问"},
		{"deadline", context.DeadlineExceeded, "订阅请求超时，保留上次节点"},
		{"dns", &net.DNSError{Err: "no such host", Name: "sub.example.com"}, "订阅域名解析失败，保留上次节点"},
		{"certificate", certErr, "订阅站点证书校验失败，保留上次节点"},
		{"dial", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "订阅站点无法连接，保留上次节点"},
		{"other", errors.New("boom"), "订阅请求失败，保留上次节点"},
	}
	for _, c := range cases {
		if got := fetchFailure(c.err); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestUpstreamStatusMessages(t *testing.T) {
	cases := map[int]string{
		401: "订阅链接已失效或无权限（HTTP 401），保留上次节点",
		403: "订阅链接已失效或无权限（HTTP 403），保留上次节点",
		404: "订阅链接不存在（HTTP 404），保留上次节点",
		429: "上游限流（HTTP 429），保留上次节点",
		502: "上游服务出错（HTTP 502），保留上次节点",
		418: "上游返回 HTTP 418，保留上次节点",
	}
	for code, want := range cases {
		if got := upstreamStatus(code); got != want {
			t.Errorf("%d: got %q want %q", code, got, want)
		}
	}
}
