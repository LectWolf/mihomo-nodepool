package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FollowSourceDetail 订阅中“跟随整个来源”的展示信息
type FollowSourceDetail struct {
	SourceID          int64              `json:"source_id"`
	Name              string             `json:"name"`
	NodeCount         int                `json:"node_count"`
	Available         bool               `json:"available"`
	MinLevel          int                `json:"min_level"`
	MaxUsers          int                `json:"max_users"`
	SubscriptionUsage *SubscriptionUsage `json:"subscription_usage,omitempty"`
	ProviderID        int64              `json:"provider_id"`
	ProviderName      string             `json:"provider_name"`
	ProviderAvatar    int                `json:"provider_avatar"`
	ProviderTitle     string             `json:"provider_title"`
}

func (s *Store) ensureFollow(ctx context.Context) error {
	for _, table := range []string{"np_subscriptions", "np_subscription_archives"} {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='follow_ids'`, table).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.DB.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN follow_ids JSON NULL`); err != nil {
				return err
			}
		}
	}
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS np_source_exposures (
 user_id BIGINT NOT NULL, source_id BIGINT NOT NULL, first_at DATETIME(6) NOT NULL, last_at DATETIME(6) NOT NULL,
 PRIMARY KEY(user_id,source_id), INDEX(user_id,last_at),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`)
	return err
}

// checkSourceExposure 限制短时间内反复更换跟随的来源，规则与节点风控一致：
// 每日新增来源数、窗口内不同来源数（倍数 × 来源额度）、临时冻结
func checkSourceExposure(ctx context.Context, q execer, u User, c CommunitySettings, current map[int64]bool, follows []int64, now time.Time) (fresh, kept []int64, err error) {
	window := map[int64]bool{}
	todayNew := 0
	if u.Role != "admin" {
		rows, err := q.QueryContext(ctx, `SELECT source_id,first_at FROM np_source_exposures WHERE user_id=? AND last_at>=?`, u.ID, now.Add(-time.Duration(c.RiskWindowDays)*24*time.Hour))
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
	for _, id := range follows {
		if current[id] || window[id] {
			kept = append(kept, id)
		} else {
			fresh = append(fresh, id)
		}
	}
	if u.Role == "admin" || !c.RiskEnabled || len(fresh) == 0 {
		return fresh, kept, nil
	}
	if u.RiskFrozenUntil != nil && u.RiskFrozenUntil.After(now) {
		return nil, nil, fmt.Errorf("%w：账号已被临时限制更换来源，至 %s 解除", ErrRiskLimit, u.RiskFrozenUntil.In(beijing).Format("01-02 15:04"))
	}
	daily := sourceDailyCap(c, u.SourceLimit)
	if todayNew+len(fresh) > daily {
		return nil, nil, fmt.Errorf("%w：今日已新增跟随 %d 个来源，本次新增 %d 个，每日上限 %d 个，请明天再试", ErrRiskLimit, todayNew, len(fresh), daily)
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
	if limit := sourceWindowLimit(c, u.SourceLimit); union > limit {
		return nil, nil, fmt.Errorf("%w：近 %d 天跟随过的不同来源将达到 %d 个，上限 %d 个", ErrRiskLimit, c.RiskWindowDays, union, limit)
	}
	return fresh, kept, nil
}

func sourceDailyCap(c CommunitySettings, sourceLimit int) int {
	if c.RiskDailyNewSources > 0 {
		return c.RiskDailyNewSources
	}
	return max(sourceLimit, 1)
}

func sourceWindowLimit(c CommunitySettings, sourceLimit int) int {
	return c.RiskExposureMultiplier * max(sourceLimit, 1)
}

func recordSourceExposure(ctx context.Context, q execer, userID int64, fresh, kept []int64, now time.Time) error {
	for _, id := range fresh {
		if _, err := q.ExecContext(ctx, `INSERT INTO np_source_exposures(user_id,source_id,first_at,last_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE first_at=VALUES(first_at),last_at=VALUES(last_at)`, userID, id, now, now); err != nil {
			return err
		}
	}
	for _, id := range kept {
		if _, err := q.ExecContext(ctx, `INSERT INTO np_source_exposures(user_id,source_id,first_at,last_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE last_at=VALUES(last_at)`, userID, id, now, now); err != nil {
			return err
		}
	}
	return nil
}

// followDetailsFrom 补全跟随来源的名称、当前可用节点数与提供方
func followDetailsFrom(ctx context.Context, db queryer, items []Subscription) error {
	ids := map[int64]bool{}
	for _, item := range items {
		for _, id := range item.FollowIDs {
			ids[id] = true
		}
	}
	found := map[int64]FollowSourceDetail{}
	if len(ids) > 0 {
		marks, args := make([]string, 0, len(ids)), make([]any, 0, len(ids))
		for id := range ids {
			marks = append(marks, "?")
			args = append(args, id)
		}
		rows, err := db.QueryContext(ctx, `SELECT s.id,s.name,(SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=s.id AND sn.excluded=0 AND n.blocked=0),(s.enabled=1 AND u.enabled=1),s.min_level,s.max_users,s.subscription_usage,u.id,COALESCE(NULLIF(u.display_name,''),u.username),u.avatar_version,u.title FROM np_sources s JOIN np_users u ON u.id=s.owner_id WHERE s.id IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d FollowSourceDetail
			var usage []byte
			if err = rows.Scan(&d.SourceID, &d.Name, &d.NodeCount, &d.Available, &d.MinLevel, &d.MaxUsers, &usage, &d.ProviderID, &d.ProviderName, &d.ProviderAvatar, &d.ProviderTitle); err != nil {
				rows.Close()
				return err
			}
			if len(usage) > 0 {
				if err = json.Unmarshal(usage, &d.SubscriptionUsage); err != nil {
					rows.Close()
					return err
				}
			}
			refreshUsageStatus(d.SubscriptionUsage)
			found[d.SourceID] = d
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for i := range items {
		items[i].FollowDetails = []FollowSourceDetail{}
		items[i].FollowNodeCount = 0
		for _, id := range items[i].FollowIDs {
			d, ok := found[id]
			if !ok {
				d = FollowSourceDetail{SourceID: id, Name: fmt.Sprintf("来源 #%d", id)}
			}
			if d.Available {
				items[i].FollowNodeCount += d.NodeCount
			}
			items[i].FollowDetails = append(items[i].FollowDetails, d)
		}
	}
	return nil
}
