package pool

import (
	"context"
	"encoding/json"
	"time"
)

// 按使用情况分级的检测间隔：
//   - 激活订阅选中的节点、跟随来源中的节点：Period
//   - 来源有人占席位但节点没被选中：2×Period
//   - 没人使用：运行设置里的闲置间隔（默认 3 天，0 表示不定期检测）
//
// 节点从闲置变为有人使用时，按新间隔提前排队。
// 国内用户最近实测能连的健康节点暂停本机检测，见 FieldPause。
type probeTiers struct {
	at     time.Time
	used   map[int64]bool
	follow map[int64]bool
	seated map[int64]bool
	idle   time.Duration
	// active 为非闲置节点及其间隔，用于发现升级的节点
	active map[int64]time.Duration
}

const ProbeTiersTTL = 10 * time.Minute

// idleNever 表示不定期检测；新节点、手动复测和重新有人使用时仍会检测
const idleNever = 10 * 365 * 24 * time.Hour

func idleInterval(days int) time.Duration {
	if days <= 0 {
		return idleNever
	}
	return time.Duration(days) * 24 * time.Hour
}

// RefreshProbeTiers 汇总激活用户的订阅与席位，并把升级的节点提前排队
func (s *Store) RefreshProbeTiers(ctx context.Context) error {
	t := &probeTiers{at: time.Now(), used: map[int64]bool{}, follow: map[int64]bool{}, seated: map[int64]bool{}, idle: idleInterval(DefaultIdleCheckDays)}
	if v, e := s.Settings(ctx); e == nil {
		t.idle = idleInterval(v.IdleDays())
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT s.nodes,COALESCE(s.follow_ids,'[]') FROM np_subscriptions s JOIN np_users u ON u.id=s.user_id WHERE u.enabled=1 AND u.subscription_expires_at>UTC_TIMESTAMP(6)`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var nodes, follows []byte
		if e = rows.Scan(&nodes, &follows); e != nil {
			rows.Close()
			return e
		}
		var sel []Selection
		var ids []int64
		_ = json.Unmarshal(nodes, &sel)
		_ = json.Unmarshal(follows, &ids)
		for _, v := range sel {
			t.used[v.NodeID] = true
		}
		for _, id := range ids {
			t.follow[id] = true
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	rows, e = s.DB.QueryContext(ctx, `SELECT DISTINCT st.source_id FROM np_source_seats st JOIN np_users u ON u.id=st.user_id JOIN np_sources s ON s.id=st.source_id
WHERE st.state='active' AND st.expires_at>UTC_TIMESTAMP(6) AND u.enabled=1 AND u.subscription_expires_at>UTC_TIMESTAMP(6) AND st.user_id<>s.owner_id`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		t.seated[id] = true
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	ix, e := s.nodeIndex(ctx)
	if e != nil {
		return e
	}
	t.active = map[int64]time.Duration{}
	ix.mu.RLock()
	for id, n := range ix.byID {
		if d := t.interval(id, n); d != t.idle {
			t.active[id] = d
		}
	}
	ix.mu.RUnlock()
	prev := s.tiers.Load()
	s.tiers.Store(t)
	// 升级的节点：按新间隔从上次检测时间算起，已过期的立即排队
	groups := map[time.Duration][]int64{}
	for id, d := range t.active {
		if prev == nil || prev.active == nil {
			groups[d] = append(groups[d], id)
		} else if old, ok := prev.active[id]; !ok || d < old {
			groups[d] = append(groups[d], id)
		}
	}
	for d, ids := range groups {
		for start := 0; start < len(ids); start += 500 {
			end := min(start+500, len(ids))
			args := append([]any{int64((d + time.Hour) / time.Second)}, int64Args(ids[start:end])...)
			if _, e = s.DB.ExecContext(ctx, `UPDATE np_nodes SET next_check_at=LEAST(next_check_at,COALESCE(DATE_ADD(last_checked_at,INTERVAL ? SECOND),UTC_TIMESTAMP(6)))
WHERE pending=0 AND lease_until IS NULL AND id IN (`+placeholders(end-start)+`)`, args...); e != nil {
				return e
			}
		}
	}
	// 闲置间隔调短后，把排得过远的健康节点拉回来
	if t.idle < idleNever && (prev == nil || t.idle < prev.idle) {
		sec := int64((t.idle + time.Hour) / time.Second)
		if _, e = s.DB.ExecContext(ctx, `UPDATE np_nodes SET next_check_at=DATE_ADD(last_checked_at,INTERVAL ? SECOND)
WHERE status='healthy' AND pending=0 AND lease_until IS NULL AND last_checked_at IS NOT NULL AND next_check_at>DATE_ADD(last_checked_at,INTERVAL ? SECOND)`, sec, sec); e != nil {
			return e
		}
	}
	return nil
}

func (t *probeTiers) interval(id int64, n *indexNode) time.Duration {
	if t.used[id] {
		return Period
	}
	seated := false
	for _, l := range n.links {
		if t.follow[l.src.id] {
			return Period
		}
		seated = seated || t.seated[l.src.id]
	}
	if seated {
		return 2 * Period
	}
	return t.idle
}

// ProbeInterval 返回节点成功后的正常检测间隔；分级数据未就绪时按最高频率
func (s *Store) ProbeInterval(id int64) time.Duration {
	t := s.tiers.Load()
	if t == nil || t.used[id] {
		return Period
	}
	ix := &s.index
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	n := ix.byID[id]
	if n == nil {
		// 索引里还没有（刚入池），按正常频率
		return Period
	}
	return t.interval(id, n)
}

// IdleProbe 报告节点是否属于闲置档
func (s *Store) IdleProbe(id int64) bool {
	t := s.tiers.Load()
	return t != nil && s.ProbeInterval(id) == t.idle
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func (s *Store) fieldWindow(ctx context.Context) time.Duration {
	days := 3
	if v, e := s.Settings(ctx); e == nil && v.FieldTestDays > 0 {
		days = v.FieldTestDays
	}
	return time.Duration(days) * 24 * time.Hour
}
