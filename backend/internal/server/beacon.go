package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/cnip"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// registerBeacon 接收客户端经节点发来的实测回报；HEAD/GET 均返回空 204，尽量少占流量
func registerBeacon(mux *http.ServeMux, s *pool.Store) {
	mux.HandleFunc("GET /b/{token}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if s.Ensure(ctx) == nil {
			if err := s.RecordBeacon(ctx, r.PathValue("token")); err != nil {
				log.Printf("beacon: %v", err)
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

// attachBeacons 回报走 HTTP，避免每次检测都做 TLS 握手；失败时原样返回订阅
func attachBeacons(ctx context.Context, s *pool.Store, raw []byte, token string, nodes []pool.Node, origin, ip string) []byte {
	base, ok := beaconBase(origin)
	if !ok {
		return raw
	}
	tokens, err := s.BeaconTokens(ctx, token, nodes, cnip.Contains(ip))
	if err != nil {
		log.Printf("beacon plan: %v", err)
		return raw
	}
	out, err := pool.AttachBeacons(raw, nodes, tokens, base)
	if err != nil {
		log.Printf("beacon attach: %v", err)
		return raw
	}
	return out
}

func beaconBase(origin string) (string, bool) {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	// HTTPS 站点的回报统一走 80 端口；纯 HTTP 站点沿用原端口
	if u.Scheme == "http" && u.Port() != "" {
		host += ":" + u.Port()
	}
	return "http://" + host, true
}
