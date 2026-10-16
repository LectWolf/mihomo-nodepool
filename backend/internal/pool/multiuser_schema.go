package pool

import (
	"context"
	"fmt"
	"strings"
)

var multiuserSchema = []string{
	`CREATE TABLE IF NOT EXISTS np_users (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, username VARCHAR(64) NOT NULL UNIQUE, display_name VARCHAR(200) NOT NULL,
 role VARCHAR(16) NOT NULL DEFAULT 'user', enabled BOOLEAN NOT NULL DEFAULT 1,
 password_hash VARCHAR(255) NOT NULL, auth_version BIGINT NOT NULL DEFAULT 1,
 node_limit INT NOT NULL DEFAULT 200, source_limit INT NOT NULL DEFAULT 3, subscription_limit INT NOT NULL DEFAULT 1,
 created_at DATETIME(6) NOT NULL
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`INSERT IGNORE INTO np_users(id,username,display_name,role,password_hash,created_at) VALUES(1,'admin','管理员','admin','',UTC_TIMESTAMP(6))`,
	`CREATE TABLE IF NOT EXISTS np_follows (
 user_id BIGINT NOT NULL, provider_id BIGINT NOT NULL, PRIMARY KEY(user_id,provider_id), INDEX(provider_id),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE,
 FOREIGN KEY(provider_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS np_source_claims (
 source_id BIGINT NOT NULL, user_id BIGINT NOT NULL, created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(source_id,user_id), FOREIGN KEY(source_id) REFERENCES np_sources(id) ON DELETE CASCADE,
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS np_subscriptions (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL, name VARCHAR(200) NOT NULL,
 token CHAR(64) NOT NULL UNIQUE, nodes JSON NOT NULL, source_ids JSON NOT NULL,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 INDEX(user_id), FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
}

func (s *Store) ensureMultiuser(ctx context.Context) error {
	for _, q := range multiuserSchema {
		if _, e := s.DB.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	var count int
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_sources' AND COLUMN_NAME='max_users'`).Scan(&count); e != nil {
		return e
	}
	if count == 0 {
		if _, e := s.DB.ExecContext(ctx, `ALTER TABLE np_sources ADD COLUMN max_users INT NOT NULL DEFAULT 0`); e != nil {
			return e
		}
	}
	// Install the composite constraint before removing the old global constraint;
	// existing IDs, memberships, cached contents and probe data remain untouched.
	rows, e := s.DB.QueryContext(ctx, `SELECT INDEX_NAME,GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_sources' AND NON_UNIQUE=0 GROUP BY INDEX_NAME`)
	if e != nil {
		return e
	}
	var old []string
	composite := false
	for rows.Next() {
		var name, cols string
		if e = rows.Scan(&name, &cols); e != nil {
			rows.Close()
			return e
		}
		if cols == "url_hash" {
			old = append(old, name)
		}
		if cols == "owner_id,url_hash" {
			composite = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if !composite {
		if _, e = s.DB.ExecContext(ctx, `ALTER TABLE np_sources ADD UNIQUE KEY source_owner_url(owner_id,url_hash)`); e != nil {
			return e
		}
	}
	for _, name := range old {
		if _, e = s.DB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE np_sources DROP INDEX `%s`", strings.ReplaceAll(name, "`", "``"))); e != nil {
			return e
		}
	}
	return nil
}
