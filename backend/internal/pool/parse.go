package pool

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/metacubex/mihomo/common/convert"
	"gopkg.in/yaml.v3"
)

// Parse accepts Clash YAML/JSON, bare proxy arrays, base64 subscriptions and
// Mihomo-supported share links. Import never accepts routing/provider directives.
func Parse(content string, validate func(map[string]any) error) (ParseResult, error) {
	result := ParseResult{Nodes: []ParsedNode{}, Warnings: []string{}}
	content = strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
	if len(content) == 0 || len(content) > 16<<20 {
		return result, errors.New("导入内容为空或超过 16 MiB")
	}
	content = stripControl(content)
	maps := []map[string]any{}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []map[string]any `yaml:"proxy-groups"`
	}
	var chains *dialerResolver
	if yaml.Unmarshal([]byte(content), &doc) == nil && len(doc.Proxies) > 0 {
		maps = doc.Proxies
		chains = newDialerResolver(doc.Proxies, doc.Groups, validate)
	} else {
		var array []map[string]any
		if yaml.Unmarshal([]byte(content), &array) == nil && len(array) > 0 {
			maps = array
			chains = newDialerResolver(array, nil, validate)
		} else {
			decoded := content
			for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				if b, e := enc.DecodeString(strings.Join(strings.Fields(content), "")); e == nil && len(b) > 0 {
					decoded = string(b)
					break
				}
			}
			for _, line := range strings.Split(decoded, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				converted, e := convert.ConvertsV2Ray([]byte(line))
				if e != nil || len(converted) == 0 {
					result.Warnings = append(result.Warnings, "一条分享链接无法解析")
					continue
				}
				maps = append(maps, converted...)
				// The upstream converter retains escaped HY2 userinfo verbatim.
				// URI auth is the entire decoded userinfo, including a literal colon.
				if u, err := url.Parse(line); err == nil && (u.Scheme == "hysteria2" || u.Scheme == "hy2") && u.User != nil {
					if password, err := url.PathUnescape(u.User.String()); err == nil {
						for _, m := range converted {
							m["password"] = password
						}
					}
				}
				// pcs 是 Xray 分享链接里的证书指纹，转换器会丢弃；保留为 mihomo 的 fingerprint（只取第一个）
				if u, err := url.Parse(line); err == nil && (u.Scheme == "vless" || u.Scheme == "trojan") {
					keepPin(converted, u.Query().Get("pcs"))
				}
				// fp is part of the VMess v2 share JSON but is omitted by the
				// upstream converter. Retain it before validation/fingerprinting.
				if strings.HasPrefix(line, "vmess://") {
					for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
						if raw, err := enc.DecodeString(strings.TrimPrefix(line, "vmess://")); err == nil {
							var share struct {
								FP  string `json:"fp"`
								PCS string `json:"pcs"`
							}
							if json.Unmarshal(raw, &share) == nil && share.FP != "" {
								for _, m := range converted {
									m["client-fingerprint"] = share.FP
								}
							}
							keepPin(converted, share.PCS)
							break
						}
					}
				}
			}
		}
	}
	if len(maps) > 50000 {
		return result, errors.New("单个来源最多 50000 个节点")
	}
	for i, m := range maps {
		if _, ok := m["dialer-proxy"]; ok && chains != nil {
			if e := chains.attach(m); e != nil {
				if len(result.Warnings) < 100 {
					result.Warnings = append(result.Warnings, fmt.Sprintf("第 %d 个节点无效：%s", i+1, e.Error()))
				}
				continue
			}
		}
		n, e := normalize(m, validate)
		if e != nil {
			if len(result.Warnings) < 100 {
				result.Warnings = append(result.Warnings, fmt.Sprintf("第 %d 个节点无效：%s", i+1, e.Error()))
			}
			continue
		}
		result.Nodes = append(result.Nodes, n)
	}
	if len(result.Nodes) == 0 {
		return result, errors.New("没有解析到有效节点，请检查文件格式或分享链接")
	}
	return result, nil
}

func keepPin(maps []map[string]any, pcs string) {
	pin, _, _ := strings.Cut(strings.TrimSpace(pcs), ",")
	if pin = strings.ToLower(strings.ReplaceAll(pin, ":", "")); len(pin) != 64 {
		return
	}
	if _, err := hex.DecodeString(pin); err != nil {
		return
	}
	for _, m := range maps {
		if str(m, "fingerprint") == "" {
			m["fingerprint"] = pin
		}
	}
}

func normalize(m map[string]any, validate func(map[string]any) error) (ParsedNode, error) {
	typ := strings.ToLower(strings.TrimSpace(fmt.Sprint(m["type"])))
	switch typ {
	case "direct", "reject", "reject-drop", "pass", "dns", "selector", "url-test", "fallback", "load-balance", "relay", "<nil>":
		return ParsedNode{}, errors.New("不是可导入的代理节点")
	}
	server, ok := m["server"].(string)
	if !ok || strings.TrimSpace(server) == "" {
		return ParsedNode{}, errors.New("缺少服务器地址")
	}
	server = strings.ToLower(strings.Trim(strings.TrimSpace(server), "[]"))
	if text(m["port"]) == "" && text(m["ports"]) != "" {
		// 端口跳跃节点可只写 ports，取第一个端口作为主端口
		m["port"] = strings.TrimSpace(strings.FieldsFunc(text(m["ports"]), func(r rune) bool { return r == ',' || r == '-' || r == '/' })[0])
	}
	port, e := strconv.Atoi(fmt.Sprint(m["port"]))
	if e != nil || port < 1 || port > 65535 {
		return ParsedNode{}, errors.New("端口无效")
	}
	name, _ := m["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("%s %s:%d", typ, server, port)
	}
	if len([]rune(name)) > 200 {
		name = string([]rune(name)[:200])
	}
	// Adapter chains and local file references cannot be resolved in an isolated node.
	if _, ok := m["dialer-proxy"]; ok {
		return ParsedNode{}, errors.New("链式代理只能引用同一订阅中的节点")
	}
	for _, key := range []string{"certificate-path", "private-key-path"} {
		if _, ok := m[key]; ok {
			return ParsedNode{}, errors.New("不支持本地证书路径")
		}
	}
	m["name"], m["type"], m["server"], m["port"] = name, typ, server, port
	if validate != nil {
		if e := validate(m); e != nil {
			// 部分机场把布尔值写成字符串 "true"，转换后再试一次
			fixed, changed := coerceBools(m)
			if !changed || validate(fixed) != nil {
				return ParsedNode{}, errors.New("Mihomo 不接受此协议配置")
			}
			m = fixed
		}
	}
	raw, e := json.Marshal(withoutNames(m))
	if e != nil {
		return ParsedNode{}, errors.New("节点配置不能序列化")
	}
	hash := sha256.Sum256(raw)
	config, e := json.Marshal(m)
	if e != nil {
		return ParsedNode{}, e
	}
	return ParsedNode{Name: name, Type: typ, Server: server, Port: port, Hash: hex.EncodeToString(hash[:]), Config: config}, nil
}
