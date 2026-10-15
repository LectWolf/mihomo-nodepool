package pool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func urlHash(raw string) any {
	if raw == "" {
		return nil
	}
	h := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(h[:])
}
func (s *Store) AddSource(ctx context.Context, in SourceInput) (Source, error) {
	if e := checkSource(in); e != nil {
		return Source{}, e
	}
	now := utcNow()
	result, e := s.DB.ExecContext(ctx, `INSERT INTO np_sources(name,kind,url,url_hash,content,exclude_pattern,last_error,created_at,next_refresh_at) VALUES(?,?,?,?,?,?,'',?,?)`, cleanName(in.Name), in.Kind, strings.TrimSpace(in.URL), urlHash(in.URL), in.Content, in.ExcludePattern, now, now)
	if e != nil {
		var me *mysql.MySQLError
		if errors.As(e, &me) && me.Number == 1062 {
			return Source{}, errors.New("此订阅链接已存在")
		}
		return Source{}, e
	}
	id, e := result.LastInsertId()
	if e != nil {
		return Source{}, e
	}
	return s.Source(ctx, id)
}
func (s *Store) PatchSource(ctx context.Context, id int64, p SourcePatch) (Source, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Source{}, e
	}
	defer tx.Rollback()
	v, e := scanSource(tx.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM np_sources WHERE id=? FOR UPDATE`, id))
	if e == sql.ErrNoRows {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if p.Name != nil {
		v.Name = cleanName(*p.Name)
	}
	if v.Name == "" || len(v.Name) > 200 {
		return v, errors.New("来源名称不能为空且最多 200 字节")
	}
	if p.ExcludePattern != nil {
		if _, e := Pattern(*p.ExcludePattern); e != nil {
			return v, e
		}
		v.ExcludePattern = *p.ExcludePattern
	}
	reparse := p.ExcludePattern != nil
	fetch := false
	if p.URL != nil && strings.TrimSpace(*p.URL) != "" {
		if v.Kind != "url" {
			return v, errors.New("只有订阅来源可以修改 URL")
		}
		v.URL = strings.TrimSpace(*p.URL)
		if e := checkSource(SourceInput{Name: v.Name, Kind: v.Kind, URL: v.URL}); e != nil {
			return v, e
		}
		fetch = true
	}
	if p.Enabled != nil {
		v.Enabled = *p.Enabled
		if v.Enabled {
			reparse = true
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE np_sources SET name=?,enabled=?,exclude_pattern=?,url=?,url_hash=?,revision=revision+1,pending=IF(? OR ?,1,pending),cached=IF(?,0,IF(?,1,cached)),status=IF(? OR ?,'queued',status) WHERE id=?`, v.Name, v.Enabled, v.ExcludePattern, v.URL, urlHash(v.URL), reparse, fetch, fetch, reparse, reparse, fetch, id)
	if e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	return s.Source(ctx, id)
}
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	r, e := s.DB.ExecContext(ctx, `DELETE FROM np_sources WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) QueueSource(ctx context.Context, id int64) error {
	r, e := s.DB.ExecContext(ctx, `UPDATE np_sources SET pending=1,cached=0,status='queued' WHERE id=? AND enabled=1`, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return errors.New("来源不存在、已停用或已排队")
	}
	return nil
}

func (s *Store) ApplySource(ctx context.Context, src Source, body string, parsed ParseResult) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Same lock order as settings changes prevents stale rules and deadlocks.
	var settingsRaw []byte
	if e = tx.QueryRowContext(ctx, `SELECT body FROM np_settings WHERE id=1 FOR UPDATE`).Scan(&settingsRaw); e != nil {
		return e
	}
	settings := Defaults()
	if e = json.Unmarshal(settingsRaw, &settings); e != nil {
		return e
	}
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM np_sources WHERE id=? FOR UPDATE`, src.ID).Scan(&revision); e == sql.ErrNoRows {
		return nil
	} else if e != nil {
		return e
	}
	if revision != src.Revision {
		_, e = tx.ExecContext(ctx, `UPDATE np_sources SET lease_until=NULL WHERE id=?`, src.ID)
		if e != nil {
			return e
		}
		return tx.Commit()
	}
	global, e := Pattern(settings.ExcludePattern)
	if e != nil {
		return e
	}
	local, e := Pattern(src.ExcludePattern)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM np_source_nodes WHERE source_id=?`, src.ID); e != nil {
		return e
	}
	insert, e := tx.PrepareContext(ctx, `INSERT INTO np_nodes(config_hash,name,type,server,port,config,last_error,created_at,next_check_at) VALUES(?,?,?,?,?,?,'',?,?) ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id)`)
	if e != nil {
		return e
	}
	defer insert.Close()
	link, e := tx.PrepareContext(ctx, `INSERT INTO np_source_nodes(source_id,node_id,node_name,excluded) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE excluded=LEAST(excluded,VALUES(excluded)),node_name=IF(VALUES(excluded)=0,VALUES(node_name),node_name)`)
	if e != nil {
		return e
	}
	defer link.Close()
	now := utcNow()
	seen := map[string]bool{}
	for _, node := range parsed.Nodes {
		excluded := (global != nil && global.MatchString(node.Name)) || (local != nil && local.MatchString(node.Name))
		r, e := insert.ExecContext(ctx, node.Hash, node.Name, node.Type, node.Server, node.Port, []byte(node.Config), now, InitialRun(node.Hash, now))
		if e != nil {
			return e
		}
		nodeID, e := r.LastInsertId()
		if e != nil {
			return e
		}
		if _, e = link.ExecContext(ctx, src.ID, nodeID, node.Name, excluded); e != nil {
			return e
		}
		old, exists := seen[node.Hash]
		seen[node.Hash] = excluded && (!exists || old)
	}
	active, excluded := 0, 0
	for _, v := range seen {
		if v {
			excluded++
		} else {
			active++
		}
	}
	warning := ""
	if len(parsed.Warnings) > 0 {
		warning = fmt.Sprintf("已跳过 %d 条无法解析的记录", len(parsed.Warnings))
	}
	next := NextRun(src.CreatedAt, fmt.Sprintf("source:%d", src.ID), now)
	var nextAt any
	if src.Kind == "url" {
		nextAt = next
	}
	_, e = tx.ExecContext(ctx, `UPDATE np_sources SET content=?,node_count=?,excluded_count=?,last_refresh_at=?,next_refresh_at=?,last_error=?,status=IF(pending=1,'queued','ready'),attempts=0,lease_until=NULL WHERE id=?`, body, active, excluded, now, nextAt, warning, src.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) FailSource(ctx context.Context, src Source, message string) error {
	// Network diagnostics deliberately omit URLs and upstream bodies (subscription tokens).
	_, e := s.DB.ExecContext(ctx, `UPDATE np_sources SET status=IF(pending=1,'queued','error'),last_error=?,lease_until=NULL,attempts=attempts+1,next_refresh_at=IF(kind='url',?,NULL) WHERE id=? AND revision=?`, message, utcNow().Add(15*time.Minute), src.ID, src.Revision)
	return e
}
