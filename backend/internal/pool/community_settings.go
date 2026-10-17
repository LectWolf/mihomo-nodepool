package pool

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

type LevelQuota struct {
	Nodes         int `json:"nodes"`
	Sources       int `json:"sources"`
	Subscriptions int `json:"subscriptions"`
}

type TitleItem struct {
	Name  string `json:"name"`
	Price int    `json:"price"`
	Color string `json:"color"`
}

// TitleColors are the palette keys understood by the web console.
var TitleColors = []string{"amber", "blue", "green", "violet", "red", "pink", "cyan", "slate", "gold"}

// CommunitySettings holds level quotas, rewards, point prices and risk limits.
type CommunitySettings struct {
	LevelQuotas []LevelQuota `json:"level_quotas"`

	ShareXPPerUser       int   `json:"share_xp_per_user"`
	ShareXPSourceCap     int   `json:"share_xp_source_cap"`
	ShareXPDailyCap      int   `json:"share_xp_daily_cap"`
	SharePointsPercent   int   `json:"share_points_percent"`
	ShareQualityFull     int   `json:"share_quality_full"`
	ShareQualityHalf     int   `json:"share_quality_half"`
	UniqueBonusPercent   int   `json:"unique_bonus_percent"`
	UniqueShareThreshold int   `json:"unique_share_threshold"`
	MilestoneXP          []int `json:"milestone_xp"`
	AchievementXP        int   `json:"achievement_xp"`
	ProbeXP              int   `json:"probe_xp"`

	MakeupPrice            int         `json:"makeup_price"`
	MakeupMonthly          int         `json:"makeup_monthly"`
	LeavePrice             int         `json:"leave_price"`
	LeaveDays              int         `json:"leave_days"`
	LeaveMonthly           int         `json:"leave_monthly"`
	BoostDays              int         `json:"boost_days"`
	BoostNodePrice         int         `json:"boost_node_price"`
	BoostNodeAmount        int         `json:"boost_node_amount"`
	BoostSourcePrice       int         `json:"boost_source_price"`
	BoostSubscriptionPrice int         `json:"boost_subscription_price"`
	BoostMaxActive         int         `json:"boost_max_active"`
	TipMax                 int         `json:"tip_max"`
	Titles                 []TitleItem `json:"titles"`

	RiskEnabled            bool `json:"risk_enabled"`
	RiskWindowDays         int  `json:"risk_window_days"`
	RiskExposureMultiplier int  `json:"risk_exposure_multiplier"`
	RiskDailyNewNodes      int  `json:"risk_daily_new_nodes"`
	RiskDailyNewSources    int  `json:"risk_daily_new_sources"`
	RiskPullsPerHour       int  `json:"risk_pulls_per_hour"`
	GuestBrowse            bool `json:"guest_browse"`
	RegistrationEnabled    bool `json:"registration_enabled"`
	RiskDistinctIPs        int  `json:"risk_distinct_ips"`

	FeedbackEnabled     bool `json:"feedback_enabled"`
	FeedbackDailyLimit  int  `json:"feedback_daily_limit"`
	FeedbackVisibleDays int  `json:"feedback_visible_days"`
	// 被确认的反馈奖励反馈人；达到人数阈值的节点在订阅中排到最后
	FeedbackRewardXP     int `json:"feedback_reward_xp"`
	FeedbackRewardPoints int `json:"feedback_reward_points"`
	FeedbackRewardDaily  int `json:"feedback_reward_daily"`
	FeedbackDemoteUsers  int `json:"feedback_demote_users"`

	UsagePollEnabled bool `json:"usage_poll_enabled"`
}

// 里程碑对应连续健康共享 30、90、180 天
var MilestoneDays = []int{30, 90, 180}

