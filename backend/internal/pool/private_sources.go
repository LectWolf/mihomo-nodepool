package pool

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 私有来源：只有提供者本人和被分享的用户能看到、选用；不进入公开目录，不计分享奖励，不占使用者的来源额度
const MaxSourceShares = 200

type SourceShare struct {
	UserID      int64     `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Avatar      int       `json:"avatar_version"`
	Enabled     bool      `json:"enabled"`
	Seated      bool      `json:"seated"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) ensurePrivateSources(ctx context.Context) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_sources' AND COLUMN_NAME='private'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_sources ADD COLUMN private TINYINT(1) NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS np_source_shares (
 source_id BIGINT NOT NULL, user_id BIGINT NOT NULL, created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(source_id,user_id), INDEX(user_id),
 FOREIGN KEY(source_id) REFERENCES np_sources(id) ON DELETE CASCADE,
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`)
	return err
}

// accessSQL 私有来源的可见条件，参数为 (用户ID, 用户ID)
func accessSQL(alias string) string {
	return "(" + alias + ".private=0 OR " + alias + ".owner_id=? OR EXISTS(SELECT 1 FROM np_source_shares sh WHERE sh.source_id=" + alias + ".id AND sh.user_id=?))"
}

// canAccessSource 事务内判断用户能否使用私有来源
func canAccessSource(ctx context.Context, q execer, u User, sourceID, owner int64, private bool) (bool, error) {
	if !private || u.ID == owner {
		return true, nil
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_shares WHERE source_id=? AND user_id=?`, sourceID, u.ID).Scan(&n)
	return n > 0, err
}

func (s *Store) shareOwner(ctx context.Context, id int64) (int64, error) {
	var owner int64
	err := s.DB.QueryRowContext(ctx, `SELECT owner_id FROM np_sources WHERE id=?`, id).Scan(&owner)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if u := CurrentUser(ctx); u.ID != owner && u.Role != "admin" {
		return 0, ErrForbidden
	}
	return owner, nil
}

func (s *Store) SourceShares(ctx context.Context, id int64) ([]SourceShare, error) {
	if _, err := s.shareOwner(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id,u.username,u.display_name,u.avatar_version,u.enabled,
 EXISTS(SELECT 1 FROM np_source_seats st WHERE st.source_id=sh.source_id AND st.user_id=u.id AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6)),sh.created_at
 FROM np_source_shares sh JOIN np_users u ON u.id=sh.user_id WHERE sh.source_id=? ORDER BY sh.created_at DESC,u.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceShare{}
	for rows.Next() {
		var v SourceShare
		if err = rows.Scan(&v.UserID, &v.Username, &v.DisplayName, &v.Avatar, &v.Enabled, &v.Seated, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AddSourceShare 按用户 ID 或用户名分享；分享对象无需等级要求
func (s *Store) AddSourceShare(ctx context.Context, id int64, ref string) (SourceShare, error) {
	owner, err := s.shareOwner(ctx, id)
	if err != nil {
		return SourceShare{}, err
	}
	ref = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ref), "@"))
	if ref == "" {
		return SourceShare{}, errors.New("请输入用户 ID 或用户名")
	}
	var v SourceShare
	query := `SELECT id,username,display_name,avatar_version,enabled FROM np_users WHERE username=?`
	args := []any{ref}
	if uid, e := strconv.ParseInt(strings.TrimPrefix(ref, "#"), 10, 64); e == nil && uid > 0 {
		query = `SELECT id,username,display_name,avatar_version,enabled FROM np_users WHERE id=? OR username=? ORDER BY id=? DESC LIMIT 1`
		args = []any{uid, ref, uid}
	}
	if err = s.DB.QueryRowContext(ctx, query, args...).Scan(&v.UserID, &v.Username, &v.DisplayName, &v.Avatar, &v.Enabled); err == sql.ErrNoRows {
		return v, errors.New("找不到该用户，请核对用户 ID")
	} else if err != nil {
		return v, err
	}
	if v.UserID == owner {
		return v, errors.New("来源提供者本人无需分享")
	}
	if !v.Enabled {
		return v, errors.New("该用户已被停用")
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_shares WHERE source_id=?`, id).Scan(&count); err != nil {
		return v, err
	}
	if count >= MaxSourceShares {
		return v, errors.New("每个来源最多分享给 " + strconv.Itoa(MaxSourceShares) + " 位用户")
	}
	v.CreatedAt = utcNow()
	res, err := s.DB.ExecContext(ctx, `INSERT IGNORE INTO np_source_shares(source_id,user_id,created_at) VALUES(?,?,?)`, id, v.UserID, v.CreatedAt)
	if err != nil {
		return v, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return v, errors.New("已分享给该用户")
	}
	return v, nil
}

// RemoveSourceShare 取消分享后对方的订阅不再输出该来源节点，并尝试换到其他来源
func (s *Store) RemoveSourceShare(ctx context.Context, id, userID int64) error {
	if _, err := s.shareOwner(ctx, id); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM np_source_shares WHERE source_id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	var private bool
	if err = s.DB.QueryRowContext(ctx, `SELECT private FROM np_sources WHERE id=?`, id).Scan(&private); err != nil || !private {
		return err
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE np_source_seats SET state='released',ended_at=UTC_TIMESTAMP(6) WHERE source_id=? AND user_id=? AND state='active'`, id, userID); err != nil {
		return err
	}
	resetRehome()
	_, _ = s.RehomeUser(ctx, userID)
	return nil
}

// releaseUnsharedSeats 来源改为私有后，释放未被分享用户的席位并为他们切换来源
func (s *Store) releaseUnsharedSeats(ctx context.Context, id int64) error {
	users, err := sourceUsers(ctx, s.DB, []int64{id})
	if err != nil {
		return err
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE np_source_seats st JOIN np_sources s ON s.id=st.source_id SET st.state='released',st.ended_at=UTC_TIMESTAMP(6)
 WHERE st.source_id=? AND st.state='active' AND st.user_id<>s.owner_id AND NOT EXISTS(SELECT 1 FROM np_source_shares sh WHERE sh.source_id=st.source_id AND sh.user_id=st.user_id)`, id); err != nil {
		return err
	}
	resetRehome()
	s.rehomeUsers(ctx, users)
	return nil
}
