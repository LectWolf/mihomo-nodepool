package pool

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 节点列表的内存索引：来源、节点、关联关系常驻内存，列表筛选排序不再逐行查库。
// 写操作调用 InvalidateIndex 后下次读取同步重建；检测结果直接就地更新；
// 另有 IndexTTL 兜底，覆盖迁移命令等进程外写入。
const (
	IndexTTL      = time.Minute
	fieldAggTTL   = 5 * time.Minute
	FieldRecent   = 24 * time.Hour // 最近一次国内回报为能连且在该时长内，本机暂停检测
	fieldGoodRate = 0.7
	fieldBadRate  = 0.3
)

type indexSource struct {
	id, owner             int64
	enabled, ownerEnabled bool
	private, drained      bool
	minLevel, maxUsers    int
}

type indexLink struct {
	src  *indexSource
	name string
}

type indexNode struct {
	id                                   int64
	name, typ, server, country, country2 string
	status                               string
	latency                              int // -1 表示无
	created                              time.Time
	blocked                              bool
	links                                []indexLink
}

type fieldAgg struct {
	FieldTest
	lastOK, lastFail time.Time
}

type nodeIndex struct {
	build   sync.Mutex
	mu      sync.RWMutex
	ready   bool
	dirty   atomic.Bool
	at      time.Time
	nodes   []*indexNode
	byID    map[int64]*indexNode
	sources map[int64]*indexSource

	fieldMu    sync.RWMutex
	fieldAt    time.Time
	fieldDirty atomic.Bool
	field      map[int64]fieldAgg
}

// InvalidateIndex 在来源、用户、关联等结构变化后调用
func (s *Store) InvalidateIndex() {
	s.index.dirty.Store(true)
	s.index.fieldDirty.Store(true)
}

func (s *Store) nodeIndex(ctx context.Context) (*nodeIndex, error) {
	ix := &s.index
	ix.mu.RLock()
	ok := ix.ready && !ix.dirty.Load() && time.Since(ix.at) < IndexTTL
	ix.mu.RUnlock()
	if !ok {
		ix.build.Lock()
		ix.mu.RLock()
		ok = ix.ready && !ix.dirty.Load() && time.Since(ix.at) < IndexTTL
		ix.mu.RUnlock()
		if !ok {
			if e := s.rebuildIndex(ctx); e != nil {
				ix.build.Unlock()
				return nil, e
			}
		}
		ix.build.Unlock()
	}
	if e := s.refreshFieldAgg(ctx, false); e != nil {
		return nil, e
	}
	return ix, nil
}

func (s *Store) rebuildIndex(ctx context.Context) error {
	ix := &s.index
	ix.dirty.Store(false)
	sources := map[int64]*indexSource{}
	rows, e := s.DB.QueryContext(ctx, `SELECT s.id,s.owner_id,s.enabled,u.enabled,s.private,`+drainedSQL("s")+`,s.min_level,s.max_users FROM np_sources s JOIN np_users u ON u.id=s.owner_id`)
	if e != nil {
		ix.dirty.Store(true)
		return e
	}
	for rows.Next() {
		v := &indexSource{}
		if e = rows.Scan(&v.id, &v.owner, &v.enabled, &v.ownerEnabled, &v.private, &v.drained, &v.minLevel, &v.maxUsers); e != nil {
			rows.Close()
			ix.dirty.Store(true)
			return e
		}
		sources[v.id] = v
	}
	rows.Close()
	plain := !DataEncryptionEnabled()
	byID := map[int64]*indexNode{}
	rows, e = s.DB.QueryContext(ctx, `SELECT id,name,type,IF(?,server,''),status,COALESCE(latency_ms,-1),country,country_code,created_at FROM np_nodes WHERE blocked=0`, plain)
	if e != nil {
		ix.dirty.Store(true)
		return e
	}
	for rows.Next() {
		v := &indexNode{}
		if e = rows.Scan(&v.id, &v.name, &v.typ, &v.server, &v.status, &v.latency, &v.country2, &v.country, &v.created); e != nil {
			rows.Close()
			ix.dirty.Store(true)
			return e
		}
		byID[v.id] = v
	}
	rows.Close()
	rows, e = s.DB.QueryContext(ctx, `SELECT node_id,source_id,node_name FROM np_source_nodes WHERE excluded=0`)
	if e != nil {
		ix.dirty.Store(true)
		return e
	}
	for rows.Next() {
		var nid, sid int64
		var name string
		if e = rows.Scan(&nid, &sid, &name); e != nil {
			rows.Close()
			ix.dirty.Store(true)
			return e
		}
		if n, src := byID[nid], sources[sid]; n != nil && src != nil {
			n.links = append(n.links, indexLink{src, name})
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		ix.dirty.Store(true)
		return e
	}
	nodes := make([]*indexNode, 0, len(byID))
	for id, n := range byID {
		if len(n.links) == 0 {
			delete(byID, id)
			continue
		}
		sort.Slice(n.links, func(i, j int) bool { return n.links[i].src.id < n.links[j].src.id })
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].id < nodes[j].id })
	ix.mu.Lock()
	ix.nodes, ix.byID, ix.sources, ix.at, ix.ready = nodes, byID, sources, time.Now(), true
	ix.mu.Unlock()
	return nil
}

