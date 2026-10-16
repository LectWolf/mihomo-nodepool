package pool

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

//go:embed default_rules.txt
var defaultRules string

var ErrProfileInUse = errors.New("规则配置仍被订阅引用，请先解除绑定")

// RuleProfile is private to its owner; the owner ID is never supplied by clients.
type RuleProfile struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	RoutingEnabled bool      `json:"routing_enabled"`
	DNSEnabled     bool      `json:"dns_enabled"`
	DNSServers     []string  `json:"dns_servers"`
	Rules          []string  `json:"rules"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type RuleProfileInput struct {
	Name           string   `json:"name"`
	RoutingEnabled bool     `json:"routing_enabled"`
	DNSEnabled     bool     `json:"dns_enabled"`
	DNSServers     []string `json:"dns_servers"`
	Rules          []string `json:"rules"`
}

var referenceGroups = []string{"NodePool", "♻️ 自动选择", "🚑 故障转移", "🌩️ Cloudflare", "🇨🇳 国内网站", "🛑 拦截广告", "🐟 漏网之鱼"}

func DefaultRuleProfile() RuleProfile {
	rules := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(defaultRules, "__PROXY__", "NodePool"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			rules = append(rules, line)
		}
	}
	return RuleProfile{Name: "默认规则", RoutingEnabled: true, DNSEnabled: true, DNSServers: []string{}, Rules: rules}
}
func safeScalar(s string) bool { return !strings.ContainsFunc(s, unicode.IsControl) }
func validDNS(s string) bool {
	if s == "" || len(s) > 2048 || !safeScalar(s) || strings.ContainsAny(s, " \t#") {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	u, e := url.Parse(s)
	if e != nil || u.User != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(u.Host, ":") || !safeScalar(u.Path) || strings.ContainsAny(u.Path, " \t") {
		return false
	}
	switch u.Scheme {
	case "https":
	case "tls", "udp", "tcp":
		if u.Path != "" {
			return false
		}
	default:
		return false
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// Only self-contained rule types are supported. GEOIP is restricted to CN and
// LAN to avoid accepting arbitrary geodata names or triggering downloads on save.
func splitLocalRule(raw string) ([]string, int, error) {
	if len(raw) > 4096 || !safeScalar(raw) {
		return nil, 0, errors.New("规则内容无效或超过4096字节")
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return nil, 0, errors.New("规则参数不能为空")
		}
	}
	if len(parts) < 2 {
		return nil, 0, errors.New("规则缺少目标")
	}
	target := 2
	if parts[0] == "MATCH" {
		target = 1
		if len(parts) != 2 {
			return nil, 0, errors.New("MATCH规则格式无效")
		}
	} else if len(parts) < 3 || len(parts) > 4 {
		return nil, 0, errors.New("规则参数数量无效")
	}
	if len(parts) == 4 && (parts[3] != "no-resolve" || (parts[0] != "IP-CIDR" && parts[0] != "IP-CIDR6" && parts[0] != "GEOIP")) {
		return nil, 0, errors.New("不支持的规则选项")
	}
	payload := ""
	if target == 2 {
		payload = parts[1]
	}
	switch parts[0] {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD":
		for _, r := range payload {
			if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune(".-_*", r) {
				return nil, 0, errors.New("域名规则参数无效")
			}
		}
	case "IP-CIDR", "IP-CIDR6":
		ip, _, e := net.ParseCIDR(payload)
		if e != nil || (parts[0] == "IP-CIDR6" && ip.To4() != nil) {
			return nil, 0, errors.New("IP网段无效")
		}
	case "DST-PORT", "SRC-PORT":
		for _, interval := range strings.Split(payload, "/") {
			bounds := strings.Split(interval, "-")
			if len(bounds) > 2 {
				return nil, 0, errors.New("端口范围无效")
			}
			prev := 0
			for _, bound := range bounds {
				if strings.Trim(bound, "0123456789") != "" {
					return nil, 0, errors.New("端口范围无效")
				}
				port, e := strconv.Atoi(bound)
				if e != nil || port < 1 || port > 65535 || port < prev {
					return nil, 0, errors.New("端口范围无效")
				}
				prev = port
			}
		}
	case "NETWORK":
		if payload != "tcp" && payload != "udp" {
			return nil, 0, errors.New("NETWORK仅支持tcp或udp")
		}
	case "MATCH":
	case "GEOIP":
		if payload != "CN" && payload != "LAN" {
			return nil, 0, errors.New("GEOIP仅支持CN或LAN")
		}
		return parts, target, nil
	default:
		return nil, 0, errors.New("不支持该规则类型；仅支持本地域名、IP、端口、NETWORK、GEOIP(CN/LAN)和MATCH规则")
	}
	return parts, target, nil
}
func (in *RuleProfileInput) Validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 || !safeScalar(in.Name) {
		return errors.New("规则名称不能为空且最多200字节，不能含控制字符")
	}
	if len(in.DNSServers) > 32 || len(in.Rules) > 20000 {
		return errors.New("DNS最多32项，规则最多20000项")
	}
	if in.DNSServers == nil {
		in.DNSServers = []string{}
	}
	if in.Rules == nil {
		in.Rules = []string{}
	}
	for i, s := range in.DNSServers {
		s = strings.TrimSpace(s)
		if !validDNS(s) {
			return fmt.Errorf("第%d项DNS必须是IP或有效TLS/HTTPS/UDP/TCP地址", i+1)
		}
		in.DNSServers[i] = s
	}
	allowed := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, g := range referenceGroups {
		allowed[g] = true
	}
	total := 0
	for i, s := range in.Rules {
		s = strings.TrimSpace(s)
		total += len(s)
		if total > 2<<20 {
			return errors.New("规则总大小最多2MiB")
		}
		parts, target, e := splitLocalRule(s)
		if e != nil {
			return fmt.Errorf("第%d条规则：%w", i+1, e)
		}
		if !allowed[parts[target]] {
			return fmt.Errorf("第%d条规则目标不在允许的策略组内", i+1)
		}
		in.Rules[i] = strings.Join(parts, ",")
	}
	return nil
}

const ruleProfileColumns = `id,name,routing_enabled,dns_enabled,dns_servers,rules,created_at,updated_at`

func scanRuleProfile(row scanner) (RuleProfile, error) {
	p := RuleProfile{}
	var dns, rules []byte
	e := row.Scan(&p.ID, &p.Name, &p.RoutingEnabled, &p.DNSEnabled, &dns, &rules, &p.CreatedAt, &p.UpdatedAt)
	if e == nil {
		e = json.Unmarshal(dns, &p.DNSServers)
	}
	if e == nil {
		e = json.Unmarshal(rules, &p.Rules)
	}
	return p, e
}
func (s *Store) RuleProfiles(ctx context.Context) ([]RuleProfile, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT `+ruleProfileColumns+` FROM np_rule_profiles WHERE user_id=? ORDER BY id`, CurrentUser(ctx).ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []RuleProfile{}
	for rows.Next() {
		p, e := scanRuleProfile(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, p)
	}
	return items, rows.Err()
}
func (s *Store) SaveRuleProfile(ctx context.Context, id int64, in RuleProfileInput) (RuleProfile, error) {
	if e := in.Validate(); e != nil {
		return RuleProfile{}, e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return RuleProfile{}, e
	}
	defer tx.Rollback()
	// Serialize binding, deletion and edits on the account row, in the same order
	// as SaveSubscription. No foreign profile existence is disclosed.
	var enabled bool
	if e = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=? FOR UPDATE`, CurrentUser(ctx).ID).Scan(&enabled); e != nil {
		return RuleProfile{}, e
	}
	if !enabled {
		return RuleProfile{}, ErrForbidden
	}
	if id > 0 {
		var found int64
		if e = tx.QueryRowContext(ctx, `SELECT id FROM np_rule_profiles WHERE id=? AND user_id=? FOR UPDATE`, id, CurrentUser(ctx).ID).Scan(&found); e != nil {
			return RuleProfile{}, e
		}
	}
	dns, _ := json.Marshal(in.DNSServers)
	rules, _ := json.Marshal(in.Rules)
	now := utcNow()
	if id == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO np_rule_profiles(user_id,name,routing_enabled,dns_enabled,dns_servers,rules,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, CurrentUser(ctx).ID, in.Name, in.RoutingEnabled, in.DNSEnabled, dns, rules, now, now)
		if err != nil {
			return RuleProfile{}, err
		}
		id, e = res.LastInsertId()
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE np_rule_profiles SET name=?,routing_enabled=?,dns_enabled=?,dns_servers=?,rules=?,updated_at=? WHERE id=?`, in.Name, in.RoutingEnabled, in.DNSEnabled, dns, rules, now, id)
	}
	if e != nil {
		return RuleProfile{}, e
	}
	p, e := scanRuleProfile(tx.QueryRowContext(ctx, `SELECT `+ruleProfileColumns+` FROM np_rule_profiles WHERE id=?`, id))
	if e != nil {
		return p, e
	}
	return p, tx.Commit()
}
func (s *Store) DeleteRuleProfile(ctx context.Context, id int64) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var enabled bool
	if e = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=? FOR UPDATE`, CurrentUser(ctx).ID).Scan(&enabled); e != nil {
		return e
	}
	if !enabled {
		return ErrForbidden
	}
	var found int64
	if e = tx.QueryRowContext(ctx, `SELECT id FROM np_rule_profiles WHERE id=? AND user_id=? FOR UPDATE`, id, CurrentUser(ctx).ID).Scan(&found); e != nil {
		return e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_subscriptions WHERE rule_profile_id=?`, id).Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return ErrProfileInUse
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM np_rule_profiles WHERE id=?`, id); e != nil {
		return e
	}
	return tx.Commit()
}

// A nullable ID and a separate presence bit preserve old clients' PUT behavior.
func (in *SubscriptionInput) UnmarshalJSON(data []byte) error {
	type plain SubscriptionInput
	var v plain
	// Keep unknown-field rejection even though this type implements UnmarshalJSON.
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&v); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(data, &fields); e != nil {
		return e
	}
	*in = SubscriptionInput(v)
	_, in.RuleProfileSet = fields["rule_profile_id"]
	if in.RuleProfileID != nil && *in.RuleProfileID <= 0 {
		return errors.New("规则配置ID必须为正整数或null")
	}
	return nil
}

func (in *RuleProfileInput) UnmarshalJSON(data []byte) error {
	type plain RuleProfileInput
	v := plain{RoutingEnabled: true, DNSEnabled: true}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if e := d.Decode(&v); e != nil {
		return e
	}
	*in = RuleProfileInput(v)
	return nil
}

// BindSubscriptionRule changes output policy without reclaiming or replacing the
// saved node snapshot. Historical selections may no longer be available.
func (s *Store) BindSubscriptionRule(ctx context.Context, id int64, profileID *int64) (Subscription, error) {
	if profileID != nil && *profileID <= 0 {
		return Subscription{}, errors.New("规则配置ID必须为正整数或null")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback()
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=? FOR UPDATE`, CurrentUser(ctx).ID).Scan(&enabled); err != nil {
		return Subscription{}, err
	}
	if !enabled {
		return Subscription{}, ErrForbidden
	}
	item, err := scanSubscription(tx.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE id=? AND user_id=? FOR UPDATE`, id, CurrentUser(ctx).ID))
	if err != nil {
		return Subscription{}, err
	}
	if profileID != nil {
		var found int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM np_rule_profiles WHERE id=? AND user_id=? FOR UPDATE`, *profileID, CurrentUser(ctx).ID).Scan(&found); err != nil {
			return Subscription{}, err
		}
	}
	item.RuleProfileID = profileID
	item.UpdatedAt = utcNow()
	if _, err = tx.ExecContext(ctx, `UPDATE np_subscriptions SET rule_profile_id=?,updated_at=? WHERE id=?`, profileID, item.UpdatedAt, item.ID); err != nil {
		return Subscription{}, err
	}
	return item, tx.Commit()
}
