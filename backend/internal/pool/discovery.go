package pool

import (
	"context"
)

type CatalogFilter struct {
	Q              string
	ProviderID     int64
	Following      bool
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
	var total int
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_sources WHERE `+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	// Neither the catalog nor management responses contain upstream URLs/content.
	rows, e := s.DB.QueryContext(ctx, `SELECT `+publicSourceColumns+` FROM np_sources WHERE `+where+` ORDER BY id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
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
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, rows.Err()
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
	query := `SELECT u.id,u.username,u.display_name,(SELECT COUNT(*) FROM np_sources s WHERE s.owner_id=u.id AND s.enabled=1),(SELECT COUNT(DISTINCT sn.node_id) FROM np_source_nodes sn JOIN np_sources s ON s.id=sn.source_id JOIN np_nodes n ON n.id=sn.node_id WHERE s.owner_id=u.id AND s.enabled=1 AND sn.excluded=0 AND n.blocked=0),(SELECT COUNT(*) FROM np_follows f WHERE f.provider_id=u.id),EXISTS(SELECT 1 FROM np_follows f WHERE f.provider_id=u.id AND f.user_id=?) FROM np_users u WHERE ` + where + ` ORDER BY u.id LIMIT ? OFFSET ?`
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
		if e = rows.Scan(&v.ID, &v.Username, &v.DisplayName, &v.SourceCount, &v.NodeCount, &v.Followers, &v.Followed); e != nil {
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
