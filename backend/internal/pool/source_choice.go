package pool

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync"
	"time"
)

// nodeHome 是某个节点在当前用户可用的一个来源
type nodeHome struct {
	SourceID int64
	Free     bool // 不限席位
	Held     bool // 用户已占有有效席位
	Room     bool // 有限席位仍有空位
}

// nodeHomes 查询节点在当前用户可用的全部来源，不限席位的排在前面
func nodeHomes(ctx context.Context, q queryer, u User, nodeIDs []int64) (map[int64][]nodeHome, error) {
	out := map[int64][]nodeHome{}
	ids := uniqueIDs(nodeIDs)
	for len(ids) > 0 {
		chunk := ids
		if len(chunk) > 500 {
			chunk = chunk[:500]
		}
		ids = ids[len(chunk):]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := []any{u.ID, u.ID}
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, u.Level, u.ID, u.Role, u.ID, u.ID)
		rows, err := q.QueryContext(ctx, `SELECT sn.node_id,s.id,s.max_users,
 COALESCE(st.state='active' AND st.expires_at>UTC_TIMESTAMP(6),0),
 (SELECT COUNT(*) FROM np_source_seats o JOIN np_users ou ON ou.id=o.user_id AND ou.enabled=1 WHERE o.source_id=s.id AND o.state='active' AND o.expires_at>UTC_TIMESTAMP(6) AND o.user_id<>?)
 FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id JOIN np_sources s ON s.id=sn.source_id JOIN np_users p ON p.id=s.owner_id
 LEFT JOIN np_source_seats st ON st.source_id=s.id AND st.user_id=?
 WHERE sn.node_id IN (`+marks+`) AND sn.excluded=0 AND n.blocked=0 AND s.enabled=1 AND p.enabled=1 AND (s.private=1 OR s.min_level<=? OR s.owner_id=? OR ?='admin') AND `+accessSQL("s")+` AND (st.state IS NULL OR st.state<>'kicked')
 ORDER BY sn.node_id, s.max_users<>0, s.id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var nid int64
			var h nodeHome
			var limit, occupied int
			if err = rows.Scan(&nid, &h.SourceID, &limit, &h.Held, &occupied); err != nil {
				rows.Close()
				return nil, err
			}
			h.Free = limit == 0
			h.Room = h.Free || occupied < limit
			out[nid] = append(out[nid], h)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// pickHome 选择节点的订阅来源：不限席位优先，其次沿用已占用的有限来源，最后才占用新的有限席位
func pickHome(homes []nodeHome, current int64, used map[int64]bool) (nodeHome, bool) {
	pick := func(match func(nodeHome) bool) (nodeHome, bool) {
		for _, h := range homes {
			if h.SourceID == current && match(h) {
				return h, true
			}
		}
		for _, h := range homes {
			if match(h) {
				return h, true
			}
		}
		return nodeHome{}, false
	}
	if h, ok := pick(func(h nodeHome) bool { return h.Free }); ok {
		return h, true
	}
	if h, ok := pick(func(h nodeHome) bool { return used[h.SourceID] || h.Held }); ok {
		return h, true
	}
	return pick(func(h nodeHome) bool { return h.Room })
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// rehomeUser 把订阅里来源失效的节点切换到其他可用来源，同时让有限席位的节点改用不限席位来源；
// 没有其他可用来源的节点原样保留，来源恢复后继续生效。调用方需已锁定用户行。
func rehomeUser(ctx context.Context, tx *sql.Tx, u User, c CommunitySettings) (bool, error) {
	if !u.Enabled || u.SubscriptionExpiresAt == nil || !u.SubscriptionExpiresAt.After(utcNow()) {
		return false, nil
	}
	limits, err := limitsFor(ctx, tx, u, c)
	if err != nil {
		return false, err
	}
	applyLimits(&u, limits)
	items, err := listSubscriptions(ctx, tx, u.ID)
	if err != nil {
		return false, err
	}
	nodeIDs := []int64{}
	sourceIDs := map[int64]bool{}
	for _, v := range items {
		for _, n := range v.Nodes {
			nodeIDs = append(nodeIDs, n.NodeID)
			sourceIDs[n.SourceID] = true
		}
		for _, sid := range v.FollowIDs {
			sourceIDs[sid] = true
		}
	}
	if len(nodeIDs) == 0 {
		return false, nil
	}
	homes, err := nodeHomes(ctx, tx, u, nodeIDs)
	if err != nil {
		return false, err
	}
	free, err := unlimitedSources(ctx, tx, sourceIDs)
	if err != nil {
		return false, err
	}
	for _, hs := range homes {
		for _, h := range hs {
			free[h.SourceID] = h.Free
		}
	}
	// 先计入跟随整个来源占用的来源，再按订阅顺序为节点挑选来源
	used := map[int64]bool{}
	for _, v := range items {
		for _, sid := range v.FollowIDs {
			used[sid] = true
		}
	}
	changed := false
	claims := map[int64]bool{}
	for _, v := range items {
		nodes := make([]Selection, 0, len(v.Nodes))
		moved := false
		for _, n := range v.Nodes {
			hs := homes[n.NodeID]
			valid := false
			for _, h := range hs {
				if h.SourceID == n.SourceID {
					valid = true
				}
			}
			h, ok := pickHome(hs, n.SourceID, used)
			if ok && h.SourceID != n.SourceID && !h.Free && !used[h.SourceID] && limitedCount(used, free) >= u.SourceLimit {
				ok = false
			}
			if ok && (h.SourceID != n.SourceID) && (h.Free || !valid) {
				n.SourceID = h.SourceID
				moved = true
				claims[h.SourceID] = true
			}
			used[n.SourceID] = true
			nodes = append(nodes, n)
		}
		if !moved {
			continue
		}
		changed = true
		set := map[int64]bool{}
		for _, n := range nodes {
			set[n.SourceID] = true
		}
		for _, sid := range v.FollowIDs {
			set[sid] = true
		}
		sources := make([]int64, 0, len(set))
		for sid := range set {
			sources = append(sources, sid)
		}
		sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
		if err = updateSubscriptionSelection(ctx, tx, v.ID, nodes, sources, v.FollowIDs); err != nil {
			return false, err
		}
	}
	if !changed {
		return false, nil
	}
	ids := make([]int64, 0, len(claims))
	for sid := range claims {
		ids = append(ids, sid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, sid := range ids {
		if err = claimSeat(ctx, tx, u, sid, *u.SubscriptionExpiresAt); err != nil {
			return false, err
		}
	}
	return true, releaseUnusedSeats(ctx, tx, u.ID)
}

// RehomeUser 在独立事务中为单个用户切换订阅来源
func (s *Store) RehomeUser(ctx context.Context, userID int64) (bool, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=? FOR UPDATE`, userID))
	if err != nil {
		return false, err
	}
	changed, err := rehomeUser(ctx, tx, u, settings.Community)
	if err != nil || !changed {
		return false, err
	}
	return true, tx.Commit()
}

