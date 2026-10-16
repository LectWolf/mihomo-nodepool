package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExportSubscriptionWithProfile applies Clash-only rules. Sources are cached
// documents, never fetched here; only groups/rules are read from them.
func ExportSubscriptionWithProfile(nodes []Node, format string, profile *RuleProfile, sources []Source) ([]byte, int, int, error) {
	if profile == nil || format != "clash" {
		raw, n, e := ExportSubscription(nodes, format)
		return raw, n, 0, e
	}
	doc := map[string]any{"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "info"}
	skipped := 0
	// Decode the node maps directly to preserve all supported protocol fields.
	configOrigins := [][]Origin{}
	configs := []map[string]any{}
	names := []string{}
	selected := map[int64]map[string]string{}
	reserved := map[string]bool{"DIRECT": true, "REJECT": true, "GLOBAL": true}
	for _, g := range referenceGroups {
		reserved[g] = true
	}
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
		if !profile.RoutingEnabled && len(n.Sources) > 0 {
			name = fmt.Sprintf("[来源 %d] 节点 %d %s", n.Sources[0].ID, n.ID, name)
		}
		base := name
		for i := 2; reserved[name]; i++ {
			name = fmt.Sprintf("%s (%d)", base, i)
		}
		reserved[name] = true
		m["name"] = name
		configOrigins = append(configOrigins, n.Sources)
		for _, o := range n.Sources {
			if selected[o.ID] == nil {
				selected[o.ID] = map[string]string{}
			}
			selected[o.ID][o.NodeName] = name
		}
		configs = append(configs, m)
		names = append(names, name)
	}
	if len(configs) == 0 {
		return nil, skipped, 0, ErrUnexportable
	}
	// A dialer-proxy is a real dependency, not a harmless transport option. Fail
	// explicitly rather than export a reference to an unselected upstream node.
	for i, m := range configs {
		if dep, ok := m["dialer-proxy"]; ok && text(dep) != "" {
			target := ""
			if text(dep) == "DIRECT" || text(dep) == "REJECT" {
				target = text(dep)
			}
			if target == "" {
				for _, o := range configOrigins[i] {
					if v := selected[o.ID][text(dep)]; v != "" {
						target = v
						break
					}
				}
			}
			if target == "" || target == text(m["name"]) {
				return nil, skipped, 0, fmt.Errorf("%w：节点的dialer-proxy依赖不在所选节点中", ErrUnexportable)
			}
			m["dialer-proxy"] = target
		}
	}
	if !validDialerGraph(configs) {
		return nil, skipped, 0, fmt.Errorf("%w：节点dialer-proxy存在循环依赖", ErrUnexportable)
	}
	doc["proxies"] = configs
	groups := []map[string]any{{"name": "NodePool", "type": "select", "proxies": append(append([]string{}, names...), "DIRECT")}}
	rules := []string{}
	warnings := 0
	if profile.RoutingEnabled {
		groups = referenceProxyGroups(names)
		rules = append(rules, profile.Rules...)
	} else {
		sources = append([]Source(nil), sources...)
		sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
		matches := []string{}
		for _, src := range sources {
			if len(selected[src.ID]) == 0 {
				continue
			}
			gs, rs, ws := localSourceRouting(src, selected[src.ID])
			groups = append(groups, gs...)
			warnings += ws
			for _, r := range rs {
				if strings.HasPrefix(r, "MATCH,") {
					matches = append(matches, r)
				} else {
					rules = append(rules, r)
				}
			}
		}
		// Specific rules from all sources precede the first terminal MATCH.
		// Source ID order gives deterministic precedence; later MATCH is unreachable.
		if len(matches) > 0 {
			rules = append(rules, matches[0])
			warnings += len(matches) - 1
		}
	}
	if len(rules) == 0 || !strings.HasPrefix(rules[len(rules)-1], "MATCH,") {
		rules = append(rules, "MATCH,NodePool")
	}
	doc["proxy-groups"] = groups
	doc["rules"] = rules
	doc["unified-delay"] = true
	if profile.DNSEnabled {
		dns := profile.DNSServers
		if len(dns) == 0 {
			dns = []string{"223.5.5.5", "119.29.29.29", "8.8.8.8"}
		}
		bootstrap := []string{}
		for _, server := range dns {
			if net.ParseIP(server) != nil {
				bootstrap = append(bootstrap, server)
			}
		}
		if len(bootstrap) == 0 {
			bootstrap = []string{"223.5.5.5", "119.29.29.29", "8.8.8.8"}
		}
		doc["dns"] = map[string]any{"enable": true, "ipv6": false, "default-nameserver": bootstrap, "nameserver": dns}
	}
	raw, e := yaml.Marshal(doc)
	return raw, skipped, warnings, e
}
func referenceProxyGroups(names []string) []map[string]any {
	auto, fall := referenceGroups[1], referenceGroups[2]
	selectGroup := func(name string, proxies []string) map[string]any {
		return map[string]any{"name": name, "type": "select", "proxies": proxies}
	}
	return []map[string]any{
		selectGroup("NodePool", append([]string{auto, fall}, names...)),
		{"name": auto, "type": "url-test", "url": "https://www.gstatic.com/generate_204", "interval": 300, "tolerance": 50, "proxies": names},
		{"name": fall, "type": "fallback", "url": "https://www.gstatic.com/generate_204", "interval": 300, "proxies": names},
		selectGroup(referenceGroups[3], []string{"NodePool", "DIRECT"}), selectGroup(referenceGroups[4], []string{"DIRECT", "NodePool"}),
		selectGroup(referenceGroups[5], []string{"REJECT", "DIRECT"}), selectGroup(referenceGroups[6], []string{"NodePool", "DIRECT"}),
	}
}

type localGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
	Use     []string `yaml:"use"`
}

func localSourceRouting(src Source, selected map[string]string) ([]map[string]any, []string, int) {
	var doc struct {
		Groups []localGroup `yaml:"proxy-groups"`
		Rules  []string     `yaml:"rules"`
	}
	if yaml.Unmarshal([]byte(src.Content), &doc) != nil {
		return nil, nil, 1
	}
	if len(doc.Groups) > 10000 || len(doc.Rules) > 100000 {
		return nil, nil, 1
	}
	definitions := map[string]localGroup{}
	order := []string{}
	renamed := map[string]string{}
	warnings := 0
	for i, g := range doc.Groups {
		if g.Name == "" || !safeScalar(g.Name) || len(g.Name) > 200 {
			warnings++
			continue
		}
		if _, duplicate := definitions[g.Name]; duplicate {
			warnings++
			continue
		}
		if _, collision := selected[g.Name]; collision {
			warnings++
			continue
		}
		switch g.Type {
		case "select", "url-test", "fallback", "load-balance":
		default:
			warnings++
			continue
		}
		definitions[g.Name] = g
		order = append(order, g.Name)
		renamed[g.Name] = fmt.Sprintf("[来源 %d] 策略 %d", src.ID, i+1)
	}
	state := map[string]int{}
	built := map[string]map[string]any{}
	var resolve func(string) string
	resolve = func(name string) string {
		if name == "DIRECT" || name == "REJECT" {
			return name
		}
		if v := selected[name]; v != "" {
			return v
		}
		if state[name] == 1 {
			return ""
		}
		if state[name] == 2 {
			if built[name] != nil {
				return renamed[name]
			}
			return ""
		}
		g, ok := definitions[name]
		if !ok {
			return ""
		}
		state[name] = 1
		proxies := []string{}
		seen := map[string]bool{}
		for _, p := range g.Proxies {
			if v := resolve(p); v != "" && !seen[v] {
				proxies = append(proxies, v)
				seen[v] = true
			} else if v == "" {
				warnings++
			}
		}
		warnings += len(g.Use) // external providers are intentionally omitted
		if len(proxies) > 0 {
			out := map[string]any{"name": renamed[name], "type": g.Type, "proxies": proxies}
			if g.Type == "url-test" || g.Type == "fallback" {
				out["url"] = "https://www.gstatic.com/generate_204"
				out["interval"] = 300
				if g.Type == "url-test" {
					out["tolerance"] = 50
				}
			}
			if g.Type == "load-balance" {
				out["strategy"] = "consistent-hashing"
				out["url"] = "https://www.gstatic.com/generate_204"
				out["interval"] = 300
			}
			built[name] = out
		} else {
			warnings++
		}
		state[name] = 2
		if built[name] != nil {
			return renamed[name]
		}
		return ""
	}
	for _, name := range order {
		resolve(name)
	}
	groups := []map[string]any{}
	for _, name := range order {
		if g := built[name]; g != nil {
			groups = append(groups, g)
		}
	}
	rules := []string{}
	for _, r := range doc.Rules {
		parts, target, e := splitLocalRule(r)
		if e != nil {
			warnings++
			continue
		}
		rewritten := resolve(parts[target])
		if rewritten == "" {
			warnings++
			continue
		}
		parts[target] = rewritten
		rules = append(rules, strings.Join(parts, ","))
	}
	return groups, rules, warnings
}