// patchIndex 检测结果就地写入索引
func (s *Store) patchIndex(id int64, status string, latency *int, country, countryCode string, blocked bool) {
	ix := &s.index
	ix.mu.Lock()
	defer ix.mu.Unlock()
	n := ix.byID[id]
	if n == nil {
		return
	}
	n.status, n.blocked = status, blocked
	n.latency = -1
	if latency != nil {
		n.latency = *latency
	}
	if countryCode != "" {
		n.country, n.country2 = countryCode, country
	}
}

// refreshFieldAgg 汇总统计窗口内国内用户的实测结果，按用户去重
func (s *Store) refreshFieldAgg(ctx context.Context, force bool) error {
	ix := &s.index
	ix.fieldMu.RLock()
	fresh := ix.field != nil && time.Since(ix.fieldAt) < fieldAggTTL && !ix.fieldDirty.Load()
	ix.fieldMu.RUnlock()
	if fresh && !force {
		return nil
	}
	ix.fieldDirty.Store(false)
	now := utcNow()
	window := s.fieldWindow(ctx)
	// 连不上的时间取该批次首次回报时间（证明客户端当时在线）
	rows, e := s.DB.QueryContext(ctx, `SELECT node_id,SUM(ok),SUM(1-ok),MAX(last_ok),MAX(last_fail) FROM (
 SELECT c.node_id,b.user_id,MAX(c.ok_at IS NOT NULL) ok,MAX(c.ok_at) last_ok,MAX(CASE WHEN c.ok_at IS NULL THEN b.first_ok_at END) last_fail FROM np_beacon_checks c JOIN np_beacon_batches b ON b.id=c.batch_id
 WHERE b.region='CN' AND b.created_at>=? AND (c.ok_at IS NOT NULL OR b.first_ok_at<=?) GROUP BY c.node_id,b.user_id) t GROUP BY node_id`, now.Add(-window), now.Add(-BeaconSettle))
	if e != nil {
		ix.fieldDirty.Store(true)
		return e
	}
	defer rows.Close()
	out := map[int64]fieldAgg{}
	for rows.Next() {
		var id int64
		var v fieldAgg
		var last, lastFail sql.NullTime
		if e = rows.Scan(&id, &v.OK, &v.Fail, &last, &lastFail); e != nil {
			ix.fieldDirty.Store(true)
			return e
		}
		if last.Valid {
			v.lastOK = last.Time
		}
		if lastFail.Valid {
			v.lastFail = lastFail.Time
		}
		out[id] = v
	}
	if e = rows.Err(); e != nil {
		return e
	}
	ix.fieldMu.Lock()
	ix.field, ix.fieldAt = out, time.Now()
	ix.fieldMu.Unlock()
	return nil
}

func (ix *nodeIndex) fieldOf(id int64) (fieldAgg, bool) {
	ix.fieldMu.RLock()
	defer ix.fieldMu.RUnlock()
	v, ok := ix.field[id]
	return v, ok
}

// fieldRank 国内实测排序档位：0 多数能连，1 无数据，2 结果不一，3 多数连不上
func fieldRank(v fieldAgg, ok bool) int {
	total := v.OK + v.Fail
	if !ok || total == 0 {
		return 1
	}
	rate := float64(v.OK) / float64(total)
	switch {
	case v.OK > 0 && rate >= fieldGoodRate:
		return 0
	case v.Fail >= 2 && rate < fieldBadRate:
		return 3
	}
	return 2
}

// FieldPause 返回本机暂停检测的截止时间：最近一次国内回报为能连、
// 窗口内多数用户能连，且距今不到 FieldRecent
func (s *Store) FieldPause(ctx context.Context, id int64) (time.Time, bool) {
	if s.refreshFieldAgg(ctx, false) != nil {
		return time.Time{}, false
	}
	v, ok := s.index.fieldOf(id)
	if !ok || v.lastOK.IsZero() || !v.lastOK.After(v.lastFail) || fieldRank(v, ok) != 0 {
		return time.Time{}, false
	}
	until := v.lastOK.Add(FieldRecent)
	return until, utcNow().Before(until)
}

