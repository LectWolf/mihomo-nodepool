package pool

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

type Selection struct {
	NodeID   int64 `json:"node_id"`
	SourceID int64 `json:"source_id"`
}
type SubscriptionInput struct {
	Name           string      `json:"name"`
	Nodes          []Selection `json:"nodes"`
	SourceIDs      []int64     `json:"source_ids"`
	RuleProfileID  *int64      `json:"rule_profile_id"`
	RuleProfileSet bool        `json:"-"`
}
type Subscription struct {
	ID            int64                    `json:"id"`
	Name          string                   `json:"name"`
	NodeCount     int                      `json:"node_count"`
	SourceCount   int                      `json:"source_count"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
	URL           string                   `json:"url"`
	Nodes         []Selection              `json:"nodes"`
	NodeIDs       []int64                  `json:"node_ids"`
	SourceIDs     []int64                  `json:"source_ids"`
	UserID        int64                    `json:"-"`
	Token         string                   `json:"-"`
	RuleProfileID *int64                   `json:"rule_profile_id"`
	NodeDetails   []SubscriptionNodeDetail `json:"node_details,omitempty"`
}
type SavedSubscription struct {
	Subscription  Subscription `json:"subscription"`
	Truncated     bool         `json:"truncated"`
	SelectedCount int          `json:"selected_count"`
}

const subscriptionColumns = `id,user_id,name,token,nodes,source_ids,created_at,updated_at,rule_profile_id`

func newToken() (string, error) {
	var b [32]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func scanSubscription(row scanner) (Subscription, error) {
	v := Subscription{Nodes: []Selection{}, NodeIDs: []int64{}, SourceIDs: []int64{}}
	var nodes, sources []byte
	e := row.Scan(&v.ID, &v.UserID, &v.Name, &v.Token, &nodes, &sources, &v.CreatedAt, &v.UpdatedAt, &v.RuleProfileID)
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal(nodes, &v.Nodes); e != nil {
		return v, e
	}
	if e = json.Unmarshal(sources, &v.SourceIDs); e != nil {
		return v, e
	}
	for _, n := range v.Nodes {
		v.NodeIDs = append(v.NodeIDs, n.NodeID)
	}
	v.NodeCount = len(v.NodeIDs)
	v.SourceCount = len(v.SourceIDs)
	v.URL = "/sub/" + v.Token
	return v, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listSubscriptions(ctx context.Context, q queryer, userID int64) ([]Subscription, error) {
	rows, e := q.QueryContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE user_id=? ORDER BY id`, userID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []Subscription{}
	for rows.Next() {
		v, e := scanSubscription(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (s *Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
	items, err := listSubscriptions(ctx, s.DB, CurrentUser(ctx).ID)
	if err == nil {
		err = s.subscriptionDetails(ctx, items)
	}
	return items, err
}
func (s *Store) Usage(ctx context.Context, id int64) (Usage, error) {
	items, e := listSubscriptions(ctx, s.DB, id)
	if e != nil {
		return Usage{}, e
	}
	nodes, sources := map[int64]bool{}, map[int64]bool{}
	for _, v := range items {
		for _, n := range v.Nodes {
			nodes[n.NodeID] = true
			sources[n.SourceID] = true
		}
	}
	return Usage{len(nodes), len(sources), len(items)}, nil
}
func (s *Store) SaveSubscription(ctx context.Context, id int64, in SubscriptionInput) (SavedSubscription, error) {
	var out SavedSubscription
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		return out, errors.New("订阅名称不能为空且最多200字节")
	}
	if len(in.Nodes) > 100000 || len(in.SourceIDs) > 10000 {
		return out, errors.New("选择条目过多")
	}
	if in.RuleProfileID != nil && *in.RuleProfileID < 0 {
		return out, errors.New("规则配置ID必须为0（官方默认规则）、正整数（私人规则）或null（不绑定）")
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	u, e := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=? FOR UPDATE`, CurrentUser(ctx).ID))
	if e != nil {
		return out, e
	}
	if !u.Enabled {
		return out, ErrForbidden
	}
	items, e := listSubscriptions(ctx, tx, u.ID)
	if e != nil {
		return out, e
	}
	usedNodes, usedSources := map[int64]bool{}, map[int64]bool{}
	var existing *Subscription
	for i := range items {
		v := &items[i]
		if v.ID == id {
			existing = v
			continue
		}
		for _, n := range v.Nodes {
			usedNodes[n.NodeID] = true
			usedSources[n.SourceID] = true
		}
	}
	if id > 0 && existing == nil {
		return out, ErrNotFound
	}
	if !in.RuleProfileSet && in.RuleProfileID == nil {
		if existing != nil {
			in.RuleProfileID = existing.RuleProfileID
		} else {
			officialID := int64(0)
			in.RuleProfileID = &officialID
		}
	}
	if in.RuleProfileID != nil && *in.RuleProfileID > 0 {
		var found int64
		if e = tx.QueryRowContext(ctx, `SELECT id FROM np_rule_profiles WHERE id=? AND user_id=? FOR UPDATE`, *in.RuleProfileID, u.ID).Scan(&found); e != nil {
			return out, e
		}
	}
	if (id == 0 && len(items) >= u.SubscriptionLimit) || (id > 0 && len(items) > u.SubscriptionLimit) {
		return out, ErrQuota
	}
	if len(usedNodes) > u.NodeLimit || len(usedSources) > u.SourceLimit {
		return out, ErrQuota
	}
	allSources := map[int64]bool{}
	for _, n := range in.Nodes {
		if n.NodeID <= 0 || n.SourceID <= 0 {
			return out, errors.New("节点及来源ID无效")
		}
		allSources[n.SourceID] = true
	}
	for _, sid := range in.SourceIDs {
		if sid <= 0 {
			return out, errors.New("来源ID无效")
		}
		allSources[sid] = true
	}
	sourceOrder := make([]int64, 0, len(allSources))
	for sid := range allSources {
		sourceOrder = append(sourceOrder, sid)
	}
	sort.Slice(sourceOrder, func(i, j int) bool { return sourceOrder[i] < sourceOrder[j] })
	maxUsers := map[int64]int{}
	for _, sid := range sourceOrder {
		var enabled, ownerEnabled bool
		var owner int64
		var max int
		e = tx.QueryRowContext(ctx, `SELECT owner_id,enabled,max_users FROM np_sources WHERE id=? FOR UPDATE`, sid).Scan(&owner, &enabled, &max)
		if e != nil {
			return out, e
		}
		if e = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=?`, owner).Scan(&ownerEnabled); e != nil {
			return out, e
		}
		if !enabled || !ownerEnabled {
			return out, ErrForbidden
		}
		maxUsers[sid] = max
	}
	chosen := []Selection{}
	seen := map[int64]bool{}
	add := func(n Selection, explicit bool) error {
		if seen[n.NodeID] {
			return nil
		}
		newSource := !usedSources[n.SourceID]
		newNode := !usedNodes[n.NodeID]
		if (newSource && len(usedSources) >= u.SourceLimit) || (newNode && len(usedNodes) >= u.NodeLimit) {
			if explicit {
				return ErrQuota
			}
			out.Truncated = true
			return nil
		}
		seen[n.NodeID] = true
		usedNodes[n.NodeID] = true
		usedSources[n.SourceID] = true
		chosen = append(chosen, n)
		return nil
	}
	for _, n := range in.Nodes {
		var valid int
		e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=? AND sn.node_id=? AND sn.excluded=0 AND n.blocked=0`, n.SourceID, n.NodeID).Scan(&valid)
		if e != nil {
			return out, e
		}
		if valid == 0 {
			return out, errors.New("所选节点不属于该来源或已不可用")
		}
		if e = add(n, true); e != nil {
			return out, e
		}
	}
	full := map[int64]bool{}
	for _, sid := range in.SourceIDs {
		full[sid] = true
	}
	// Merge entire-source candidates globally by node ID, independent of the input source order.
	candidates := []Selection{}
	for _, sid := range sourceOrder {
		if !full[sid] {
			continue
		}
		rows, e := tx.QueryContext(ctx, `SELECT sn.node_id FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=? AND sn.excluded=0 AND n.blocked=0 ORDER BY sn.node_id`, sid)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var nid int64
			if e = rows.Scan(&nid); e != nil {
				rows.Close()
				return out, e
			}
			candidates = append(candidates, Selection{nid, sid})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].NodeID == candidates[j].NodeID {
			return candidates[i].SourceID < candidates[j].SourceID
		}
		return candidates[i].NodeID < candidates[j].NodeID
	})
	for _, n := range candidates {
		if e = add(n, false); e != nil {
			return out, e
		}
	}
	if len(chosen) == 0 {
		return out, errors.New("请至少选择一个可用节点")
	}
	selectedSources := map[int64]bool{}
	for _, n := range chosen {
		selectedSources[n.SourceID] = true
	}
	sources := []int64{}
	for _, sid := range sourceOrder {
		if !selectedSources[sid] {
			continue
		}
		sources = append(sources, sid)
		var claimed int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_claims WHERE source_id=? AND user_id=?`, sid, u.ID).Scan(&claimed); e != nil {
			return out, e
		}
		if claimed == 0 {
			var count int
			if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_claims WHERE source_id=?`, sid).Scan(&count); e != nil {
				return out, e
			}
			if maxUsers[sid] > 0 && count >= maxUsers[sid] {
				return out, ErrQuota
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO np_source_claims(source_id,user_id,created_at) VALUES(?,?,?)`, sid, u.ID, utcNow()); e != nil {
				return out, e
			}
		}
	}
	raw, _ := json.Marshal(chosen)
	srcRaw, _ := json.Marshal(sources)
	now := utcNow()
	if id == 0 {
		token, e := newToken()
		if e != nil {
			return out, e
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO np_subscriptions(user_id,name,token,nodes,source_ids,created_at,updated_at,rule_profile_id) VALUES(?,?,?,?,?,?,?,?)`, u.ID, in.Name, token, raw, srcRaw, now, now, in.RuleProfileID)
		if e != nil {
			return out, e
		}
		id, e = res.LastInsertId()
		if e != nil {
			return out, e
		}
	} else {
		if _, e = tx.ExecContext(ctx, `UPDATE np_subscriptions SET name=?,nodes=?,source_ids=?,updated_at=?,rule_profile_id=? WHERE id=? AND user_id=?`, in.Name, raw, srcRaw, now, in.RuleProfileID, id, u.ID); e != nil {
			return out, e
		}
	}
	out.Subscription, e = scanSubscription(tx.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE id=?`, id))
	if e != nil {
		return out, e
	}
	out.SelectedCount = len(chosen)
	return out, tx.Commit()
}
func (s *Store) MutateSubscription(ctx context.Context, id int64, rotate bool) (Subscription, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Subscription{}, e
	}
	defer tx.Rollback()
	var enabled bool
	if e = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=? FOR UPDATE`, CurrentUser(ctx).ID).Scan(&enabled); e != nil {
		return Subscription{}, e
	}
	if !enabled {
		return Subscription{}, ErrForbidden
	}
	v, e := scanSubscription(tx.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE id=? AND user_id=? FOR UPDATE`, id, CurrentUser(ctx).ID))
	if e != nil {
		return v, e
	}
	if rotate {
		v.Token, e = newToken()
		if e != nil {
			return v, e
		}
		v.URL = "/sub/" + v.Token
		v.UpdatedAt = utcNow()
		_, e = tx.ExecContext(ctx, `UPDATE np_subscriptions SET token=?,updated_at=? WHERE id=?`, v.Token, v.UpdatedAt, id)
	} else {
		_, e = tx.ExecContext(ctx, `DELETE FROM np_subscriptions WHERE id=?`, id)
	}
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}

