package pool

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrRiskLimit = errors.New("节点更换过于频繁")
var ErrRateLimited = errors.New("订阅拉取过于频繁，请稍后再试")

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

func (s *Store) riskSummary(ctx context.Context, q execer, u User, c CommunitySettings, now time.Time) (RiskSummary, error) {
	r := RiskSummary{UserID: u.ID, Flags: []string{}, FrozenUntil: u.RiskFrozenUntil, WindowDays: c.RiskWindowDays, DistinctIPs: c.RiskDistinctIPs}
	limits, err := limitsFor(ctx, q, u, c)
	if err != nil {
		return r, err
	}
	r.NodeLimit = limits.Nodes
	r.WindowLimit = c.RiskExposureMultiplier * limits.Nodes
	r.DailyNewCap = c.RiskDailyNewNodes
	if r.DailyNewCap == 0 {
		r.DailyNewCap = limits.Nodes
	}
	var last *time.Time
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(first_at>=?),0),MAX(last_at) FROM np_node_exposures WHERE user_id=? AND last_at>=?`, dayStart(now), u.ID, now.Add(-time.Duration(c.RiskWindowDays)*24*time.Hour)).Scan(&r.WindowNodes, &r.TodayNew, &last); err != nil {
		return r, err
	}
	r.LastExposure = last
	r.SourceWindowLimit = sourceWindowLimit(c, limits.Sources)
	r.SourceDailyCap = sourceDailyCap(c, limits.Sources)
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(first_at>=?),0) FROM np_source_exposures WHERE user_id=? AND last_at>=?`, dayStart(now), u.ID, now.Add(-time.Duration(c.RiskWindowDays)*24*time.Hour)).Scan(&r.WindowSources, &r.TodayNewSources); err != nil {
		return r, err
	}
	day, _ := checkinDay(now)
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(pulls),0) FROM np_pull_stats WHERE user_id=? AND day=?`, u.ID, day).Scan(&r.TodayIPs, &r.TodayPulls); err != nil {
		return r, err
	}
	if r.WindowNodes*10 >= r.WindowLimit*8 {
		r.Flags = append(r.Flags, "frequent_swaps")
	}
	if r.TodayNew*10 >= r.DailyNewCap*8 && r.TodayNew > 0 {
		r.Flags = append(r.Flags, "daily_new_high")
	}
	if r.WindowSources*10 >= r.SourceWindowLimit*8 && r.WindowSources > 1 {
		r.Flags = append(r.Flags, "frequent_source_swaps")
	}
	if r.TodayIPs > c.RiskDistinctIPs {
		r.Flags = append(r.Flags, "many_ips")
	}
	if r.FrozenUntil != nil && r.FrozenUntil.After(now) {
		r.Flags = append(r.Flags, "frozen")
	}
	return r, nil
}

func (s *Store) RiskSummaries(ctx context.Context, ids []int64) (map[int64]RiskSummary, error) {
	out := map[int64]RiskSummary{}
	c := currentCommunity()
	now := utcNow()
	for _, id := range ids {
		u, err := s.User(ctx, id)
		if err != nil {
			return out, err
		}
		r, err := s.riskSummary(ctx, s.DB, u, c, now)
		if err != nil {
			return out, err
		}
		out[id] = r
	}
	return out, nil
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

func (s *Store) PruneRisk(ctx context.Context) error {
	now := utcNow()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM np_node_exposures WHERE last_at<?`, now.Add(-366*24*time.Hour)); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM np_source_exposures WHERE last_at<?`, now.Add(-366*24*time.Hour)); err != nil {
		return err
	}
	day, _ := checkinDay(now.Add(-31 * 24 * time.Hour))
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM np_pull_stats WHERE day<?`, day); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM np_feedback WHERE created_at<?`, now.Add(-90*24*time.Hour))
	return err
}
