package pool

import (
	"context"
	"database/sql"
	"fmt"
)

// AutoPauseReasonNoHealthy 来源长期没有可用节点被自动停用的原因
const AutoPauseReasonNoHealthy = "no_healthy_nodes"

// healthyNodes 统计来源下未排除、未屏蔽且检测健康的节点
const healthyNodes = `(SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=np_sources.id AND sn.excluded=0 AND n.blocked=0 AND n.status='healthy')`

// AutoPauseDeadSources 按站点设置扫描长期没有可用节点的来源，返回本次自动停用的数量。
// 只有所有者主动开启「自动停用」后，自己的来源才会被处理；站点把天数设为 0 时整体关闭。
func (s *Store) AutoPauseDeadSources(ctx context.Context) (int, error) {
	settings, e := s.Settings(ctx)
	if e != nil {
		return 0, e
	}
	return s.SweepDeadSources(ctx, settings.SourceAutoPauseDays)
}

// SweepDeadSources 维护来源的 dead_since：第一次发现没有可用节点时记下时间，
// 恢复可用后清空；连续 days 天仍然没有可用节点就停用并记录原因。
func (s *Store) SweepDeadSources(ctx context.Context, days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	now := utcNow()
	cutoff := now.AddDate(0, 0, -days)
	rows, e := s.DB.QueryContext(ctx, `SELECT np_sources.id, `+healthyNodes+`>0, np_sources.dead_since FROM np_sources WHERE np_sources.enabled=1 AND np_sources.takedown_at IS NULL AND np_sources.created_at<=? AND EXISTS(SELECT 1 FROM np_users u WHERE u.id=np_sources.owner_id AND u.auto_pause_dead_sources=1)`, cutoff)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	type candidate struct {
		id        int64
		healthy   bool
		deadSince sql.NullTime
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if e = rows.Scan(&c.id, &c.healthy, &c.deadSince); e != nil {
			return 0, e
		}
		candidates = append(candidates, c)
	}
	if e = rows.Err(); e != nil {
		return 0, e
	}
	pause := []int64{}
	for _, c := range candidates {
		switch {
		case c.healthy && c.deadSince.Valid:
			if _, e = s.DB.ExecContext(ctx, `UPDATE np_sources SET dead_since=NULL WHERE id=?`, c.id); e != nil {
				return 0, e
			}
		case !c.healthy && !c.deadSince.Valid:
			if _, e = s.DB.ExecContext(ctx, `UPDATE np_sources SET dead_since=? WHERE id=?`, now, c.id); e != nil {
				return 0, e
			}
		case !c.healthy && c.deadSince.Valid && !c.deadSince.Time.After(cutoff):
			pause = append(pause, c.id)
		}
	}
	if len(pause) == 0 {
		return 0, nil
	}
	if e = s.pauseDeadSources(ctx, pause, days); e != nil {
		return 0, e
	}
	return len(pause), nil
}

// pauseDeadSources 停用来源：保留数据与内容，所有者重新启用后照常重新刷新或解析
func (s *Store) pauseDeadSources(ctx context.Context, ids []int64, days int) error {
	message := fmt.Sprintf("连续 %d 天没有可用节点，已自动停用", days)
	args := make([]any, 0, len(ids)+3)
	args = append(args, utcNow(), AutoPauseReasonNoHealthy, message)
	for _, id := range ids {
		args = append(args, id)
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE np_sources SET enabled=0,auto_paused_at=?,auto_pause_reason=?,pending=0,lease_until=NULL,next_refresh_at=NULL,revision=revision+1,status='error',last_error=? WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if e != nil {
		return e
	}
	s.InvalidateIndex()
	s.rehomeSources(ctx, ids...)
	return nil
}
