package pool

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// DialerKey 节点内嵌前置代理的配置键；导出时展开为隐藏节点并写回 dialer-proxy
const DialerKey = "x-np-dialer"

const maxDialerDepth = 3

// stripControl 去掉 YAML 不允许的控制字符（常见于机场节点名），保留换行与制表符
func stripControl(s string) string {
	if strings.IndexFunc(s, isBadControl) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isBadControl(r) {
			return -1
		}
		return r
	}, s)
}

func isBadControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' || r == 0x85 {
		return false
	}
	return unicode.IsControl(r) || r == 0xfffe || r == 0xffff
}

// withoutNames 指纹不含节点名，内嵌前置代理同样去掉名字
func withoutNames(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch {
		case k == "name":
		case k == DialerKey:
			if inner, ok := v.(map[string]any); ok {
				out[k] = withoutNames(inner)
			}
		default:
			out[k] = v
		}
	}
	return out
}

// dialerResolver 把 dialer-proxy 引用解析为同一文件中的具体节点并内嵌，
// 这样节点脱离原文件后仍可独立检测和导出
type dialerResolver struct {
	proxies  map[string]map[string]any
	groups   map[string]map[string]any
	validate func(map[string]any) error
}

func newDialerResolver(proxies, groups []map[string]any, validate func(map[string]any) error) *dialerResolver {
	r := &dialerResolver{proxies: map[string]map[string]any{}, groups: map[string]map[string]any{}, validate: validate}
	for _, p := range proxies {
		if name := strings.TrimSpace(text(p["name"])); name != "" {
			if _, dup := r.proxies[name]; !dup {
				r.proxies[name] = copyMap(p)
			}
		}
	}
	for _, g := range groups {
		if name := strings.TrimSpace(text(g["name"])); name != "" {
			r.groups[name] = g
		}
	}
	return r
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// attach 用前置代理的完整配置替换 dialer-proxy；前置为 DIRECT 时直接去掉
func (r *dialerResolver) attach(m map[string]any) error {
	target := strings.TrimSpace(text(m["dialer-proxy"]))
	delete(m, "dialer-proxy")
	if target == "" {
		return nil
	}
	cfg, err := r.resolve(target, 0, map[string]bool{strings.TrimSpace(text(m["name"])): true})
	if err != nil {
		return err
	}
	if cfg != nil {
		m[DialerKey] = cfg
	}
	return nil
}

func (r *dialerResolver) resolve(name string, depth int, visiting map[string]bool) (map[string]any, error) {
	switch strings.ToUpper(name) {
	case "DIRECT":
		return nil, nil
	case "REJECT", "REJECT-DROP", "PASS":
		return nil, errors.New("前置代理为拒绝策略")
	}
	if depth >= maxDialerDepth {
		return nil, errors.New("前置代理层级超过 3 层")
	}
	if visiting[name] {
		return nil, errors.New("前置代理存在循环引用")
	}
	visiting[name] = true
	defer delete(visiting, name)
	if p, ok := r.proxies[name]; ok {
		m := copyMap(p)
		if _, chained := m["dialer-proxy"]; chained {
			target := strings.TrimSpace(text(m["dialer-proxy"]))
			delete(m, "dialer-proxy")
			inner, err := r.resolve(target, depth+1, visiting)
			if err != nil {
				return nil, err
			}
			if inner != nil {
				m[DialerKey] = inner
			}
		}
		n, err := normalize(m, r.validate)
		if err != nil {
			return nil, fmt.Errorf("前置代理 %s 无效：%s", name, err.Error())
		}
		var out map[string]any
		if err = json.Unmarshal(n.Config, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	if g, ok := r.groups[name]; ok {
		// 代理组按默认选择（第一个成员）解析
		if g["use"] != nil || g["include-all"] == true || g["include-all-proxies"] == true {
			return nil, fmt.Errorf("前置代理组 %s 依赖外部提供者，无法解析", name)
		}
		members, _ := g["proxies"].([]any)
		if len(members) == 0 {
			return nil, fmt.Errorf("前置代理组 %s 没有成员", name)
		}
		return r.resolve(strings.TrimSpace(text(members[0])), depth+1, visiting)
	}
	return nil, fmt.Errorf("找不到前置代理 %s", name)
}

// expandDialers 把内嵌前置代理展开为隐藏节点（不进入任何策略组），相同前置只输出一次
func expandDialers(configs []map[string]any, reserved map[string]bool) []map[string]any {
	hidden := []map[string]any{}
	seen := map[string]string{}
	var expand func(m map[string]any)
	expand = func(m map[string]any) {
		inner, ok := m[DialerKey].(map[string]any)
		delete(m, DialerKey)
		if !ok {
			return
		}
		inner = copyMap(inner)
		expand(inner)
		key, _ := json.Marshal(withoutNames(inner))
		name, ok := seen[string(key)]
		if !ok {
			base := "前置 · " + text(inner["name"])
			name = base
			for i := 2; reserved[name]; i++ {
				name = fmt.Sprintf("%s (%d)", base, i)
			}
			reserved[name] = true
			seen[string(key)] = name
			inner["name"] = name
			hidden = append(hidden, inner)
		}
		m["dialer-proxy"] = name
	}
	for _, m := range configs {
		expand(m)
	}
	return hidden
}

// coerceBools 把字符串形式的 true/false 转为布尔值（含嵌套选项），不修改原 map
func coerceBools(m map[string]any) (map[string]any, bool) {
	out := make(map[string]any, len(m))
	changed := false
	for k, v := range m {
		switch x := v.(type) {
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true":
				out[k], changed = true, true
				continue
			case "false":
				out[k], changed = false, true
				continue
			}
		case map[string]any:
			if k != DialerKey {
				if inner, c := coerceBools(x); c {
					out[k], changed = inner, true
					continue
				}
			}
		}
		out[k] = v
	}
	return out, changed
}
