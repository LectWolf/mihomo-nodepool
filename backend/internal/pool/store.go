package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

type Store struct {
	DB          *sql.DB
	initMu      sync.Mutex
	initialized bool
}

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

var schema = []string{
	`CREATE TABLE IF NOT EXISTS np_settings (id INT PRIMARY KEY, body JSON NOT NULL) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS np_sources (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, owner_id BIGINT NOT NULL DEFAULT 1,
 name VARCHAR(200) NOT NULL, kind VARCHAR(16) NOT NULL, url TEXT NOT NULL, url_hash CHAR(64) NULL UNIQUE,
 content LONGTEXT NOT NULL, exclude_pattern TEXT NOT NULL, enabled BOOLEAN NOT NULL DEFAULT 1,
 status VARCHAR(16) NOT NULL DEFAULT 'queued', node_count INT NOT NULL DEFAULT 0, excluded_count INT NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL, last_refresh_at DATETIME(6) NULL, next_refresh_at DATETIME(6) NULL,
 created_at DATETIME(6) NOT NULL, revision BIGINT NOT NULL DEFAULT 1,
 pending BOOLEAN NOT NULL DEFAULT 1, cached BOOLEAN NOT NULL DEFAULT 0, lease_until DATETIME(6) NULL,
 attempts INT NOT NULL DEFAULT 0, INDEX source_due(enabled,next_refresh_at), INDEX source_pending(pending,lease_until)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS np_nodes (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, owner_id BIGINT NOT NULL DEFAULT 1,
 config_hash CHAR(64) NOT NULL UNIQUE, name VARCHAR(800) NOT NULL, type VARCHAR(32) NOT NULL,
 server VARCHAR(512) NOT NULL, port INT NOT NULL, config JSON NOT NULL,
 status VARCHAR(16) NOT NULL DEFAULT 'unknown', latency_ms INT NULL,
 country VARCHAR(128) NOT NULL DEFAULT '', country_code VARCHAR(8) NOT NULL DEFAULT '', exit_ip VARCHAR(64) NOT NULL DEFAULT '',
 last_checked_at DATETIME(6) NULL, exit_checked_at DATETIME(6) NULL, next_check_at DATETIME(6) NOT NULL,
 last_error TEXT NOT NULL, created_at DATETIME(6) NOT NULL,
 failures INT NOT NULL DEFAULT 0, successes INT NOT NULL DEFAULT 0,
 pending BOOLEAN NOT NULL DEFAULT 0, lease_until DATETIME(6) NULL,
 INDEX node_due(next_check_at,lease_until), INDEX node_pending(pending,lease_until), INDEX node_type(type), INDEX node_country(country_code)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS np_source_nodes (
 source_id BIGINT NOT NULL, node_id BIGINT NOT NULL, node_name VARCHAR(800) NOT NULL, excluded BOOLEAN NOT NULL,
 PRIMARY KEY(source_id,node_id), INDEX membership(node_id,excluded),
 CONSTRAINT np_source_fk FOREIGN KEY(source_id) REFERENCES np_sources(id) ON DELETE CASCADE,
 CONSTRAINT np_node_fk FOREIGN KEY(node_id) REFERENCES np_nodes(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
}

func (s *Store) Ensure(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return ErrUnavailable
	}
	s.initMu.Lock()
	defer s.initMu.Unlock()
	if e := s.DB.PingContext(ctx); e != nil {
		return ErrUnavailable
	}
	if s.initialized {
		return nil
	}
	for _, q := range schema {
		if _, e := s.DB.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	b, _ := json.Marshal(Defaults())
	if _, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO np_settings(id,body) VALUES(1,?)`, b); e != nil {
		return e
	}
	s.initialized = true
	return nil
}
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `SELECT body FROM np_settings WHERE id=1`).Scan(&raw)
	v := Defaults()
	if err == nil {
		err = json.Unmarshal(raw, &v)
	}
	v.RefreshHours, v.CheckHours = 6, 6
	return v, err
}
func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	v.RefreshHours, v.CheckHours = 6, 6
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var oldRaw []byte
	if e = tx.QueryRowContext(ctx, `SELECT body FROM np_settings WHERE id=1 FOR UPDATE`).Scan(&oldRaw); e != nil {
		return e
	}
	var old Settings
	_ = json.Unmarshal(oldRaw, &old)
	b, _ := json.Marshal(v)
	if _, e = tx.ExecContext(ctx, `UPDATE np_settings SET body=? WHERE id=1`, b); e != nil {
		return e
	}
	if old.ExcludePattern != v.ExcludePattern {
		// Preserve existing network refresh requirements when merging rule changes.
		_, e = tx.ExecContext(ctx, `UPDATE np_sources SET cached=IF(kind='url' AND ((cached=0 AND (pending=1 OR lease_until IS NOT NULL)) OR next_refresh_at<=UTC_TIMESTAMP(6)),0,1),pending=1,revision=revision+1,status='queued' WHERE enabled=1`)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

const sourceColumns = `id,name,kind,enabled,exclude_pattern,status,node_count,excluded_count,last_error,last_refresh_at,next_refresh_at,created_at,url,content,revision,cached`

type scanner interface{ Scan(...any) error }

func scanSource(row scanner) (Source, error) {
	var v Source
	e := row.Scan(&v.ID, &v.Name, &v.Kind, &v.Enabled, &v.ExcludePattern, &v.Status, &v.NodeCount, &v.ExcludedCount, &v.LastError, &v.LastRefreshAt, &v.NextRefreshAt, &v.CreatedAt, &v.URL, &v.Content, &v.Revision, &v.Cached)
	return v, e
}
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,name,kind,enabled,exclude_pattern,status,node_count,excluded_count,last_error,last_refresh_at,next_refresh_at,created_at,'','',revision,cached FROM np_sources ORDER BY id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		v, e := scanSource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Source(ctx context.Context, id int64) (Source, error) {
	v, e := scanSource(s.DB.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM np_sources WHERE id=?`, id))
	if e == sql.ErrNoRows {
		e = ErrNotFound
	}
	return v, e
}
func utcNow() time.Time { return time.Now().UTC() }