func DefaultCommunity() CommunitySettings {
	return CommunitySettings{
		LevelQuotas: []LevelQuota{
			{200, 3, 1}, {200, 3, 2}, {300, 4, 2}, {400, 5, 3}, {500, 6, 3}, {700, 8, 4}, {1000, 10, 5},
		},
		ShareXPPerUser: 1, ShareXPSourceCap: 10, ShareXPDailyCap: 30, SharePointsPercent: 50,
		ShareQualityFull: 80, ShareQualityHalf: 50, UniqueBonusPercent: 50, UniqueShareThreshold: 50,
		MilestoneXP: []int{30, 100, 300}, AchievementXP: 15, ProbeXP: 1,
		MakeupPrice: 10, MakeupMonthly: 3, LeavePrice: 20, LeaveDays: 7, LeaveMonthly: 1,
		BoostDays: 30, BoostNodePrice: 30, BoostNodeAmount: 100, BoostSourcePrice: 50, BoostSubscriptionPrice: 30, BoostMaxActive: 3,
		TipMax:      500,
		Titles:      []TitleItem{{"共享先锋", 300, "green"}, {"节点守望者", 800, "blue"}, {"彼岸摆渡人", 1500, "violet"}},
		RiskEnabled: true, RiskWindowDays: 30, RiskExposureMultiplier: 3, RiskDailyNewNodes: 0, RiskPullsPerHour: 120, GuestBrowse: true, RegistrationEnabled: true, RiskDistinctIPs: 6,
		FeedbackEnabled: true, FeedbackDailyLimit: 20, FeedbackVisibleDays: 30,
		FeedbackRewardXP: 2, FeedbackRewardPoints: 2, FeedbackRewardDaily: 5, FeedbackDemoteUsers: 2,
		UsagePollEnabled: true,
	}
}

func (c CommunitySettings) Quota(level int) LevelQuota {
	if len(c.LevelQuotas) == 0 {
		return DefaultCommunity().LevelQuotas[0]
	}
	if level < 0 {
		level = 0
	}
	if level >= len(c.LevelQuotas) {
		level = len(c.LevelQuotas) - 1
	}
	return c.LevelQuotas[level]
}

func between(v, low, high int) bool { return v >= low && v <= high }

