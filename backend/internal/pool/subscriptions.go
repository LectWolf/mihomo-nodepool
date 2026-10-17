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
	FollowIDs      []int64     `json:"follow_source_ids"`
	RuleProfileID  *int64      `json:"rule_profile_id"`
	RuleProfileSet bool        `json:"-"`
}
type Subscription struct {
	ID            int64                `json:"id"`
	Name          string               `json:"name"`
	NodeCount     int                  `json:"node_count"`
	SourceCount   int                  `json:"source_count"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	URL           string               `json:"url"`
	Nodes         []Selection          `json:"nodes"`
	NodeIDs       []int64              `json:"node_ids"`
	SourceIDs     []int64              `json:"source_ids"`
	FollowIDs     []int64              `json:"follow_source_ids"`
	FollowDetails []FollowSourceDetail `json:"follow_details,omitempty"`
	// 跟随来源当前可用节点数之和，不计入节点额度
	FollowNodeCount int                      `json:"follow_node_count"`
	UserID          int64                    `json:"-"`
	Token           string                   `json:"-"`
	RuleProfileID   *int64                   `json:"rule_profile_id"`
	NodeDetails     []SubscriptionNodeDetail `json:"node_details,omitempty"`
}
type SavedSubscription struct {
	Subscription  Subscription `json:"subscription"`
	Truncated     bool         `json:"truncated"`
	SelectedCount int          `json:"selected_count"`
}

const subscriptionColumns = `id,user_id,name,token,nodes,source_ids,created_at,updated_at,rule_profile_id,COALESCE(follow_ids,'[]')`

// validLinkToken 接受 32 位（当前与旧版）或 64 位（早期版本）十六进制 token
func validLinkToken(token string) bool {
	if len(token) != 32 && len(token) != 64 {
		return false
	}
	for _, c := range token {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// newToken 生成 32 位十六进制链接 token（128 位随机数，与旧版订阅链接同长度）
func newToken() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func scanSubscription(row scanner) (Subscription, error) {
	v := Subscription{Nodes: []Selection{}, NodeIDs: []int64{}, SourceIDs: []int64{}, FollowIDs: []int64{}}
	var nodes, sources, follows []byte
	e := row.Scan(&v.ID, &v.UserID, &v.Name, &v.Token, &nodes, &sources, &v.CreatedAt, &v.UpdatedAt, &v.RuleProfileID, &follows)
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal(follows, &v.FollowIDs); e != nil {
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
		// 跟随整个来源不计节点额度，但占用来源额度
		for _, sid := range v.FollowIDs {
			sources[sid] = true
		}
	}
	free, e := unlimitedSources(ctx, s.DB, sources)
	if e != nil {
		return Usage{}, e
	}
	return Usage{len(nodes), limitedCount(sources, free), len(items)}, nil
}

// unlimitedSources 返回未设置使用人数上限的来源，这类来源不占用户的来源额度
func unlimitedSources(ctx context.Context, q execer, ids map[int64]bool) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	marks, args := make([]string, 0, len(ids)), make([]any, 0, len(ids))
	for id := range ids {
		marks = append(marks, "?")
		args = append(args, id)
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM np_sources WHERE max_users=0 AND id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func limitedCount(ids, free map[int64]bool) int {
	n := 0
	for id := range ids {
		if !free[id] {
			n++
		}
	}
	return n
}
func (s *Store) SaveSubscription(ctx context.Context, id int64, in SubscriptionInput) (SavedSubscription, error) {
	var out SavedSubscription
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		return out, errors.New("订阅名称不能为空且最多200字节")
	}
	if len(in.Nodes) > 100000 || len(in.SourceIDs) > 10000 || len(in.FollowIDs) > 1000 {
		return out, errors.New("选择条目过多")
	}
	if in.RuleProfileID != nil && *in.RuleProfileID < 0 {
		return out, errors.New("规则配置ID必须为0（官方默认规则）、正整数（私人规则）或null（不绑定）")
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		return out, e
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
	limits, e := limitsFor(ctx, tx, u, settings.Community)
	if e != nil {
		return out, e
	}
	applyLimits(&u, limits)
	if u.SubscriptionExpiresAt == nil || !u.SubscriptionExpiresAt.After(utcNow()) {
		return out, ErrSubscriptionInactive
	}
	items, e := listSubscriptions(ctx, tx, u.ID)
	if e != nil {
		return out, e
	}
	usedNodes, usedSources, currentFollows := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
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
		for _, sid := range v.FollowIDs {
			usedSources[sid] = true
			currentFollows[sid] = true
		}
	}
	if id > 0 && existing == nil {
		return out, ErrNotFound
	}
	current := map[int64]bool{}
	for nid := range usedNodes {
		current[nid] = true
	}
	if existing != nil {
		for _, n := range existing.Nodes {
			current[n.NodeID] = true
		}
		for _, sid := range existing.FollowIDs {
			currentFollows[sid] = true
		}
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
	allSources := map[int64]bool{}
	follows, followSet := []int64{}, map[int64]bool{}
	for _, sid := range in.FollowIDs {
		if sid <= 0 {
			return out, errors.New("来源ID无效")
		}
		if !followSet[sid] {
			followSet[sid] = true
			follows = append(follows, sid)
			allSources[sid] = true
		}
	}
	// 已跟随整个来源时，同一来源的单独节点与整源快照都不再需要
	nodesIn := make([]Selection, 0, len(in.Nodes))
	for _, n := range in.Nodes {
		if n.NodeID <= 0 || n.SourceID <= 0 {
			return out, errors.New("节点及来源ID无效")
		}
		if followSet[n.SourceID] {
			continue
		}
		allSources[n.SourceID] = true
		nodesIn = append(nodesIn, n)
	}
	in.Nodes = nodesIn
	sourcesIn := make([]int64, 0, len(in.SourceIDs))
	for _, sid := range in.SourceIDs {
		if sid <= 0 {
			return out, errors.New("来源ID无效")
		}
		if followSet[sid] {
			continue
		}
		allSources[sid] = true
		sourcesIn = append(sourcesIn, sid)
	}
	in.SourceIDs = sourcesIn
	known := map[int64]bool{}
	for sid := range usedSources {
		known[sid] = true
	}
	for sid := range allSources {
		known[sid] = true
	}
	free, e := unlimitedSources(ctx, tx, known)
	if e != nil {
		return out, e
	}
	if len(usedNodes) > u.NodeLimit || limitedCount(usedSources, free) > u.SourceLimit {
		return out, ErrQuota
	}
	limitedUsed := limitedCount(usedSources, free)
	sourceOrder := make([]int64, 0, len(allSources))
	for sid := range allSources {
		sourceOrder = append(sourceOrder, sid)
	}
	sort.Slice(sourceOrder, func(i, j int) bool { return sourceOrder[i] < sourceOrder[j] })
	for _, sid := range sourceOrder {
		var enabled, ownerEnabled bool
		var owner int64
		e = tx.QueryRowContext(ctx, `SELECT owner_id,enabled FROM np_sources WHERE id=? FOR UPDATE`, sid).Scan(&owner, &enabled)
		if e != nil {
			return out, e
		}
		if e = tx.QueryRowContext(ctx, `SELECT enabled FROM np_users WHERE id=?`, owner).Scan(&ownerEnabled); e != nil {
			return out, e
		}
		if !enabled || !ownerEnabled {
			return out, ErrForbidden
		}
	}
	for _, sid := range follows {
		if !usedSources[sid] && !free[sid] {
			if limitedUsed >= u.SourceLimit {
				return out, ErrQuota
			}
			limitedUsed++
		}
		usedSources[sid] = true
	}
	// 节点出现在多个来源时，按 pickHome 的优先级选择实际占用的来源
	homes := map[int64][]nodeHome{}
	loadHomes := func(list []Selection) error {
		ids := make([]int64, 0, len(list))
		for _, n := range list {
			ids = append(ids, n.NodeID)
		}
		found, e := nodeHomes(ctx, tx, u, ids)
		if e != nil {
			return e
		}
		for nid, hs := range found {
			homes[nid] = hs
			for _, h := range hs {
				free[h.SourceID] = h.Free
			}
		}
		return nil
	}
	if e = loadHomes(in.Nodes); e != nil {
		return out, e
	}
	chosen := []Selection{}
	seen := map[int64]bool{}
	add := func(n Selection, explicit bool) error {
		if seen[n.NodeID] {
			return nil
		}
		if h, ok := pickHome(homes[n.NodeID], n.SourceID, usedSources); ok {
			n.SourceID = h.SourceID
		}
		newSource := !usedSources[n.SourceID] && !free[n.SourceID]
		newNode := !usedNodes[n.NodeID]
		if (newSource && limitedUsed >= u.SourceLimit) || (newNode && len(usedNodes) >= u.NodeLimit) {
			if explicit {
				return ErrQuota
			}
			out.Truncated = true
			return nil
		}
		if newSource {
			limitedUsed++
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
	if e = loadHomes(candidates); e != nil {
		return out, e
	}
	for _, n := range candidates {
		if e = add(n, false); e != nil {
			return out, e
		}
	}
	if len(chosen) == 0 && len(follows) == 0 {
		return out, errors.New("请至少选择一个可用节点或来源")
	}
	now := utcNow()
	fresh, kept, e := checkExposure(ctx, tx, u, settings.Community, current, chosen, now)
	if e != nil {
		return out, e
	}
	freshSources, keptSources, e := checkSourceExposure(ctx, tx, u, settings.Community, currentFollows, follows, now)
	if e != nil {
		return out, e
	}
	if e = recordExposure(ctx, tx, u.ID, fresh, kept, now); e != nil {
		return out, e
	}
	if e = recordSourceExposure(ctx, tx, u.ID, freshSources, keptSources, now); e != nil {
		return out, e
	}
	selectedSources := map[int64]bool{}
	for _, n := range chosen {
		selectedSources[n.SourceID] = true
	}
	for _, sid := range follows {
		selectedSources[sid] = true
	}
	sources := make([]int64, 0, len(selectedSources))
	for sid := range selectedSources {
		sources = append(sources, sid)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	for _, sid := range sources {
		if e = claimSeat(ctx, tx, u, sid, *u.SubscriptionExpiresAt); e != nil {
			return out, e
		}
	}
	raw, _ := json.Marshal(chosen)
	srcRaw, _ := json.Marshal(sources)
	followRaw, _ := json.Marshal(follows)
	if id == 0 {
		token, e := newToken()
		if e != nil {
			return out, e
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO np_subscriptions(user_id,name,token,nodes,source_ids,follow_ids,created_at,updated_at,rule_profile_id) VALUES(?,?,?,?,?,?,?,?,?)`, u.ID, in.Name, token, raw, srcRaw, followRaw, now, now, in.RuleProfileID)
		if e != nil {
			return out, e
		}
		id, e = res.LastInsertId()
		if e != nil {
			return out, e
		}
		if e = achievement(ctx, tx, u.ID, "first_subscription", "成就：创建第一个订阅"); e != nil {
			return out, e
		}
	} else {
		if e = archiveSubscription(ctx, tx, *existing, "replaced"); e != nil {
			return out, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE np_subscriptions SET name=?,nodes=?,source_ids=?,follow_ids=?,updated_at=?,rule_profile_id=? WHERE id=? AND user_id=?`, in.Name, raw, srcRaw, followRaw, now, in.RuleProfileID, id, u.ID); e != nil {
			return out, e
		}
	}
	out.Subscription, e = scanSubscription(tx.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM np_subscriptions WHERE id=?`, id))
	if e != nil {
		return out, e
	}
	saved := []Subscription{out.Subscription}
	if e = followDetailsFrom(ctx, tx, saved); e != nil {
		return out, e
	}
	out.Subscription = saved[0]
	out.SelectedCount = len(chosen) + out.Subscription.FollowNodeCount
	if e = releaseUnusedSeats(ctx, tx, u.ID); e != nil {
		return out, e
	}
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
		if e = archiveSubscription(ctx, tx, v, "deleted"); e != nil {
			return v, e
		}
		_, e = tx.ExecContext(ctx, `DELETE FROM np_subscriptions WHERE id=?`, id)
	}
	if e != nil {
		return v, e
	}
	if !rotate {
		if e = releaseUnusedSeats(ctx, tx, v.UserID); e != nil {
			return v, e
		}
	}
	if rotate {
		if _, e = tx.ExecContext(ctx, `UPDATE np_users SET probe_uuid=NULL WHERE id=?`, v.UserID); e != nil {
			return v, e
		}
	}
	return v, tx.Commit()
}

