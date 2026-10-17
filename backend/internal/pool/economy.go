package pool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var beijing = time.FixedZone("Asia/Shanghai", 8*3600)

// 最近一次读取的社区设置，供事务内的成就奖励使用
var communityCache atomic.Pointer[CommunitySettings]
var maintenanceCache atomic.Pointer[Maintenance]

// CurrentMaintenance returns the last loaded maintenance state.
func CurrentMaintenance() Maintenance {
	if m := maintenanceCache.Load(); m != nil {
		return *m
	}
	return Maintenance{}
}

func currentCommunity() CommunitySettings {
	if c := communityCache.Load(); c != nil {
		return *c
	}
	return DefaultCommunity()
}

func monthStart(now time.Time) time.Time {
	local := now.In(beijing)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, beijing).UTC()
}

type LedgerEntry struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	XP        int       `json:"xp"`
	Points    int       `json:"points"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type LedgerPage struct {
	Items    []LedgerEntry `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// award 写入流水并调整经验与积分；uniq 非空时同一奖励只发放一次
func award(ctx context.Context, q execer, userID int64, kind string, xp, points int, ref int64, note, uniq string) (bool, error) {
	var key any
	if uniq != "" {
		key = uniq
	}
	res, err := q.ExecContext(ctx, `INSERT IGNORE INTO np_ledger(user_id,kind,xp,points,ref_id,note,uniq,created_at) VALUES(?,?,?,?,?,?,?,?)`, userID, kind, xp, points, ref, note, key, utcNow())
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if xp != 0 || points != 0 {
		if _, err = q.ExecContext(ctx, `UPDATE np_users SET xp=LEAST(GREATEST(xp+?,0),1000000000),points=LEAST(GREATEST(points+?,0),1000000000) WHERE id=?`, xp, points, userID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// logLedger 只记录流水，账号数值已由调用方更新
func logLedger(ctx context.Context, q execer, userID int64, kind string, xp, points int, note, uniq string) error {
	var key any
	if uniq != "" {
		key = uniq
	}
	_, err := q.ExecContext(ctx, `INSERT IGNORE INTO np_ledger(user_id,kind,xp,points,note,uniq,created_at) VALUES(?,?,?,?,?,?,?)`, userID, kind, xp, points, note, key, utcNow())
	return err
}

func achievement(ctx context.Context, q execer, userID int64, key, note string) error {
	xp := currentCommunity().AchievementXP
	if xp <= 0 {
		return nil
	}
	_, err := award(ctx, q, userID, "achievement", xp, 0, 0, note, fmt.Sprintf("achv:%s:%d", key, userID))
	return err
}

func activeBoosts(ctx context.Context, q execer, userID int64) (LevelQuota, error) {
	var b LevelQuota
	rows, err := q.QueryContext(ctx, `SELECT kind,COALESCE(SUM(amount),0) FROM np_quota_boosts WHERE user_id=? AND expires_at>? GROUP BY kind`, userID, utcNow())
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var amount int
		if err = rows.Scan(&kind, &amount); err != nil {
			return b, err
		}
		switch kind {
		case "node":
			b.Nodes = amount
		case "source":
			b.Sources = amount
		case "subscription":
			b.Subscriptions = amount
		}
	}
	return b, rows.Err()
}

func limitsFor(ctx context.Context, q execer, u User, c CommunitySettings) (EffectiveLimits, error) {
	boost, err := activeBoosts(ctx, q, u.ID)
	if err != nil {
		return EffectiveLimits{}, err
	}
	return composeLimits(u, c, boost), nil
}

// applyLimits 将账号额度替换为实际生效值，仅用于本次校验
func applyLimits(u *User, l EffectiveLimits) {
	u.NodeLimit, u.SourceLimit, u.SubscriptionLimit = l.Nodes, l.Sources, l.Subscriptions
}

func (s *Store) EffectiveLimits(ctx context.Context, u User) (EffectiveLimits, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return EffectiveLimits{}, err
	}
	return limitsFor(ctx, s.DB, u, settings.Community)
}

type QuotaBoost struct {
	Kind      string    `json:"kind"`
	Amount    int       `json:"amount"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Wallet struct {
	User          User            `json:"user"`
	Limits        EffectiveLimits `json:"limits"`
	Boosts        []QuotaBoost    `json:"boosts"`
	Titles        []string        `json:"titles"`
	MakeupUsed    int             `json:"makeup_used"`
	LeaveUsed     int             `json:"leave_used"`
	MissedDays    int             `json:"missed_days"`
	TodayShareXP  int             `json:"today_share_xp"`
	Rules         CommunityRules  `json:"rules"`
	AchievementOK map[string]bool `json:"achievements"`
}

// CommunityRules is the public subset of community settings shown to users.
type CommunityRules struct {
	LevelQuotas            []LevelQuota `json:"level_quotas"`
	ShareXPPerUser         int          `json:"share_xp_per_user"`
	ShareXPSourceCap       int          `json:"share_xp_source_cap"`
	ShareXPDailyCap        int          `json:"share_xp_daily_cap"`
	SharePointsPercent     int          `json:"share_points_percent"`
	ShareQualityFull       int          `json:"share_quality_full"`
	ShareQualityHalf       int          `json:"share_quality_half"`
	UniqueBonusPercent     int          `json:"unique_bonus_percent"`
	UniqueShareThreshold   int          `json:"unique_share_threshold"`
	MilestoneDays          []int        `json:"milestone_days"`
	MilestoneXP            []int        `json:"milestone_xp"`
	AchievementXP          int          `json:"achievement_xp"`
	ProbeXP                int          `json:"probe_xp"`
	MakeupPrice            int          `json:"makeup_price"`
	MakeupMonthly          int          `json:"makeup_monthly"`
	LeavePrice             int          `json:"leave_price"`
	LeaveDays              int          `json:"leave_days"`
	LeaveMonthly           int          `json:"leave_monthly"`
	BoostDays              int          `json:"boost_days"`
	BoostNodePrice         int          `json:"boost_node_price"`
	BoostNodeAmount        int          `json:"boost_node_amount"`
	BoostSourcePrice       int          `json:"boost_source_price"`
	BoostSubscriptionPrice int          `json:"boost_subscription_price"`
	BoostMaxActive         int          `json:"boost_max_active"`
	TipMax                 int          `json:"tip_max"`
	Titles                 []TitleItem  `json:"titles"`
	RiskEnabled            bool         `json:"risk_enabled"`
	RiskWindowDays         int          `json:"risk_window_days"`
	RiskExposureMultiplier int          `json:"risk_exposure_multiplier"`
	RiskDailyNewNodes      int          `json:"risk_daily_new_nodes"`
	FeedbackEnabled        bool         `json:"feedback_enabled"`
	FeedbackVisibleDays    int          `json:"feedback_visible_days"`
	FeedbackRewardXP       int          `json:"feedback_reward_xp"`
	FeedbackRewardPoints   int          `json:"feedback_reward_points"`
	FeedbackRewardDaily    int          `json:"feedback_reward_daily"`
	FeedbackDemoteUsers    int          `json:"feedback_demote_users"`
}

func publicRules(c CommunitySettings) CommunityRules {
	return CommunityRules{c.LevelQuotas, c.ShareXPPerUser, c.ShareXPSourceCap, c.ShareXPDailyCap, c.SharePointsPercent, c.ShareQualityFull, c.ShareQualityHalf,
		c.UniqueBonusPercent, c.UniqueShareThreshold, MilestoneDays, c.MilestoneXP, c.AchievementXP, c.ProbeXP, c.MakeupPrice, c.MakeupMonthly, c.LeavePrice, c.LeaveDays,
		c.LeaveMonthly, c.BoostDays, c.BoostNodePrice, c.BoostNodeAmount, c.BoostSourcePrice, c.BoostSubscriptionPrice, c.BoostMaxActive, c.TipMax, c.Titles,
		c.RiskEnabled, c.RiskWindowDays, c.RiskExposureMultiplier, c.RiskDailyNewNodes, c.FeedbackEnabled, c.FeedbackVisibleDays,
		c.FeedbackRewardXP, c.FeedbackRewardPoints, c.FeedbackRewardDaily, c.FeedbackDemoteUsers}
}

func missedDays(checkinOn string, now time.Time) []string {
	if checkinOn == "" {
		return nil
	}
	_, yesterday := checkinDay(now)
	last, err := time.ParseInLocation("2006-01-02", checkinOn, beijing)
	if err != nil {
		return nil
	}
	out := []string{}
	for d := last.AddDate(0, 0, 1); d.Format("2006-01-02") <= yesterday && len(out) < 62; d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}

func monthlyUsage(ctx context.Context, q execer, userID int64, kind string, sumRef bool, now time.Time) (int, error) {
	expr := "COUNT(*)"
	if sumRef {
		expr = "COALESCE(SUM(ref_id),0)"
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT `+expr+` FROM np_ledger WHERE user_id=? AND kind=? AND created_at>=?`, userID, kind, monthStart(now)).Scan(&n)
	return n, err
}

