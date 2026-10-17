package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const visible = `n.blocked=0 AND EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id JOIN np_users u ON u.id=s.owner_id WHERE sn.node_id=n.id AND sn.excluded=0 AND s.enabled=1 AND u.enabled=1)`
const nodeColumns = `n.id,n.name,n.type,n.server,n.port,n.latency_ms,n.status,n.country,n.country_code,n.region,n.city,n.exit_ip,n.last_checked_at,n.last_success_at,n.exit_checked_at,n.next_check_at,n.last_error,n.config,n.created_at,n.failures,n.successes,n.blocked`

func scanNode(row scanner) (Node, error) {
	var n Node
	n.Sources = []Origin{}
	e := row.Scan(&n.ID, &n.Name, &n.Type, &n.Server, &n.Port, &n.LatencyMS, &n.Status, &n.Country, &n.CountryCode, &n.Region, &n.City, &n.ExitIP, &n.LastCheckedAt, &n.LastSuccessAt, &n.ExitCheckedAt, &n.NextCheckAt, &n.LastError, &n.Config, &n.CreatedAt, &n.Failures, &n.Successes, &n.Blocked)
	if e == nil {
		n.Server, e = unseal(purposeNodeServer, n.Server)
	}
	if e == nil {
		n.Config, e = unsealConfig(n.Config)
	}
	if e == nil {
		n.Insights = InspectNode(n)
	}
	return n, e
}

