package pool

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrUnexportable = errors.New("没有可导出的节点；v2ray格式仅支持兼容分享URI的客户端导入")

func ExportFormat(explicit, ua string) (string, error) {
	switch explicit {
	case "clash", "v2ray":
		return explicit, nil
	case "", "auto":
	default:
		return "", errors.New("format须为clash、v2ray或auto")
	}
	ua = strings.ToLower(ua)
	for _, name := range []string{"mihomo", "clash", "stash"} {
		if strings.Contains(ua, name) {
			return "clash", nil
		}
	}
	for _, name := range []string{"v2rayn", "v2rayng", "nekobox", "shadowrocket", "hiddify"} {
		if strings.Contains(ua, name) {
			return "v2ray", nil
		}
	}
	return "clash", nil
}

// ExportSubscription 不输出来源地址、来源内容或路由指令；v2ray 转换规则见 shareURI
func ExportSubscription(nodes []Node, format string) ([]byte, int, error) {
	configs := []map[string]any{}
	used := map[string]bool{"DIRECT": true, "REJECT": true, "NodePool": true, SubscriptionDisplayName: true}
	skipped := 0
	for _, n := range nodes {
		var m map[string]any
		if json.Unmarshal(n.Config, &m) != nil || m == nil {
			skipped++
			continue
		}
		name := n.Name
		if name == "" {
			name = text(m["name"])
		}
		if name == "" {
			name = fmt.Sprintf("节点 %d", n.ID)
		}
		base := name
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s (%d)", base, i)
		}
		used[name] = true
		m["name"] = name
		configs = append(configs, m)
	}
	if format == "clash" {
		if len(configs) == 0 {
			return nil, skipped, ErrUnexportable
		}
		names := []string{}
		for _, m := range configs {
			names = append(names, text(m["name"]))
		}
		hidden := expandDialers(configs, used)
		doc := map[string]any{"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "info", "proxies": append(append([]map[string]any{}, configs...), hidden...), "proxy-groups": []any{map[string]any{"name": SubscriptionDisplayName, "type": "select", "proxies": append(names, "DIRECT")}}, "rules": []string{"MATCH," + SubscriptionDisplayName}}
		raw, e := yaml.Marshal(doc)
		return raw, skipped, e
	}
	if format != "v2ray" {
		return nil, skipped, errors.New("不支持的导出格式")
	}
	uris := []string{}
	for _, m := range configs {
		u, e := shareURI(m)
		if e != nil {
			skipped++
			continue
		}
		uris = append(uris, u)
	}
	if len(uris) == 0 {
		return nil, skipped, ErrUnexportable
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(uris, "\n") + "\n"))), skipped, nil
}
func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func str(m map[string]any, k string) string { return text(m[k]) }

// truthy 兼容布尔值和 "true"/"1" 字符串
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(x))
		return b
	case float64:
		return x != 0
	case int:
		return x != 0
	}
	return false
}
func opts(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
}

// first 取字符串或字符串列表的第一个非空值
func first(v any) string {
	switch x := v.(type) {
	case []any:
		for _, a := range x {
			if s := text(a); s != "" {
				return s
			}
		}
	case []string:
		for _, s := range x {
			if s != "" {
				return s
			}
		}
	case nil:
	default:
		return text(x)
	}
	return ""
}
func stringList(v any) string {
	items := []string{}
	switch x := v.(type) {
	case []any:
		for _, a := range x {
			if s, ok := a.(string); ok && s != "" && !strings.Contains(s, ",") {
				items = append(items, s)
			}
		}
	case []string:
		items = x
	case string:
		return x
	}
	return strings.Join(items, ",")
}
func headerHost(h map[string]any) string {
	for k, v := range h {
		if strings.EqualFold(k, "host") {
			return first(v)
		}
	}
	return ""
}

