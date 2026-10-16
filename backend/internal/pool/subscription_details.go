package pool

import (
	"context"
	"fmt"
	"strings"
)

// SubscriptionNodeDetail contains display metadata only. Saved selections remain
// unchanged, including entries whose source membership has since disappeared.
type SubscriptionNodeDetail struct {
	NodeID     int64  `json:"node_id"`
	SourceID   int64  `json:"source_id"`
	Name       string `json:"name"`
	SourceName string `json:"source_name"`
	Type       string `json:"type"`
	Server     string `json:"server"`
	Port       int    `json:"port"`
	Available  bool   `json:"available"`
}

func (s *Store) subscriptionDetails(ctx context.Context, items []Subscription) error {
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
		rows, err := s.DB.QueryContext(ctx, `SELECT n.id,s.id,sn.node_name,s.name,n.type,n.server,n.port,(sn.excluded=0 AND n.blocked=0 AND s.enabled=1 AND u.enabled=1) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id JOIN np_sources s ON s.id=sn.source_id JOIN np_users u ON u.id=s.owner_id WHERE (sn.node_id,sn.source_id) IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var detail SubscriptionNodeDetail
			if err = rows.Scan(&detail.NodeID, &detail.SourceID, &detail.Name, &detail.SourceName, &detail.Type, &detail.Server, &detail.Port, &detail.Available); err != nil {
				rows.Close()
				return err
			}
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
	return nil
}