func (s *Store) Wallet(ctx context.Context) (Wallet, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Wallet{}, err
	}
	c := settings.Community
	u, err := s.User(ctx, CurrentUser(ctx).ID)
	if err != nil {
		return Wallet{}, err
	}
	w := Wallet{User: u, Boosts: []QuotaBoost{}, Titles: []string{}, Rules: publicRules(c), AchievementOK: map[string]bool{}}
	if w.Limits, err = limitsFor(ctx, s.DB, u, c); err != nil {
		return w, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT kind,amount,expires_at FROM np_quota_boosts WHERE user_id=? AND expires_at>? ORDER BY expires_at`, u.ID, utcNow())
	if err != nil {
		return w, err
	}
	for rows.Next() {
		var b QuotaBoost
		if err = rows.Scan(&b.Kind, &b.Amount, &b.ExpiresAt); err != nil {
			rows.Close()
			return w, err
		}
		w.Boosts = append(w.Boosts, b)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT title FROM np_user_titles WHERE user_id=? ORDER BY created_at`, u.ID)
	if err != nil {
		return w, err
	}
	for rows.Next() {
		var t string
		if err = rows.Scan(&t); err != nil {
			rows.Close()
			return w, err
		}
		w.Titles = append(w.Titles, t)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT uniq FROM np_ledger WHERE user_id=? AND kind='achievement' AND uniq IS NOT NULL`, u.ID)
	if err != nil {
		return w, err
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return w, err
		}
		parts := strings.Split(key, ":")
		if len(parts) >= 2 {
			w.AchievementOK[strings.Join(parts[1:len(parts)-1], ":")] = true
		}
	}
	rows.Close()
	now := utcNow()
	if w.MakeupUsed, err = monthlyUsage(ctx, s.DB, u.ID, "makeup", true, now); err != nil {
		return w, err
	}
	if w.LeaveUsed, err = monthlyUsage(ctx, s.DB, u.ID, "leave", false, now); err != nil {
		return w, err
	}
	w.MissedDays = len(missedDays(u.CheckinOn, now))
	today, _ := checkinDay(now)
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(xp),0) FROM np_ledger WHERE user_id=? AND uniq=?`, u.ID, fmt.Sprintf("share:%s:%d", today, u.ID)).Scan(&w.TodayShareXP)
	return w, nil
}

