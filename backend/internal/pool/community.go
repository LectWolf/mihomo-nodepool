package pool

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

var LevelThresholds = []int64{0, 50, 200, 1000, 3500, 10000, 30000}
var LevelNames = []string{"初至", "识途", "通行", "常客", "熟路", "向导", "彼岸"}

func LevelForXP(xp int64) int {
	level := 0
	for i, threshold := range LevelThresholds {
		if xp >= threshold {
			level = i
		}
	}
	return level
}
func checkinDay(now time.Time) (string, string) {
	local := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	return local.Format("2006-01-02"), local.AddDate(0, 0, -1).Format("2006-01-02")
}

var ErrSubscriptionInactive = errors.New("订阅已到期，请前往平台签到激活")

type ActivationSkip struct {
	SourceID int64  `json:"source_id"`
	Name     string `json:"name"`
	Reason   string `json:"reason"`
}
type CheckinResult struct {
	User              User             `json:"user"`
	Awarded           bool             `json:"awarded"`
	XPAdded           int64            `json:"xp_added"`
	PointsAdded       int64            `json:"points_added"`
	NeedsConfirmation bool             `json:"needs_confirmation"`
	Skipped           []ActivationSkip `json:"skipped"`
}

func recordSeatEvent(ctx context.Context, tx *sql.Tx, sourceID, userID int64, action string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO np_source_events(source_id,owner_id,source_name,user_id,action,created_at) SELECT id,owner_id,name,?,?,? FROM np_sources WHERE id=?`, userID, action, now, sourceID)
	return err
}

// Caller holds the account and then source row. Capacity uses current leases,
// never historical claims. Expired seats cease occupying capacity immediately.
func claimSeat(ctx context.Context, tx *sql.Tx, u User, sourceID int64, until time.Time) error {
	var owner int64
	var enabled, ownerEnabled bool
	var limit, minLevel int
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,enabled,max_users,min_level FROM np_sources WHERE id=? FOR UPDATE`, sourceID).Scan(&owner, &enabled, &limit, &minLevel); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=?`, owner).Scan(&ownerEnabled); err != nil {
		return err
	}
	if !enabled || !ownerEnabled {
		return ErrForbidden
	}
	if u.ID != owner && u.Role != "admin" && u.Level < minLevel {
		return errors.New("账号等级不足，无法领取该来源")
	}
	var state string
	var oldUntil time.Time
	err := tx.QueryRowContext(ctx, `SELECT state,expires_at FROM np_source_seats WHERE source_id=? AND user_id=?`, sourceID, u.ID).Scan(&state, &oldUntil)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if state == "kicked" {
		return errors.New("提供方已禁止你领取此来源")
	}
	now := utcNow()
	if state != "active" || !oldUntil.After(now) {
		var occupied int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_seats st JOIN np_users u ON u.id=st.user_id AND u.enabled=1 WHERE st.source_id=? AND st.state='active' AND st.expires_at>? AND st.user_id<>?`, sourceID, now, u.ID).Scan(&occupied); err != nil {
			return err
		}
		if limit > 0 && occupied >= limit {
			return ErrQuota
		}
		if err := recordSeatEvent(ctx, tx, sourceID, u.ID, "claimed", now); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO np_source_seats(source_id,user_id,state,claimed_at,expires_at,last_renewed_at) VALUES(?,?,'active',?,?,?) ON DUPLICATE KEY UPDATE state='active',expires_at=VALUES(expires_at),ended_at=NULL`, sourceID, u.ID, now, until, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO np_source_claims(source_id,user_id,created_at) VALUES(?,?,?)`, sourceID, u.ID, now)
	return err
}

