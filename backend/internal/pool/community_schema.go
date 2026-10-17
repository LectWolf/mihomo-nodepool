package pool

import (
	"context"
	"fmt"
)

func (s *Store) ensureCommunity(ctx context.Context) error {
	columns := []struct{ table, name, definition string }{
		{"np_users", "xp", "BIGINT NOT NULL DEFAULT 0"},
		{"np_users", "points", "BIGINT NOT NULL DEFAULT 0"},
		{"np_users", "checkin_on", "VARCHAR(10) NOT NULL DEFAULT ''"},
		{"np_users", "checkin_streak", "INT NOT NULL DEFAULT 0"},
		{"np_users", "subscription_expires_at", "DATETIME(6) NULL"},
		{"np_users", "probe_uuid", "CHAR(36) NULL UNIQUE"},
		{"np_sources", "min_level", "INT NOT NULL DEFAULT 0"},
		{"np_sources", "share_token", "CHAR(64) NULL UNIQUE"},
		{"np_sources", "subscription_usage", "JSON NULL"},
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
		`UPDATE np_users SET subscription_expires_at=DATE_ADD(UTC_TIMESTAMP(6), INTERVAL 14 DAY) WHERE subscription_expires_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS np_source_seats (
 source_id BIGINT NOT NULL, user_id BIGINT NOT NULL, state VARCHAR(16) NOT NULL DEFAULT 'active',
 claimed_at DATETIME(6) NOT NULL, expires_at DATETIME(6) NOT NULL, last_pull_at DATETIME(6) NULL,
 last_renewed_at DATETIME(6) NULL, renew_method VARCHAR(16) NOT NULL DEFAULT 'claim', ended_at DATETIME(6) NULL,
 PRIMARY KEY(source_id,user_id), INDEX seat_expiry(state,expires_at),
 FOREIGN KEY(source_id) REFERENCES np_sources(id) ON DELETE CASCADE,
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_source_events (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, source_id BIGINT NOT NULL, owner_id BIGINT NOT NULL,
 source_name VARCHAR(200) NOT NULL, user_id BIGINT NOT NULL, action VARCHAR(24) NOT NULL,
 created_at DATETIME(6) NOT NULL, INDEX(source_id,created_at), INDEX(created_at)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
		`CREATE TABLE IF NOT EXISTS np_subscription_archives (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL, subscription_id BIGINT NOT NULL,
 name VARCHAR(200) NOT NULL, nodes JSON NOT NULL, source_ids JSON NOT NULL, node_details JSON NULL, rule_profile_id BIGINT NULL,
 reason VARCHAR(24) NOT NULL, archived_at DATETIME(6) NOT NULL, INDEX(user_id,archived_at), INDEX(archived_at),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
		`INSERT IGNORE INTO np_source_seats(source_id,user_id,state,claimed_at,expires_at,ended_at)
 SELECT c.source_id,c.user_id,IF(EXISTS(SELECT 1 FROM np_subscriptions sub WHERE sub.user_id=c.user_id AND JSON_CONTAINS(sub.source_ids,CAST(c.source_id AS CHAR))),'active','released'),c.created_at,u.subscription_expires_at,
 IF(EXISTS(SELECT 1 FROM np_subscriptions sub WHERE sub.user_id=c.user_id AND JSON_CONTAINS(sub.source_ids,CAST(c.source_id AS CHAR))),NULL,UTC_TIMESTAMP(6))
 FROM np_source_claims c JOIN np_users u ON u.id=c.user_id`,
	}
	for _, query := range statements {
		if _, err := s.DB.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	var archiveDetails int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_subscription_archives' AND COLUMN_NAME='node_details'`).Scan(&archiveDetails); err != nil {
		return err
	}
	if archiveDetails == 0 {
		if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_subscription_archives ADD COLUMN node_details JSON NULL`); err != nil {
			return err
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM np_sources WHERE share_token IS NULL`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		token, err := newToken()
		if err != nil {
			return err
		}
		if _, err = s.DB.ExecContext(ctx, `UPDATE np_sources SET share_token=? WHERE id=? AND share_token IS NULL`, token, id); err != nil {
			return err
		}
	}
	return nil
}
