package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func archiveSubscription(ctx context.Context, tx *sql.Tx, item Subscription, reason string) error {
	nodes, _ := json.Marshal(item.Nodes)
	sources, _ := json.Marshal(item.SourceIDs)
	snapshots := []Subscription{item}
	if err := subscriptionDetailsFrom(ctx, tx, snapshots); err != nil {
		return err
	}
	details, err := json.Marshal(snapshots[0].NodeDetails)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO np_subscription_archives(user_id,subscription_id,name,nodes,source_ids,node_details,rule_profile_id,reason,archived_at) VALUES(?,?,?,?,?,?,?,?,?)`, item.UserID, item.ID, item.Name, nodes, sources, details, item.RuleProfileID, reason, utcNow())
	return err
}

type SubscriptionArchive struct {
	ID             int64                    `json:"id"`
	SubscriptionID int64                    `json:"subscription_id"`
	Name           string                   `json:"name"`
	Nodes          []Selection              `json:"nodes"`
	SourceIDs      []int64                  `json:"source_ids"`
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
	rows, err := s.DB.QueryContext(ctx, `SELECT id,subscription_id,name,nodes,source_ids,node_details,rule_profile_id,reason,archived_at FROM np_subscription_archives WHERE user_id=? AND archived_at>=? ORDER BY id DESC LIMIT ? OFFSET ?`, CurrentUser(ctx).ID, cutoff, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	items := []SubscriptionArchive{}
	for rows.Next() {
		var v SubscriptionArchive
		var nodes, sources, details []byte
		if err = rows.Scan(&v.ID, &v.SubscriptionID, &v.Name, &nodes, &sources, &details, &v.RuleProfileID, &v.Reason, &v.ArchivedAt); err != nil {
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
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size, "history_retention_days": settings.HistoryRetentionDays}, nil
}
