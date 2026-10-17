package pool

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LegacyOptions 旧版单点登录（accounts）与订阅共享（proxy）数据库迁移参数
type LegacyOptions struct {
	Accounts   *sql.DB
	Proxy      *sql.DB
	SubSecret  string
	AdminEmail string
	DryRun     bool
	Avatars    bool
	Validate   func(map[string]any) error
	Log        io.Writer
}

// LegacyReport 迁移计划与结果统计
type LegacyReport struct {
	DryRun          bool             `json:"dry_run"`
	Users           int              `json:"users"`
	Admin           string           `json:"admin"`
	Managers        []string         `json:"managers"`
	GeneratedNames  int              `json:"generated_usernames"`
	Disabled        int              `json:"disabled_users"`
	Emails          int              `json:"emails"`
	DroppedPassword int              `json:"dropped_passwords"`
	NoLogin         []string         `json:"users_without_login"`
	Identities      int              `json:"identities"`
	DupIdentities   []string         `json:"dropped_duplicate_identities"`
	Ledger          map[string]int   `json:"ledger"`
	Sources         int              `json:"sources"`
	SourcesEnabled  int              `json:"sources_enabled"`
	SourcesSkipped  int              `json:"sources_skipped_deleted"`
	SourcesFile     []int64          `json:"sources_without_url"`
	ParsedNodes     int              `json:"parsed_nodes"`
	ParseFailed     map[int64]string `json:"parse_failed"`
	Subscriptions   int              `json:"subscriptions"`
	SeatsOrphan     int              `json:"seats_without_source"`
	Events          int              `json:"events"`
	Claims          int              `json:"claims"`
	Follows         int              `json:"follows"`
	Avatars         int              `json:"avatars"`
	AvatarFailed    int              `json:"avatar_failed"`
	Settings        []string         `json:"settings"`
}

type legacyUser struct {
	id, newID                     int64
	public, username, name, email string
	hasPassword, admin, disabled  bool
	created                       time.Time
	xp, points                    int64
	checkinOn                     string
	streak                        int
	picture                       string
	banned, plain                 bool
}

type legacySource struct {
	id                   int64
	owner                int64
	name, status, rawURL string
	body, share          string
	maxUsers, minLevel   int
	created              time.Time
	parsed               *ParseResult
}

type legacyEvent struct {
	source, user int64
	action       string
	at           time.Time
}

var legacyNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func legacyLog(o LegacyOptions, format string, args ...any) {
	if o.Log != nil {
		fmt.Fprintf(o.Log, format+"\n", args...)
	}
}

