package traffic

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// 统计的是应用层读写的字节，不含 IP/TCP/UDP 头和重传；小包多的握手实际网卡流量会更高一些

type conn struct {
	net.Conn
	c *Counter
}

// WrapConn 让连接的读写计入 c
func WrapConn(nc net.Conn, c *Counter) net.Conn {
	if nc == nil || c == nil {
		return nc
	}
	return &conn{nc, c}
}
func (x *conn) Read(b []byte) (int, error) {
	n, err := x.Conn.Read(b)
	x.c.AddIn(int64(n))
	return n, err
}
func (x *conn) Write(b []byte) (int, error) {
	n, err := x.Conn.Write(b)
	x.c.AddOut(int64(n))
	return n, err
}
func (x *conn) CloseWrite() error {
	if cw, ok := x.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return errors.ErrUnsupported
}
func (x *conn) Unwrap() net.Conn { return x.Conn }

type packetConn struct {
	net.PacketConn
	c *Counter
}

func WrapPacketConn(pc net.PacketConn, c *Counter) net.PacketConn {
	if pc == nil || c == nil {
		return pc
	}
	return &packetConn{pc, c}
}
func (x *packetConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := x.PacketConn.ReadFrom(b)
	x.c.AddIn(int64(n))
	return n, addr, err
}
func (x *packetConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := x.PacketConn.WriteTo(b, addr)
	x.c.AddOut(int64(n))
	return n, err
}
func (x *packetConn) SetReadBuffer(n int) error {
	if s, ok := x.PacketConn.(interface{ SetReadBuffer(int) error }); ok {
		return s.SetReadBuffer(n)
	}
	return errors.ErrUnsupported
}
func (x *packetConn) SetWriteBuffer(n int) error {
	if s, ok := x.PacketConn.(interface{ SetWriteBuffer(int) error }); ok {
		return s.SetWriteBuffer(n)
	}
	return errors.ErrUnsupported
}

// Tag 可切换的计数目标：同一个节点先做延迟检测、再做出口查询时分别计数
type Tag struct{ p atomic.Pointer[Counter] }

func NewTag(c *Counter) *Tag {
	t := &Tag{}
	t.p.Store(c)
	return t
}
func (t *Tag) Set(c *Counter) {
	if t != nil {
		t.p.Store(c)
	}
}
func (t *Tag) Counter() *Counter {
	if t == nil {
		return nil
	}
	return t.p.Load()
}

type detailKey struct{}

// WithDetail 给出站请求指定 detail，只对 Transport 新建的连接生效
func WithDetail(ctx context.Context, detail string) context.Context {
	return context.WithValue(ctx, detailKey{}, detail)
}

// Transport 新建连接按 kind 计数；关闭复用时可以按请求的 detail 精确区分
func Transport(m *Meter, kind string, keepAlive bool) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		nc, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		detail, _ := ctx.Value(detailKey{}).(string)
		return WrapConn(nc, m.Counter(kind, detail)), nil
	}
	t.DisableKeepAlives = !keepAlive
	return t
}