func (s *Store) Ledger(ctx context.Context, page, size int) (LedgerPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	out := LedgerPage{Items: []LedgerEntry{}, Page: page, PageSize: size}
	uid := CurrentUser(ctx).ID
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_ledger WHERE user_id=?`, uid).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,kind,xp,points,note,created_at FROM np_ledger WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, uid, size, (page-1)*size)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e LedgerEntry
		if err = rows.Scan(&e.ID, &e.Kind, &e.XP, &e.Points, &e.Note, &e.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	return out, rows.Err()
}

var ErrPoints = errors.New("积分不足")

// spend 在锁定账号后执行兑换逻辑并扣除积分
func (s *Store) spend(ctx context.Context, fn func(tx *sql.Tx, u *User, c CommunitySettings, now time.Time) (cost int, kind, note string, ref int64, err error)) (Wallet, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Wallet{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Wallet{}, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=? FOR UPDATE`, CurrentUser(ctx).ID))
	if err != nil {
		return Wallet{}, err
	}
	if !u.Enabled {
		return Wallet{}, ErrForbidden
	}
	cost, kind, note, ref, err := fn(tx, &u, settings.Community, utcNow())
	if err != nil {
		return Wallet{}, err
	}
	if int64(cost) > u.Points {
		return Wallet{}, fmt.Errorf("%w：需要 %d 积分，当前 %d", ErrPoints, cost, u.Points)
	}
	if _, err = award(ctx, tx, u.ID, kind, 0, -cost, ref, note, ""); err != nil {
		return Wallet{}, err
	}
	if err = tx.Commit(); err != nil {
		return Wallet{}, err
	}
	return s.Wallet(ctx)
}

