package pool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// SourceTakedown 管理员下架来源的记录，下架期间来源保持停用且提供方不能自行启用
type SourceTakedown struct {
	Reason      string    `json:"reason"`
	ReasonLabel string    `json:"reason_label"`
	Detail      string    `json:"detail"`
	At          time.Time `json:"at"`
	By          string    `json:"by"`
}

// TakedownReasons 下架原因，顺序即前端展示顺序
var TakedownReasons = []struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}{
	{"owner_request", "订阅链接所有方要求下架"},
	{"invalid", "节点长期失效或质量过差"},
	{"abuse", "违规内容或滥用"},
	{"duplicate", "重复或盗用他人来源"},
	{"risk", "存在安全风险"},
	{"other", "其他原因"},
}

func takedownLabel(key string) string {
	for _, r := range TakedownReasons {
		if r.Key == key {
			return r.Label
		}
	}
	return ""
}

var ErrTakenDown = errors.New("来源已被管理员下架")

type ModerationEvent struct {
	ID          int64     `json:"id"`
	Action      string    `json:"action"`
	Reason      string    `json:"reason"`
	ReasonLabel string    `json:"reason_label"`
	Detail      string    `json:"detail"`
	Operator    string    `json:"operator"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) ensureModeration(ctx context.Context) error {
	columns := []struct{ name, definition string }{
		{"takedown_reason", "VARCHAR(32) NULL"},
		{"takedown_detail", "VARCHAR(500) NOT NULL DEFAULT ''"},
		{"takedown_at", "DATETIME(6) NULL"},
		{"takedown_by", "BIGINT NULL"},
	}
	for _, c := range columns {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_sources' AND COLUMN_NAME=?`, c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_sources ADD COLUMN `+c.name+` `+c.definition); err != nil {
				return err
			}
		}
	}
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS np_source_moderation (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, source_id BIGINT NOT NULL, action VARCHAR(16) NOT NULL, reason VARCHAR(32) NOT NULL DEFAULT '',
 detail VARCHAR(500) NOT NULL DEFAULT '', operator_id BIGINT NOT NULL, created_at DATETIME(6) NOT NULL,
 INDEX(source_id,created_at), FOREIGN KEY(source_id) REFERENCES np_sources(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`)
	return err
}

// CanModerate 管理员可处理全部来源；用户管理员只能处理普通用户和自己的来源
func CanModerate(actor User, ownerID int64, ownerRole string) bool {
	switch actor.Role {
	case "admin":
		return true
	case "manager":
		return ownerID == actor.ID || ownerRole == "user"
	}
	return false
}

type ModerationResult struct {
	Source Source
	Owner  User
}

// ModerateSource 下架或恢复来源；下架必须选择原因，选“其他”时必须填写说明
func (s *Store) ModerateSource(ctx context.Context, id int64, action, reason, detail string) (ModerationResult, error) {
	var out ModerationResult
	actor := CurrentUser(ctx)
	detail = strings.TrimSpace(detail)
	if utf8.RuneCountInString(detail) > 500 {
		return out, errors.New("说明最多 500 字")
	}
	switch action {
	case "takedown":
		if takedownLabel(reason) == "" {
			return out, errors.New("请选择下架原因")
		}
		if reason == "other" && detail == "" {
			return out, errors.New("选择“其他原因”时请填写说明")
		}
	case "restore":
		reason = ""
	default:
		return out, errors.New("操作无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var ownerID int64
	var takenAt sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT owner_id,takedown_at FROM np_sources WHERE id=? FOR UPDATE`, id).Scan(&ownerID, &takenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, err
	}
	if out.Owner, err = scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=?`, ownerID)); err != nil {
		return out, err
	}
	if !CanModerate(actor, ownerID, out.Owner.Role) {
		return out, fmt.Errorf("%w：用户管理员只能处理普通用户的来源", ErrForbidden)
	}
	now := utcNow()
	if action == "takedown" {
		_, err = tx.ExecContext(ctx, `UPDATE np_sources SET enabled=0,takedown_reason=?,takedown_detail=?,takedown_at=?,takedown_by=?,revision=revision+1 WHERE id=?`, reason, detail, now, actor.ID, id)
	} else {
		if !takenAt.Valid {
			return out, errors.New("该来源未被下架")
		}
		_, err = tx.ExecContext(ctx, `UPDATE np_sources SET enabled=1,takedown_reason=NULL,takedown_detail='',takedown_at=NULL,takedown_by=NULL,pending=1,cached=0,status='queued',revision=revision+1 WHERE id=?`, id)
	}
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO np_source_moderation(source_id,action,reason,detail,operator_id,created_at) VALUES(?,?,?,?,?,?)`, id, action, reason, detail, actor.ID, now); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Source, err = s.Source(ctx, id)
	return out, err
}

func (s *Store) ModerationHistory(ctx context.Context, sourceID int64) ([]ModerationEvent, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id,m.action,m.reason,m.detail,COALESCE(NULLIF(u.display_name,''),u.username,''),m.created_at FROM np_source_moderation m LEFT JOIN np_users u ON u.id=m.operator_id WHERE m.source_id=? ORDER BY m.id DESC LIMIT 50`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModerationEvent{}
	for rows.Next() {
		var v ModerationEvent
		if err = rows.Scan(&v.ID, &v.Action, &v.Reason, &v.Detail, &v.Operator, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.ReasonLabel = takedownLabel(v.Reason)
		out = append(out, v)
	}
	return out, rows.Err()
}

type ModerationFilter struct {
	OwnerID int64
	Status  string // active / takedown / disabled
	Q       string
}

// ModerationSources 供管理员与用户管理员查看提供方的全部来源（含已下架）
func (s *Store) ModerationSources(ctx context.Context, f ModerationFilter, page, size int) (map[string]any, error) {
	actor := CurrentUser(ctx)
	if actor.Role != "admin" && actor.Role != "manager" {
		return nil, ErrForbidden
	}
	page, size = pagination(page, size)
	where := `1=1`
	args := []any{}
	if actor.Role == "manager" {
		where += ` AND (np_sources.owner_id=? OR EXISTS(SELECT 1 FROM np_users o WHERE o.id=np_sources.owner_id AND o.role='user'))`
		args = append(args, actor.ID)
	}
	if f.OwnerID > 0 {
		where += ` AND np_sources.owner_id=?`
		args = append(args, f.OwnerID)
	}
	switch f.Status {
	case "active":
		where += ` AND np_sources.enabled=1`
	case "takedown":
		where += ` AND np_sources.takedown_at IS NOT NULL`
	case "disabled":
		where += ` AND np_sources.enabled=0 AND np_sources.takedown_at IS NULL`
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		where += ` AND (np_sources.name LIKE ? OR EXISTS(SELECT 1 FROM np_users o WHERE o.id=np_sources.owner_id AND (o.username LIKE ? OR o.display_name LIKE ?)))`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	var total, takedowns int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(takedown_at IS NOT NULL),0) FROM np_sources WHERE `+where, args...).Scan(&total, &takedowns); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+publicSourceColumns+` FROM np_sources WHERE `+where+` ORDER BY (takedown_at IS NULL),id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Source{}
	for rows.Next() {
		v, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	reasons := TakedownReasons
	return map[string]any{"items": items, "total": total, "takedowns": takedowns, "page": page, "page_size": size, "reasons": reasons}, s.attachSourceFeedback(ctx, items)
}