func (s *Store) Nodes(ctx context.Context, f Filter) (NodePage, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 50
	}
	if f.PageSize > 200 {
		f.PageSize = 200
	}
	if f.Page > 1000000 {
		return NodePage{}, errors.New("页码超出范围")
	}
	out := NodePage{Items: []Node{}, Page: f.Page, PageSize: f.PageSize}
	ids, e := s.indexedNodes(ctx, f, &out)
	if e != nil {
		return out, e
	}
	if len(ids) == 0 {
		return out, nil
	}
	viewer := CurrentUser(ctx)
	rows, e := s.DB.QueryContext(ctx, `SELECT `+nodeColumns+` FROM np_nodes n WHERE n.id IN (`+placeholders(len(ids))+`)`, int64Args(ids)...)
	if e != nil {
		return out, e
	}
	loaded := map[int64]Node{}
	for rows.Next() {
		n, e := scanNode(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		loaded[n.ID] = n
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, id := range ids {
		if n, ok := loaded[id]; ok {
			out.Items = append(out.Items, n)
		}
	}
	if len(out.Items) == 0 {
		return out, nil
	}
	marks := make([]string, len(out.Items))
	originArgs := make([]any, len(out.Items))
	index := map[int64]int{}
	for i, n := range out.Items {
		marks[i] = "?"
		originArgs[i] = n.ID
		index[n.ID] = i
	}
	origins, e := s.DB.QueryContext(ctx, `SELECT sn.node_id,s.id,s.name,sn.node_name,s.owner_id,u.display_name,s.min_level,s.subscription_usage,u.username,u.avatar_version,u.title,s.max_users,(SELECT COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.source_id=s.id AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6)),COALESCE((SELECT IF(me.state='kicked','kicked',IF(me.state='active' AND me.expires_at>UTC_TIMESTAMP(6),'active','')) FROM np_source_seats me WHERE me.source_id=s.id AND me.user_id=?),'') FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id JOIN np_users u ON u.id=s.owner_id WHERE u.enabled=1 AND s.enabled=1 AND sn.excluded=0 AND sn.node_id IN (`+strings.Join(marks, ",")+`) ORDER BY s.id`, append([]any{viewer.ID}, originArgs...)...)
	if e != nil {
		return out, e
	}
	defer origins.Close()
	for origins.Next() {
		var id int64
		var v Origin
		var usage []byte
		var seat string
		if e = origins.Scan(&id, &v.ID, &v.Name, &v.NodeName, &v.OwnerID, &v.ProviderName, &v.MinLevel, &usage, &v.ProviderUsername, &v.ProviderAvatar, &v.ProviderTitle, &v.MaxUsers, &v.ActiveUsers, &seat); e != nil {
			return out, e
		}
		v.Seated = seat == "active"
		v.Unavailable = originBlock(viewer, v, seat)
		if len(usage) > 0 {
			if e = json.Unmarshal(usage, &v.SubscriptionUsage); e != nil {
				return out, e
			}
		}
		refreshUsageStatus(v.SubscriptionUsage)
		i := index[id]
		out.Items[i].Sources = append(out.Items[i].Sources, v)
		if len(out.Items[i].Sources) == 1 {
			out.Items[i].Name = v.NodeName
		}
	}
	if e = origins.Err(); e != nil {
		return out, e
	}
	fresh := utcNow().Add(-FreshWindow)
	for i := range out.Items {
		n := &out.Items[i]
		n.Fresh = n.CreatedAt.After(fresh)
		n.Unavailable = nodeBlock(n.Sources)
	}
	maskServers(viewer, out.Items)
	if e = s.attachNodeFeedback(ctx, out.Items); e != nil {
		return out, e
	}
	return out, s.attachFieldTests(ctx, out.Items)
}

// FreshWindow 内首次入池的节点视为新上架
const FreshWindow = 72 * time.Hour

// usableSQL 判断当前用户能否通过任一来源选用节点，规则与 claimSeat 一致
func usableSQL(u User) (string, []any) {
	return `EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id JOIN np_users u ON u.id=s.owner_id WHERE sn.node_id=n.id AND sn.excluded=0 AND s.enabled=1 AND u.enabled=1 AND (s.owner_id=? OR ?='admin' OR s.min_level<=?) AND NOT EXISTS(SELECT 1 FROM np_source_seats k WHERE k.source_id=s.id AND k.user_id=? AND k.state='kicked') AND (s.max_users=0 OR EXISTS(SELECT 1 FROM np_source_seats me WHERE me.source_id=s.id AND me.user_id=? AND me.state='active' AND me.expires_at>UTC_TIMESTAMP(6)) OR (SELECT COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.source_id=s.id AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6))<s.max_users))`, []any{u.ID, u.Role, u.Level, u.ID, u.ID}
}

func originBlock(u User, o Origin, seat string) string {
	switch {
	case seat == "kicked":
		return "kicked"
	case o.OwnerID != u.ID && u.Role != "admin" && u.Level < o.MinLevel:
		return "level"
	case o.MaxUsers > 0 && seat != "active" && o.ActiveUsers >= o.MaxUsers:
		return "full"
	}
	return ""
}

// nodeBlock 任一来源可用即可选；否则优先返回席位已满，便于提示等待
func nodeBlock(origins []Origin) string {
	reason := ""
	for _, o := range origins {
		if o.Unavailable == "" {
			return ""
		}
		if reason == "" || o.Unavailable == "full" {
			reason = o.Unavailable
		}
	}
	return reason
}

func (s *Store) QueueNodes(ctx context.Context, ids []int64) error {
	if CurrentUser(ctx).Role != "admin" {
		return ErrForbidden
	}
	if len(ids) > 10000 {
		return errors.New("单次最多指定 10000 个节点")
	}
	where := `EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources src ON src.id=sn.source_id WHERE sn.node_id=n.id AND sn.excluded=0 AND src.enabled=1)`
	args := []any{}
	if len(ids) > 0 {
		marks := make([]string, len(ids))
		for i, id := range ids {
			if id <= 0 {
				return errors.New("节点 ID 无效")
			}
			marks[i] = "?"
			args = append(args, id)
		}
		where += " AND n.id IN (" + strings.Join(marks, ",") + ")"
	} else {
		where += " AND n.blocked=0"
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE np_nodes n SET pending=1 WHERE `+where, args...)
	return e
}

func (s *Store) Options(ctx context.Context) (map[string]any, error) {
	return s.indexedOptions(ctx)
}

func (s *Store) ClaimSource(ctx context.Context) (Source, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Source{}, e
	}
	defer tx.Rollback()
	v, e := scanSource(tx.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM np_sources WHERE enabled=1 AND (lease_until IS NULL OR lease_until<UTC_TIMESTAMP(6)) AND (pending=1 OR (kind='url' AND next_refresh_at<=UTC_TIMESTAMP(6)) OR status='processing') ORDER BY pending DESC,next_refresh_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`))
	if e != nil {
		return v, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE np_sources SET pending=0,cached=0,status='processing',lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 5 MINUTE) WHERE id=?`, v.ID)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}

// eligible 节点至少关联一个启用来源中未被排除的条目
// 写成带 LIMIT 的标量子查询，避免优化器整体物化后再连接；只对扫描到的行逐个判断
const eligible = `(SELECT 1 FROM np_source_nodes sn JOIN np_sources src ON src.id=sn.source_id WHERE sn.node_id=n.id AND sn.excluded=0 AND src.enabled=1 LIMIT 1) IS NOT NULL`

// ClaimNode 领取一个检测任务：先手动复测，再按 next_check_at 顺序取到期节点。
// 两条查询都能走索引，不再对全部节点逐行求值。
func (s *Store) ClaimNode(ctx context.Context) (Node, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Node{}, e
	}
	defer tx.Rollback()
	free := `(n.lease_until IS NULL OR n.lease_until<UTC_TIMESTAMP(6))`
	v, e := scanNode(tx.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM np_nodes n WHERE n.pending=1 AND `+free+` AND `+eligible+` ORDER BY n.id LIMIT 1 FOR UPDATE SKIP LOCKED`))
	if e == nil {
		v.Manual = true
	} else if errors.Is(e, sql.ErrNoRows) {
		v, e = scanNode(tx.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM np_nodes n WHERE n.next_check_at<=UTC_TIMESTAMP(6) AND n.blocked=0 AND `+free+` AND `+eligible+` ORDER BY n.next_check_at LIMIT 1 FOR UPDATE SKIP LOCKED`))
	}
	if e != nil {
		return v, e
	}
	// 租约过期后按到期顺序被重新领取，手动任务也一样
	_, e = tx.ExecContext(ctx, `UPDATE np_nodes SET pending=0,next_check_at=LEAST(next_check_at,UTC_TIMESTAMP(6)),lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 2 MINUTE) WHERE id=?`, v.ID)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}

// ParkIdleNodes 把到期但不可检测的节点（已屏蔽、未关联启用来源）推后一天，
// 避免它们堆在到期队列前面拖慢领取。来源重新启用或刷新时会重新排队。
func (s *Store) ParkIdleNodes(ctx context.Context) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE np_nodes n SET n.next_check_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 DAY) WHERE n.next_check_at<=UTC_TIMESTAMP(6) AND n.pending=0 AND n.lease_until IS NULL AND (n.blocked=1 OR NOT `+eligible+`)`)
	return e
}

