package pool

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	maps := []map[string]any{}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if yaml.Unmarshal([]byte(content), &doc) == nil && len(doc.Proxies) > 0 {
		maps = doc.Proxies
	} else {
		var array []map[string]any
		if yaml.Unmarshal([]byte(content), &array) == nil && len(array) > 0 {
			maps = array
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
			}
		}
	}
	if len(maps) > 50000 {
		return result, errors.New("单个来源最多 50000 个节点")
	}
	for i, m := range maps {
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
	for _, key := range []string{"dialer-proxy", "certificate-path", "private-key-path"} {
		if _, ok := m[key]; ok {
			return ParsedNode{}, errors.New("不支持链式代理或本地证书路径")
		}
	}
	m["name"], m["type"], m["server"], m["port"] = name, typ, server, port
	if validate != nil {
		if e := validate(m); e != nil {
			return ParsedNode{}, errors.New("Mihomo 不接受此协议配置")
		}
	}
	fingerprint := make(map[string]any, len(m))
	for k, v := range m {
		if k != "name" {
			fingerprint[k] = v
		}
	}
	raw, e := json.Marshal(fingerprint)
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
