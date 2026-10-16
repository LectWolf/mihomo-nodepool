package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/coder/websocket"
	"gopkg.in/yaml.v3"
)

const keepaliveURL = "http://www.gstatic.com/generate_204"

func registerCommunity(business businessRegistrar, s *pool.Store) {
	business("GET /api/v1/subscriptions/archives", func(w http.ResponseWriter, r *http.Request) error {
		f := catalogFilter(r)
		v, err := s.SubscriptionArchives(r.Context(), f.Page, f.PageSize)
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	business("POST /api/v1/me/checkin", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			ConfirmPartial bool `json:"confirm_partial"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		v, err := s.CheckIn(r.Context(), in.ConfirmPartial)
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	business("GET /api/v1/levels", func(w http.ResponseWriter, r *http.Request) error {
		v, err := s.LevelDistribution(r.Context())
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	business("GET /api/v1/subscriptions/history", func(w http.ResponseWriter, r *http.Request) error {
		f := catalogFilter(r)
		v, err := s.SubscriptionHistory(r.Context(), f.Page, f.PageSize)
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	business("GET /api/v1/sources/{id}/subscribers", func(w http.ResponseWriter, r *http.Request) error {
		id, err := idOf(r)
		if err != nil {
			return err
		}
		f := catalogFilter(r)
		v, err := s.SourceSubscribers(r.Context(), id, r.URL.Query().Get("state"), f.Page, f.PageSize)
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	business("PATCH /api/v1/sources/{id}/subscribers/{userID}", func(w http.ResponseWriter, r *http.Request) error {
		id, err := idOf(r)
		if err != nil {
			return err
		}
		userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
		if err != nil || userID < 1 {
			return errors.New("账号ID无效")
		}
		var in struct {
			Blocked *bool `json:"blocked"`
		}
		if err = decode(w, r, &in); err != nil {
			return err
		}
		if in.Blocked == nil {
			return errors.New("请指定是否禁止领取")
		}
		err = s.SetSourceSubscriberBlocked(r.Context(), id, userID, *in.Blocked)
		if err == nil {
			writeJSON(w, 200, map[string]bool{"blocked": *in.Blocked})
		}
		return err
	})
	business("POST /api/v1/sources/{id}/link/rotate", func(w http.ResponseWriter, r *http.Request) error {
		id, err := idOf(r)
		if err != nil {
			return err
		}
		path, err := s.RotateSourceLink(r.Context(), id)
		if err == nil {
			writeJSON(w, 200, map[string]string{"url": path})
		}
		return err
	})
}

func publicOrigin(r *http.Request, settings pool.Settings) string {
	if settings.PublicURL != "" {
		return strings.TrimRight(settings.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
func inactiveSubscription(format string) ([]byte, error) {
	marker := map[string]any{"name": "订阅已到期 · 请到平台签到激活", "type": "socks5", "server": "127.0.0.1", "port": 9, "udp": false}
	if format == "v2ray" {
		return []byte(base64.StdEncoding.EncodeToString([]byte("socks5://127.0.0.1:9#" + url.PathEscape("订阅已到期 · 请到平台签到激活") + "\n"))), nil
	}
	return yaml.Marshal(map[string]any{"proxies": []any{marker}, "proxy-groups": []any{map[string]any{"name": "请到平台签到激活", "type": "select", "proxies": []string{marker["name"].(string)}}}, "rules": []string{"MATCH,REJECT"}})
}
func attachKeepalive(raw []byte, format string, u pool.User, origin string) ([]byte, error) {
	if u.ProbeUUID == "" || u.SubscriptionExpiresAt == nil {
		return raw, nil
	}
	endpoint, err := url.Parse(origin)
	if err != nil || endpoint.Hostname() == "" {
		return nil, errors.New("公网地址无效")
	}
	port := 80
	if endpoint.Scheme == "https" {
		port = 443
	}
	if endpoint.Port() != "" {
		port, err = strconv.Atoi(endpoint.Port())
		if err != nil {
			return nil, err
		}
	}
	name := "席位保活 · 到期 " + u.SubscriptionExpiresAt.In(time.FixedZone("CST", 8*3600)).Format("01-02 15:04")
	path := "/ws/lease/" + u.ProbeUUID
	if format == "v2ray" {
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return nil, err
		}
		probe := url.URL{Scheme: "vless", User: url.User(u.ProbeUUID), Host: net.JoinHostPort(endpoint.Hostname(), strconv.Itoa(port)), Fragment: name}
		q := url.Values{"type": {"ws"}, "path": {path}, "host": {endpoint.Host}, "encryption": {"none"}}
		if endpoint.Scheme == "https" {
			q.Set("security", "tls")
			q.Set("sni", endpoint.Hostname())
		} else {
			q.Set("security", "none")
		}
		probe.RawQuery = q.Encode()
		return []byte(base64.StdEncoding.EncodeToString(append(decoded, []byte(probe.String()+"\n")...))), nil
	}
	var doc map[string]any
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	// Reserve generated names without overwriting selected user nodes/groups.
	reserved := map[string]bool{}
	for _, key := range []string{"proxies", "proxy-groups"} {
		if items, ok := doc[key].([]any); ok {
			for _, v := range items {
				if m, ok := v.(map[string]any); ok {
					if n, ok := m["name"].(string); ok {
						reserved[n] = true
					}
				}
			}
		}
	}
	for reserved[name] {
		name += " ·"
	}
	groupName := "席位自动续期"
	for reserved[groupName] || groupName == name {
		groupName += " ·"
	}
	node := map[string]any{"name": name, "type": "vless", "server": endpoint.Hostname(), "port": port, "uuid": u.ProbeUUID, "network": "ws", "tls": endpoint.Scheme == "https", "udp": false, "ws-opts": map[string]any{"path": path, "headers": map[string]string{"Host": endpoint.Host}}}
	if endpoint.Scheme == "https" {
		node["servername"] = endpoint.Hostname()
	}
	proxies, _ := doc["proxies"].([]any)
	groups, _ := doc["proxy-groups"].([]any)
	doc["proxies"] = append(proxies, node)
	doc["proxy-groups"] = append(groups, map[string]any{"name": groupName, "type": "url-test", "proxies": []string{name}, "url": keepaliveURL, "interval": 3600, "lazy": false, "hidden": true})
	return yaml.Marshal(doc)
}

func registerSourceLink(mux *http.ServeMux, s *pool.Store) {
	mux.HandleFunc("GET /link/{token}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, private, max-age=0")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Vary", "User-Agent")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.Ensure(ctx); err != nil {
			writeError(w, err)
			return
		}
		format, err := pool.ExportFormat(r.URL.Query().Get("format"), r.UserAgent())
		if err != nil {
			writeError(w, err)
			return
		}
		raw, skipped, routing, usage, err := s.ExportSourceLink(ctx, r.PathValue("token"), format)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("X-Subscription-Skipped", strconv.Itoa(skipped))
		w.Header().Set("X-Subscription-Routing-Skipped", strconv.Itoa(routing))
		w.Header().Set("X-Subscription-Format", format)
		if usage != nil {
			parts := []string{}
			if usage.UploadBytes != nil {
				parts = append(parts, "upload="+strconv.FormatInt(*usage.UploadBytes, 10))
			}
			if usage.DownloadBytes != nil {
				parts = append(parts, "download="+strconv.FormatInt(*usage.DownloadBytes, 10))
			}
			if usage.TotalBytes != nil {
				parts = append(parts, "total="+strconv.FormatInt(*usage.TotalBytes, 10))
			}
			if usage.ExpiresAt != nil {
				parts = append(parts, "expire="+strconv.FormatInt(usage.ExpiresAt.Unix(), 10))
			}
			if len(parts) > 0 {
				w.Header().Set("Subscription-Userinfo", strings.Join(parts, "; "))
			}
		}
		if format == "clash" {
			w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(200)
		_, _ = w.Write(raw)
	})
}

// This implements only a VLESS/WS request to the fixed HTTP 204 target. It never
// opens outbound connections and cannot act as a general-purpose proxy.
func registerLeaseProbe(mux *http.ServeMux, s *pool.Store) {
	mux.HandleFunc("GET /ws/lease/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		uuid := r.PathValue("uuid")
		uuidBytes, err := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
		if err != nil || len(uuidBytes) != 16 {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err = s.Ensure(ctx); err != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(4096)
		stream := websocket.NetConn(ctx, conn, websocket.MessageBinary)
		reader := bufio.NewReader(io.LimitReader(stream, 4096))
		header := make([]byte, 18)
		if _, err = io.ReadFull(reader, header); err != nil || header[0] != 0 || string(header[1:17]) != string(uuidBytes) {
			return
		}
		if header[17] > 0 {
			if _, err = io.CopyN(io.Discard, reader, int64(header[17])); err != nil {
				return
			}
		}
		command := make([]byte, 4)
		if _, err = io.ReadFull(reader, command); err != nil || command[0] != 1 || command[1] != 0 || command[2] != 80 || command[3] != 2 {
			return
		}
		length, err := reader.ReadByte()
		if err != nil || length == 0 {
			return
		}
		host := make([]byte, int(length))
		if _, err = io.ReadFull(reader, host); err != nil || string(host) != "www.gstatic.com" {
			return
		}
		// VLESS response precedes the application request. The destination remains
		// fixed; only HEAD/GET /generate_204 actually renews the lease.
		if _, err = stream.Write([]byte{0, 0}); err != nil {
			return
		}
		request, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		if request.Body != nil {
			defer request.Body.Close()
		}
		if (request.Method != "GET" && request.Method != "HEAD") || request.URL.Path != "/generate_204" || request.URL.RawQuery != "" || request.Host != "www.gstatic.com" {
			return
		}
		if err = s.RenewByProbe(ctx, uuid); err != nil {
			return
		}
		_, _ = io.WriteString(stream, "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	})
}