// DeferNode 释放租约并推迟检测，用于国内用户正在实测的节点
func (s *Store) DeferNode(ctx context.Context, id int64, until time.Time) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE np_nodes SET lease_until=NULL,next_check_at=? WHERE id=?`, until, id)
	return e
}

func (s *Store) SaveProbe(ctx context.Context, n Node, r ProbeResult) error {
	now := utcNow()
	status := n.Status
	failures, successes := n.Failures, n.Successes
	blocked := n.Blocked
	lastSuccess := n.LastSuccessAt
	var next time.Time
	if r.LatencyMS != nil {
		successes++
		failures = 0
		status = "healthy"
		blocked = false
		lastSuccess = &now
		// 按使用情况分级；订阅来源刷新后会更早重排，这里多留 1 小时兜底
		next = now.Add(s.ProbeInterval(n.ID) + time.Hour)
	} else {
		failures++
		successes = 0
		// 从未成功过的节点首次失败即标记异常，避免长期停留在“待检测”
		if failures >= 3 || status == "unknown" {
			status = "unhealthy"
		}
		base := n.CreatedAt
		if lastSuccess != nil {
			base = *lastSuccess
		}
		if now.Sub(base) >= StaleAfter {
			blocked = true
		}
		next = now.Add(RetryDelay(failures))
		// 闲置节点确认不可用后不再按失败退避重试，改按闲置间隔
		if status == "unhealthy" && s.IdleProbe(n.ID) {
			if d := s.ProbeInterval(n.ID); d > RetryDelay(failures) {
				next = now.Add(d)
			}
		}
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE np_nodes SET latency_ms=?,status=?,failures=?,successes=?,last_checked_at=?,last_success_at=?,next_check_at=?,last_error=?,lease_until=NULL,blocked=? WHERE id=?`, r.LatencyMS, status, failures, successes, now, lastSuccess, next, r.Error, blocked, n.ID)
	if e != nil {
		return e
	}
	if r.ExitAttempted {
		if r.ExitIP != "" {
			// Partial fallback responses may omit geography. Preserve known fields
			// only when the exit is unchanged; never attach an old city to a new IP.
			if r.ExitIP == n.ExitIP {
				if r.Country == "" {
					r.Country = n.Country
				}
				if r.CountryCode == "" {
					r.CountryCode = n.CountryCode
				}
				if r.Region == "" {
					r.Region = n.Region
				}
				if r.City == "" {
					r.City = n.City
				}
			}
			_, e = s.DB.ExecContext(ctx, `UPDATE np_nodes SET exit_ip=?,country=?,country_code=?,region=?,city=?,exit_checked_at=? WHERE id=?`, r.ExitIP, r.Country, r.CountryCode, r.Region, r.City, now, n.ID)
		} else {
			_, e = s.DB.ExecContext(ctx, `UPDATE np_nodes SET exit_checked_at=? WHERE id=?`, now, n.ID)
		}
	}
	country, code := "", ""
	if r.ExitAttempted && r.ExitIP != "" {
		country, code = r.Country, r.CountryCode
	}
	s.patchIndex(n.ID, status, r.LatencyMS, country, code, blocked)
	return e
}

func (s *Store) TaskStatus(ctx context.Context) (Tasks, error) {
	var t Tasks
	e := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+sourceWork+` AND (lease_until IS NULL OR lease_until<UTC_TIMESTAMP(6))),0),COALESCE(SUM(lease_until>=UTC_TIMESTAMP(6)),0) FROM np_sources WHERE enabled=1`).Scan(&t.PendingSources, &t.ActiveSources)
	if e != nil {
		return t, e
	}
	e = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+nodeWork+` AND (n.lease_until IS NULL OR n.lease_until<UTC_TIMESTAMP(6))),0),COALESCE(SUM(n.lease_until>=UTC_TIMESTAMP(6)),0) FROM np_nodes n WHERE `+nodeWorkEligible).Scan(&t.PendingNodes, &t.ActiveNodes)
	return t, e
}