// FieldTested 报告该节点是否处于国内实测暂停期
func (s *Store) FieldTested(ctx context.Context, id int64) bool {
	_, ok := s.FieldPause(ctx, id)
	return ok
}

type viewerSeats struct {
	seat   map[int64]string
	active map[int64]int
	follow map[int64]bool
	shared map[int64]bool
}

func (s *Store) loadViewer(ctx context.Context, viewer User, following bool, followUser int64) (viewerSeats, error) {
	v := viewerSeats{seat: map[int64]string{}, active: map[int64]int{}, follow: map[int64]bool{}, shared: map[int64]bool{}}
	if viewer.ID > 0 {
		rows, e := s.DB.QueryContext(ctx, `SELECT source_id FROM np_source_shares WHERE user_id=?`, viewer.ID)
		if e != nil {
			return v, e
		}
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return v, e
			}
			v.shared[id] = true
		}
		rows.Close()
	}
	if viewer.ID > 0 {
		rows, e := s.DB.QueryContext(ctx, `SELECT source_id,IF(state='kicked','kicked',IF(state='active' AND expires_at>UTC_TIMESTAMP(6),'active','')) FROM np_source_seats WHERE user_id=?`, viewer.ID)
		if e != nil {
			return v, e
		}
		for rows.Next() {
			var id int64
			var st string
			if e = rows.Scan(&id, &st); e != nil {
				rows.Close()
				return v, e
			}
			v.seat[id] = st
		}
		rows.Close()
	}
	{
		rows, e := s.DB.QueryContext(ctx, `SELECT st.source_id,COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.state='active' AND st.expires_at>UTC_TIMESTAMP(6) GROUP BY st.source_id`)
		if e != nil {
			return v, e
		}
		for rows.Next() {
			var id int64
			var n int
			if e = rows.Scan(&id, &n); e != nil {
				rows.Close()
				return v, e
			}
			v.active[id] = n
		}
		rows.Close()
	}
	if following {
		rows, e := s.DB.QueryContext(ctx, `SELECT provider_id FROM np_follows WHERE user_id=?`, followUser)
		if e != nil {
			return v, e
		}
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return v, e
			}
			v.follow[id] = true
		}
		rows.Close()
	}
	return v, nil
}

// sees 私有来源只对提供者本人与被分享的用户可见
func (v viewerSeats) sees(u User, src *indexSource) bool {
	return !src.private || (u.ID > 0 && (src.owner == u.ID || v.shared[src.id]))
}

// usable 与 usableSQL、claimSeat 规则一致
func (v viewerSeats) usable(u User, src *indexSource) bool {
	if !src.enabled || !src.ownerEnabled || src.drained || !v.sees(u, src) {
		return false
	}
	if !src.private && src.owner != u.ID && u.Role != "admin" && src.minLevel > u.Level {
		return false
	}
	seat := v.seat[src.id]
	if seat == "kicked" {
		return false
	}
	return src.maxUsers == 0 || seat == "active" || v.active[src.id] < src.maxUsers
}

type rankedNode struct {
	n      *indexNode
	tier   int
	field  int
	fresh  bool
	health bool
}

