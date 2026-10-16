package pool

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// RuleGroup only selects from the nodes already selected by a subscription.
// Groups contains policy references, never node names.
type RuleGroup struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	IncludeAll bool     `json:"include_all"`
	NodeIDs    []int64  `json:"node_ids"`
	Countries  []string `json:"countries"`
	Groups     []string `json:"groups"`
	Interval   int      `json:"interval"`
	Tolerance  int      `json:"tolerance"`
}

func (g *RuleGroup) UnmarshalJSON(data []byte) error {
	type plain RuleGroup
	v := plain{Interval: 300, Tolerance: 50}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return err
	}
	*g = RuleGroup(v)
	return nil
}

func defaultRuleGroups() []RuleGroup {
	group := func(name, kind string, all bool, refs ...string) RuleGroup {
		return RuleGroup{Name: name, Type: kind, IncludeAll: all, NodeIDs: []int64{}, Countries: []string{}, Groups: append([]string{}, refs...), Interval: 300, Tolerance: 50}
	}
	return []RuleGroup{
		group("NodePool", "select", true, referenceGroups[1], referenceGroups[2]),
		group(referenceGroups[1], "url-test", true),
		group(referenceGroups[2], "fallback", true),
		group(referenceGroups[3], "select", false, "NodePool", "DIRECT"),
		group(referenceGroups[4], "select", false, "DIRECT", "NodePool"),
		group(referenceGroups[5], "select", false, "REJECT", "DIRECT"),
		group(referenceGroups[6], "select", false, "NodePool", "DIRECT"),
	}
}

// Normalize into a fresh copy so validation/export never mutates a caller's draft.
func normalizeRuleGroups(groups []RuleGroup) ([]RuleGroup, error) {
	if groups == nil {
		return defaultRuleGroups(), nil
	}
	if len(groups) == 0 || len(groups) > 64 {
		return nil, errors.New("策略组必须为1至64组，且包含NodePool主组")
	}
	result := make([]RuleGroup, len(groups))
	names := map[string]bool{"DIRECT": true, "REJECT": true, "GLOBAL": true}
	for i, original := range groups {
		g := original
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" || utf8.RuneCountInString(g.Name) > 100 || !safeScalar(original.Name) || strings.Contains(g.Name, ",") || names[g.Name] {
			return nil, fmt.Errorf("第%d个策略组名称无效、重复或使用保留名称（最多100字符）", i+1)
		}
		switch g.Type {
		case "select", "url-test", "fallback", "load-balance":
		default:
			return nil, fmt.Errorf("策略组%s类型无效", g.Name)
		}
		if len(g.NodeIDs) > 2000 || len(g.Countries) > 64 || len(g.Groups) > 64 {
			return nil, fmt.Errorf("策略组%s最多2000个节点、64个国家和64个引用", g.Name)
		}
		if g.Interval == 0 {
			g.Interval = 300
		}
		if g.Interval < 60 || g.Interval > 86400 || g.Tolerance < 0 || g.Tolerance > 1000 {
			return nil, fmt.Errorf("策略组%s检测间隔须为60至86400秒，容差须为0至1000毫秒", g.Name)
		}
		g.NodeIDs = append([]int64{}, g.NodeIDs...)
		for _, id := range g.NodeIDs {
			if id <= 0 {
				return nil, fmt.Errorf("策略组%s节点ID必须为正整数", g.Name)
			}
		}
		g.Countries = append([]string{}, g.Countries...)
		for j, code := range g.Countries {
			code = strings.ToUpper(strings.TrimSpace(code))
			if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' || code == "CF" {
				return nil, fmt.Errorf("策略组%s国家须为两位国家代码，CF不是国家分组", g.Name)
			}
			g.Countries[j] = code
		}
		g.Groups = append([]string{}, g.Groups...)
		for j, ref := range g.Groups {
			g.Groups[j] = strings.TrimSpace(ref)
		}
		result[i] = g
		names[g.Name] = true
	}
	if !names["NodePool"] {
		return nil, errors.New("必须保留NodePool主组")
	}
	byName := map[string]RuleGroup{}
	for _, g := range result {
		byName[g.Name] = g
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(name string) error {
		if name == "DIRECT" || name == "REJECT" {
			return nil
		}
		if state[name] == 1 {
			return fmt.Errorf("策略组%s存在自引用或循环引用", name)
		}
		if state[name] == 2 {
			return nil
		}
		g, ok := byName[name]
		if !ok {
			return fmt.Errorf("策略组引用%s不存在；仅可引用DIRECT、REJECT或其他策略组", name)
		}
		state[name] = 1
		for _, ref := range g.Groups {
			if err := visit(ref); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for _, g := range result {
		if err := visit(g.Name); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func configuredProxyGroups(groups []RuleGroup, nodes []Node, names []string) []map[string]any {
	// Retain the historical order as well as behavior for untouched seven-group profiles.
	if reflect.DeepEqual(groups, defaultRuleGroups()) {
		return referenceProxyGroups(names)
	}
	result := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		ids, countries := map[int64]bool{}, map[string]bool{}
		for _, id := range g.NodeIDs {
			ids[id] = true
		}
		for _, code := range g.Countries {
			countries[code] = true
		}
		members, seen := []string{}, map[string]bool{}
		appendMember := func(name string) {
			if !seen[name] {
				members = append(members, name)
				seen[name] = true
			}
		}
		for i, n := range nodes {
			code := strings.ToUpper(n.CountryCode)
			if g.IncludeAll || ids[n.ID] || (code != "CF" && countries[code]) {
				appendMember(names[i])
			}
		}
		for _, ref := range g.Groups {
			appendMember(ref)
		}
		if len(members) == 0 {
			members = append(members, "DIRECT")
		}
		out := map[string]any{"name": g.Name, "type": g.Type, "proxies": members}
		if g.Type != "select" {
			out["url"] = "https://www.gstatic.com/generate_204"
			out["interval"] = g.Interval
			if g.Type == "url-test" {
				out["tolerance"] = g.Tolerance
			}
			if g.Type == "load-balance" {
				out["strategy"] = "consistent-hashing"
			}
		}
		result = append(result, out)
	}
	return result
}