func (s *Store) CheckIn(ctx context.Context, confirmPartial bool) (CheckinResult, error) {
	out := CheckinResult{Skipped: []ActivationSkip{}}
	settings, err := s.Settings(ctx)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=? FOR UPDATE`, CurrentUser(ctx).ID))
	if err != nil {
		return out, err
	}
	if !u.Enabled {
		return out, ErrForbidden
	}
	originalUser := u
	now := utcNow()
	today, yesterday := checkinDay(now)
	if u.CheckinOn != today {
		if u.CheckinOn == yesterday {
			u.CheckinStreak++
		} else {
			u.CheckinStreak = 1
		}
		out.XPAdded, out.PointsAdded = 5, 2
		if u.CheckinStreak%7 == 0 {
			out.XPAdded += 20
			out.PointsAdded += 8
		}
		if u.XP > 1000000000-out.XPAdded || u.Points > 1000000000-out.PointsAdded {
			return out, errors.New("经验或积分已达上限")
		}
		u.XP += out.XPAdded
		u.Points += out.PointsAdded
		u.Level = LevelForXP(u.XP)
		u.CheckinOn = today
		out.Awarded = true
	}
	items, err := listSubscriptions(ctx, tx, u.ID)
	if err != nil {
		return out, err
	}
	sourceSet := map[int64]bool{}
	for _, item := range items {
		for _, id := range item.SourceIDs {
			sourceSet[id] = true
		}
	}
	ids := make([]int64, 0, len(sourceSet))
	for id := range sourceSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	until := now.Add(time.Duration(settings.SubscriptionLeaseDays) * 24 * time.Hour)
	excluded := map[int64]bool{}
	// Lock all referenced sources in stable order before acquiring any seat.
	for _, id := range ids {
		var name string
		var owner int64
		var enabled, ownerEnabled bool
		var limit, minimum int
		err = tx.QueryRowContext(ctx, `SELECT name,owner_id,enabled,max_users,min_level FROM np_sources WHERE id=? FOR UPDATE`, id).Scan(&name, &owner, &enabled, &limit, &minimum)
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=?`, owner).Scan(&ownerEnabled)
		}
		reason := ""
		if err == sql.ErrNoRows {
			reason = "来源已删除"
		} else if err != nil {
			return out, err
		} else if !enabled || !ownerEnabled {
			reason = "来源已停用"
		} else if u.ID != owner && u.Role != "admin" && u.Level < minimum {
			reason = "等级不足"
		}
		if reason == "" {
			var state string
			var expiry time.Time
			err = tx.QueryRowContext(ctx, `SELECT state,expires_at FROM np_source_seats WHERE source_id=? AND user_id=?`, id, u.ID).Scan(&state, &expiry)
			if err != nil && err != sql.ErrNoRows {
				return out, err
			}
			if state == "kicked" {
				reason = "已被提供方禁止"
			} else if state != "active" || !expiry.After(now) {
				var count int
				if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_seats st JOIN np_users u ON u.id=st.user_id AND u.enabled=1 WHERE st.source_id=? AND st.state='active' AND st.expires_at>? AND st.user_id<>?`, id, now, u.ID).Scan(&count); err != nil {
					return out, err
				}
				if limit > 0 && count >= limit {
					reason = "来源席位已满"
				}
			}
		}
		if reason != "" {
			out.Skipped = append(out.Skipped, ActivationSkip{id, name, reason})
			excluded[id] = true
		}
	}
	if len(out.Skipped) > 0 && !confirmPartial {
		out.NeedsConfirmation = true
		out.User = originalUser
		out.Awarded = false
		out.XPAdded = 0
		out.PointsAdded = 0
		return out, nil
	}
	if len(excluded) > 0 {
		if err = removeSubscriptionSources(ctx, tx, u.ID, excluded); err != nil {
			return out, err
		}
	}
	for _, id := range ids {
		if !excluded[id] {
			if err = claimSeat(ctx, tx, u, id, until); err != nil {
				return out, err
			}
		}
	}
	// Only web activation can revive an expired account. Probe requests cannot.
	probe, err := newProbeUUID()
	if err != nil {
		return out, err
	}
	if u.ProbeUUID != "" && u.SubscriptionExpiresAt != nil && u.SubscriptionExpiresAt.After(now) {
		probe = u.ProbeUUID
	}
	if _, err = tx.ExecContext(ctx, `UPDATE np_users SET xp=?,points=?,checkin_on=?,checkin_streak=?,subscription_expires_at=?,probe_uuid=? WHERE id=?`, u.XP, u.Points, u.CheckinOn, u.CheckinStreak, until, probe, u.ID); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE np_source_seats SET expires_at=?,last_renewed_at=?,renew_method='checkin' WHERE user_id=? AND state='active'`, until, now, u.ID); err != nil {
		return out, err
	}
	if err = releaseUnusedSeats(ctx, tx, u.ID); err != nil {
		return out, err
	}
	u.SubscriptionExpiresAt = &until
	u.ProbeUUID = probe
	out.User = u
	return out, tx.Commit()
}

func removeSubscriptionSources(ctx context.Context, tx *sql.Tx, userID int64, excluded map[int64]bool) error {
	items, err := listSubscriptions(ctx, tx, userID)
	if err != nil {
		return err
	}
	for _, item := range items {
		kept := []Selection{}
		sources := map[int64]bool{}
		changed := false
		for _, node := range item.Nodes {
			if excluded[node.SourceID] {
				changed = true
				continue
			}
			kept = append(kept, node)
			sources[node.SourceID] = true
		}
		if !changed {
			continue
		}
		if err = archiveSubscription(ctx, tx, item, "source_removed"); err != nil {
			return err
		}
		ids := []int64{}
		for id := range sources {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if err = updateSubscriptionSelection(ctx, tx, item.ID, kept, ids); err != nil {
			return err
		}
	}
	return nil
}
func releaseUnusedSeats(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO np_source_events(source_id,owner_id,source_name,user_id,action,created_at) SELECT st.source_id,s.owner_id,s.name,st.user_id,'released',UTC_TIMESTAMP(6) FROM np_source_seats st JOIN np_sources s ON s.id=st.source_id WHERE st.user_id=? AND st.state='active' AND NOT EXISTS(SELECT 1 FROM np_subscriptions sub WHERE sub.user_id=st.user_id AND JSON_CONTAINS(sub.source_ids,CAST(st.source_id AS CHAR)))`, userID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE np_source_seats st SET state='released',ended_at=UTC_TIMESTAMP(6) WHERE st.user_id=? AND st.state='active' AND NOT EXISTS(SELECT 1 FROM np_subscriptions sub WHERE sub.user_id=st.user_id AND JSON_CONTAINS(sub.source_ids,CAST(st.source_id AS CHAR)))`, userID)
	return err
}

