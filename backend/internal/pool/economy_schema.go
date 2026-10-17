package pool

import (
	"context"
	"fmt"
)

func (s *Store) ensureEconomy(ctx context.Context) error {
	columns := []struct{ table, name, definition string }{
		{"np_users", "title", "VARCHAR(48) NOT NULL DEFAULT ''"},
		{"np_users", "avatar", "MEDIUMBLOB NULL"},
		{"np_users", "avatar_type", "VARCHAR(32) NOT NULL DEFAULT ''"},
		{"np_users", "avatar_version", "INT NOT NULL DEFAULT 0"},
		{"np_users", "risk_frozen_until", "DATETIME(6) NULL"},
		{"np_sources", "healthy_since", "DATETIME(6) NULL"},
		{"np_nodes", "orphaned_at", "DATETIME(6) NULL"},
	}
	for _, column := range columns {
		var count int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, column.table, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := s.DB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.name, column.definition)); err != nil {
				return err
			}
		}
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS np_ledger (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL, kind VARCHAR(24) NOT NULL,
 xp INT NOT NULL DEFAULT 0, points INT NOT NULL DEFAULT 0, ref_id BIGINT NOT NULL DEFAULT 0,
 note VARCHAR(200) NOT NULL DEFAULT '', uniq VARCHAR(96) NULL UNIQUE, created_at DATETIME(6) NOT NULL,
 INDEX(user_id,created_at), INDEX(kind,created_at),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
		`CREATE TABLE IF NOT EXISTS np_daily_jobs (
 kind VARCHAR(24) NOT NULL, day VARCHAR(10) NOT NULL, created_at DATETIME(6) NOT NULL, PRIMARY KEY(kind,day)
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_quota_boosts (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL, kind VARCHAR(16) NOT NULL, amount INT NOT NULL,
 expires_at DATETIME(6) NOT NULL, created_at DATETIME(6) NOT NULL, INDEX(user_id,expires_at),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_user_titles (
 user_id BIGINT NOT NULL, title VARCHAR(48) NOT NULL, created_at DATETIME(6) NOT NULL, PRIMARY KEY(user_id,title),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
		`CREATE TABLE IF NOT EXISTS np_node_exposures (
 user_id BIGINT NOT NULL, node_id BIGINT NOT NULL, first_at DATETIME(6) NOT NULL, last_at DATETIME(6) NOT NULL,
 PRIMARY KEY(user_id,node_id), INDEX(user_id,last_at),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_pull_stats (
 user_id BIGINT NOT NULL, day VARCHAR(10) NOT NULL, ip_hash CHAR(16) NOT NULL, pulls INT NOT NULL DEFAULT 0,
 last_at DATETIME(6) NOT NULL, PRIMARY KEY(user_id,day,ip_hash), INDEX(day),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_sessions (
 token_hash BINARY(32) PRIMARY KEY, user_id BIGINT NOT NULL, auth_version BIGINT NOT NULL, persistent TINYINT(1) NOT NULL DEFAULT 0,
 expires_at DATETIME(6) NOT NULL, created_at DATETIME(6) NOT NULL, INDEX(expires_at), INDEX(user_id),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_source_hourly (
 source_id BIGINT NOT NULL, hour DATETIME NOT NULL, users INT NOT NULL DEFAULT 0, nodes INT NOT NULL DEFAULT 0, healthy INT NOT NULL DEFAULT 0,
 used_bytes BIGINT NULL, PRIMARY KEY(source_id,hour), INDEX(hour),
 FOREIGN KEY(source_id) REFERENCES np_sources(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_feedback (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, target VARCHAR(8) NOT NULL, node_id BIGINT NOT NULL DEFAULT 0,
 source_id BIGINT NOT NULL, user_id BIGINT NOT NULL, kind VARCHAR(16) NOT NULL, comment VARCHAR(600) NOT NULL DEFAULT '',
 probe_status VARCHAR(16) NOT NULL DEFAULT '', probe_latency INT NULL, usage_days INT NOT NULL DEFAULT 0, user_level INT NOT NULL DEFAULT 0,
 created_at DATETIME(6) NOT NULL, resolved_at DATETIME(6) NULL, resolved_by BIGINT NULL,
 INDEX(node_id,created_at), INDEX(source_id,created_at), INDEX(user_id,created_at),
 FOREIGN KEY(source_id) REFERENCES np_sources(id) ON DELETE CASCADE,
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	}
	for _, query := range statements {
		if _, err := s.DB.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	// 旧版默认额度 200/3/1 实际等同于跟随等级，一次性改为 0 以免固定个人额度
	res, err := s.DB.ExecContext(ctx, `INSERT IGNORE INTO np_daily_jobs(kind,day,created_at) VALUES('migrate','limits-v1',?)`, utcNow())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		if _, err = s.DB.ExecContext(ctx, `UPDATE np_users SET node_limit=0,source_limit=0,subscription_limit=0 WHERE node_limit=200 AND source_limit=3 AND subscription_limit=1`); err != nil {
			return err
		}
	}
	// 每小时拉取次数旧默认 60 调整为 120，只改仍为旧默认值的站点
	res, err = s.DB.ExecContext(ctx, `INSERT IGNORE INTO np_daily_jobs(kind,day,created_at) VALUES('migrate','pulls-v1',?)`, utcNow())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		if _, err = s.DB.ExecContext(ctx, `UPDATE np_settings SET body=JSON_SET(body,'$.community.risk_pulls_per_hour',120) WHERE id=1 AND JSON_VALUE(body,'$.community.risk_pulls_per_hour')=60`); err != nil {
			return err
		}
	}
	return nil
}