// sourceUsers 返回订阅中引用了这些来源的用户
func sourceUsers(ctx context.Context, q queryer, sourceIDs []int64) ([]int64, error) {
	users := map[int64]bool{}
	for _, sid := range uniqueIDs(sourceIDs) {
		rows, err := q.QueryContext(ctx, `SELECT DISTINCT user_id FROM np_subscriptions WHERE JSON_CONTAINS(source_ids,CAST(? AS CHAR))`, sid)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			users[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	out := make([]int64, 0, len(users))
	for id := range users {
		out = append(out, id)
	}
	return uniqueIDs(out), nil
}

// rehomeUsers 来源下架、停用或删除后，为受影响的用户切换来源；失败不影响原操作
func (s *Store) rehomeUsers(ctx context.Context, userIDs []int64) {
	for _, id := range userIDs {
		_, _ = s.RehomeUser(ctx, id)
	}
}

func (s *Store) rehomeSources(ctx context.Context, sourceIDs ...int64) {
	users, err := sourceUsers(ctx, s.DB, sourceIDs)
	if err == nil {
		s.rehomeUsers(ctx, users)
	}
}

// 订阅拉取发现失效节点时按用户节流尝试切换，避免每次拉取都写库
var rehomeAttempts sync.Map

// 来源恢复或改为不限席位后清空节流，让失效订阅尽快切回
func resetRehome() { rehomeAttempts.Clear() }

func rehomeDue(userID int64) bool {
	now := time.Now()
	if v, ok := rehomeAttempts.Load(userID); ok && now.Sub(v.(time.Time)) < 5*time.Minute {
		return false
	}
	rehomeAttempts.Store(userID, now)
	return true
}
