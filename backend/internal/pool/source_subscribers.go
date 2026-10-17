package pool

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

func newProbeUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
func updateSubscriptionSelection(ctx context.Context, tx *sql.Tx, id int64, nodes []Selection, sources, follows []int64) error {
	raw, _ := json.Marshal(nodes)
	src, _ := json.Marshal(sources)
	fol, _ := json.Marshal(follows)
	_, err := tx.ExecContext(ctx, `UPDATE np_subscriptions SET nodes=?,source_ids=?,follow_ids=?,updated_at=? WHERE id=?`, raw, src, fol, utcNow(), id)
	return err
}

type SourceSubscriber struct {
	UserID            int64      `json:"user_id"`
	Username          string     `json:"username"`
	DisplayName       string     `json:"display_name"`
	AvatarVersion     int        `json:"avatar_version"`
	Title             string     `json:"title"`
	Level             int        `json:"level"`
	XP                int64      `json:"xp"`
	Enabled           bool       `json:"enabled"`
	State             string     `json:"state"`
	ClaimedAt         time.Time  `json:"claimed_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	LastPullAt        *time.Time `json:"last_pull_at"`
	LastRenewedAt     *time.Time `json:"last_renewed_at"`
	RenewMethod       string     `json:"renew_method"`
	EndedAt           *time.Time `json:"ended_at"`
	NodeCount         int        `json:"node_count"`
	SubscriptionCount int        `json:"subscription_count"`
}

func (s *Store) SourceSubscribers(ctx context.Context, id int64, state string, page, size int) (map[string]any, error) {
	if err := s.SourcePermission(ctx, id); err != nil {
		return nil, err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	page, size = pagination(page, size)
	where := `st.source_id=? AND (st.state='kicked' OR (st.state='active' AND st.expires_at>UTC_TIMESTAMP(6)) OR COALESCE(st.ended_at,st.expires_at)>=?)`
	args := []any{id, utcNow().Add(-time.Duration(settings.HistoryRetentionDays) * 24 * time.Hour)}
	if state == "current" {
		where += ` AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6)`
	} else if state == "history" {
		where += ` AND (st.state<>'active' OR st.expires_at<=UTC_TIMESTAMP(6))`
	}
	var total int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_seats st WHERE `+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT st.user_id,u.username,u.display_name,u.avatar_version,u.title,u.xp,u.enabled,st.state,st.claimed_at,st.expires_at,st.last_pull_at,st.last_renewed_at,st.renew_method,st.ended_at FROM np_source_seats st JOIN np_users u ON u.id=st.user_id WHERE `+where+` ORDER BY st.claimed_at DESC,st.user_id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	items := []SourceSubscriber{}
	for rows.Next() {
		var v SourceSubscriber
		if err = rows.Scan(&v.UserID, &v.Username, &v.DisplayName, &v.AvatarVersion, &v.Title, &v.XP, &v.Enabled, &v.State, &v.ClaimedAt, &v.ExpiresAt, &v.LastPullAt, &v.LastRenewedAt, &v.RenewMethod, &v.EndedAt); err != nil {
			rows.Close()
			return nil, err
		}
		v.Level = LevelForXP(v.XP)
		if v.State == "active" && !v.ExpiresAt.After(utcNow()) {
			v.State = "expired"
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range items {
		subscriptions, err := listSubscriptions(ctx, s.DB, items[i].UserID)
		if err != nil {
			return nil, err
		}
		nodes := map[int64]bool{}
		for _, sub := range subscriptions {
			references := false
			for _, pair := range sub.Nodes {
				if pair.SourceID == id {
					references = true
					nodes[pair.NodeID] = true
				}
			}
			if references {
				items[i].SubscriptionCount++
			}
		}
		items[i].NodeCount = len(nodes)
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size, "history_retention_days": settings.HistoryRetentionDays}, nil
}
func (s *Store) SetSourceSubscriberBlocked(ctx context.Context, sourceID, userID int64, blocked bool) error {
	if err := s.SourcePermission(ctx, sourceID); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=? FOR UPDATE`, userID).Scan(&enabled); err != nil {
		return err
	}
	var owner int64
	if err = tx.QueryRowContext(ctx, `SELECT owner_id FROM np_sources WHERE id=? FOR UPDATE`, sourceID).Scan(&owner); err != nil {
		return err
	}
	if CurrentUser(ctx).Role != "admin" && owner != CurrentUser(ctx).ID {
		return ErrForbidden
	}
	if owner == userID {
		return fmt.Errorf("不能踢出来源所有者")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_seats WHERE source_id=? AND user_id=?`, sourceID, userID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	action := "unblocked"
	state := "released"
	if blocked {
		action = "kicked"
		state = "kicked"
		if err = removeSubscriptionSources(ctx, tx, userID, map[int64]bool{sourceID: true}); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE np_source_seats SET state=?,ended_at=? WHERE source_id=? AND user_id=?`, state, utcNow(), sourceID, userID); err != nil {
		return err
	}
	if err = recordSeatEvent(ctx, tx, sourceID, userID, action, utcNow()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RotateSourceLink(ctx context.Context, id int64) (string, error) {
	if err := s.SourcePermission(ctx, id); err != nil {
		return "", err
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE np_sources SET share_token=? WHERE id=?`, token, id)
	return "/link/" + token, err
}
func (s *Store) ExportSourceLink(ctx context.Context, token, format string) ([]byte, int, int, *SubscriptionUsage, error) {
	if !validLinkToken(token) {
		return nil, 0, 0, nil, ErrNotFound
	}
	src, err := scanSource(s.DB.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM np_sources WHERE share_token=? AND enabled=1 AND EXISTS(SELECT 1 FROM np_users u WHERE u.id=np_sources.owner_id AND u.enabled=1)`, token))
	if err != nil {
		return nil, 0, 0, nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+nodeColumns+` FROM np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id WHERE sn.source_id=? AND sn.excluded=0 AND n.blocked=0 ORDER BY n.id`, src.ID)
	if err != nil {
		return nil, 0, 0, nil, err
	}
	nodes := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			rows.Close()
			return nil, 0, 0, nil, err
		}
		n.Sources = []Origin{{ID: src.ID, Name: src.Name, NodeName: n.Name}}
		nodes = append(nodes, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, 0, nil, err
	}
	profile := DefaultRuleProfile()
	raw, skipped, routing, err := ExportSubscriptionWithProfile(nodes, format, &profile, []Source{src})
	return raw, skipped, routing, src.SubscriptionUsage, err
}
func (s *Store) SubscriptionDeliveryInfo(ctx context.Context, token string) (User, error) {
	sub, err := scanSubscription(s.DB.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE token=?`, token))
	if err != nil {
		return User{}, err
	}
	u, err := s.User(ctx, sub.UserID)
	if err != nil {
		return u, err
	}
	if !u.Enabled {
		return u, ErrForbidden
	}
	if u.ProbeUUID == "" && u.SubscriptionExpiresAt != nil && u.SubscriptionExpiresAt.After(utcNow()) {
		uuid, err := newProbeUUID()
		if err != nil {
			return u, err
		}
		if _, err = s.DB.ExecContext(ctx, `UPDATE np_users SET probe_uuid=? WHERE id=? AND probe_uuid IS NULL`, uuid, u.ID); err != nil {
			return u, err
		}
		u, err = s.User(ctx, u.ID)
		if err != nil {
			return u, err
		}
	}
	return u, nil
}
func (s *Store) TouchSubscriptionPull(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE np_source_seats st JOIN np_subscriptions sub ON sub.user_id=st.user_id SET st.last_pull_at=UTC_TIMESTAMP(6) WHERE sub.token=? AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6) AND JSON_CONTAINS(sub.source_ids,CAST(st.source_id AS CHAR))`, token)
	return err
}
func (s *Store) SubscriptionHistory(ctx context.Context, page, size int) (map[string]any, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	page, size = pagination(page, size)
	cutoff := utcNow().Add(-time.Duration(settings.HistoryRetentionDays) * 24 * time.Hour)
	var total int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_events WHERE user_id=? AND created_at>=?`, CurrentUser(ctx).ID, cutoff).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT source_id,source_name,action,created_at FROM np_source_events WHERE user_id=? AND created_at>=? ORDER BY id DESC LIMIT ? OFFSET ?`, CurrentUser(ctx).ID, cutoff, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, action string
		var at time.Time
		if err = rows.Scan(&id, &name, &action, &at); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"source_id": id, "source_name": name, "action": action, "created_at": at})
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size, "history_retention_days": settings.HistoryRetentionDays}, rows.Err()
}

// Stable order is shared by subscription activation and claim operations.
func sortedSourceIDs(nodes []Selection) []int64 {
	set := map[int64]bool{}
	for _, n := range nodes {
		set[n.SourceID] = true
	}
	ids := []int64{}
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