// SubscriptionNodes checks current memberships and global account quotas while
// retaining the saved snapshot. Removed nodes are never silently substituted.
func (s *Store) SubscriptionNodes(ctx context.Context, token string) ([]Node, error) {
	if !validLinkToken(token) {
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
	if u.SubscriptionExpiresAt == nil || !u.SubscriptionExpiresAt.After(utcNow()) {
		return nil, ErrSubscriptionInactive
	}
	limits, e := limitsFor(ctx, tx, u, currentCommunity())
	if e != nil {
		return nil, e
	}
	applyLimits(&u, limits)
	items, e := listSubscriptions(ctx, tx, u.ID)
	if e != nil {
		return nil, e
	}
	seenNodes, seenSources := map[int64]bool{}, map[int64]bool{}
	free, freeKnown, limitedSeen := map[int64]bool{}, map[int64]bool{}, 0
	out := []Node{}
	outSeen := map[int64]bool{}
	found, stale := false, false
	for i, v := range items {
		if i >= u.SubscriptionLimit {
			break
		}
		if v.ID == target.ID && v.Token == token {
			found = true
		}
		for _, pair := range v.Nodes {
			n, e := scanNode(tx.QueryRowContext(ctx, `SELECT `+strings.Replace(nodeColumns, "n.name", "sn.node_name", 1)+` FROM np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id JOIN np_sources s ON s.id=sn.source_id JOIN np_users p ON p.id=s.owner_id JOIN np_source_seats c ON c.source_id=s.id AND c.user_id=? AND c.state='active' AND c.expires_at>UTC_TIMESTAMP(6) WHERE n.id=? AND s.id=? AND sn.excluded=0 AND n.blocked=0 AND s.enabled=1 AND p.enabled=1 AND (s.min_level<=? OR s.owner_id=? OR ?='admin')`, u.ID, pair.NodeID, pair.SourceID, u.Level, u.ID, u.Role))
			if errors.Is(e, sql.ErrNoRows) {
				stale = true
				continue
			}
			if e != nil {
				return nil, e
			}
			if !freeKnown[pair.SourceID] {
				var maxUsers int
				if e = tx.QueryRowContext(ctx, `SELECT max_users FROM np_sources WHERE id=?`, pair.SourceID).Scan(&maxUsers); e != nil {
					return nil, e
				}
				freeKnown[pair.SourceID] = true
				free[pair.SourceID] = maxUsers == 0
			}
			newSource := !seenSources[pair.SourceID] && !free[pair.SourceID]
			if (!seenNodes[n.ID] && len(seenNodes) >= u.NodeLimit) || (newSource && limitedSeen >= u.SourceLimit) {
				continue
			}
			if newSource {
				limitedSeen++
			}
			seenNodes[n.ID] = true
			seenSources[pair.SourceID] = true
			if v.ID == target.ID {
				n.Sources = []Origin{{ID: pair.SourceID, NodeName: n.Name}}
				out = append(out, n)
				outSeen[n.ID] = true
			}
		}
		// 跟随整个来源：实时输出该来源全部可用节点，不计节点额度，占来源额度
		for _, sid := range v.FollowIDs {
			var ok int
			e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_sources s JOIN np_users p ON p.id=s.owner_id JOIN np_source_seats c ON c.source_id=s.id AND c.user_id=? AND c.state='active' AND c.expires_at>UTC_TIMESTAMP(6) WHERE s.id=? AND s.enabled=1 AND p.enabled=1 AND (s.min_level<=? OR s.owner_id=? OR ?='admin')`, u.ID, sid, u.Level, u.ID, u.Role).Scan(&ok)
			if e != nil {
				return nil, e
			}
			if ok == 0 {
				continue
			}
			if !freeKnown[sid] {
				var maxUsers int
				if e = tx.QueryRowContext(ctx, `SELECT max_users FROM np_sources WHERE id=?`, sid).Scan(&maxUsers); e != nil {
					return nil, e
				}
				freeKnown[sid] = true
				free[sid] = maxUsers == 0
			}
			newSource := !seenSources[sid] && !free[sid]
			if newSource && limitedSeen >= u.SourceLimit {
				continue
			}
			if newSource {
				limitedSeen++
			}
			seenSources[sid] = true
			if v.ID != target.ID {
				continue
			}
			rows, e := tx.QueryContext(ctx, `SELECT `+strings.Replace(nodeColumns, "n.name", "sn.node_name", 1)+` FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=? AND sn.excluded=0 AND n.blocked=0 ORDER BY sn.node_id`, sid)
			if e != nil {
				return nil, e
			}
			for rows.Next() {
				n, e := scanNode(rows)
				if e != nil {
					rows.Close()
					return nil, e
				}
				if outSeen[n.ID] {
					continue
				}
				outSeen[n.ID] = true
				n.Sources = []Origin{{ID: sid, NodeName: n.Name}}
				out = append(out, n)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
		}
	}
	if !found {
		return nil, ErrForbidden
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	// 有节点的来源失效时尝试切换到其他来源，成功后重新生成
	if stale && rehomeDue(u.ID) {
		if changed, err := s.RehomeUser(ctx, u.ID); err == nil && changed {
			return s.SubscriptionNodes(ctx, token)
		}
	}
	ids := make([]int64, 0, len(out))
	for _, n := range out {
		ids = append(ids, n.ID)
	}
	// 多人反馈异常的节点保留但排到最后，客户端自动选择时会优先使用其它节点
	if demoted, err := s.demotedNodes(ctx, ids); err == nil && len(demoted) > 0 {
		sort.SliceStable(out, func(i, j int) bool { return !demoted[out[i].ID] && demoted[out[j].ID] })
	}
	return out, nil
}
