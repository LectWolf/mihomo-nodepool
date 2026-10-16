package pool

import (
	"context"
	"fmt"
)

// Add only the new location fields on startup. Existing node identities, probe
// history and source memberships remain in place. The updater/rollback never
// calls this function or changes database contents.
func (s *Store) ensureLocationColumns(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_nodes' AND COLUMN_NAME IN ('region','city')`)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		present[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range []string{"region", "city"} {
		if !present[name] {
			if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_nodes ADD COLUMN `+name+` VARCHAR(128) NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("添加节点地区字段 %s 失败（需要 ALTER 权限）: %w", name, err)
			}
		}
	}
	return nil
}

func (s *Store) ensureNodeProbeColumns(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_nodes' AND COLUMN_NAME IN ('last_success_at','blocked')`)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		present[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, statement := range []struct{ name, sql string }{
		{"last_success_at", `ALTER TABLE np_nodes ADD COLUMN last_success_at DATETIME(6) NULL`},
		{"blocked", `ALTER TABLE np_nodes ADD COLUMN blocked BOOLEAN NOT NULL DEFAULT 0`},
	} {
		if !present[statement.name] {
			if _, err := s.DB.ExecContext(ctx, statement.sql); err != nil {
				return fmt.Errorf("添加节点检测字段 %s 失败（需要 ALTER 权限）: %w", statement.name, err)
			}
		}
	}
	// Older schemas have no success timestamp. Seed healthy nodes from their
	// last completed probe; all other nodes use creation time as their first
	// success baseline so startup does not immediately block the whole pool.
	if _, err := s.DB.ExecContext(ctx, `UPDATE np_nodes SET last_success_at=CASE WHEN last_checked_at IS NOT NULL AND status='healthy' THEN last_checked_at ELSE created_at END WHERE last_success_at IS NULL`); err != nil {
		return fmt.Errorf("回填节点首次成功检测时间失败: %w", err)
	}
	return nil
}
