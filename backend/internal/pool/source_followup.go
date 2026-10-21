package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// QueueStep 来源刷新后同一来源相邻节点的检测间隔，使同一提供方大致逐个检测
const QueueStep = 3 * time.Second

// SourceRecheckAfter 来源刷新时，距上次检测超过该时长的节点重新排队；
// 多个来源共有的节点因此每个周期只测一次
const SourceRecheckAfter = Period * 3 / 4

type txQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func sourceNodeNames(ctx context.Context, q txQueryer, sourceID int64) (map[int64]string, error) {
	rows, e := q.QueryContext(ctx, `SELECT node_id,node_name FROM np_source_nodes WHERE source_id=?`, sourceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if e = rows.Scan(&id, &name); e != nil {
			return nil, e
		}
		out[id] = name
	}
	return out, rows.Err()
}

// sourceVisibleNodeNames 取该来源当前参与展示的 (节点, 名称)，
// 用于判断一次刷新后内存索引的内容是否真的变了。
func sourceVisibleNodeNames(ctx context.Context, q txQueryer, sourceID int64) (map[int64]string, error) {
	rows, e := q.QueryContext(ctx, `SELECT node_id,node_name FROM np_source_nodes WHERE source_id=? AND excluded=0`, sourceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if e = rows.Scan(&id, &name); e != nil {
			return nil, e
		}
		out[id] = name
	}
	return out, rows.Err()
}

// sameLinks 比较两次快照的 (节点, 名称) 集合是否完全一致。
func sameLinks(before, after map[int64]string) bool {
	if len(before) != len(after) {
		return false
	}
	for id, name := range before {
		if got, ok := after[id]; !ok || got != name {
			return false
		}
	}
	return true
}

// QueueSourceNodes 来源刷新后接着检测它的节点：新节点优先，按固定间隔错开。
// 已到分级间隔的才排队；国内用户正在实测能连的健康节点跳过。
func (s *Store) QueueSourceNodes(ctx context.Context, sourceID int64, now time.Time) error {
	rows, e := s.DB.QueryContext(ctx, `SELECT n.id,n.status,n.last_checked_at FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id
WHERE sn.source_id=? AND sn.excluded=0 AND n.blocked=0 AND n.pending=0 AND (n.last_checked_at IS NULL OR n.last_checked_at<?)
ORDER BY n.last_checked_at IS NOT NULL, n.id`, sourceID, now.Add(-SourceRecheckAfter))
	if e != nil {
		return e
	}
	type candidate struct {
		id      int64
		status  string
		checked sql.NullTime
	}
	list := []candidate{}
	for rows.Next() {
		var c candidate
		if e = rows.Scan(&c.id, &c.status, &c.checked); e != nil {
			rows.Close()
			return e
		}
		list = append(list, c)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	ids := []int64{}
	for _, c := range list {
		if c.checked.Valid {
			if now.Sub(c.checked.Time) < s.ProbeInterval(c.id)*3/4 {
				continue
			}
			if c.status == "healthy" && s.FieldTested(ctx, c.id) {
				continue
			}
		}
		ids = append(ids, c.id)
	}
	if len(ids) == 0 {
		return nil
	}
	// 超大来源压缩间隔，保证在四分之一周期内排完
	step := QueueStep
	if span := Period / 4; time.Duration(len(ids))*step > span {
		step = span / time.Duration(len(ids))
	}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		var cases, in strings.Builder
		args := make([]any, 0, (end-start)*3)
		for i := start; i < end; i++ {
			cases.WriteString(" WHEN ? THEN ?")
			args = append(args, ids[i], now.Add(time.Duration(i)*step))
		}
		for i := start; i < end; i++ {
			if i > start {
				in.WriteByte(',')
			}
			in.WriteByte('?')
			args = append(args, ids[i])
		}
		if _, e = s.DB.ExecContext(ctx, `UPDATE np_nodes SET next_check_at=CASE id`+cases.String()+` END WHERE id IN (`+in.String()+`)`, args...); e != nil {
			return e
		}
	}
	return nil
}

// RebindSelections 订阅里选中的节点在来源刷新后消失时，换成该来源中同名的节点
func (s *Store) RebindSelections(ctx context.Context, sourceID int64, oldNames map[int64]string) error {
	current, e := sourceNodeNames(ctx, s.DB, sourceID)
	if e != nil {
		return e
	}
	excluded := map[int64]bool{}
	rows, e := s.DB.QueryContext(ctx, `SELECT node_id FROM np_source_nodes WHERE source_id=? AND excluded=1`, sourceID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		excluded[id] = true
	}
	rows.Close()
	byName := map[string][]int64{}
	for id, name := range current {
		if !excluded[id] {
			byName[name] = append(byName[name], id)
		}
	}
	for _, ids := range byName {
		slices.Sort(ids)
	}
	rows, e = s.DB.QueryContext(ctx, `SELECT id FROM np_subscriptions WHERE JSON_CONTAINS(nodes,JSON_OBJECT('source_id',?))`, sourceID)
	if e != nil {
		return e
	}
	subs := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		subs = append(subs, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	nameOf := func(id int64) string {
		if name, ok := oldNames[id]; ok {
			return name
		}
		// 早先刷新已消失的节点，用节点表里保留的名称
		var name string
		if s.DB.QueryRowContext(ctx, `SELECT name FROM np_nodes WHERE id=?`, id).Scan(&name) == nil {
			oldNames[id] = name
		}
		return name
	}
	for _, id := range subs {
		if e = s.rebindOne(ctx, id, sourceID, current, excluded, byName, nameOf); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) rebindOne(ctx context.Context, subID, sourceID int64, current map[int64]string, excluded map[int64]bool, byName map[string][]int64, nameOf func(int64) string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw []byte
	if e = tx.QueryRowContext(ctx, `SELECT nodes FROM np_subscriptions WHERE id=? FOR UPDATE`, subID).Scan(&raw); e != nil {
		if e == sql.ErrNoRows {
			return nil
		}
		return e
	}
	var list []Selection
	if e = json.Unmarshal(raw, &list); e != nil {
		return e
	}
	alive := func(id int64) bool { _, ok := current[id]; return ok && !excluded[id] }
	used := map[int64]bool{}
	for _, v := range list {
		if v.SourceID == sourceID && alive(v.NodeID) {
			used[v.NodeID] = true
		}
	}
	changed := false
	for i, v := range list {
		if v.SourceID != sourceID || alive(v.NodeID) {
			continue
		}
		name := nameOf(v.NodeID)
		if name == "" {
			continue
		}
		for _, c := range byName[name] {
			if !used[c] {
				list[i].NodeID = c
				used[c] = true
				changed = true
				break
			}
		}
	}
	if !changed {
		return nil
	}
	b, e := json.Marshal(list)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE np_subscriptions SET nodes=? WHERE id=?`, b, subID); e != nil {
		return e
	}
	return tx.Commit()
}