// indexedNodes 在内存中完成可见性、筛选、统计和排序，返回当页节点 ID
func (s *Store) indexedNodes(ctx context.Context, f Filter, out *NodePage) ([]int64, error) {
	viewer := CurrentUser(ctx)
	ix, e := s.nodeIndex(ctx)
	if e != nil {
		return nil, e
	}
	vs, e := s.loadViewer(ctx, viewer, f.Following, f.UserID)
	if e != nil {
		return nil, e
	}
	if f.MinLevel != nil && (*f.MinLevel < 0 || *f.MinLevel > 6) {
		return nil, errors.New("最低等级筛选须为Lv0–Lv6")
	}
	countries := map[string]bool{}
	if len(f.Countries) == 0 && f.Country != "" {
		f.Countries = []string{f.Country}
	}
	for _, c := range f.Countries {
		if c = strings.TrimSpace(c); c != "" {
			countries[c] = true
		}
	}
	sourceFilter := f.SourceID > 0 || f.ProviderID > 0 || f.Following || f.Exclusive || f.MinLevel != nil
	adminSearch := viewer.Role == "admin" && !DataEncryptionEnabled()
	freshSince := utcNow().Add(-FreshWindow)
	ix.mu.RLock()
	list := make([]rankedNode, 0, 256)
	for _, n := range ix.nodes {
		if n.blocked {
			continue
		}
		visible, matched, usable, named := false, !sourceFilter, false, f.Q == ""
		for _, l := range n.links {
			src := l.src
			if !src.enabled || !src.ownerEnabled || !vs.sees(viewer, src) {
				continue
			}
			if !named && strings.Contains(l.name, f.Q) {
				named = true
			}
			visible = true
			if !matched && (f.SourceID == 0 || src.id == f.SourceID) && (f.ProviderID == 0 || src.owner == f.ProviderID) &&
				(!f.Following || vs.follow[src.owner]) && (!f.Exclusive || (viewer.ID > 0 && src.owner == viewer.ID) || (src.private && vs.shared[src.id])) &&
				(f.MinLevel == nil || src.minLevel == *f.MinLevel) {
				matched = true
			}
			if !usable && vs.usable(viewer, src) {
				usable = true
			}
		}
		if !visible {
			continue
		}
		out.Stats.Total++
		switch n.status {
		case "healthy":
			out.Stats.Healthy++
		case "unhealthy":
			out.Stats.Unhealthy++
		case "unknown":
			out.Stats.Pending++
		}
		if (f.Type != "" && n.typ != f.Type) || (f.Status != "" && n.status != f.Status) || (len(countries) > 0 && !countries[n.country]) || !matched {
			continue
		}
		if f.Available && !usable {
			continue
		}
		if f.MaxLatency > 0 && (n.latency < 0 || n.latency > f.MaxLatency) {
			continue
		}
		if !named && !(adminSearch && strings.Contains(n.server, f.Q)) {
			continue
		}
		agg, has := ix.fieldOf(n.id)
		r := rankedNode{n: n, field: fieldRank(agg, has), health: n.status == "healthy"}
		r.fresh = r.health && !n.created.Before(freshSince)
		// 可选 → 席位已满等暂不可选 → 不可用；国内用户实测能连时不按本机不可用处理。
		// 管理员领取同样受席位上限限制（只跳过等级），排序一致
		switch {
		case (n.status == "unhealthy" && r.field != 0) || r.field == 3:
			r.tier = 2
		case usable:
			r.tier = 0
		default:
			r.tier = 1
		}
		list = append(list, r)
	}
	ix.mu.RUnlock()
	out.Total = len(list)
	latencyLess := func(a, b *indexNode) (bool, bool) {
		if (a.latency < 0) != (b.latency < 0) {
			return a.latency >= 0, true
		}
		if a.latency != b.latency {
			return a.latency < b.latency, true
		}
		return false, false
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		switch f.Sort {
		case "latency":
			if a.field != b.field {
				return a.field < b.field
			}
			if less, ok := latencyLess(a.n, b.n); ok {
				return less
			}
			return a.n.id < b.n.id
		case "name":
			if a.n.name != b.n.name {
				return a.n.name < b.n.name
			}
			return a.n.id < b.n.id
		case "recommend":
			if a.fresh != b.fresh {
				return a.fresh
			}
			if a.field != b.field {
				return a.field < b.field
			}
			if a.health != b.health {
				return a.health
			}
			if less, ok := latencyLess(a.n, b.n); ok {
				return less
			}
			return a.n.id < b.n.id
		}
		if !a.n.created.Equal(b.n.created) {
			return a.n.created.After(b.n.created)
		}
		return a.n.id > b.n.id
	})
	from := (f.Page - 1) * f.PageSize
	if from >= len(list) {
		return []int64{}, nil
	}
	to := min(from+f.PageSize, len(list))
	ids := make([]int64, 0, to-from)
	for _, r := range list[from:to] {
		ids = append(ids, r.n.id)
	}
	return ids, nil
}

// indexedOptions 列表筛选项：可见节点的协议与国家
func (s *Store) indexedOptions(ctx context.Context) (map[string]any, error) {
	ix, e := s.nodeIndex(ctx)
	if e != nil {
		return nil, e
	}
	typeSet, names := map[string]bool{}, map[string]string{}
	ix.mu.RLock()
	for _, n := range ix.nodes {
		if n.blocked {
			continue
		}
		seen := false
		for _, l := range n.links {
			if l.src.enabled && l.src.ownerEnabled {
				seen = true
				break
			}
		}
		if !seen {
			continue
		}
		typeSet[n.typ] = true
		if old, ok := names[n.country]; n.country != "" && (!ok || n.country2 > old) {
			names[n.country] = n.country2
		}
	}
	ix.mu.RUnlock()
	types := make([]string, 0, len(typeSet))
	for t := range typeSet {
		types = append(types, t)
	}
	sort.Strings(types)
	codes := make([]string, 0, len(names))
	for c := range names {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	countries := make([]map[string]string, 0, len(codes))
	for _, c := range codes {
		countries = append(countries, map[string]string{"code": c, "name": names[c]})
	}
	return map[string]any{"types": types, "countries": countries}, nil
}