// legacyOpen 解密旧站 sub_url_enc：AES-256-GCM，密钥为 sha256(SUB_SECRET)，前 12 字节为 nonce
func legacyOpen(key []byte, enc string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("密文过短")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

// clip 按字符截断
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func nullTime(t sql.NullTime) any {
	if t.Valid {
		return t.Time.UTC()
	}
	return nil
}

// ImportLegacy 清空当前数据（保留运行设置）后导入旧站数据；DryRun 只读取并输出计划
func (s *Store) ImportLegacy(ctx context.Context, o LegacyOptions) (LegacyReport, error) {
	r := LegacyReport{DryRun: o.DryRun, Ledger: map[string]int{}, ParseFailed: map[int64]string{}, Managers: []string{}, NoLogin: []string{}, DupIdentities: []string{}, SourcesFile: []int64{}}
	if o.Accounts == nil || o.Proxy == nil {
		return r, errors.New("缺少旧库连接")
	}
	if strings.TrimSpace(o.SubSecret) == "" {
		return r, errors.New("缺少 SUB_SECRET，无法解密来源地址")
	}
	if o.AdminEmail == "" {
		return r, errors.New("缺少管理员邮箱")
	}
	now := utcNow()
	if !o.DryRun {
		if err := s.Ensure(ctx); err != nil {
			return r, err
		}
	}
	// 试运行可以不连目标库，租期按默认值估算
	var settings Settings
	var err error
	if s.DB != nil {
		if settings, err = s.Settings(ctx); err != nil && !o.DryRun {
			return r, err
		}
	} else if !o.DryRun {
		return r, errors.New("目标数据库未配置，请设置 MYSQL_* 环境变量")
	}
	if settings.SubscriptionLeaseDays == 0 {
		settings.SubscriptionLeaseDays = 14
	}
	until := now.Add(time.Duration(settings.SubscriptionLeaseDays) * 24 * time.Hour)

	// 账号
	users, byPublic, byID := []*legacyUser{}, map[string]*legacyUser{}, map[int64]*legacyUser{}
	rows, err := o.Accounts.QueryContext(ctx, `SELECT id,public_id,COALESCE(email,''),COALESCE(name,''),COALESCE(password_hash,'')<>'',is_admin=1,disabled_at IS NOT NULL,created_at,COALESCE(username,''),xp,points FROM users ORDER BY id`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		u := &legacyUser{}
		if err = rows.Scan(&u.id, &u.public, &u.email, &u.name, &u.hasPassword, &u.admin, &u.disabled, &u.created, &u.username, &u.xp, &u.points); err != nil {
			rows.Close()
			return r, err
		}
		u.newID, u.email, u.created = u.id, strings.ToLower(strings.TrimSpace(u.email)), u.created.UTC()
		users = append(users, u)
		byPublic[u.public], byID[u.id] = u, u
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	var admin *legacyUser
	for _, u := range users {
		if u.email == strings.ToLower(o.AdminEmail) {
			admin = u
		}
		if u.id == 1 {
			return r, errors.New("旧库存在 id=1 的账号，无法把管理员放到 id=1")
		}
	}
	if admin == nil {
		return r, fmt.Errorf("旧库中找不到管理员邮箱 %s", o.AdminEmail)
	}
	admin.newID = 1
	rows, err = o.Proxy.QueryContext(ctx, `SELECT sso_id,COALESCE(picture,''),COALESCE(banned,0)=1,COALESCE(DATE_FORMAT(checkin_on,'%Y-%m-%d'),''),COALESCE(checkin_streak,0),COALESCE(clash_wrap,1)=0 FROM profiles`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var sso, pic, day string
		var banned, plain bool
		var streak int
		if err = rows.Scan(&sso, &pic, &banned, &day, &streak, &plain); err != nil {
			rows.Close()
			return r, err
		}
		if u := byPublic[sso]; u != nil {
			u.picture, u.banned, u.checkinOn, u.streak, u.plain = pic, banned, day, streak, plain
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	taken := map[string]bool{}
	for _, u := range users {
		if legacyNameRE.MatchString(u.username) {
			taken[strings.ToLower(u.username)] = true
		} else {
			u.username = ""
		}
	}
	for _, u := range users {
		if u.username == "" {
			// 缺用户名时按旧 ID 生成
			base := "user" + strconv.FormatInt(u.id, 10)
			name := base
			for i := 2; taken[strings.ToLower(name)]; i++ {
				name = base + "_" + strconv.Itoa(i)
			}
			u.username, taken[strings.ToLower(name)] = name, true
			r.GeneratedNames++
		}
		if strings.TrimSpace(u.name) == "" {
			u.name = u.username
		}
		if u.email != "" {
			r.Emails++
		}
		if u.hasPassword {
			r.DroppedPassword++
		}
		if u.disabled || u.banned {
			r.Disabled++
		}
		if u == admin {
			r.Admin = u.username
		} else if u.admin {
			r.Managers = append(r.Managers, u.username)
		}
	}
	r.Users = len(users)

	// 第三方绑定：同一账号同平台多个绑定时保留最近一次
	type identity struct {
		provider, subject, login string
		user                     int64
		at                       time.Time
	}
	idents := map[string]identity{}
	subjects := map[string]bool{}
	rows, err = o.Accounts.QueryContext(ctx, `SELECT provider,provider_id,COALESCE(username,''),user_id,created_at FROM oauth_bindings ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var v identity
		if err = rows.Scan(&v.provider, &v.subject, &v.login, &v.user, &v.at); err != nil {
			rows.Close()
			return r, err
		}
		u := byID[v.user]
		if u == nil || (v.provider != "github" && v.provider != "linuxdo") || v.subject == "" || len(v.subject) > 64 {
			continue
		}
		v.login = clip(v.login, 100)
		key := fmt.Sprintf("%d/%s", v.user, v.provider)
		if _, ok := idents[key]; ok || subjects[v.provider+"/"+v.subject] {
			r.DupIdentities = append(r.DupIdentities, fmt.Sprintf("%s：%s %s", u.username, v.provider, v.login))
			continue
		}
		v.user, v.at = u.newID, v.at.UTC()
		idents[key], subjects[v.provider+"/"+v.subject] = v, true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	r.Identities = len(idents)
	hasIdentity := map[int64]bool{}
	for _, v := range idents {
		hasIdentity[v.user] = true
	}
	for _, u := range users {
		if u != admin && u.email == "" && !hasIdentity[u.newID] {
			r.NoLogin = append(r.NoLogin, u.username)
		}
	}

	// 来源
	key := sha256.Sum256([]byte(strings.TrimSpace(o.SubSecret)))
	sources, byLegacySource := []*legacySource{}, map[int64]*legacySource{}
	if err = o.Proxy.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscriptions WHERE deleted_at IS NOT NULL`).Scan(&r.SourcesSkipped); err != nil {
		return r, err
	}
	rows, err = o.Proxy.QueryContext(ctx, `SELECT id,owner_sso,name,status,COALESCE(sub_url_enc,''),COALESCE(body_yaml,''),COALESCE(share_token,''),max_claims,min_level,created_at FROM subscriptions WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		v := &legacySource{}
		var owner, enc string
		var maxClaims int64
		if err = rows.Scan(&v.id, &owner, &v.name, &v.status, &enc, &v.body, &v.share, &maxClaims, &v.minLevel, &v.created); err != nil {
			rows.Close()
			return r, err
		}
		u := byPublic[owner]
		if u == nil {
			continue
		}
		v.owner, v.created = u.newID, v.created.UTC()
		if maxClaims > 0 && maxClaims < 1000000 {
			v.maxUsers = int(maxClaims)
		}
		v.minLevel = min(max(v.minLevel, 0), 6)
		if enc != "" {
			plain, err := legacyOpen(key[:], enc)
			if err != nil {
				rows.Close()
				return r, fmt.Errorf("来源 %d 地址解密失败，请检查 SUB_SECRET", v.id)
			}
			if u, e := url.Parse(strings.TrimSpace(plain)); e == nil && u.Hostname() != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil {
				v.rawURL = strings.TrimSpace(plain)
			}
		}
		if v.rawURL == "" {
			r.SourcesFile = append(r.SourcesFile, v.id)
		}
		if cleanName(v.name) == "" {
			v.name = fmt.Sprintf("来源 %d", v.id)
		}
		if !validLinkToken(v.share) {
			v.share = ""
		}
		sources = append(sources, v)
		byLegacySource[v.id] = v
		if v.status == "live" {
			r.SourcesEnabled++
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	r.Sources = len(sources)
	legacyLog(o, "解析 %d 个来源快照…", len(sources))
	for _, v := range sources {
		if strings.TrimSpace(v.body) == "" {
			if v.rawURL == "" {
				r.ParseFailed[v.id] = "既没有订阅地址也没有快照"
			} else {
				r.ParseFailed[v.id] = "没有快照，等待首次拉取"
			}
			continue
		}
		parsed, err := Parse(v.body, o.Validate)
		if err != nil {
			r.ParseFailed[v.id] = err.Error()
			continue
		}
		v.parsed = &parsed
		r.ParsedNodes += len(parsed.Nodes)
	}

	// 席位与订阅：旧站每人一个席位，订阅地址沿用旧 token
	type seat struct {
		user          *legacyUser
		source        *legacySource
		token         string
		claimed, pull sql.NullTime
	}
	pulls := map[string]time.Time{}
	rows, err = o.Proxy.QueryContext(ctx, `SELECT sso_id,subscription_id,last_at FROM usage_pulls WHERE last_at IS NOT NULL`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var sso string
		var sid int64
		var at time.Time
		if err = rows.Scan(&sso, &sid, &at); err != nil {
			rows.Close()
			return r, err
		}
		k := fmt.Sprintf("%s/%d", sso, sid)
		if at.After(pulls[k]) {
			pulls[k] = at.UTC()
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	seats := []seat{}
	rows, err = o.Proxy.QueryContext(ctx, `SELECT sso_id,subscription_id,token,claimed_at FROM seats WHERE subscription_id IS NOT NULL AND subscription_id>0`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var sso, token string
		var sid int64
		var claimed sql.NullTime
		if err = rows.Scan(&sso, &sid, &token, &claimed); err != nil {
			rows.Close()
			return r, err
		}
		u, src := byPublic[sso], byLegacySource[sid]
		if u == nil || src == nil || !validLinkToken(token) {
			r.SeatsOrphan++
			continue
		}
		v := seat{user: u, source: src, token: token, claimed: claimed}
		if at, ok := pulls[fmt.Sprintf("%s/%d", sso, sid)]; ok {
			v.pull = sql.NullTime{Time: at, Valid: true}
		}
		seats = append(seats, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	r.Subscriptions = len(seats)

	// 领取记录：切换记为旧来源释放+新来源领取
	events := []legacyEvent{}
	claims := map[[2]int64]time.Time{}
	addClaim := func(src, user int64, at time.Time) {
		k := [2]int64{src, user}
		if old, ok := claims[k]; !ok || at.Before(old) {
			claims[k] = at
		}
	}
	rows, err = o.Proxy.QueryContext(ctx, `SELECT sso_id,action,subscription_id,created_at FROM records WHERE subscription_id IS NOT NULL AND subscription_id>0 ORDER BY sso_id,created_at,id`)
	if err != nil {
		return r, err
	}
	current := map[string]int64{}
	for rows.Next() {
		var sso, action string
		var sid int64
		var at time.Time
		if err = rows.Scan(&sso, &action, &sid, &at); err != nil {
			rows.Close()
			return r, err
		}
		u := byPublic[sso]
		if u == nil {
			continue
		}
		at = at.UTC()
		emit := func(id int64, act string) {
			if byLegacySource[id] != nil {
				events = append(events, legacyEvent{source: id, user: u.newID, action: act, at: at})
			}
		}
		switch action {
		case "领取", "切换":
			if cur := current[sso]; cur != 0 && cur != sid {
				emit(cur, "released")
			}
			emit(sid, "claimed")
			if byLegacySource[sid] != nil {
				addClaim(sid, u.newID, at)
			}
			current[sso] = sid
		case "取消":
			emit(sid, "released")
			current[sso] = 0
		case "超时":
			emit(sid, "expired")
			current[sso] = 0
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	for _, v := range seats {
		at := now
		if v.claimed.Valid {
			at = v.claimed.Time.UTC()
		}
		addClaim(v.source.id, v.user.newID, at)
	}
	r.Events, r.Claims = len(events), len(claims)

	// 关注
	follows := map[[2]int64]bool{}
	rows, err = o.Proxy.QueryContext(ctx, `SELECT follower_sso,owner_sso FROM follows WHERE deleted_at IS NULL`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			rows.Close()
			return r, err
		}
		if ua, ub := byPublic[a], byPublic[b]; ua != nil && ub != nil && ua != ub {
			follows[[2]int64{ua.newID, ub.newID}] = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	r.Follows = len(follows)

	// 流水
	type entry struct {
		user             int64
		kind, note, uniq string
		xp, points       int
		at               time.Time
	}
	ledger := []entry{}
	kinds := map[string]string{"checkin": "checkin", "linuxdo_trust": "legacy_linuxdo", "provide": "legacy_provide", "debit": "legacy_debit"}
	rows, err = o.Accounts.QueryContext(ctx, `SELECT user_id,public_id,event_type,xp_delta,points_delta,COALESCE(reason,''),created_at FROM ledger_entries ORDER BY id`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var v entry
		var uid int64
		var public, typ string
		if err = rows.Scan(&uid, &public, &typ, &v.xp, &v.points, &v.note, &v.at); err != nil {
			rows.Close()
			return r, err
		}
		u := byID[uid]
		if u == nil {
			continue
		}
		v.kind = kinds[typ]
		if v.kind == "" {
			v.kind = "legacy"
		}
		v.user, v.uniq, v.at = u.newID, "legacy:"+public, v.at.UTC()
		v.note = clip(v.note, 200)
		ledger = append(ledger, v)
		r.Ledger[v.kind]++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}

	// 登录设置
	legacySettings := map[string]string{}
	rows, err = o.Accounts.QueryContext(ctx, `SELECT k,COALESCE(v,'') FROM settings`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			rows.Close()
			return r, err
		}
		legacySettings[k] = v
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return r, err
	}
	login := settings.Login
	login.RequireEmail = false
	if v := legacySettings["email_suffixes"]; strings.TrimSpace(v) != "" {
		login.EmailSuffixes = strings.FieldsFunc(v, func(c rune) bool { return c == '\n' || c == '\r' || c == ',' || c == ' ' })
		r.Settings = append(r.Settings, fmt.Sprintf("邮箱后缀 %d 个", len(login.EmailSuffixes)))
	}
	if h := strings.TrimSpace(legacySettings["smtp_host"]); h != "" {
		login.SMTP.Host, login.SMTP.Username, login.SMTP.Password = h, legacySettings["smtp_user"], legacySettings["smtp_password"]
		login.SMTP.From, login.SMTP.FromName = legacySettings["smtp_from"], legacySettings["smtp_from_name"]
		if p, e := strconv.Atoi(legacySettings["smtp_port"]); e == nil {
			login.SMTP.Port = p
		}
		switch {
		case legacySettings["smtp_tls"] == "1" && login.SMTP.Port == 465:
			login.SMTP.Security = "ssl"
		case legacySettings["smtp_tls"] == "1":
			login.SMTP.Security = "starttls"
		default:
			login.SMTP.Security = "none"
		}
		r.Settings = append(r.Settings, "SMTP "+h)
	}
	for _, p := range []struct {
		name string
		dst  *OAuthSettings
	}{{"github", &login.GitHub}, {"linuxdo", &login.LinuxDo}} {
		if id := strings.TrimSpace(legacySettings[p.name+"_client_id"]); id != "" {
			p.dst.ClientID, p.dst.ClientSecret, p.dst.Enabled = id, strings.TrimSpace(legacySettings[p.name+"_client_secret"]), legacySettings[p.name+"_enabled"] == "1"
			r.Settings = append(r.Settings, p.name+" OAuth")
		}
	}
	sort.Strings(r.Managers)
	if o.DryRun {
		return r, nil
	}

	// 正式导入：清空 np_* 业务数据（保留 np_settings），关闭外键检查只用于清空
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return r, err
	}
	defer conn.Close()
	tables := []string{}
	rows, err = conn.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME LIKE 'np\_%' AND TABLE_NAME<>'np_settings'`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var t string
		if err = rows.Scan(&t); err != nil {
			rows.Close()
			return r, err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if _, err = conn.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=0`); err != nil {
		return r, err
	}
	for _, t := range tables {
		if _, err = conn.ExecContext(ctx, "TRUNCATE TABLE `"+t+"`"); err != nil {
			_, _ = conn.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=1`)
			return r, err
		}
	}
	if _, err = conn.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=1`); err != nil {
		return r, err
	}
	legacyLog(o, "已清空 %d 张表，开始写入…", len(tables))
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	active := map[int64]bool{}
	for _, v := range seats {
		active[v.user.newID] = true
	}
	b := newBulk(tx)
	for _, u := range users {
		role := "user"
		if u == admin {
			role = "admin"
		} else if u.admin {
			role = "manager"
		}
		var email, verified, expires any
		if u.email != "" {
			email, verified = u.email, u.created
		}
		if active[u.newID] {
			expires = until
		}
		enabled := !u.disabled && !u.banned
		if u == admin {
			enabled = true
		}
		if err = b.add(ctx, "INSERT INTO np_users(id,username,display_name,role,enabled,password_hash,node_limit,source_limit,subscription_limit,created_at,xp,points,checkin_on,checkin_streak,subscription_expires_at,email,email_verified_at)", 17,
			u.newID, u.username, u.name, role, enabled, "", 0, 0, 0, u.created, u.xp, u.points, u.checkinOn, u.streak, expires, email, verified); err != nil {
			return r, err
		}
	}
	for _, v := range idents {
		if err = b.add(ctx, "INSERT INTO np_user_identities(provider,subject,user_id,login,created_at)", 5, v.provider, v.subject, v.user, v.login, v.at); err != nil {
			return r, err
		}
	}
	for _, v := range ledger {
		if err = b.add(ctx, "INSERT INTO np_ledger(user_id,kind,xp,points,note,uniq,created_at)", 7, v.user, v.kind, v.xp, v.points, v.note, v.uniq, v.at); err != nil {
			return r, err
		}
	}
	for k := range follows {
		if err = b.add(ctx, "INSERT INTO np_follows(user_id,provider_id)", 2, k[0], k[1]); err != nil {
			return r, err
		}
	}
	if err = b.flush(ctx); err != nil {
		return r, err
	}
	for _, v := range sources {
		kind, rawURL, content := "url", v.rawURL, ""
		if rawURL == "" {
			kind, content = "file", v.body
		}
		sealedURL, err := seal(purposeSourceURL, rawURL)
		if err != nil {
			return r, err
		}
		sealedContent, err := seal(purposeSourceContent, content)
		if err != nil {
			return r, err
		}
		var share any
		if v.share != "" {
			share = v.share
		}
		// 有快照的来源随后写入节点；没有快照的立即排队首次拉取
		pending, next, status, lastErr := 0, any(nil), "queued", ""
		if v.parsed == nil && kind == "url" {
			pending, next = 1, InitialRun(fmt.Sprintf("legacy-source:%d", v.id), now).Add(time.Duration(v.id%60)*time.Minute)
		} else if v.parsed == nil {
			status, lastErr = "error", r.ParseFailed[v.id]
		}
		if err = b.add(ctx, "INSERT INTO np_sources(id,owner_id,name,kind,url,url_hash,content,exclude_pattern,enabled,status,last_error,created_at,next_refresh_at,pending,max_users,min_level,share_token)", 17,
			v.id, v.owner, cleanName(v.name), kind, sealedURL, urlHash(rawURL), sealedContent, "", v.status == "live", status, lastErr, v.created, next, pending, v.maxUsers, v.minLevel, share); err != nil {
			return r, err
		}
	}
	if err = b.flush(ctx); err != nil {
		return r, err
	}
	for _, v := range seats {
		claimed := now
		if v.claimed.Valid {
			claimed = v.claimed.Time.UTC()
		}
		ids, _ := json.Marshal([]int64{v.source.id})
		// 旧站关闭“分流包装”的用户导出纯节点，其余使用官方规则
		var rule any = 0
		if v.user.plain {
			rule = nil
		}
		if err = b.add(ctx, "INSERT INTO np_subscriptions(user_id,name,token,nodes,source_ids,follow_ids,created_at,updated_at,rule_profile_id)", 9,
			v.user.newID, cleanName(v.source.name), v.token, "[]", string(ids), string(ids), claimed, now, rule); err != nil {
			return r, err
		}
		if err = b.add(ctx, "INSERT INTO np_source_seats(source_id,user_id,state,claimed_at,expires_at,last_pull_at,last_renewed_at)", 7,
			v.source.id, v.user.newID, "active", claimed, until, nullTime(v.pull), now); err != nil {
			return r, err
		}
		if err = b.add(ctx, "INSERT INTO np_source_exposures(user_id,source_id,first_at,last_at)", 4, v.user.newID, v.source.id, claimed, now); err != nil {
			return r, err
		}
	}
	for k, at := range claims {
		if err = b.add(ctx, "INSERT INTO np_source_claims(source_id,user_id,created_at)", 3, k[0], k[1], at); err != nil {
			return r, err
		}
	}
	for _, e := range events {
		src := byLegacySource[e.source]
		if err = b.add(ctx, "INSERT INTO np_source_events(source_id,owner_id,source_name,user_id,action,created_at)", 6, e.source, src.owner, cleanName(src.name), e.user, e.action, e.at); err != nil {
			return r, err
		}
	}
	if err = b.flush(ctx); err != nil {
		return r, err
	}
	// 旧站没有成就，预先标记已获得，避免写入节点时补发经验导致余额与流水不符
	marked := map[string]bool{}
	for _, v := range sources {
		marked[fmt.Sprintf("first_source:%d", v.owner)] = true
	}
	for k := range claims {
		if src := byLegacySource[k[0]]; src != nil && src.owner != k[1] {
			marked[fmt.Sprintf("first_user:%d", src.owner)] = true
		}
	}
	for k := range marked {
		owner, _ := strconv.ParseInt(k[strings.IndexByte(k, ':')+1:], 10, 64)
		if err = b.add(ctx, "INSERT INTO np_ledger(user_id,kind,xp,points,note,uniq,created_at)", 7, owner, "achievement", 0, 0, "旧站迁移", "achv:"+k, now); err != nil {
			return r, err
		}
	}
	if err = b.flush(ctx); err != nil {
		return r, err
	}
	// 旧站当天已发放提供奖励，避免迁移当天重复发放
	today, _ := checkinDay(now)
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO np_daily_jobs(kind,day,created_at) VALUES('share',?,?)`, today, now); err != nil {
		return r, err
	}
	if err = tx.Commit(); err != nil {
		return r, err
	}
	conn.Close()
	settings, err = s.Settings(ctx)
	if err != nil {
		return r, err
	}
	settings.Login = login
	if err = s.SaveSettings(ctx, settings); err != nil {
		return r, fmt.Errorf("登录设置写入失败：%w", err)
	}

	// 用快照写入节点，下次拉取时间按来源分散在刷新周期内排队
	legacyLog(o, "写入来源节点…")
	for i, v := range sources {
		if v.parsed == nil {
			continue
		}
		src, err := s.Source(ctx, v.id)
		if err != nil {
			return r, err
		}
		src.Revision = 1
		if src.Kind != "url" {
			src.Content = v.body
		}
		if err = s.ApplySource(ctx, src, v.body, *v.parsed); err != nil {
			return r, fmt.Errorf("来源 %d 节点写入失败：%w", v.id, err)
		}
		if (i+1)%50 == 0 {
			legacyLog(o, "  %d/%d", i+1, len(sources))
		}
	}
	// 以导入时间作为首次成功基线，避免启动时整表回填
	if _, err = s.DB.ExecContext(ctx, `UPDATE np_nodes SET last_success_at=created_at WHERE last_success_at IS NULL`); err != nil {
		return r, err
	}
	if o.Avatars {
		r.Avatars, r.AvatarFailed = s.importLegacyAvatars(ctx, o, users)
	}
	return r, nil
}

// bulk 按表合并多行 INSERT，减少往返
type bulk struct {
	tx     *sql.Tx
	prefix string
	cols   int
	args   []any
}

func newBulk(tx *sql.Tx) *bulk { return &bulk{tx: tx} }

func (b *bulk) add(ctx context.Context, prefix string, cols int, args ...any) error {
	if b.prefix != prefix || len(b.args)/max(b.cols, 1) >= 500 {
		if err := b.flush(ctx); err != nil {
			return err
		}
		b.prefix, b.cols = prefix, cols
	}
	b.args = append(b.args, args...)
	return nil
}

func (b *bulk) flush(ctx context.Context) error {
	if len(b.args) == 0 {
		return nil
	}
	row := "(" + strings.TrimSuffix(strings.Repeat("?,", b.cols), ",") + ")"
	n := len(b.args) / b.cols
	q := b.prefix + " VALUES" + strings.TrimSuffix(strings.Repeat(row+",", n), ",")
	_, err := b.tx.ExecContext(ctx, q, b.args...)
	if err != nil {
		return fmt.Errorf("%s：%w", strings.Fields(b.prefix)[2], err)
	}
	b.args = b.args[:0]
	return nil
}

// importLegacyAvatars 并发下载第三方头像，失败的留空
func (s *Store) importLegacyAvatars(ctx context.Context, o LegacyOptions, users []*legacyUser) (ok, failed int) {
	client := &http.Client{Timeout: 15 * time.Second}
	jobs := make(chan *legacyUser)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				err := s.fetchLegacyAvatar(ctx, client, u)
				mu.Lock()
				if err != nil {
					failed++
				} else {
					ok++
				}
				mu.Unlock()
			}
		}()
	}
	for _, u := range users {
		if strings.HasPrefix(u.picture, "https://") {
			jobs <- u
		}
	}
	close(jobs)
	wg.Wait()
	legacyLog(o, "头像：成功 %d，失败 %d", ok, failed)
	return ok, failed
}

func (s *Store) fetchLegacyAvatar(ctx context.Context, client *http.Client, u *legacyUser) error {
	link := u.picture
	if strings.Contains(link, "avatars.githubusercontent.com") {
		if strings.Contains(link, "?") {
			link += "&s=256"
		} else {
			link += "?s=256"
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxAvatarBytes+1))
	if err != nil || len(data) > MaxAvatarBytes {
		return errors.New("头像过大")
	}
	data, kind, err := staticAvatar(data)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE np_users SET avatar=?,avatar_type=?,avatar_version=ABS(avatar_version)+1 WHERE id=?`, data, kind, u.newID)
	return err
}
