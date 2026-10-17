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

// ExportSubscription never includes provider URLs, source contents or directives.
// URI conversion is fail-closed: every configuration key must be represented or
// known to have the same semantics as the URI format's default.
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

type uriConfig struct {
	values map[string]any
	used   map[string]bool
	err    error
}

func (c *uriConfig) take(k string) any   { c.used[k] = true; return c.values[k] }
func (c *uriConfig) str(k string) string { return text(c.take(k)) }
func (c *uriConfig) flag(k string) bool {
	v := c.take(k)
	if v == nil {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		c.err = ErrUnexportable
	}
	return b
}
func (c *uriConfig) complete() error {
	if c.err != nil {
		return c.err
	}
	for k := range c.values {
		if !c.used[k] {
			return fmt.Errorf("URI不能保真表示参数%s", k)
		}
	}
	return nil
}
func stringList(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case []any:
		items := []string{}
		for _, a := range x {
			s, ok := a.(string)
			if !ok || strings.Contains(s, ",") {
				return "", ErrUnexportable
			}
			items = append(items, s)
		}
		return strings.Join(items, ","), nil
	case []string:
		return strings.Join(x, ","), nil
	default:
		return "", ErrUnexportable
	}
}
func shareURI(m map[string]any) (string, error) {
	c := &uriConfig{values: m, used: map[string]bool{}}
	typ := c.str("type")
	name := c.str("name")
	host := c.str("server")
	port := c.str("port")
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 || host == "" {
		return "", ErrUnexportable
	}
	if udp, ok := m["udp"]; ok {
		if udp != true {
			return "", ErrUnexportable
		}
		c.take("udp")
	}
	u := &url.URL{Scheme: typ, Host: net.JoinHostPort(strings.Trim(host, "[]"), port), Fragment: name}
	q := url.Values{}
	switch typ {
	case "ss":
		cipher, password := c.str("cipher"), c.str("password")
		if cipher == "" || password == "" {
			return "", ErrUnexportable
		}
		if strings.HasPrefix(cipher, "2022-") {
			u.User = url.UserPassword(cipher, password)
		} else {
			u.User = url.User(base64.RawURLEncoding.EncodeToString([]byte(cipher + ":" + password)))
		}
		plugin := c.str("plugin")
		if plugin != "" {
			if plugin != "obfs" && plugin != "v2ray-plugin" {
				return "", ErrUnexportable
			}
			rawOpts := c.take("plugin-opts")
			opts, ok := rawOpts.(map[string]any)
			if rawOpts != nil && !ok {
				return "", ErrUnexportable
			}
			encoded, e := pluginOptions(plugin, opts)
			if e != nil {
				return "", e
			}
			q.Set("plugin", encoded)
		}
	case "vmess":
		vm := map[string]any{"v": "2", "ps": name, "add": host, "port": port, "id": c.str("uuid"), "aid": "0", "scy": "auto", "net": "tcp", "type": "none", "host": "", "path": "", "tls": ""}
		if vm["id"] == "" {
			return "", ErrUnexportable
		}
		if v := c.str("alterId"); v != "" {
			vm["aid"] = v
		}
		if v := c.str("cipher"); v != "" {
			vm["scy"] = v
		}
		if e = transport(c, q); e != nil {
			return "", e
		}
		vm["net"] = q.Get("type")
		if vm["net"] == "" {
			vm["net"] = "tcp"
		}
		if q.Get("path") != "" {
			vm["path"] = q.Get("path")
		}
		if q.Get("host") != "" {
			vm["host"] = q.Get("host")
		}
		if q.Get("serviceName") != "" {
			vm["path"] = q.Get("serviceName")
		}
		if q.Get("mode") != "" {
			return "", ErrUnexportable
		}
		if c.flag("tls") {
			vm["tls"] = "tls"
		}
		if v := c.str("servername"); v != "" {
			vm["sni"] = v
		}
		if c.flag("skip-cert-verify") {
			// VMess v2 JSON has no portable certificate-verification override.
			return "", ErrUnexportable
		}
		if v := c.str("client-fingerprint"); v != "" {
			vm["fp"] = v
		}
		alpn, e := stringList(c.take("alpn"))
		if e != nil {
			return "", e
		}
		if alpn != "" {
			vm["alpn"] = alpn
		}
		if e = c.complete(); e != nil {
			return "", e
		}
		raw, e := json.Marshal(vm)
		if e != nil {
			return "", e
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(raw), nil
	case "vless", "trojan":
		var secret string
		if typ == "trojan" {
			secret = c.str("password")
		} else {
			secret = c.str("uuid")
		}
		if secret == "" {
			return "", ErrUnexportable
		}
		u.User = url.User(secret)
		if e = transport(c, q); e != nil {
			return "", e
		}
		if typ == "vless" {
			q.Set("encryption", "none")
			if v := c.str("flow"); v != "" {
				q.Set("flow", v)
			}
		}
		tls := typ == "trojan"
		if _, ok := m["tls"]; ok {
			tls = c.flag("tls")
		}
		if typ == "trojan" && !tls {
			return "", ErrUnexportable
		}
		if tls {
			q.Set("security", "tls")
		} else {
			q.Set("security", "none")
		}
		if e = tlsQuery(c, q); e != nil {
			return "", e
		}
		if opts, ok := m["reality-opts"]; ok {
			c.take("reality-opts")
			o, ok := opts.(map[string]any)
			if !ok || typ != "vless" || !tls {
				return "", ErrUnexportable
			}
			for k, v := range o {
				switch k {
				case "public-key":
					q.Set("pbk", text(v))
				case "short-id":
					q.Set("sid", text(v))
				default:
					return "", ErrUnexportable
				}
			}
			if q.Get("pbk") == "" {
				return "", ErrUnexportable
			}
			q.Set("security", "reality")
		}
	case "hysteria2", "hy2":
		u.Scheme = "hysteria2"
		password := c.str("password")
		if password == "" {
			return "", ErrUnexportable
		}
		u.User = url.User(password)
		if e = tlsQuery(c, q); e != nil {
			return "", e
		}
		if q.Get("allowInsecure") == "1" {
			q.Del("allowInsecure")
			q.Set("insecure", "1")
		}
		obfs := c.str("obfs")
		if obfs != "" {
			if obfs != "salamander" {
				return "", ErrUnexportable
			}
			q.Set("obfs", obfs)
			pass := c.str("obfs-password")
			if pass == "" {
				return "", ErrUnexportable
			}
			q.Set("obfs-password", pass)
		}
	case "tuic":
		uuid, password := c.str("uuid"), c.str("password")
		if uuid == "" || password == "" {
			return "", ErrUnexportable
		}
		u.User = url.UserPassword(uuid, password)
		if e = tlsQuery(c, q); e != nil {
			return "", e
		}
		if q.Get("allowInsecure") == "1" {
			q.Del("allowInsecure")
			q.Set("allow_insecure", "1")
		}
		for key, param := range map[string]string{"congestion-controller": "congestion_control", "udp-relay-mode": "udp_relay_mode"} {
			if v := c.str(key); v != "" {
				q.Set(param, v)
			}
		}
		if c.flag("reduce-rtt") {
			q.Set("reduce_rtt", "1")
		}
		if c.flag("disable-sni") {
			q.Set("disable_sni", "1")
		}
	case "http", "socks5", "socks":
		if typ == "socks" {
			u.Scheme = "socks5"
		}
		username, password := c.str("username"), c.str("password")
		if username != "" || password != "" {
			u.User = url.UserPassword(username, password)
		}
		if c.flag("tls") {
			if typ != "http" {
				return "", ErrUnexportable
			}
			u.Scheme = "https" // There is no portable HTTPS-proxy query for custom TLS parameters.
		}
	default:
		return "", ErrUnexportable
	}
	if e = c.complete(); e != nil {
		return "", e
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func tlsQuery(c *uriConfig, q url.Values) error {
	sni := c.str("sni")
	if v := c.str("servername"); v != "" {
		if sni != "" && sni != v {
			return ErrUnexportable
		}
		sni = v
	}
	if sni != "" {
		q.Set("sni", sni)
	}
	if c.flag("skip-cert-verify") {
		q.Set("allowInsecure", "1")
	}
	if typ := text(c.values["type"]); typ == "vless" || typ == "trojan" {
		if v := c.str("client-fingerprint"); v != "" {
			q.Set("fp", v)
		}
	}
	if typ := text(c.values["type"]); typ != "hysteria2" && typ != "hy2" {
		alpn, e := stringList(c.take("alpn"))
		if e != nil {
			return e
		}
		if alpn != "" {
			q.Set("alpn", alpn)
		}
	}
	return nil
}
func transport(c *uriConfig, q url.Values) error {
	network := c.str("network")
	if network == "" {
		network = "tcp"
	}
	q.Set("type", network)
	switch network {
	case "tcp":
		return nil
	case "ws":
		raw := c.take("ws-opts")
		if raw == nil {
			return nil
		}
		opts, ok := raw.(map[string]any)
		if !ok {
			return ErrUnexportable
		}
		for k, v := range opts {
			switch k {
			case "path":
				q.Set("path", text(v))
			case "headers":
				headers, ok := v.(map[string]any)
				if !ok {
					return ErrUnexportable
				}
				for key, value := range headers {
					if !strings.EqualFold(key, "host") {
						return ErrUnexportable
					}
					if q.Get("host") != "" && q.Get("host") != text(value) {
						return ErrUnexportable
					}
					q.Set("host", text(value))
				}
			default:
				return ErrUnexportable
			}
		}
		return nil
	case "grpc":
		raw := c.take("grpc-opts")
		if raw == nil {
			return nil
		}
		opts, ok := raw.(map[string]any)
		if !ok {
			return ErrUnexportable
		}
		for k, v := range opts {
			if k != "grpc-service-name" {
				return ErrUnexportable
			}
			q.Set("serviceName", text(v))
		}
		return nil
	default:
		return ErrUnexportable
	}
}
func pluginOptions(plugin string, opts map[string]any) (string, error) {
	name := plugin
	if plugin == "obfs" {
		name = "obfs-local"
	}
	parts := []string{name}
	keys := []string{}
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		key := k
		if plugin == "obfs" {
			switch k {
			case "mode":
				key = "obfs"
			case "host":
				key = "obfs-host"
			default:
				return "", ErrUnexportable
			}
		} else {
			switch k {
			case "mode", "host", "path", "tls", "skip-cert-verify":
			default:
				return "", ErrUnexportable
			}
		}
		v := opts[k]
		if b, ok := v.(bool); ok {
			if k != "tls" && k != "skip-cert-verify" {
				return "", ErrUnexportable
			}
			if b {
				parts = append(parts, key)
			}
			continue
		}
		if k == "tls" || k == "skip-cert-verify" {
			return "", ErrUnexportable
		}
		s, ok := v.(string)
		if !ok || strings.ContainsAny(s, ";=\r\n") {
			return "", ErrUnexportable
		}
		parts = append(parts, key+"="+s)
	}
	return strings.Join(parts, ";"), nil
}
