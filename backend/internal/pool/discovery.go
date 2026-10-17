package pool

import (
	"context"
	"strings"
	"time"
)

type CatalogFilter struct {
	Q              string
	ProviderID     int64
	Following      bool
	Sort           string
	Available      bool
	Page, PageSize int
}
type Provider struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	SourceCount  int    `json:"source_count"`
	NodeCount    int    `json:"node_count"`
	Followers    int    `json:"followers"`
	Followed     bool   `json:"followed"`
	Avatar       int    `json:"avatar_version"`
	Title        string `json:"title"`
	ProviderPath string `json:"provider_path"`
}

func (s *Store) Catalog(ctx context.Context, f CatalogFilter) (map[string]any, error) {
	page, size := pagination(f.Page, f.PageSize)
	where := `enabled=1 AND EXISTS(SELECT 1 FROM np_users u WHERE u.id=np_sources.owner_id AND u.enabled=1) AND EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=np_sources.id AND sn.excluded=0 AND n.blocked=0)`
	args := []any{}
	if f.Q != "" {
		where += ` AND (name LIKE ? OR EXISTS(SELECT 1 FROM np_users u WHERE u.id=np_sources.owner_id AND (u.username LIKE ? OR u.display_name LIKE ?)))`
		q := "%" + f.Q + "%"
		args = append(args, q, q, q)
	}
	if f.ProviderID > 0 {
		where += ` AND owner_id=?`
		args = append(args, f.ProviderID)
	}
	if f.Following {
		where += ` AND EXISTS(SELECT 1 FROM np_follows f WHERE f.provider_id=np_sources.owner_id AND f.user_id=?)`
		args = append(args, CurrentUser(ctx).ID)
	}
	viewer := CurrentUser(ctx)
	usable, usableArgs := sourceUsableSQL(viewer)
	if f.Available && viewer.Role != "admin" {
		where += " AND " + usable
		args = append(args, usableArgs...)
	}
	var total int
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_sources WHERE `+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	order, orderArgs := "id", []any{}
	switch f.Sort {
	case "recommend":
		// 可选优先，新上架来源优先曝光，再按健康节点数
		order = "created_at>=? DESC,(SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=np_sources.id AND sn.excluded=0 AND n.blocked=0 AND n.status='healthy') DESC,id DESC"
		orderArgs = append(orderArgs, utcNow().Add(FreshSourceWindow*-1))
		if viewer.Role != "admin" {
			order = usable + " DESC," + order
			orderArgs = append(append([]any{}, usableArgs...), orderArgs...)
		}
	case "new":
		order = "created_at DESC,id DESC"
	}
	// Neither the catalog nor management responses contain upstream URLs/content.
	rows, e := s.DB.QueryContext(ctx, `SELECT `+publicSourceColumns+` FROM np_sources WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(append(append([]any{}, args...), orderArgs...), size, (page-1)*size)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []Source{}
	for rows.Next() {
		v, e := scanSource(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if e = s.attachSourceFeedback(ctx, items); e != nil {
		return nil, e
	}
	if e = s.attachSourceStats(ctx, viewer, items); e != nil {
		return nil, e
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, nil
}

// FreshSourceWindow 内创建的来源标记为新上架
const FreshSourceWindow = 7 * 24 * time.Hour

func sourceUsableSQL(u User) (string, []any) {
	return `((owner_id=? OR ?='admin' OR min_level<=?) AND NOT EXISTS(SELECT 1 FROM np_source_seats k WHERE k.source_id=np_sources.id AND k.user_id=? AND k.state='kicked') AND (max_users=0 OR EXISTS(SELECT 1 FROM np_source_seats me WHERE me.source_id=np_sources.id AND me.user_id=? AND me.state='active' AND me.expires_at>UTC_TIMESTAMP(6)) OR (SELECT COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.source_id=np_sources.id AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6))<max_users))`, []any{u.ID, u.Role, u.Level, u.ID, u.ID}
}

// attachSourceStats 补充来源目录的健康概况和当前用户可用性
func (s *Store) attachSourceStats(ctx context.Context, viewer User, items []Source) error {
	if len(items) == 0 {
		return nil
	}
	marks, args, index := make([]string, len(items)), make([]any, len(items)), map[int64]int{}
	fresh := utcNow().Add(-FreshSourceWindow)
	for i := range items {
		marks[i], args[i], index[items[i].ID] = "?", items[i].ID, i
		items[i].Fresh = items[i].CreatedAt.After(fresh)
	}
	in := strings.Join(marks, ",")
	rows, e := s.DB.QueryContext(ctx, `SELECT sn.source_id,COALESCE(SUM(n.status='healthy'),0),CAST(AVG(CASE WHEN n.status='healthy' THEN n.latency_ms END) AS SIGNED) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.excluded=0 AND n.blocked=0 AND sn.source_id IN (`+in+`) GROUP BY sn.source_id`, args...)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id int64
		var healthy int
		var latency *int
		if e = rows.Scan(&id, &healthy, &latency); e != nil {
			rows.Close()
			return e
		}
		items[index[id]].HealthyCount, items[index[id]].AvgLatency = healthy, latency
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	rows, e = s.DB.QueryContext(ctx, `SELECT sn.source_id,n.country_code,COUNT(*) c FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.excluded=0 AND n.blocked=0 AND n.country_code<>'' AND sn.source_id IN (`+in+`) GROUP BY sn.source_id,n.country_code ORDER BY sn.source_id,c DESC,n.country_code`, args...)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id int64
		var code string
		var count int
		if e = rows.Scan(&id, &code, &count); e != nil {
			rows.Close()
			return e
		}
		if v := &items[index[id]]; len(v.Countries) < 8 {
			v.Countries = append(v.Countries, code)
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	seats := map[int64]string{}
	if viewer.ID > 0 {
		rows, e = s.DB.QueryContext(ctx, `SELECT source_id,IF(state='kicked','kicked',IF(state='active' AND expires_at>UTC_TIMESTAMP(6),'active','')) FROM np_source_seats WHERE user_id=? AND source_id IN (`+in+`)`, append([]any{viewer.ID}, args...)...)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id int64
			var state string
			if e = rows.Scan(&id, &state); e != nil {
				rows.Close()
				return e
			}
			seats[id] = state
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
	}
	for i := range items {
		v := &items[i]
		v.Seated = seats[v.ID] == "active"
		v.Unavailable = originBlock(viewer, Origin{OwnerID: v.OwnerID, MinLevel: v.MinLevel, MaxUsers: v.MaxUsers, ActiveUsers: v.ActiveUsers}, seats[v.ID])
	}
	return nil
}
func (s *Store) Providers(ctx context.Context, f CatalogFilter) (map[string]any, error) {
	page, size := pagination(f.Page, f.PageSize)
	where := `u.enabled=1 AND EXISTS(SELECT 1 FROM np_sources s WHERE s.owner_id=u.id AND s.enabled=1)`
	args := []any{}
	if f.Q != "" {
		where += ` AND (u.username LIKE ? OR u.display_name LIKE ?)`
		args = append(args, "%"+f.Q+"%", "%"+f.Q+"%")
	}
	if f.Following {
		where += ` AND EXISTS(SELECT 1 FROM np_follows f WHERE f.provider_id=u.id AND f.user_id=?)`
		args = append(args, CurrentUser(ctx).ID)
	}
	var total int
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_users u WHERE `+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	query := `SELECT u.id,u.username,u.display_name,(SELECT COUNT(*) FROM np_sources s WHERE s.owner_id=u.id AND s.enabled=1),(SELECT COUNT(DISTINCT sn.node_id) FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id JOIN np_nodes n ON n.id=sn.node_id WHERE s.owner_id=u.id AND s.enabled=1 AND sn.excluded=0 AND n.blocked=0),(SELECT COUNT(*) FROM np_follows f WHERE f.provider_id=u.id),EXISTS(SELECT 1 FROM np_follows f WHERE f.provider_id=u.id AND f.user_id=?),u.avatar_version,u.title FROM np_users u WHERE ` + where + ` ORDER BY u.id LIMIT ? OFFSET ?`
	queryArgs := append([]any{CurrentUser(ctx).ID}, args...)
	queryArgs = append(queryArgs, size, (page-1)*size)
	rows, e := s.DB.QueryContext(ctx, query, queryArgs...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []Provider{}
	for rows.Next() {
		var v Provider
		if e = rows.Scan(&v.ID, &v.Username, &v.DisplayName, &v.SourceCount, &v.NodeCount, &v.Followers, &v.Followed, &v.Avatar, &v.Title); e != nil {
			return nil, e
		}
		v.ProviderPath = ProviderPath(v.ID)
		items = append(items, v)
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, rows.Err()
}
func (s *Store) Follow(ctx context.Context, id int64, follow bool) error {
	u, e := s.User(ctx, id)
	if e != nil {
		return e
	}
	if !u.Enabled {
		return ErrNotFound
	}
	if follow {
		_, e = s.DB.ExecContext(ctx, `INSERT IGNORE INTO np_follows(user_id,provider_id) VALUES(?,?)`, CurrentUser(ctx).ID, id)
	} else {
		_, e = s.DB.ExecContext(ctx, `DELETE FROM np_follows WHERE user_id=? AND provider_id=?`, CurrentUser(ctx).ID, id)
	}
	return e
}