func releaseDisabledUserSeats(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT source_id FROM np_source_seats WHERE user_id=? AND state='active' ORDER BY source_id`, userID)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		var locked int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM np_sources WHERE id=? FOR UPDATE`, id).Scan(&locked); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return err
		}
		if err = recordSeatEvent(ctx, tx, id, userID, "released", utcNow()); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE np_source_seats SET state='released',ended_at=UTC_TIMESTAMP(6) WHERE user_id=? AND state='active'`, userID)
	return err
}

func (s *Store) RenewByProbe(ctx context.Context, uuid string) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE probe_uuid=? FOR UPDATE`, uuid))
	if err != nil {
		return ErrNotFound
	}
	now := utcNow()
	if !u.Enabled || u.SubscriptionExpiresAt == nil || !u.SubscriptionExpiresAt.After(now) {
		return ErrSubscriptionInactive
	}
	// Do not refresh every five-minute health check into a write storm.
	until := now.Add(time.Duration(settings.SubscriptionLeaseDays) * 24 * time.Hour)
	if u.SubscriptionExpiresAt.After(until.Add(-time.Hour)) {
		return nil
	}
	// Serialize with source acquisition. A probe waiting across expiry must not
	// revive a seat whose capacity another account can already acquire.
	rows, err := tx.QueryContext(ctx, `SELECT source_id FROM np_source_seats WHERE user_id=? AND state='active' ORDER BY source_id`, u.ID)
	if err != nil {
		return err
	}
	sources := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		sources = append(sources, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range sources {
		var locked int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM np_sources WHERE id=? FOR UPDATE`, id).Scan(&locked); err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	now = utcNow()
	if !u.SubscriptionExpiresAt.After(now) {
		return ErrSubscriptionInactive
	}
	until = now.Add(time.Duration(settings.SubscriptionLeaseDays) * 24 * time.Hour)
	if _, err = tx.ExecContext(ctx, `UPDATE np_users SET subscription_expires_at=? WHERE id=?`, until, u.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE np_source_seats SET expires_at=?,last_renewed_at=?,renew_method='probe' WHERE user_id=? AND state='active' AND expires_at>?`, until, now, u.ID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SweepCommunity(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT user_id FROM np_source_seats WHERE state='active' AND expires_at<=UTC_TIMESTAMP(6) ORDER BY user_id LIMIT 200`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.expireUserSeats(ctx, id); err != nil {
			return err
		}
	}
	cutoff := utcNow().Add(-time.Duration(settings.HistoryRetentionDays) * 24 * time.Hour)
	for _, query := range []string{
		`DELETE c FROM np_source_claims c JOIN np_source_seats st ON st.source_id=c.source_id AND st.user_id=c.user_id WHERE st.state<>'active' AND st.ended_at<?`,
		`DELETE FROM np_source_seats WHERE state IN ('released','expired') AND ended_at<?`,
		`DELETE FROM np_source_events WHERE created_at<?`,
		`DELETE FROM np_subscription_archives WHERE archived_at<?`,
	} {
		if _, err = s.DB.ExecContext(ctx, query, cutoff); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) expireUserSeats(ctx context.Context, userID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM np_users WHERE id=? FOR UPDATE`, userID).Scan(&locked); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO np_source_events(source_id,owner_id,source_name,user_id,action,created_at) SELECT st.source_id,s.owner_id,s.name,st.user_id,'expired',st.expires_at FROM np_source_seats st JOIN np_sources s ON s.id=st.source_id WHERE st.user_id=? AND st.state='active' AND st.expires_at<=UTC_TIMESTAMP(6)`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE np_source_seats SET state='expired',ended_at=expires_at WHERE user_id=? AND state='active' AND expires_at<=UTC_TIMESTAMP(6)`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LevelDistribution(ctx context.Context) (map[string]any, error) {
	counts := make([]int, 7)
	var total int
	rows, err := s.DB.QueryContext(ctx, `SELECT xp,COUNT(*) FROM np_users WHERE enabled=1 AND role='user' GROUP BY xp`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var xp int64
		var count int
		if err = rows.Scan(&xp, &count); err != nil {
			return nil, err
		}
		counts[LevelForXP(xp)] += count
		total += count
	}
	return map[string]any{"total": total, "counts": counts, "thresholds": LevelThresholds, "names": LevelNames}, rows.Err()
}
