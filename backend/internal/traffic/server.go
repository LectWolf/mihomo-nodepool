package traffic

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// 服务端连接：请求处理完成（进入空闲、关闭或被接管后关闭）时，把这段时间的读写计入当前请求的分类
type serverConn struct {
	net.Conn
	m       *Meter
	in, out atomic.Int64
	mu      sync.Mutex
	markIn  int64
	markOut int64
	key     atomic.Pointer[Key]
}

func (x *serverConn) Read(b []byte) (int, error) {
	n, err := x.Conn.Read(b)
	x.in.Add(int64(n))
	return n, err
}
func (x *serverConn) Write(b []byte) (int, error) {
	n, err := x.Conn.Write(b)
	x.out.Add(int64(n))
	return n, err
}
func (x *serverConn) Close() error {
	err := x.Conn.Close()
	x.settle()
	return err
}
func (x *serverConn) CloseWrite() error {
	if cw, ok := x.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

func (x *serverConn) settle() {
	x.mu.Lock()
	in, out := x.in.Load(), x.out.Load()
	dIn, dOut := in-x.markIn, out-x.markOut
	x.markIn, x.markOut = in, out
	x.mu.Unlock()
	if dIn == 0 && dOut == 0 {
		return
	}
	k := x.key.Load()
	if k == nil {
		k = &Key{KindWeb, "unrouted"}
	}
	c := x.m.Counter(k.Kind, k.Detail)
	c.AddIn(dIn)
	c.AddOut(dOut)
}

type listener struct {
	net.Listener
	m *Meter
}

func (l listener) Accept() (net.Conn, error) {
	nc, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &serverConn{Conn: nc, m: l.m}, nil
}

// Listen 包装监听器；配合 Server 使用
func Listen(ln net.Listener, m *Meter) net.Listener { return listener{ln, m} }

type connKey struct{}

// Server 给 http.Server 挂上连接上下文和状态回调
func Server(srv *http.Server) {
	prevCtx, prevState := srv.ConnContext, srv.ConnState
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if prevCtx != nil {
			ctx = prevCtx(ctx, c)
		}
		if sc, ok := c.(*serverConn); ok {
			ctx = context.WithValue(ctx, connKey{}, sc)
		}
		return ctx
	}
	srv.ConnState = func(c net.Conn, s http.ConnState) {
		if prevState != nil {
			prevState(c, s)
		}
		if sc, ok := c.(*serverConn); ok && (s == http.StateIdle || s == http.StateClosed) {
			sc.settle()
		}
	}
}

// Tag 修改当前请求的分类，处理函数里知道更细的类型时调用（例如订阅格式）
func TagRequest(r *http.Request, kind, detail string) {
	if sc, ok := r.Context().Value(connKey{}).(*serverConn); ok {
		prev := sc.key.Swap(&Key{kind, detail})
		// 请求数跟着分类迁移
		if prev != nil && *prev != (Key{kind, detail}) {
			sc.m.Counter(prev.Kind, prev.Detail).requests.Add(-1)
			sc.m.Counter(kind, detail).requests.Add(1)
		}
	}
}

// Classify 按路径给请求分类，并计一次请求
func Classify(m *Meter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sc, ok := r.Context().Value(connKey{}).(*serverConn); ok {
			k := PathKind(r.URL.Path)
			sc.key.Store(&k)
			m.Counter(k.Kind, k.Detail).Hit(false)
		}
		next.ServeHTTP(w, r)
	})
}

func PathKind(p string) Key {
	switch {
	case strings.HasPrefix(p, "/sub/"):
		return Key{KindSub, ""}
	case strings.HasPrefix(p, "/b/"):
		return Key{KindBeacon, ""}
	case strings.HasPrefix(p, "/ws/"):
		return Key{KindLease, ""}
	case p == "/healthz" || p == "/readyz":
		return Key{KindHealth, ""}
	case strings.HasPrefix(p, "/api/"):
		return Key{KindAPI, ""}
	}
	return Key{KindWeb, ""}
}