// shareURI 把 Mihomo 节点转换为 v2rayN 等客户端通用的分享链接。
// 只要求连接必需的信息能完整表示；TFO、多路复用、UDP 开关、early data 等
// 客户端调优参数忽略，链式代理、未知协议、未知传输和插件无法连接，跳过。
func shareURI(m map[string]any) (string, error) {
	typ := str(m, "type")
	name := str(m, "name")
	host := strings.Trim(str(m, "server"), "[]")
	port := str(m, "port")
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 || host == "" || str(m, "dialer-proxy") != "" || m[DialerKey] != nil {
		return "", ErrUnexportable
	}
	port = strconv.Itoa(p)
	u := &url.URL{Scheme: typ, Host: net.JoinHostPort(host, port), Fragment: name}
	q := url.Values{}
	switch typ {
	case "ss":
		cipher, password := str(m, "cipher"), str(m, "password")
		if cipher == "" || password == "" {
			return "", ErrUnexportable
		}
		if strings.HasPrefix(cipher, "2022-") {
			u.User = url.UserPassword(cipher, password)
		} else {
			u.User = url.User(base64.RawURLEncoding.EncodeToString([]byte(cipher + ":" + password)))
		}
		if plugin := str(m, "plugin"); plugin != "" {
			encoded, e := pluginOptions(plugin, opts(m, "plugin-opts"))
			if e != nil {
				return "", e
			}
			q.Set("plugin", encoded)
		}
	case "vmess":
		vm := map[string]any{"v": "2", "ps": name, "add": host, "port": port, "id": str(m, "uuid"), "aid": "0", "scy": "auto", "net": "tcp", "type": "none", "host": "", "path": "", "tls": ""}
		if vm["id"] == "" {
			return "", ErrUnexportable
		}
		if v := str(m, "alterId"); v != "" {
			vm["aid"] = v
		}
		if v := str(m, "cipher"); v != "" {
			vm["scy"] = v
		}
		if e = transport(m, q); e != nil {
			return "", e
		}
		vm["net"] = q.Get("type")
		if q.Get("headerType") != "" {
			vm["type"] = q.Get("headerType")
		}
		if q.Get("mode") != "" {
			vm["type"] = q.Get("mode")
		}
		vm["host"] = q.Get("host")
		vm["path"] = q.Get("path")
		if q.Get("type") == "grpc" {
			vm["path"] = q.Get("serviceName")
		}
		if truthy(m["tls"]) {
			vm["tls"] = "tls"
			if v := firstNonEmpty(str(m, "servername"), str(m, "sni")); v != "" {
				vm["sni"] = v
			}
			if v := str(m, "client-fingerprint"); v != "" {
				vm["fp"] = v
			}
			if v := stringList(m["alpn"]); v != "" {
				vm["alpn"] = v
			}
			if truthy(m["skip-cert-verify"]) {
				vm["insecure"] = "1"
			}
		}
		raw, e := json.Marshal(vm)
		if e != nil {
			return "", e
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(raw), nil
	case "vless", "trojan":
		secret := str(m, "uuid")
		if typ == "trojan" {
			secret = str(m, "password")
		}
		if secret == "" {
			return "", ErrUnexportable
		}
		u.User = url.User(secret)
		if e = transport(m, q); e != nil {
			return "", e
		}
		if typ == "vless" {
			enc := str(m, "encryption")
			if enc == "" {
				enc = "none"
			}
			q.Set("encryption", enc)
			if v := str(m, "flow"); v != "" {
				q.Set("flow", v)
			}
		}
		// Trojan 始终使用 TLS
		tls := typ == "trojan" || truthy(m["tls"])
		if !tls {
			q.Set("security", "none")
			break
		}
		q.Set("security", "tls")
		tlsQuery(m, q, "allowInsecure")
		if v := str(m, "client-fingerprint"); v != "" {
			q.Set("fp", v)
		}
		if ro := opts(m, "reality-opts"); ro != nil && typ == "vless" {
			if str(ro, "public-key") == "" {
				return "", ErrUnexportable
			}
			q.Set("security", "reality")
			q.Set("pbk", str(ro, "public-key"))
			if v := str(ro, "short-id"); v != "" {
				q.Set("sid", v)
			}
		}
	case "hysteria2", "hy2":
		u.Scheme = "hysteria2"
		password := str(m, "password")
		if password == "" {
			return "", ErrUnexportable
		}
		u.User = url.User(password)
		tlsQuery(m, q, "insecure")
		if v := str(m, "fingerprint"); v != "" {
			q.Set("pinSHA256", v)
		}
		if v := str(m, "ports"); v != "" {
			q.Set("mport", strings.ReplaceAll(v, ":", "-"))
		}
		if obfs := str(m, "obfs"); obfs != "" {
			if obfs != "salamander" || str(m, "obfs-password") == "" {
				return "", ErrUnexportable
			}
			q.Set("obfs", obfs)
			q.Set("obfs-password", str(m, "obfs-password"))
		}
	case "tuic":
		uuid, password := str(m, "uuid"), str(m, "password")
		if uuid == "" || password == "" {
			return "", ErrUnexportable
		}
		u.User = url.UserPassword(uuid, password)
		tlsQuery(m, q, "allow_insecure")
		for key, param := range map[string]string{"congestion-controller": "congestion_control", "udp-relay-mode": "udp_relay_mode"} {
			if v := str(m, key); v != "" {
				q.Set(param, v)
			}
		}
		if truthy(m["reduce-rtt"]) {
			q.Set("reduce_rtt", "1")
		}
		if truthy(m["disable-sni"]) {
			q.Set("disable_sni", "1")
		}
	case "anytls":
		password := str(m, "password")
		if password == "" {
			return "", ErrUnexportable
		}
		u.User = url.User(password)
		tlsQuery(m, q, "insecure")
		if v := str(m, "client-fingerprint"); v != "" {
			q.Set("fp", v)
		}
	case "http", "socks5", "socks":
		if typ == "socks" {
			u.Scheme = "socks5"
		}
		username, password := str(m, "username"), str(m, "password")
		if username != "" || password != "" {
			u.User = url.UserPassword(username, password)
		}
		if truthy(m["tls"]) {
			if typ != "http" {
				return "", ErrUnexportable
			}
			u.Scheme = "https"
		}
	default:
		return "", ErrUnexportable
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// tlsQuery 写入 SNI、ALPN 和跳过证书验证；insecure 为该协议使用的参数名
func tlsQuery(m map[string]any, q url.Values, insecure string) {
	if v := firstNonEmpty(str(m, "servername"), str(m, "sni")); v != "" {
		q.Set("sni", v)
	}
	if v := stringList(m["alpn"]); v != "" {
		q.Set("alpn", v)
	}
	if truthy(m["skip-cert-verify"]) {
		q.Set(insecure, "1")
		if insecure == "allowInsecure" {
			q.Set("insecure", "1")
		}
	}
}
func transport(m map[string]any, q url.Values) error {
	network := str(m, "network")
	if network == "" {
		network = "tcp"
	}
	q.Set("type", network)
	switch network {
	case "tcp":
	case "ws", "httpupgrade":
		o := opts(m, "ws-opts")
		if truthy(o["v2ray-http-upgrade"]) {
			q.Set("type", "httpupgrade")
		}
		setNonEmpty(q, "path", str(o, "path"))
		setNonEmpty(q, "host", headerHost(opts(o, "headers")))
	case "grpc":
		setNonEmpty(q, "serviceName", str(opts(m, "grpc-opts"), "grpc-service-name"))
	case "h2":
		o := opts(m, "h2-opts")
		q.Set("type", "http")
		setNonEmpty(q, "path", str(o, "path"))
		setNonEmpty(q, "host", stringList(o["host"]))
	case "http":
		o := opts(m, "http-opts")
		q.Set("type", "tcp")
		q.Set("headerType", "http")
		setNonEmpty(q, "path", first(o["path"]))
		setNonEmpty(q, "host", headerHost(opts(o, "headers")))
	case "xhttp":
		o := opts(m, "xhttp-opts")
		setNonEmpty(q, "path", str(o, "path"))
		setNonEmpty(q, "host", str(o, "host"))
		setNonEmpty(q, "mode", str(o, "mode"))
	default:
		return ErrUnexportable
	}
	return nil
}
func setNonEmpty(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

// pluginOptions 生成 SIP002 插件参数；只支持 obfs 和 v2ray-plugin
func pluginOptions(plugin string, o map[string]any) (string, error) {
	switch plugin {
	case "obfs":
		parts := []string{"obfs-local"}
		if v := str(o, "mode"); v != "" {
			parts = append(parts, "obfs="+v)
		}
		if v := str(o, "host"); v != "" {
			parts = append(parts, "obfs-host="+v)
		}
		return strings.Join(parts, ";"), nil
	case "v2ray-plugin":
		parts := []string{"v2ray-plugin"}
		for _, k := range []string{"host", "mode", "path"} {
			if v := str(o, k); v != "" {
				if strings.ContainsAny(v, ";=\r\n") {
					return "", ErrUnexportable
				}
				parts = append(parts, k+"="+v)
			}
		}
		for _, k := range []string{"skip-cert-verify", "tls"} {
			if truthy(o[k]) {
				parts = append(parts, k)
			}
		}
		sort.Strings(parts[1:])
		return strings.Join(parts, ";"), nil
	}
	return "", ErrUnexportable
}