func (c CommunitySettings) Validate() error {
	if len(c.LevelQuotas) != len(LevelThresholds) {
		return fmt.Errorf("等级额度须为 %d 档", len(LevelThresholds))
	}
	for i, q := range c.LevelQuotas {
		if !between(q.Nodes, 1, 100000) || !between(q.Sources, 1, 10000) || !between(q.Subscriptions, 1, 1000) {
			return fmt.Errorf("Lv%d 额度须为节点 1–100000、来源 1–10000、订阅 1–1000", i)
		}
	}
	if len(c.MilestoneXP) != len(MilestoneDays) {
		return errors.New("里程碑经验须为 3 档")
	}
	for _, v := range c.MilestoneXP {
		if !between(v, 0, 100000) {
			return errors.New("里程碑经验须为 0–100000")
		}
	}
	checks := []struct {
		v, low, high int
		name         string
	}{
		{c.ShareXPPerUser, 0, 100, "每位使用者经验"}, {c.ShareXPSourceCap, 1, 1000, "单来源每日人数上限"}, {c.ShareXPDailyCap, 0, 10000, "共享每日经验上限"},
		{c.SharePointsPercent, 0, 100, "共享积分比例"}, {c.ShareQualityFull, 1, 100, "全额质量线"}, {c.ShareQualityHalf, 0, 100, "半额质量线"},
		{c.UniqueBonusPercent, 0, 500, "独特节点加成"}, {c.UniqueShareThreshold, 1, 100, "独特节点占比"}, {c.AchievementXP, 0, 10000, "成就经验"}, {c.ProbeXP, 0, 100, "客户端续期经验"},
		{c.MakeupPrice, 0, 100000, "补签价格"}, {c.MakeupMonthly, 0, 31, "每月补签天数"}, {c.LeavePrice, 0, 100000, "请假价格"}, {c.LeaveDays, 1, 60, "请假天数"}, {c.LeaveMonthly, 0, 10, "每月请假次数"},
		{c.BoostDays, 1, 365, "扩容天数"}, {c.BoostNodePrice, 0, 100000, "节点扩容价格"}, {c.BoostNodeAmount, 1, 10000, "节点扩容数量"}, {c.BoostSourcePrice, 0, 100000, "来源扩容价格"},
		{c.BoostSubscriptionPrice, 0, 100000, "订阅扩容价格"}, {c.BoostMaxActive, 0, 20, "同类扩容叠加上限"}, {c.TipMax, 1, 100000, "单次打赏上限"},
		{c.RiskWindowDays, 1, 365, "风控窗口"}, {c.RiskExposureMultiplier, 1, 100, "窗口内节点倍数"}, {c.RiskDailyNewNodes, 0, 100000, "每日新增节点"}, {c.RiskDailyNewSources, 0, 1000, "每日新增跟随来源"},
		{c.RiskPullsPerHour, 1, 10000, "每小时拉取次数"}, {c.RiskDistinctIPs, 1, 1000, "每日拉取 IP 数"},
		{c.FeedbackDailyLimit, 1, 1000, "每日反馈次数"}, {c.FeedbackVisibleDays, 1, 90, "反馈展示天数"},
		{c.FeedbackRewardXP, 0, 100, "反馈奖励经验"}, {c.FeedbackRewardPoints, 0, 100, "反馈奖励积分"}, {c.FeedbackRewardDaily, 0, 100, "每日反馈奖励次数"}, {c.FeedbackDemoteUsers, 0, 100, "节点降序反馈人数"},
	}
	for _, item := range checks {
		if !between(item.v, item.low, item.high) {
			return fmt.Errorf("%s须为 %d–%d", item.name, item.low, item.high)
		}
	}
	if c.ShareQualityHalf > c.ShareQualityFull {
		return errors.New("半额质量线不能高于全额质量线")
	}
	if len(c.Titles) > 30 {
		return errors.New("称号最多 30 个")
	}
	seen := map[string]bool{}
	for _, t := range c.Titles {
		name := strings.TrimSpace(t.Name)
		if name == "" || utf8.RuneCountInString(name) > 12 || name != t.Name || seen[name] || !between(t.Price, 0, 1000000) {
			return errors.New("称号名称须为 1–12 字且不重复，价格 0–1000000")
		}
		if t.Color != "" && !slices.Contains(TitleColors, t.Color) {
			return errors.New("称号颜色无效")
		}
		seen[name] = true
	}
	return nil
}

// EffectiveLimits explains how quotas were composed for one account.
type EffectiveLimits struct {
	Nodes         int        `json:"nodes"`
	Sources       int        `json:"sources"`
	Subscriptions int        `json:"subscriptions"`
	Level         LevelQuota `json:"level"`
	Personal      LevelQuota `json:"personal"`
	Boost         LevelQuota `json:"boost"`
}

func composeLimits(u User, c CommunitySettings, boost LevelQuota) EffectiveLimits {
	level := c.Quota(u.Level)
	personal := LevelQuota{u.NodeLimit, u.SourceLimit, u.SubscriptionLimit}
	return EffectiveLimits{
		Nodes:         max(level.Nodes, personal.Nodes) + boost.Nodes,
		Sources:       max(level.Sources, personal.Sources) + boost.Sources,
		Subscriptions: max(level.Subscriptions, personal.Subscriptions) + boost.Subscriptions,
		Level:         level, Personal: personal, Boost: boost,
	}
}

// CommunityNow 返回当前社区设置，优先使用缓存
func (s *Store) CommunityNow(ctx context.Context) CommunitySettings {
	if c := communityCache.Load(); c != nil {
		return *c
	}
	if v, err := s.Settings(ctx); err == nil {
		return v.Community
	}
	return DefaultCommunity()
}
