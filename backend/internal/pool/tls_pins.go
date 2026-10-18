package pool

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/quic-go"
	mtls "github.com/metacubex/tls"
)

// 证书指纹：Xray 新版移除了 allowInsecure（跳过证书验证），配置里只有它而没有
// pinnedPeerCertSha256 时内核拒绝启动，新版 v2rayN 也不再写入 allowInsecure。
// 本站直连跳过证书验证的 TLS 节点读取证书，v2ray 格式导出时附带 pcs，新旧客户端都能用。
const (
	TLSPinRefresh = 24 * time.Hour
	TLSPinRetry   = 6 * time.Hour
	tlsPinBatch   = 200
	tlsPinTimeout = 6 * time.Second
	// pinKey 只在 v2ray 导出时临时写入节点配置
	pinKey = "_nodepool_pcs"
)

// TLSPinPrivateTargets 仅供测试：允许读取本机和内网地址的证书
var TLSPinPrivateTargets = false

func (s *Store) ensureTLSPins(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS np_node_tls (
 node_id BIGINT PRIMARY KEY, needed TINYINT(1) NOT NULL, pin VARCHAR(200) NOT NULL DEFAULT '',
 checked_at DATETIME(6) NOT NULL, next_at DATETIME(6) NOT NULL, INDEX(needed,next_at),
 FOREIGN KEY(node_id) REFERENCES np_nodes(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
	return err
}

type tlsTarget struct {
	host, port, sni string
	alpn            []string
	quic            bool
}

// pinTarget 判断节点是否需要证书指纹：TLS（非 Reality）且跳过证书验证、还没有自带指纹
func pinTarget(config json.RawMessage) (tlsTarget, bool) {
	var m map[string]any
	if json.Unmarshal(config, &m) != nil || !truthy(m["skip-cert-verify"]) || certPin(m) != "" || opts(m, "reality-opts") != nil {
		return tlsTarget{}, false
	}
	t := tlsTarget{host: strings.Trim(str(m, "server"), "[]"), port: str(m, "port")}
	switch strings.ToLower(str(m, "type")) {
	case "vmess", "vless":
		if !truthy(m["tls"]) {
			return t, false
		}
	case "trojan":
	case "hysteria2", "hy2":
		if str(m, "obfs") != "" {
			return t, false
		}
		t.quic, t.alpn = true, []string{"h3"}
	default:
		return t, false
	}
	if t.host == "" || t.port == "" {
		return t, false
	}
	t.sni = str(m, "servername")
	if t.sni == "" {
		t.sni = str(m, "sni")
	}
	if t.sni == "" {
		for _, k := range []string{"ws-opts", "http-upgrade-opts", "h2-opts"} {
			if h := opts(opts(m, k), "headers"); h != nil && str(h, "Host") != "" {
				t.sni = str(h, "Host")
				break
			}
		}
	}
	if t.sni == "" && net.ParseIP(t.host) == nil {
		t.sni = t.host
	}
	if !t.quic {
		for _, v := range listValue(m["alpn"]) {
			t.alpn = append(t.alpn, v)
		}
	}
	return t, true
}

func listValue(v any) []string {
	switch x := v.(type) {
	case []any:
		out := []string{}
		for _, a := range x {
			if s := strings.TrimSpace(text(a)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.FieldsFunc(x, func(r rune) bool { return r == ',' })
	}
	return nil
}

// certPin 节点自带的证书指纹（mihomo 的 fingerprint 字段），去掉冒号并转小写
func certPin(m map[string]any) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(str(m, "fingerprint")), ":", ""))
}

func publicIP(ctx context.Context, host string) (net.IP, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if TLSPinPrivateTargets || !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()) {
			return ip, nil
		}
	}
	return nil, errors.New("节点地址不是公网地址")
}

