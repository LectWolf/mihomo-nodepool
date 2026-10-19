package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func archiveSubscription(ctx context.Context, tx *sql.Tx, item Subscription, reason string) error {
	nodes, _ := json.Marshal(item.Nodes)
	sources, _ := json.Marshal(item.SourceIDs)
	follows, _ := json.Marshal(item.FollowIDs)
	snapshots := []Subscription{item}
	if err := subscriptionDetailsFrom(ctx, tx, snapshots); err != nil {
		return err
	}
	// 历史快照不保存服务器地址，避免绕过加密
	for i := range snapshots[0].NodeDetails {
		snapshots[0].NodeDetails[i].Server, snapshots[0].NodeDetails[i].Port = "", 0
	}
	details, err := json.Marshal(snapshots[0].NodeDetails)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO np_subscription_archives(user_id,subscription_id,name,nodes,source_ids,follow_ids,node_details,rule_profile_id,reason,archived_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, item.UserID, item.ID, item.Name, nodes, sources, follows, details, item.RuleProfileID, reason, utcNow()); err != nil {
		return err
	}
	// 每人只保留最近若干次历史组合
	var floor int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM np_subscription_archives WHERE user_id=? ORDER BY id DESC LIMIT 1 OFFSET ?`, item.UserID, SubscriptionArchiveKeep-1).Scan(&floor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM np_subscription_archives WHERE user_id=? AND id<?`, item.UserID, floor)
	return err
}

// SubscriptionArchiveKeep 每位用户保留的历史组合数量
const SubscriptionArchiveKeep = 10

type RestoredSubscription struct {
	SavedSubscription
	Skipped int `json:"skipped"`
}

// RestoreSubscriptionArchive 用历史组合新建订阅（target=0）或覆盖已有订阅，已失效的节点会被跳过
func (s *Store) RestoreSubscriptionArchive(ctx context.Context, archiveID, target int64) (RestoredSubscription, error) {
	var out RestoredSubscription
	defer s.subs.clear()
	user := CurrentUser(ctx)
	var name string
	var raw, followRaw []byte
	var rule *int64
	err := s.DB.QueryRowContext(ctx, `SELECT name,nodes,COALESCE(follow_ids,'[]'),rule_profile_id FROM np_subscription_archives WHERE id=? AND user_id=?`, archiveID, user.ID).Scan(&name, &raw, &followRaw, &rule)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	var nodes []Selection
	if err = json.Unmarshal(raw, &nodes); err != nil {
		return out, err
	}
	valid := make([]Selection, 0, len(nodes))
	for _, n := range nodes {
		var ok int
		if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id JOIN np_sources s ON s.id=sn.source_id JOIN np_users o ON o.id=s.owner_id WHERE sn.source_id=? AND sn.node_id=? AND sn.excluded=0 AND n.blocked=0 AND s.enabled=1 AND o.enabled=1`, n.SourceID, n.NodeID).Scan(&ok); err != nil {
			return out, err
		}
		if ok > 0 {
			valid = append(valid, n)
		}
	}
	var follows []int64
	if err = json.Unmarshal(followRaw, &follows); err != nil {
		return out, err
	}
	validFollows := make([]int64, 0, len(follows))
	for _, sid := range follows {
		var ok int
		if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_sources s JOIN np_users o ON o.id=s.owner_id WHERE s.id=? AND s.enabled=1 AND o.enabled=1`, sid).Scan(&ok); err != nil {
			return out, err
		}
		if ok > 0 {
			validFollows = append(validFollows, sid)
		}
	}
	out.Skipped = len(nodes) - len(valid) + len(follows) - len(validFollows)
	if len(valid) == 0 && len(validFollows) == 0 {
		return out, errors.New("该历史组合中的节点均已不可用")
	}
	if rule != nil && *rule > 0 {
		var found int
		if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_rule_profiles WHERE id=? AND user_id=?`, *rule, user.ID).Scan(&found); err != nil {
			return out, err
		}
		if found == 0 {
			official := int64(0)
			rule = &official
		}
	}
	saved, err := s.SaveSubscription(ctx, target, SubscriptionInput{Name: name, Nodes: valid, FollowIDs: validFollows, RuleProfileID: rule, RuleProfileSet: true})
	if err != nil {
		return out, err
	}
	out.SavedSubscription = saved
	return out, nil
}

type SubscriptionArchive struct {
	ID             int64                    `json:"id"`
	SubscriptionID int64                    `json:"subscription_id"`
	Name           string                   `json:"name"`
	Nodes          []Selection              `json:"nodes"`
	SourceIDs      []int64                  `json:"source_ids"`
	FollowIDs      []int64                  `json:"follow_source_ids"`
	FollowDetails  []FollowSourceDetail     `json:"follow_details"`
	NodeDetails    []SubscriptionNodeDetail `json:"node_details"`
	RuleProfileID  *int64                   `json:"rule_profile_id"`
	Reason         string                   `json:"reason"`
	ArchivedAt     time.Time                `json:"archived_at"`
}

func (s *Store) SubscriptionArchives(ctx context.Context, page, size int) (map[string]any, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	page, size = pagination(page, size)
	cutoff := utcNow().Add(-time.Duration(settings.HistoryRetentionDays) * 24 * time.Hour)
	var total int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_subscription_archives WHERE user_id=? AND archived_at>=?`, CurrentUser(ctx).ID, cutoff).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,subscription_id,name,nodes,source_ids,COALESCE(follow_ids,'[]'),node_details,rule_profile_id,reason,archived_at FROM np_subscription_archives WHERE user_id=? AND archived_at>=? ORDER BY id DESC LIMIT ? OFFSET ?`, CurrentUser(ctx).ID, cutoff, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	items := []SubscriptionArchive{}
	for rows.Next() {
		var v SubscriptionArchive
		var nodes, sources, follows, details []byte
		if err = rows.Scan(&v.ID, &v.SubscriptionID, &v.Name, &nodes, &sources, &follows, &details, &v.RuleProfileID, &v.Reason, &v.ArchivedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(nodes, &v.Nodes); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(sources, &v.SourceIDs); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(follows, &v.FollowIDs); err != nil {
			rows.Close()
			return nil, err
		}
		if len(details) > 0 {
			if err = json.Unmarshal(details, &v.NodeDetails); err != nil {
				rows.Close()
				return nil, err
			}
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].NodeDetails == nil {
			snapshots := []Subscription{{Nodes: items[i].Nodes}}
			if err = s.subscriptionDetails(ctx, snapshots); err != nil {
				return nil, err
			}
			items[i].NodeDetails = snapshots[0].NodeDetails
		}
	}
	follows := make([]Subscription, len(items))
	for i := range items {
		follows[i].FollowIDs = items[i].FollowIDs
	}
	if err = followDetailsFrom(ctx, s.DB, follows); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].FollowDetails = follows[i].FollowDetails
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size, "history_retention_days": settings.HistoryRetentionDays}, nil
}
