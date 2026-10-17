package pool

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UserStats 用户管理详情页的统计数据
type UserStats struct {
	Sources          int        `json:"sources"`
	SourcesActive    int        `json:"sources_active"`
	SourcesTakenDown int        `json:"sources_taken_down"`
	ProvidedNodes    int        `json:"provided_nodes"`
	Subscribers      int        `json:"subscribers"`
	Followers        int        `json:"followers"`
	Following        int        `json:"following"`
	Subscriptions    int        `json:"subscriptions"`
	Seats            int        `json:"seats"`
	Pulls7d          int        `json:"pulls_7d"`
	PullIPs7d        int        `json:"pull_ips_7d"`
	LastPullAt       *time.Time `json:"last_pull_at"`
	Sessions         int        `json:"sessions"`
	LastLoginAt      *time.Time `json:"last_login_at"`
	Feedback         int        `json:"feedback"`
	PointsEarned30d  int        `json:"points_earned_30d"`
	PointsSpent30d   int        `json:"points_spent_30d"`
	XPEarned30d      int        `json:"xp_earned_30d"`
}

type UserDetail struct {
	User       User       `json:"user"`
	Identities []Identity `json:"identities"`
	Stats      UserStats  `json:"stats"`
	Sources    []Source   `json:"sources"`
}

// managedUser 读取用户并检查用户管理员的权限范围
func (s *Store) managedUser(ctx context.Context, id int64) (User, error) {
	u, err := s.User(ctx, id)
	if err != nil {
		return u, err
	}
	actor := CurrentUser(ctx)
	if !CanManageUsers(actor) {
		return u, ErrForbidden
	}
	if actor.Role == "manager" && u.Role != "user" && u.ID != actor.ID {
		return u, fmt.Errorf("%w：用户管理员只能管理普通用户", ErrForbidden)
	}
	return u, nil
}

func (s *Store) UserDetail(ctx context.Context, id int64) (UserDetail, error) {
	var out UserDetail
	var err error
	if out.User, err = s.managedUser(ctx, id); err != nil {
		return out, err
	}
	if out.Identities, err = s.Identities(ctx, id); err != nil {
		return out, err
	}
	now := utcNow()
	st := &out.Stats
	day7 := now.Add(-6 * 24 * time.Hour).Format("2006-01-02")
	var lastPull, lastLogin sql.NullTime
	err = s.DB.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM np_sources WHERE owner_id=?),
 (SELECT COUNT(*) FROM np_sources WHERE owner_id=? AND enabled=1),
 (SELECT COUNT(*) FROM np_sources WHERE owner_id=? AND takedown_at IS NOT NULL),
 (SELECT COUNT(DISTINCT sn.node_id) FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id JOIN np_nodes n ON n.id=sn.node_id WHERE s.owner_id=? AND s.enabled=1 AND sn.excluded=0 AND n.blocked=0),
 (SELECT COUNT(DISTINCT st.user_id) FROM np_source_seats st JOIN np_sources s ON s.id=st.source_id WHERE s.owner_id=? AND st.user_id<>? AND st.state='active' AND st.expires_at>?),
 (SELECT COUNT(*) FROM np_follows WHERE provider_id=?),
 (SELECT COUNT(*) FROM np_follows WHERE user_id=?),
 (SELECT COUNT(*) FROM np_subscriptions WHERE user_id=?),
 (SELECT COUNT(*) FROM np_source_seats st JOIN np_sources s ON s.id=st.source_id WHERE st.user_id=? AND s.owner_id<>? AND st.state='active' AND st.expires_at>?),
 (SELECT COALESCE(SUM(pulls),0) FROM np_pull_stats WHERE user_id=? AND day>=?),
 (SELECT COUNT(DISTINCT ip_hash) FROM np_pull_stats WHERE user_id=? AND day>=?),
 (SELECT MAX(last_at) FROM np_pull_stats WHERE user_id=?),
 (SELECT COUNT(*) FROM np_sessions WHERE user_id=? AND expires_at>?),
 (SELECT MAX(created_at) FROM np_sessions WHERE user_id=?),
 (SELECT COUNT(*) FROM np_feedback WHERE user_id=?),
 (SELECT COALESCE(SUM(GREATEST(points,0)),0) FROM np_ledger WHERE user_id=? AND created_at>?),
 (SELECT COALESCE(SUM(LEAST(points,0)),0) FROM np_ledger WHERE user_id=? AND created_at>?),
 (SELECT COALESCE(SUM(GREATEST(xp,0)),0) FROM np_ledger WHERE user_id=? AND created_at>?)`,
		id, id, id, id, id, id, now, id, id, id, id, id, now, id, day7, id, day7, id, id, now, id, id,
		id, now.Add(-30*24*time.Hour), id, now.Add(-30*24*time.Hour), id, now.Add(-30*24*time.Hour),
	).Scan(&st.Sources, &st.SourcesActive, &st.SourcesTakenDown, &st.ProvidedNodes, &st.Subscribers, &st.Followers, &st.Following,
		&st.Subscriptions, &st.Seats, &st.Pulls7d, &st.PullIPs7d, &lastPull, &st.Sessions, &lastLogin, &st.Feedback,
		&st.PointsEarned30d, &st.PointsSpent30d, &st.XPEarned30d)
	if err != nil {
		return out, err
	}
	st.PointsSpent30d = -st.PointsSpent30d
	if lastPull.Valid {
		st.LastPullAt = &lastPull.Time
	}
	if lastLogin.Valid {
		st.LastLoginAt = &lastLogin.Time
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+publicSourceColumns+` FROM np_sources WHERE owner_id=? ORDER BY (takedown_at IS NULL),id DESC LIMIT 50`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Sources = []Source{}
	for rows.Next() {
		v, err := scanSource(rows)
		if err != nil {
			return out, err
		}
		out.Sources = append(out.Sources, v)
	}
	return out, rows.Err()
}

// AdminUnlinkIdentity 管理员解绑用户的第三方账号
func (s *Store) AdminUnlinkIdentity(ctx context.Context, id int64, provider string) error {
	if _, err := s.managedUser(ctx, id); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM np_user_identities WHERE user_id=? AND provider=?`, id, provider)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
