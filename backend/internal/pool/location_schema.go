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
