package pool

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrRiskLimit = errors.New("节点更换过于频繁")
var ErrRateLimited = errors.New("订阅拉取过于频繁，请稍后再试")
var ErrTooManyAttempts = errors.New("当前密码错误次数过多，请 15 分钟后再试")

// maskServers 只有管理员和来源提供方可以看到服务器地址
func maskServers(u User, items []Node) {
	if u.Role == "admin" {
		return
	}
	for i := range items {
		own := false
		for _, o := range items[i].Sources {
			if u.ID != 0 && o.OwnerID == u.ID {
				own = true
				break
			}
		}
		if !own {
			items[i].Server, items[i].Port, items[i].ServerHidden = "", 0, true
		}
	}
}

func dayStart(now time.Time) time.Time {
	l := now.In(beijing)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, beijing).UTC()
}

// checkExposure 限制短时间内反复更换节点，current 为保存前已在订阅中的节点
func checkExposure(ctx context.Context, q execer, u User, c CommunitySettings, current map[int64]bool, chosen []Selection, now time.Time) (fresh, kept []int64, err error) {
	window := map[int64]bool{}
	todayNew := 0
	if u.Role != "admin" {
		rows, err := q.QueryContext(ctx, `SELECT node_id,first_at FROM np_node_exposures WHERE user_id=? AND last_at>=?`, u.ID, now.Add(-time.Duration(c.RiskWindowDays)*24*time.Hour))
		if err != nil {
			return nil, nil, err
		}
		start := dayStart(now)
		for rows.Next() {
			var id int64
			var first time.Time
			if err = rows.Scan(&id, &first); err != nil {
				rows.Close()
				return nil, nil, err
			}
			window[id] = true
			if !first.Before(start) {
				todayNew++
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	for _, n := range chosen {
		if current[n.NodeID] || window[n.NodeID] {
			kept = append(kept, n.NodeID)
		} else {
			fresh = append(fresh, n.NodeID)
		}
	}
	if u.Role == "admin" || !c.RiskEnabled || len(fresh) == 0 {
		return fresh, kept, nil
	}
	if u.RiskFrozenUntil != nil && u.RiskFrozenUntil.After(now) {
		return nil, nil, fmt.Errorf("%w：账号已被临时限制更换节点，至 %s 解除", ErrRiskLimit, u.RiskFrozenUntil.In(beijing).Format("01-02 15:04"))
	}
	daily := c.RiskDailyNewNodes
	if daily == 0 {
		daily = u.NodeLimit
	}
	if todayNew+len(fresh) > daily {
		return nil, nil, fmt.Errorf("%w：今日已新增 %d 个节点，本次新增 %d 个，每日上限 %d 个，请明天再试或保留更多原有节点", ErrRiskLimit, todayNew, len(fresh), daily)
	}
	union := len(window)
	for _, id := range fresh {
		if !window[id] {
			union++
		}
	}
	for id := range current {
		if !window[id] {
			union++
		}
	}
	limit := c.RiskExposureMultiplier * u.NodeLimit
	if union > limit {
		return nil, nil, fmt.Errorf("%w：近 %d 天使用过的不同节点将达到 %d 个，上限 %d 个", ErrRiskLimit, c.RiskWindowDays, union, limit)
	}
	return fresh, kept, nil
}

func recordExposure(ctx context.Context, q execer, userID int64, fresh, kept []int64, now time.Time) error {
	write := func(ids []int64, reset bool) error {
		for start := 0; start < len(ids); start += 500 {
			part := ids[start:min(start+500, len(ids))]
			values := strings.TrimSuffix(strings.Repeat("(?,?,?,?),", len(part)), ",")
			args := make([]any, 0, len(part)*4)
			for _, id := range part {
				args = append(args, userID, id, now, now)
			}
			update := "last_at=VALUES(last_at)"
			if reset {
				update = "first_at=VALUES(first_at),last_at=VALUES(last_at)"
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO np_node_exposures(user_id,node_id,first_at,last_at) VALUES`+values+` ON DUPLICATE KEY UPDATE `+update, args...); err != nil {
				return err
			}
		}
		return nil
	}
	if err := write(fresh, true); err != nil {
		return err
	}
	return write(kept, false)
}

// 拉取限速只在单实例内存中计数，重启后重新计算
type pullLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
}

var pulls = pullLimiter{buckets: map[string][]time.Time{}}

func (p *pullLimiter) allow(key string, perHour int, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	cut := now.Add(-time.Hour)
	list := p.buckets[key]
	i := 0
	for i < len(list) && list[i].Before(cut) {
		i++
	}
	list = list[i:]
	if len(p.buckets) > 50000 {
		for k, v := range p.buckets {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(p.buckets, k)
			}
		}
	}
	if len(list) >= perHour {
		p.buckets[key] = list
		return false
	}
	p.buckets[key] = append(list, now)
	return true
}

var ipKey = func() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}()

func hashIP(ip string) string {
	m := hmac.New(sha256.New, ipKey)
	m.Write([]byte(ip))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// RecordPull limits subscription pulls per token and records hashed client IPs.
func (s *Store) RecordPull(ctx context.Context, token, ip string) error {
	c := currentCommunity()
	now := utcNow()
	if !pulls.allow(token, c.RiskPullsPerHour, now) {
		return ErrRateLimited
	}
	var userID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM np_subscriptions WHERE token=?`, token).Scan(&userID); err != nil {
		return nil
	}
	day, _ := checkinDay(now)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO np_pull_stats(user_id,day,ip_hash,pulls,last_at) VALUES(?,?,?,1,?) ON DUPLICATE KEY UPDATE pulls=pulls+1,last_at=VALUES(last_at)`, userID, day, hashIP(ip), now)
	return err
}

type RiskSummary struct {
	UserID      int64      `json:"user_id"`
	WindowNodes int        `json:"window_nodes"`
	TodayNew    int        `json:"today_new"`
	TodayIPs    int        `json:"today_ips"`
	TodayPulls  int        `json:"today_pulls"`
	NodeLimit   int        `json:"node_limit"`
	Flags       []string   `json:"flags"`
	FrozenUntil *time.Time `json:"frozen_until,omitempty"`
	WindowDays  int        `json:"window_days"`
	WindowLimit int        `json:"window_limit"`
	DistinctIPs int        `json:"distinct_ips_threshold"`
	DailyNewCap int        `json:"daily_new_cap"`
	// 跟随整个来源的风控统计
	WindowSources     int        `json:"window_sources"`
	TodayNewSources   int        `json:"today_new_sources"`
	SourceWindowLimit int        `json:"source_window_limit"`
	SourceDailyCap    int        `json:"source_daily_cap"`
	LastExposure      *time.Time `json:"last_exposure,omitempty"`
}

// riskStats 是风控摘要依赖的窗口统计
type riskStats struct {
	windowNodes, todayNew, windowSources, todayNewSources, todayIPs, todayPulls int
	lastExposure                                                                *time.Time
}

// buildRisk 由统计值计算风控标记；无活动的账号不会被标记
func buildRisk(u User, c CommunitySettings, limits EffectiveLimits, st riskStats, now time.Time) RiskSummary {
	r := RiskSummary{UserID: u.ID, Flags: []string{}, FrozenUntil: u.RiskFrozenUntil, WindowDays: c.RiskWindowDays, DistinctIPs: c.RiskDistinctIPs}
	r.NodeLimit = limits.Nodes
	r.WindowLimit = c.RiskExposureMultiplier * limits.Nodes
	r.DailyNewCap = c.RiskDailyNewNodes
	if r.DailyNewCap == 0 {
		r.DailyNewCap = limits.Nodes
	}
	r.WindowNodes, r.TodayNew, r.LastExposure = st.windowNodes, st.todayNew, st.lastExposure
	r.SourceWindowLimit = sourceWindowLimit(c, limits.Sources)
	r.SourceDailyCap = sourceDailyCap(c, limits.Sources)
	r.WindowSources, r.TodayNewSources = st.windowSources, st.todayNewSources
	r.TodayIPs, r.TodayPulls = st.todayIPs, st.todayPulls
	if r.WindowNodes > 0 && r.WindowNodes*10 >= r.WindowLimit*8 {
		r.Flags = append(r.Flags, "frequent_swaps")
	}
	if r.TodayNew > 0 && r.TodayNew*10 >= r.DailyNewCap*8 {
		r.Flags = append(r.Flags, "daily_new_high")
	}
	if r.WindowSources > 1 && r.WindowSources*10 >= r.SourceWindowLimit*8 {
		r.Flags = append(r.Flags, "frequent_source_swaps")
	}
	if r.TodayIPs > c.RiskDistinctIPs {
		r.Flags = append(r.Flags, "many_ips")
	}
	if r.FrozenUntil != nil && r.FrozenUntil.After(now) {
		r.Flags = append(r.Flags, "frozen")
	}
	return r
}

const riskBatch = 500

func inList(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(?" + strings.Repeat(",?", len(ids)-1) + ")", args
}

// batchRisk 按批聚合统计，避免逐个用户查询
func batchRisk(ctx context.Context, q execer, users []User, c CommunitySettings, now time.Time) (map[int64]RiskSummary, error) {
	out := make(map[int64]RiskSummary, len(users))
	since, today := now.Add(-time.Duration(c.RiskWindowDays)*24*time.Hour), dayStart(now)
	day, _ := checkinDay(now)
	for start := 0; start < len(users); start += riskBatch {
		chunk := users[start:min(start+riskBatch, len(users))]
		ids := make([]int64, len(chunk))
		for i, u := range chunk {
			ids[i] = u.ID
		}
		in, idArgs := inList(ids)
		boosts := map[int64]LevelQuota{}
		stats := map[int64]*riskStats{}
		stat := func(id int64) *riskStats {
			if stats[id] == nil {
				stats[id] = &riskStats{}
			}
			return stats[id]
		}
		each := func(query string, args []any, scan func(*sql.Rows) error) error {
			rows, err := q.QueryContext(ctx, query, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				if err = scan(rows); err != nil {
					return err
				}
			}
			return rows.Err()
		}
		with := func(head ...any) []any { return append(head, idArgs...) }
		if err := each(`SELECT user_id,kind,COALESCE(SUM(amount),0) FROM np_quota_boosts WHERE expires_at>? AND user_id IN `+in+` GROUP BY user_id,kind`, with(now), func(r *sql.Rows) error {
			var id int64
			var kind string
			var amount int
			if err := r.Scan(&id, &kind, &amount); err != nil {
				return err
			}
			b := boosts[id]
			switch kind {
			case "node":
				b.Nodes = amount
			case "source":
				b.Sources = amount
			case "subscription":
				b.Subscriptions = amount
			}
			boosts[id] = b
			return nil
		}); err != nil {
			return out, err
		}
		if err := each(`SELECT user_id,COUNT(*),COALESCE(SUM(first_at>=?),0),MAX(last_at) FROM np_node_exposures WHERE last_at>=? AND user_id IN `+in+` GROUP BY user_id`, with(today, since), func(r *sql.Rows) error {
			var id int64
			var v riskStats
			if err := r.Scan(&id, &v.windowNodes, &v.todayNew, &v.lastExposure); err != nil {
				return err
			}
			st := stat(id)
			st.windowNodes, st.todayNew, st.lastExposure = v.windowNodes, v.todayNew, v.lastExposure
			return nil
		}); err != nil {
			return out, err
		}
		if err := each(`SELECT user_id,COUNT(*),COALESCE(SUM(first_at>=?),0) FROM np_source_exposures WHERE last_at>=? AND user_id IN `+in+` GROUP BY user_id`, with(today, since), func(r *sql.Rows) error {
			var id int64
			var a, b int
			if err := r.Scan(&id, &a, &b); err != nil {
				return err
			}
			st := stat(id)
			st.windowSources, st.todayNewSources = a, b
			return nil
		}); err != nil {
			return out, err
		}
		if err := each(`SELECT user_id,COUNT(*),COALESCE(SUM(pulls),0) FROM np_pull_stats WHERE day=? AND user_id IN `+in+` GROUP BY user_id`, with(day), func(r *sql.Rows) error {
			var id int64
			var a, b int
			if err := r.Scan(&id, &a, &b); err != nil {
				return err
			}
			st := stat(id)
			st.todayIPs, st.todayPulls = a, b
			return nil
		}); err != nil {
			return out, err
		}
		for _, u := range chunk {
			st := riskStats{}
			if v := stats[u.ID]; v != nil {
				st = *v
			}
			out[u.ID] = buildRisk(u, c, composeLimits(u, c, boosts[u.ID]), st, now)
		}
	}
	return out, nil
}

// usersByIDs 批量读取账号，保持传入顺序
func (s *Store) usersByIDs(ctx context.Context, ids []int64) ([]User, error) {
	byID := make(map[int64]User, len(ids))
	for start := 0; start < len(ids); start += riskBatch {
		chunk := ids[start:min(start+riskBatch, len(ids))]
		in, args := inList(chunk)
		rows, err := s.DB.QueryContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id IN `+in, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			byID[u.ID] = u
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]User, 0, len(ids))
	for _, id := range ids {
		if u, ok := byID[id]; ok {
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *Store) RiskSummaries(ctx context.Context, ids []int64) (map[int64]RiskSummary, error) {
	if len(ids) == 0 {
		return map[int64]RiskSummary{}, nil
	}
	users, err := s.usersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return batchRisk(ctx, s.DB, users, currentCommunity(), utcNow())
}

// BatchLimits 批量计算账号的实际额度
func (s *Store) BatchLimits(ctx context.Context, users []User) (map[int64]EffectiveLimits, error) {
	out := make(map[int64]EffectiveLimits, len(users))
	if len(users) == 0 {
		return out, nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	in, args := inList(ids)
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id,kind,COALESCE(SUM(amount),0) FROM np_quota_boosts WHERE expires_at>? AND user_id IN `+in+` GROUP BY user_id,kind`, append([]any{utcNow()}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boosts := map[int64]LevelQuota{}
	for rows.Next() {
		var id int64
		var kind string
		var amount int
		if err = rows.Scan(&id, &kind, &amount); err != nil {
			return nil, err
		}
		b := boosts[id]
		switch kind {
		case "node":
			b.Nodes = amount
		case "source":
			b.Sources = amount
		case "subscription":
			b.Subscriptions = amount
		}
		boosts[id] = b
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = composeLimits(u, settings.Community, boosts[u.ID])
	}
	return out, nil
}

// RiskUserIDs 返回风控筛选命中的账号；只对近期有活动或被冻结的账号计算，其余必然正常
func (s *Store) RiskUserIDs(ctx context.Context, f UserFilter, risk string) (ids, exclude []int64, err error) {
	c, now := currentCommunity(), utcNow()
	where, args := f.where()
	if risk == "frozen" {
		ids, err = s.idList(ctx, `SELECT id FROM np_users WHERE `+where+` AND risk_frozen_until>? ORDER BY id LIMIT 20000`, append(args, now)...)
		return ids, nil, err
	}
	since := now.Add(-time.Duration(c.RiskWindowDays) * 24 * time.Hour)
	day, _ := checkinDay(now)
	active := ` AND (risk_frozen_until>? OR EXISTS(SELECT 1 FROM np_node_exposures e WHERE e.user_id=np_users.id AND e.last_at>=?) OR EXISTS(SELECT 1 FROM np_source_exposures e WHERE e.user_id=np_users.id AND e.last_at>=?) OR EXISTS(SELECT 1 FROM np_pull_stats p WHERE p.user_id=np_users.id AND p.day=?))`
	candidates, err := s.idList(ctx, `SELECT id FROM np_users WHERE `+where+active+` ORDER BY id LIMIT 20000`, append(args, now, since, since, day)...)
	if err != nil {
		return nil, nil, err
	}
	summaries, err := s.RiskSummaries(ctx, candidates)
	if err != nil {
		return nil, nil, err
	}
	flagged := []int64{}
	for _, id := range candidates {
		if len(summaries[id].Flags) > 0 {
			flagged = append(flagged, id)
		}
	}
	if risk == "clean" {
		return nil, flagged, nil
	}
	return flagged, nil, nil
}

func (s *Store) idList(ctx context.Context, query string, args ...any) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FreezeUser blocks node swaps until the given time; zero hours unfreezes.
func (s *Store) FreezeUser(ctx context.Context, id int64, hours int) (User, error) {
	if hours < 0 || hours > 24*90 {
		return User{}, errors.New("限制时长须为 0–2160 小时")
	}
	var until any
	if hours > 0 {
		until = utcNow().Add(time.Duration(hours) * time.Hour)
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE np_users SET risk_frozen_until=? WHERE id=? AND role<>'admin'`, until, id)
	if err != nil {
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err = s.User(ctx, id); err != nil {
			return User{}, err
		}
	}
	return s.User(ctx, id)
}

// ResetExposure clears the swap window for one user after manual review.
func (s *Store) ResetExposure(ctx context.Context, id int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM np_node_exposures WHERE user_id=?`, id); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM np_source_exposures WHERE user_id=?`, id)
	return err
}

// PruneRisk 每日清理过期数据，保持表体积稳定
func (s *Store) PruneRisk(ctx context.Context) error {
	now := utcNow()
	// 换绑窗口之外的曝光记录不再参与风控
	keep := max(currentCommunity().RiskWindowDays, 30) + 7
	day, _ := checkinDay(now.Add(-31 * 24 * time.Hour))
	for _, q := range []struct {
		sql string
		arg any
	}{
		{`DELETE FROM np_node_exposures WHERE last_at<?`, now.AddDate(0, 0, -keep)},
		{`DELETE FROM np_source_exposures WHERE last_at<?`, now.AddDate(0, 0, -keep)},
		{`DELETE FROM np_pull_stats WHERE day<?`, day},
		{`DELETE FROM np_feedback WHERE created_at<?`, now.Add(-90 * 24 * time.Hour)},
		// 成就记录用于防止重复发放，永久保留
		{`DELETE FROM np_ledger WHERE created_at<? AND kind<>'achievement'`, now.AddDate(-1, 0, 0)},
		{`DELETE FROM np_quota_boosts WHERE expires_at<?`, now.AddDate(0, 0, -30)},
		{`DELETE FROM np_email_codes WHERE expires_at<?`, now.Add(-24 * time.Hour)},
		{`DELETE FROM np_daily_jobs WHERE created_at<?`, now.AddDate(0, 0, -60)},
		{`DELETE FROM np_source_moderation WHERE created_at<?`, now.AddDate(-1, 0, 0)},
	} {
		if _, err := s.DB.ExecContext(ctx, q.sql, q.arg); err != nil {
			return err
		}
	}
	return s.PruneBeacons(ctx)
}
