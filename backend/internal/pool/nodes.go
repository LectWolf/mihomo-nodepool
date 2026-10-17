package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(status='healthy'),0),COALESCE(SUM(status='unhealthy'),0),COALESCE(SUM(status='unknown'),0) FROM np_nodes n WHERE `+visible).Scan(&out.Stats.Total, &out.Stats.Healthy, &out.Stats.Unhealthy, &out.Stats.Pending)
	if e != nil {
		return out, e
	}
	where := visible
	args := []any{}
	for _, item := range []struct{ column, value string }{{"n.type", f.Type}, {"n.status", f.Status}} {
		if item.value != "" {
			where += " AND " + item.column + "=?"
			args = append(args, item.value)
		}
	}
	if len(f.Countries) == 0 && f.Country != "" {
		f.Countries = []string{f.Country}
	}
	if len(f.Countries) > 0 {
		marks := []string{}
		for _, c := range f.Countries {
			c = strings.TrimSpace(c)
			if c != "" {
				marks = append(marks, "?")
				args = append(args, c)
			}
		}
		if len(marks) > 0 {
			where += " AND n.country_code IN (" + strings.Join(marks, ",") + ")"
		}
	}
	if f.SourceID > 0 || f.ProviderID > 0 || f.Following || f.MinLevel != nil {
		where += ` AND EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id JOIN np_users u ON u.id=s.owner_id WHERE sn.node_id=n.id AND sn.excluded=0 AND s.enabled=1 AND u.enabled=1`
		if f.SourceID > 0 {
			where += ` AND s.id=?`
			args = append(args, f.SourceID)
		}
		if f.ProviderID > 0 {
			where += ` AND s.owner_id=?`
			args = append(args, f.ProviderID)
		}
		if f.Following {
			where += ` AND EXISTS(SELECT 1 FROM np_follows f WHERE f.provider_id=s.owner_id AND f.user_id=?)`
			args = append(args, f.UserID)
		}
		if f.MinLevel != nil {
			if *f.MinLevel < 0 || *f.MinLevel > 6 {
				return out, errors.New("最低等级筛选须为Lv0–Lv6")
			}
			where += ` AND s.min_level=?`
			args = append(args, *f.MinLevel)
		}
		where += `)`
	}
	viewer := CurrentUser(ctx)
	usable, usableArgs := usableSQL(viewer)
	if f.Available && viewer.Role != "admin" {
		where += " AND " + usable
		args = append(args, usableArgs...)
	}
	if f.MaxLatency > 0 {
		where += " AND n.latency_ms<=?"
		args = append(args, f.MaxLatency)
	}
	if f.Q != "" {
		nameMatch := `EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id WHERE sn.node_id=n.id AND s.enabled=1 AND sn.excluded=0 AND sn.node_name LIKE ?)`
		// 服务器地址只允许管理员在未加密时搜索
		if CurrentUser(ctx).Role == "admin" && !DataEncryptionEnabled() {
			where += ` AND (n.server LIKE ? OR ` + nameMatch + `)`
			args = append(args, "%"+f.Q+"%", "%"+f.Q+"%")
		} else {
			where += ` AND ` + nameMatch
			args = append(args, "%"+f.Q+"%")
		}
	}
	if e = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_nodes n WHERE `+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	order := "n.created_at DESC,n.id DESC"
	switch f.Sort {
	case "latency":
		order = "n.latency_ms IS NULL,n.latency_ms,n.id"
	case "name":
		order = "n.name,n.id"
	case "recommend":
		// 新上架且健康的节点优先曝光，其余按健康与延迟排序
		order = "(n.status='healthy' AND n.created_at>=?) DESC,n.status='healthy' DESC,n.latency_ms IS NULL,n.latency_ms,n.id"
	}
	orderArgs := []any{}
	if f.Sort == "recommend" {
		orderArgs = append(orderArgs, utcNow().Add(-FreshWindow))
	}
	// 普通用户看不可选的节点排在后面
	if viewer.Role != "admin" {
		order = usable + " DESC," + order
		orderArgs = append(append([]any{}, usableArgs...), orderArgs...)
	}
	queryArgs := append(append(append([]any{}, args...), orderArgs...), f.PageSize, (f.Page-1)*f.PageSize)
	rows, e := s.DB.QueryContext(ctx, `SELECT `+nodeColumns+` FROM np_nodes n WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, queryArgs...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		n, e := scanNode(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
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
	return out, s.attachNodeFeedback(ctx, out.Items)
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
	types := []string{}
	countries := []map[string]string{}
	rows, e := s.DB.QueryContext(ctx, `SELECT DISTINCT type FROM np_nodes n WHERE `+visible+` ORDER BY type`)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return nil, e
		}
		types = append(types, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = s.DB.QueryContext(ctx, `SELECT country_code,MAX(country) FROM np_nodes n WHERE `+visible+` AND country_code<>'' GROUP BY country_code ORDER BY country_code`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var code, name string
		if e = rows.Scan(&code, &name); e != nil {
			return nil, e
		}
		countries = append(countries, map[string]string{"code": code, "name": name})
	}
	return map[string]any{"types": types, "countries": countries}, rows.Err()
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

func (s *Store) ClaimNode(ctx context.Context) (Node, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Node{}, e
	}
	defer tx.Rollback()
	v, e := scanNode(tx.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM np_nodes n WHERE ((n.blocked=0 AND EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources src ON src.id=sn.source_id WHERE sn.node_id=n.id AND sn.excluded=0 AND src.enabled=1)) OR (n.blocked=1 AND n.pending=1 AND EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources src ON src.id=sn.source_id WHERE sn.node_id=n.id AND sn.excluded=0 AND src.enabled=1))) AND (n.lease_until IS NULL OR n.lease_until<UTC_TIMESTAMP(6)) AND (n.pending=1 OR n.next_check_at<=UTC_TIMESTAMP(6) OR n.lease_until IS NOT NULL) ORDER BY n.pending DESC,n.next_check_at,n.id LIMIT 1 FOR UPDATE SKIP LOCKED`))
	if e != nil {
		return v, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE np_nodes SET pending=0,lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 2 MINUTE) WHERE id=?`, v.ID)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
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
		next = NextRun(n.CreatedAt, fmt.Sprintf("node:%d", n.ID), now)
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