// ExportSavedSubscription keeps source contents and rule ownership inside the
// backend. Its output contains only selected proxies, safe groups and rules.
func (s *Store) ExportSavedSubscription(ctx context.Context, token, format string) ([]byte, int, int, error) {
	nodes, e := s.SubscriptionNodes(ctx, token)
	if e != nil {
		return nil, 0, 0, e
	}
	if format != "clash" {
		raw, n, e := ExportSubscription(nodes, format)
		return raw, n, 0, e
	}
	target, e := scanSubscription(s.DB.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE token=?`, token))
	if e != nil {
		return nil, 0, 0, e
	}
	if target.RuleProfileID == nil {
		raw, n, e := ExportSubscription(nodes, format)
		return raw, n, 0, e
	}
	var p RuleProfile
	if *target.RuleProfileID == 0 {
		p = DefaultRuleProfile()
	} else {
		p, e = scanRuleProfile(s.DB.QueryRowContext(ctx, `SELECT `+ruleProfileColumns+` FROM np_rule_profiles WHERE id=? AND user_id=?`, *target.RuleProfileID, target.UserID))
		if e != nil {
			return nil, 0, 0, e
		}
	}
	sources := []Source{}
	if !p.RoutingEnabled {
		ids := map[int64]bool{}
		for _, n := range nodes {
			for _, o := range n.Sources {
				ids[o.ID] = true
			}
		}
		order := []int64{}
		for id := range ids {
			order = append(order, id)
		}
		sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
		for _, id := range order {
			src := Source{ID: id}
			if e = s.DB.QueryRowContext(ctx, `SELECT content FROM np_sources WHERE id=?`, id).Scan(&src.Content); e != nil {
				return nil, 0, 0, e
			}
			sources = append(sources, src)
		}
	}
	return ExportSubscriptionWithProfile(nodes, format, &p, sources)
}

func validDialerGraph(configs []map[string]any) bool {
	dependencies := map[string]string{}
	for _, m := range configs {
		dependencies[text(m["name"])] = text(m["dialer-proxy"])
	}
	state := map[string]int{}
	for _, m := range configs {
		name := text(m["name"])
		path := []string{}
		for name != "" && name != "DIRECT" && name != "REJECT" && state[name] != 2 {
			if state[name] == 1 {
				return false
			}
			state[name] = 1
			path = append(path, name)
			name = dependencies[name]
		}
		for _, item := range path {
			state[item] = 2
		}
	}
	return true
}
