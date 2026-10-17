package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// SubscriptionNodeDetail contains display metadata only. Saved selections remain
// unchanged, including entries whose source membership has since disappeared.
type SubscriptionNodeDetail struct {
	NodeID            int64              `json:"node_id"`
	SourceID          int64              `json:"source_id"`
	Name              string             `json:"name"`
	SourceName        string             `json:"source_name"`
	Type              string             `json:"type"`
	Server            string             `json:"server"`
	Port              int                `json:"port"`
	Available         bool               `json:"available"`
	Insights          NodeInsights       `json:"insights"`
	MinLevel          int                `json:"min_level"`
	SubscriptionUsage *SubscriptionUsage `json:"subscription_usage,omitempty"`
	Demoted           bool               `json:"demoted,omitempty"`
	// MyFailed 当前用户的客户端实测连不上，订阅中已排到末尾
	MyFailed       bool   `json:"my_failed,omitempty"`
	ProviderID     int64  `json:"provider_id"`
	ProviderName   string `json:"provider_name"`
	ProviderAvatar int    `json:"provider_avatar"`
	ProviderTitle  string `json:"provider_title"`
}

func (s *Store) subscriptionDetails(ctx context.Context, items []Subscription) error {
	if err := subscriptionDetailsFrom(ctx, s.DB, items); err != nil {
		return err
	}
	ids := []int64{}
	for _, item := range items {
		for _, d := range item.NodeDetails {
			ids = append(ids, d.NodeID)
		}
	}
	demoted, err := s.demotedNodes(ctx, ids)
	if err != nil {
		return err
	}
	failed := map[int64]bool{}
	if len(items) > 0 && items[0].UserID > 0 {
		if failed, err = s.failedForUser(ctx, items[0].UserID, ids); err != nil {
			return err
		}
	}
	for i := range items {
		for j := range items[i].NodeDetails {
			id := items[i].NodeDetails[j].NodeID
			items[i].NodeDetails[j].Demoted = demoted[id]
			items[i].NodeDetails[j].MyFailed = failed[id]
		}
	}
	return nil
}

func subscriptionDetailsFrom(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, items []Subscription) error {
	pairs := []Selection{}
	seen := map[Selection]bool{}
	for _, item := range items {
		for _, pair := range item.Nodes {
			if !seen[pair] {
				seen[pair] = true
				pairs = append(pairs, pair)
			}
		}
	}
	found := map[Selection]SubscriptionNodeDetail{}
	for start := 0; start < len(pairs); start += 256 {
		end := min(start+256, len(pairs))
		placeholders := []string{}
		args := []any{}
		for _, pair := range pairs[start:end] {
			placeholders = append(placeholders, "(?,?)")
			args = append(args, pair.NodeID, pair.SourceID)
		}
		rows, err := db.QueryContext(ctx, `SELECT n.id,s.id,sn.node_name,s.name,n.type,n.server,n.port,(sn.excluded=0 AND n.blocked=0 AND s.enabled=1 AND u.enabled=1),n.config,s.min_level,s.subscription_usage,u.id,COALESCE(NULLIF(u.display_name,''),u.username),u.avatar_version,u.title FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id JOIN np_sources s ON s.id=sn.source_id JOIN np_users u ON u.id=s.owner_id WHERE (sn.node_id,sn.source_id) IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var detail SubscriptionNodeDetail
			var config, usage []byte
			if err = rows.Scan(&detail.NodeID, &detail.SourceID, &detail.Name, &detail.SourceName, &detail.Type, &detail.Server, &detail.Port, &detail.Available, &config, &detail.MinLevel, &usage, &detail.ProviderID, &detail.ProviderName, &detail.ProviderAvatar, &detail.ProviderTitle); err != nil {
				rows.Close()
				return err
			}
			if detail.Server, err = unseal(purposeNodeServer, detail.Server); err == nil {
				config, err = unsealConfig(config)
			}
			if err != nil {
				rows.Close()
				return err
			}
			detail.Insights = InspectNode(Node{Config: config})
			if len(usage) > 0 {
				if err = json.Unmarshal(usage, &detail.SubscriptionUsage); err != nil {
					rows.Close()
					return err
				}
			}
			refreshUsageStatus(detail.SubscriptionUsage)
			found[Selection{NodeID: detail.NodeID, SourceID: detail.SourceID}] = detail
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for i := range items {
		items[i].NodeDetails = []SubscriptionNodeDetail{}
		for _, pair := range items[i].Nodes {
			detail, ok := found[pair]
			if !ok {
				detail = SubscriptionNodeDetail{NodeID: pair.NodeID, SourceID: pair.SourceID, Name: fmt.Sprintf("节点 #%d", pair.NodeID), SourceName: fmt.Sprintf("来源 #%d", pair.SourceID)}
			}
			items[i].NodeDetails = append(items[i].NodeDetails, detail)
		}
	}
	return followDetailsFrom(ctx, db, items)
}