// fetchPin 读取服务器证书，返回叶子证书和签发证书的 SHA256（逗号分隔）。
// 签发证书用于证书续期后仍能通过：Xray 匹配到 CA 时会按域名校验叶子证书
func fetchPin(ctx context.Context, t tlsTarget) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tlsPinTimeout)
	defer cancel()
	ip, err := publicIP(ctx, t.host)
	if err != nil {
		return "", err
	}
	addr := net.JoinHostPort(ip.String(), t.port)
	var certs []*x509.Certificate
	if t.quic {
		// mihomo 的 quic-go 使用自带的 tls 分支
		conn, err := quic.DialAddr(ctx, addr, &mtls.Config{ServerName: t.sni, InsecureSkipVerify: true, NextProtos: t.alpn}, &quic.Config{HandshakeIdleTimeout: tlsPinTimeout})
		if err != nil {
			return "", err
		}
		certs = conn.ConnectionState().TLS.PeerCertificates
		_ = conn.CloseWithError(0, "")
	} else {
		d := tls.Dialer{NetDialer: &net.Dialer{Timeout: tlsPinTimeout}, Config: &tls.Config{ServerName: t.sni, InsecureSkipVerify: true, NextProtos: t.alpn}}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return "", err
		}
		certs = conn.(*tls.Conn).ConnectionState().PeerCertificates
		_ = conn.Close()
	}
	if len(certs) == 0 {
		return "", errors.New("服务器没有返回证书")
	}
	pins := []string{}
	for i, c := range certs {
		if i > 1 {
			break
		}
		sum := sha256.Sum256(c.Raw)
		pins = append(pins, hex.EncodeToString(sum[:]))
	}
	return strings.Join(pins, ","), nil
}

// RefreshTLSPins 为跳过证书验证的节点读取证书指纹：新节点先处理，成功后每天复查，失败 6 小时后重试
func (s *Store) RefreshTLSPins(ctx context.Context) error {
	now := utcNow()
	rows, err := s.DB.QueryContext(ctx, `SELECT `+nodeColumns+` FROM np_nodes n LEFT JOIN np_node_tls t ON t.node_id=n.id
 WHERE n.blocked=0 AND n.type IN ('vmess','vless','trojan','hysteria2','hy2') AND (t.node_id IS NULL OR (t.needed=1 AND t.next_at<=?))
 ORDER BY t.node_id IS NOT NULL, t.next_at LIMIT `+strconv.Itoa(tlsPinBatch), now)
	if err != nil {
		return err
	}
	nodes := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			rows.Close()
			return err
		}
		nodes = append(nodes, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	type result struct {
		id     int64
		needed bool
		pin    string
		next   time.Time
	}
	results := make([]result, len(nodes))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, n := range nodes {
		t, ok := pinTarget(n.Config)
		if !ok {
			results[i] = result{id: n.ID, next: now}
			continue
		}
		wg.Add(1)
		go func(i int, id int64, t tlsTarget) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			pin, err := fetchPin(ctx, t)
			if err != nil {
				results[i] = result{id: id, needed: true, next: now.Add(TLSPinRetry)}
				return
			}
			results[i] = result{id: id, needed: true, pin: pin, next: now.Add(TLSPinRefresh)}
		}(i, n.ID, t)
	}
	wg.Wait()
	for _, r := range results {
		// 读取失败时保留旧指纹
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO np_node_tls(node_id,needed,pin,checked_at,next_at) VALUES(?,?,?,?,?)
 ON DUPLICATE KEY UPDATE needed=VALUES(needed),pin=IF(VALUES(pin)='',pin,VALUES(pin)),checked_at=VALUES(checked_at),next_at=VALUES(next_at)`, r.id, r.needed, r.pin, now, r.next); err != nil {
			return err
		}
	}
	return nil
}

// attachTLSPins 为 v2ray 导出补上证书指纹
func (s *Store) attachTLSPins(ctx context.Context, nodes []Node) error {
	index := map[int64][]int{}
	ids := []any{}
	for i, n := range nodes {
		if _, ok := index[n.ID]; !ok {
			ids = append(ids, n.ID)
		}
		index[n.ID] = append(index[n.ID], i)
	}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		rows, err := s.DB.QueryContext(ctx, `SELECT node_id,pin FROM np_node_tls WHERE needed=1 AND pin<>'' AND node_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`)`, chunk...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var pin string
			if err = rows.Scan(&id, &pin); err != nil {
				rows.Close()
				return err
			}
			for _, i := range index[id] {
				nodes[i].TLSPin = pin
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
	}
	return nil
}