// SubscriptionNodes checks current memberships and global account quotas while
// retaining the saved snapshot. Removed nodes are never silently substituted.
func (s *Store) SubscriptionNodes(ctx context.Context, token string) ([]Node, error) {
	if len(token) != 64 {
		return nil, ErrNotFound
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelReadCommitted})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	target, e := scanSubscription(tx.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE token=?`, token))
	if e != nil {
		return nil, e
	}
	u, e := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=?`, target.UserID))
	if e != nil {
		return nil, e
	}
	if !u.Enabled {
		return nil, ErrForbidden
	}
	items, e := listSubscriptions(ctx, tx, u.ID)
	if e != nil {
		return nil, e
	}
	seenNodes, seenSources := map[int64]bool{}, map[int64]bool{}
	out := []Node{}
	found := false
	for i, v := range items {
		if i >= u.SubscriptionLimit {
			break
		}
		if v.ID == target.ID && v.Token == token {
			found = true
		}
		for _, pair := range v.Nodes {
			n, e := scanNode(tx.QueryRowContext(ctx, `SELECT `+strings.Replace(nodeColumns, "n.name", "sn.node_name", 1)+` FROM np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id JOIN np_sources s ON s.id=sn.source_id JOIN np_users p ON p.id=s.owner_id JOIN np_source_claims c ON c.source_id=s.id AND c.user_id=? WHERE n.id=? AND s.id=? AND sn.excluded=0 AND n.blocked=0 AND s.enabled=1 AND p.enabled=1`, u.ID, pair.NodeID, pair.SourceID))
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return nil, e
			}
			if (!seenNodes[n.ID] && len(seenNodes) >= u.NodeLimit) || (!seenSources[pair.SourceID] && len(seenSources) >= u.SourceLimit) {
				continue
			}
			seenNodes[n.ID] = true
			seenSources[pair.SourceID] = true
			if v.ID == target.ID {
				n.Sources = []Origin{{ID: pair.SourceID, NodeName: n.Name}}
				out = append(out, n)
			}
		}
	}
	if !found {
		return nil, ErrForbidden
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return out, nil
}