func (s *Store) Makeup(ctx context.Context) (Wallet, error) {
	return s.spend(ctx, func(tx *sql.Tx, u *User, c CommunitySettings, now time.Time) (int, string, string, int64, error) {
		days := missedDays(u.CheckinOn, now)
		if len(days) == 0 {
			return 0, "", "", 0, errors.New("没有需要补签的日期：昨天已签到或尚未开始连续签到")
		}
		used, err := monthlyUsage(ctx, tx, u.ID, "makeup", true, now)
		if err != nil {
			return 0, "", "", 0, err
		}
		if len(days) > c.MakeupMonthly-used {
			return 0, "", "", 0, fmt.Errorf("本月还可补签 %d 天，需要补 %d 天，无法保住连续签到", max(c.MakeupMonthly-used, 0), len(days))
		}
		_, yesterday := checkinDay(now)
		if _, err = tx.ExecContext(ctx, `UPDATE np_users SET checkin_on=?,checkin_streak=? WHERE id=?`, yesterday, u.CheckinStreak+len(days), u.ID); err != nil {
			return 0, "", "", 0, err
		}
		return len(days) * c.MakeupPrice, "makeup", fmt.Sprintf("补签 %d 天（%s 起）", len(days), days[0]), int64(len(days)), nil
	})
}

func (s *Store) Leave(ctx context.Context) (Wallet, error) {
	return s.spend(ctx, func(tx *sql.Tx, u *User, c CommunitySettings, now time.Time) (int, string, string, int64, error) {
		if u.SubscriptionExpiresAt == nil || !u.SubscriptionExpiresAt.After(now) {
			return 0, "", "", 0, errors.New("订阅已到期，请先签到激活后再请假延期")
		}
		used, err := monthlyUsage(ctx, tx, u.ID, "leave", false, now)
		if err != nil {
			return 0, "", "", 0, err
		}
		if used >= c.LeaveMonthly {
			return 0, "", "", 0, fmt.Errorf("本月请假延期次数已用完（%d 次）", c.LeaveMonthly)
		}
		until := u.SubscriptionExpiresAt.Add(time.Duration(c.LeaveDays) * 24 * time.Hour)
		if _, err = tx.ExecContext(ctx, `UPDATE np_users SET subscription_expires_at=? WHERE id=?`, until, u.ID); err != nil {
			return 0, "", "", 0, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE np_source_seats SET expires_at=?,last_renewed_at=?,renew_method='leave' WHERE user_id=? AND state='active' AND expires_at>?`, until, now, u.ID, now); err != nil {
			return 0, "", "", 0, err
		}
		return c.LeavePrice, "leave", fmt.Sprintf("请假延期 %d 天，至 %s", c.LeaveDays, until.In(beijing).Format("2006-01-02 15:04")), 0, nil
	})
}

func (s *Store) Boost(ctx context.Context, kind string) (Wallet, error) {
	return s.spend(ctx, func(tx *sql.Tx, u *User, c CommunitySettings, now time.Time) (int, string, string, int64, error) {
		price, amount, label := 0, 1, ""
		switch kind {
		case "node":
			price, amount, label = c.BoostNodePrice, c.BoostNodeAmount, "节点"
		case "source":
			price, label = c.BoostSourcePrice, "来源"
		case "subscription":
			price, label = c.BoostSubscriptionPrice, "订阅"
		default:
			return 0, "", "", 0, errors.New("扩容类型无效")
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_quota_boosts WHERE user_id=? AND kind=? AND expires_at>?`, u.ID, kind, now).Scan(&active); err != nil {
			return 0, "", "", 0, err
		}
		if active >= c.BoostMaxActive {
			return 0, "", "", 0, fmt.Errorf("%s扩容最多同时生效 %d 份", label, c.BoostMaxActive)
		}
		until := now.Add(time.Duration(c.BoostDays) * 24 * time.Hour)
		if _, err := tx.ExecContext(ctx, `INSERT INTO np_quota_boosts(user_id,kind,amount,expires_at,created_at) VALUES(?,?,?,?,?)`, u.ID, kind, amount, until, now); err != nil {
			return 0, "", "", 0, err
		}
		return price, "boost_" + kind, fmt.Sprintf("%s额度 +%d，%d 天", label, amount, c.BoostDays), 0, nil
	})
}

