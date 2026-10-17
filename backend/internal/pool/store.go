package pool

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
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
 region VARCHAR(128) NOT NULL DEFAULT '', city VARCHAR(128) NOT NULL DEFAULT '',
 last_checked_at DATETIME(6) NULL, last_success_at DATETIME(6) NULL, exit_checked_at DATETIME(6) NULL, next_check_at DATETIME(6) NOT NULL,
 last_error TEXT NOT NULL, created_at DATETIME(6) NOT NULL,
 failures INT NOT NULL DEFAULT 0, successes INT NOT NULL DEFAULT 0,
 pending BOOLEAN NOT NULL DEFAULT 0, lease_until DATETIME(6) NULL, blocked BOOLEAN NOT NULL DEFAULT 0,
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
	if err := s.ensureLocationColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureNodeProbeColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureMultiuser(ctx); err != nil {
		return err
	}
	if err := s.ensureCommunity(ctx); err != nil {
		return err
	}
	if err := s.ensureRuleGroups(ctx); err != nil {
		return err
	}
	if err := s.ensureEconomy(ctx); err != nil {
		return err
	}
	if err := s.ensureIdentity(ctx); err != nil {
		return err
	}
	if err := s.ensureModeration(ctx); err != nil {
		return err
	}
	if err := s.ensureFollow(ctx); err != nil {
		return err
	}
	if err := s.ensureEncryption(ctx); err != nil {
		return err
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
	if err == nil {
		c, m, l := v.Community, v.Maintenance, v.Login
		communityCache.Store(&c)
		maintenanceCache.Store(&m)
		loginCache.Store(&l)
	}
	return v, err
}
func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	if v.SubscriptionLeaseDays == 0 {
		v.SubscriptionLeaseDays = 14
	}
	if v.HistoryRetentionDays == 0 {
		v.HistoryRetentionDays = 30
	}
	if len(v.Community.LevelQuotas) == 0 {
		// 旧版前端不提交社区设置时保留已保存的值
		current, err := s.Settings(ctx)
		if err != nil {
			return err
		}
		v.Community = current.Community
	}
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
	old := Defaults()
	_ = json.Unmarshal(oldRaw, &old)
	if v.Login.EmailSuffixes == nil {
		// 未提交登录设置时保留原值
		v.Login = old.Login
	} else {
		if e = v.Login.normalize(); e != nil {
			return e
		}
		if e = v.Login.mergeSecrets(old.Login); e != nil {
			return e
		}
	}
	v.Maintenance.Message = strings.TrimSpace(v.Maintenance.Message)
	if !v.Maintenance.Enabled {
		v.Maintenance.Since = nil
	} else if old.Maintenance.Enabled && old.Maintenance.Since != nil {
		v.Maintenance.Since = old.Maintenance.Since
	} else {
		now := utcNow()
		v.Maintenance.Since = &now
	}
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
	if err := tx.Commit(); err != nil {
		return err
	}
	c, m, l := v.Community, v.Maintenance, v.Login
	communityCache.Store(&c)
	maintenanceCache.Store(&m)
	loginCache.Store(&l)
	return nil
}

const sourceBaseColumns = `id,name,kind,enabled,exclude_pattern,status,node_count,excluded_count,last_error,last_refresh_at,next_refresh_at,created_at`
const sourceOwnerColumns = `revision,cached,owner_id,max_users,(SELECT COUNT(*) FROM np_source_claims c WHERE c.source_id=np_sources.id),(SELECT COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.source_id=np_sources.id AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6)),COALESCE((SELECT display_name FROM np_users u WHERE u.id=np_sources.owner_id),''),min_level,COALESCE(share_token,''),subscription_usage,(SELECT COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.source_id=np_sources.id AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6)),COALESCE((SELECT username FROM np_users u WHERE u.id=np_sources.owner_id),''),COALESCE((SELECT avatar_version FROM np_users u WHERE u.id=np_sources.owner_id),0),COALESCE((SELECT title FROM np_users u WHERE u.id=np_sources.owner_id),''),COALESCE((SELECT role FROM np_users u WHERE u.id=np_sources.owner_id),''),takedown_reason,takedown_detail,takedown_at,COALESCE((SELECT COALESCE(NULLIF(u.display_name,''),u.username) FROM np_users u WHERE u.id=np_sources.takedown_by),'')`
const sourceColumns = sourceBaseColumns + `,url,content,` + sourceOwnerColumns
const publicSourceColumns = sourceBaseColumns + `,'','',` + sourceOwnerColumns

type scanner interface{ Scan(...any) error }

func scanSource(row scanner) (Source, error) {
	var v Source
	var usage []byte
	var takeReason sql.NullString
	var takeDetail, takeBy string
	var takeAt sql.NullTime
	e := row.Scan(&v.ID, &v.Name, &v.Kind, &v.Enabled, &v.ExcludePattern, &v.Status, &v.NodeCount, &v.ExcludedCount, &v.LastError, &v.LastRefreshAt, &v.NextRefreshAt, &v.CreatedAt, &v.URL, &v.Content, &v.Revision, &v.Cached, &v.OwnerID, &v.MaxUsers, &v.ClaimedUsers, &v.ActiveUsers, &v.ProviderName, &v.MinLevel, &v.ShareToken, &usage, &v.OccupiedUsers, &v.ProviderUsername, &v.ProviderAvatar, &v.ProviderTitle, &v.ProviderRole, &takeReason, &takeDetail, &takeAt, &takeBy)
	if e == nil && takeAt.Valid {
		v.Takedown = &SourceTakedown{Reason: takeReason.String, ReasonLabel: takedownLabel(takeReason.String), Detail: takeDetail, At: takeAt.Time, By: takeBy}
	}
	if e == nil && len(usage) > 0 {
		e = json.Unmarshal(usage, &v.SubscriptionUsage)
	}
	if e == nil {
		v.URL, e = unseal(purposeSourceURL, v.URL)
	}
	if e == nil {
		v.Content, e = unseal(purposeSourceContent, v.Content)
	}
	refreshUsageStatus(v.SubscriptionUsage)
	return v, e
}
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	u := CurrentUser(ctx)
	where := ""
	args := []any{}
	if u.Role != "admin" {
		where = " WHERE owner_id=?"
		args = append(args, u.ID)
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT `+publicSourceColumns+` FROM np_sources`+where+` ORDER BY id DESC`, args...)
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
		if v.ShareToken != "" {
			v.ShareURL = "/link/" + v.ShareToken
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return out, s.attachSourceFeedback(ctx, out)
}
func (s *Store) Source(ctx context.Context, id int64) (Source, error) {
	v, e := scanSource(s.DB.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM np_sources WHERE id=?`, id))
	if e == sql.ErrNoRows {
		e = ErrNotFound
	}
	return v, e
}
func utcNow() time.Time { return time.Now().UTC() }
