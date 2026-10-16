package pool

import "context"

// ensureRuleGroups adds nullable storage without rewriting historical profiles.
// SQL NULL and JSON null both retain the original seven-group configuration.
func (s *Store) ensureRuleGroups(ctx context.Context) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_rule_profiles' AND COLUMN_NAME='groups'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := s.DB.ExecContext(ctx, "ALTER TABLE np_rule_profiles ADD COLUMN `groups` JSON NULL")
		return err
	}
	return nil
}