func (s *Store) BuyTitle(ctx context.Context, name string) (Wallet, error) {
	return s.spend(ctx, func(tx *sql.Tx, u *User, c CommunitySettings, now time.Time) (int, string, string, int64, error) {
		price := -1
		for _, t := range c.Titles {
			if t.Name == name {
				price = t.Price
			}
		}
		if price < 0 {
			return 0, "", "", 0, errors.New("称号不存在或已下架")
		}
		var owned int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_user_titles WHERE user_id=? AND title=?`, u.ID, name).Scan(&owned); err != nil {
			return 0, "", "", 0, err
		}
		if owned > 0 {
			return 0, "", "", 0, errors.New("已拥有该称号，可直接佩戴")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO np_user_titles(user_id,title,created_at) VALUES(?,?,?)`, u.ID, name, now); err != nil {
			return 0, "", "", 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE np_users SET title=? WHERE id=?`, name, u.ID); err != nil {
			return 0, "", "", 0, err
		}
		return price, "title", "兑换称号「" + name + "」", 0, nil
	})
}

func (s *Store) EquipTitle(ctx context.Context, name string) (Wallet, error) {
	uid := CurrentUser(ctx).ID
	if name != "" {
		var owned int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_user_titles WHERE user_id=? AND title=?`, uid, name).Scan(&owned); err != nil {
			return Wallet{}, err
		}
		if owned == 0 {
			return Wallet{}, errors.New("尚未拥有该称号")
		}
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE np_users SET title=? WHERE id=?`, name, uid); err != nil {
		return Wallet{}, err
	}
	return s.Wallet(ctx)
}

func (s *Store) Tip(ctx context.Context, providerID int64, amount int) (Wallet, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Wallet{}, err
	}
	me := CurrentUser(ctx).ID
	if providerID == me {
		return Wallet{}, errors.New("不能打赏自己")
	}
	if amount < 1 || amount > settings.Community.TipMax {
		return Wallet{}, fmt.Errorf("单次打赏须为 1–%d 积分", settings.Community.TipMax)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Wallet{}, err
	}
	defer tx.Rollback()
	// 两个账号按 ID 顺序加锁，避免互相打赏时死锁
	first, second := min(me, providerID), max(me, providerID)
	users := map[int64]User{}
	for _, id := range []int64{first, second} {
		u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=? FOR UPDATE`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return Wallet{}, ErrNotFound
		}
		if err != nil {
			return Wallet{}, err
		}
		users[id] = u
	}
	sender, provider := users[me], users[providerID]
	if !sender.Enabled || !provider.Enabled {
		return Wallet{}, ErrForbidden
	}
	var sources int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_sources WHERE owner_id=? AND enabled=1`, providerID).Scan(&sources); err != nil {
		return Wallet{}, err
	}
	if sources == 0 {
		return Wallet{}, errors.New("只能打赏正在共享来源的提供方")
	}
	if int64(amount) > sender.Points {
		return Wallet{}, fmt.Errorf("%w：需要 %d 积分，当前 %d", ErrPoints, amount, sender.Points)
	}
	name := func(u User) string {
		if u.DisplayName != "" {
			return u.DisplayName
		}
		return u.Username
	}
	if _, err = award(ctx, tx, me, "tip_out", 0, -amount, providerID, "打赏 "+name(provider), ""); err != nil {
		return Wallet{}, err
	}
	if _, err = award(ctx, tx, providerID, "tip_in", 0, amount, me, "收到 "+name(sender)+" 的打赏", ""); err != nil {
		return Wallet{}, err
	}
	if err = tx.Commit(); err != nil {
		return Wallet{}, err
	}
	return s.Wallet(ctx)
}

type shareRow struct {
	id, owner                   int64
	users, total, healthy, uniq int
	healthySince                *time.Time
}

// AwardDaily grants daily sharing rewards once per Beijing day.
func (s *Store) AwardDaily(ctx context.Context) (err error) {
	now := utcNow()
	today, _ := checkinDay(now)
	res, err := s.DB.ExecContext(ctx, `INSERT IGNORE INTO np_daily_jobs(kind,day,created_at) VALUES('share',?,?)`, today, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	defer func() {
		if err != nil {
			// 失败时撤销标记，下一轮重试；奖励有唯一键不会重复发放
			_, _ = s.DB.ExecContext(context.WithoutCancel(ctx), `DELETE FROM np_daily_jobs WHERE kind='share' AND day=?`, today)
		}
	}()
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if err = s.PruneRisk(ctx); err != nil {
		return err
	}
	c := settings.Community
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id,s.owner_id,
 (SELECT COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.source_id=s.id AND st.state='active' AND st.expires_at>? AND st.user_id<>s.owner_id),
 (SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=s.id AND sn.excluded=0 AND n.blocked=0),
 (SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=s.id AND sn.excluded=0 AND n.blocked=0 AND n.last_success_at>=?),
 (SELECT COUNT(*) FROM np_source_nodes sn WHERE sn.source_id=s.id AND sn.excluded=0 AND NOT EXISTS(SELECT 1 FROM np_source_nodes o JOIN np_sources os ON os.id=o.source_id WHERE o.node_id=sn.node_id AND o.excluded=0 AND os.enabled=1 AND os.private=0 AND os.owner_id<>s.owner_id)),
 s.healthy_since
 FROM np_sources s JOIN np_users u ON u.id=s.owner_id WHERE s.enabled=1 AND s.private=0 AND u.enabled=1 ORDER BY s.id`, now, now.Add(-24*time.Hour))
	if err != nil {
		return err
	}
	list := []shareRow{}
	for rows.Next() {
		var r shareRow
		if err = rows.Scan(&r.id, &r.owner, &r.users, &r.total, &r.healthy, &r.uniq, &r.healthySince); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE np_sources SET healthy_since=NULL WHERE enabled=0 AND healthy_since IS NOT NULL`); err != nil {
		return err
	}
	type total struct{ xp, sources, users int }
	owners := map[int64]*total{}
	order := []int64{}
	for _, r := range list {
		quality := 0
		if r.total > 0 {
			quality = r.healthy * 100 / r.total
		}
		factor := 0.0
		if r.total > 0 && quality >= c.ShareQualityFull {
			factor = 1
		} else if r.total > 0 && quality >= c.ShareQualityHalf {
			factor = 0.5
		}
		if factor > 0 && r.healthySince == nil {
			_, err = s.DB.ExecContext(ctx, `UPDATE np_sources SET healthy_since=? WHERE id=?`, now, r.id)
		} else if factor == 0 && r.healthySince != nil {
			_, err = s.DB.ExecContext(ctx, `UPDATE np_sources SET healthy_since=NULL WHERE id=?`, r.id)
		}
		if err != nil {
			return err
		}
		if r.users == 0 || factor == 0 {
			continue
		}
		xp := float64(min(r.users, c.ShareXPSourceCap)*c.ShareXPPerUser) * factor
		if r.total > 0 && r.uniq*100/r.total >= c.UniqueShareThreshold {
			xp *= 1 + float64(c.UniqueBonusPercent)/100
		}
		t := owners[r.owner]
		if t == nil {
			t = &total{}
			owners[r.owner] = t
			order = append(order, r.owner)
		}
		t.xp += int(xp)
		t.sources++
		t.users += r.users
		if r.healthySince != nil {
			for i, days := range MilestoneDays {
				if now.Sub(*r.healthySince) >= time.Duration(days)*24*time.Hour && c.MilestoneXP[i] > 0 {
					if _, err = award(ctx, s.DB, r.owner, "milestone", c.MilestoneXP[i], 0, r.id, fmt.Sprintf("来源 #%d 连续健康共享 %d 天", r.id, days), fmt.Sprintf("milestone:%d:%d:%d", days, r.id, r.owner)); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, owner := range order {
		t := owners[owner]
		xp := min(t.xp, c.ShareXPDailyCap)
		if xp <= 0 {
			continue
		}
		points := xp * c.SharePointsPercent / 100
		if _, err = award(ctx, s.DB, owner, "share", xp, points, 0, fmt.Sprintf("%d 个来源被 %d 人次使用", t.sources, t.users), fmt.Sprintf("share:%s:%d", today, owner)); err != nil {
			return err
		}
	}
	return nil
}
