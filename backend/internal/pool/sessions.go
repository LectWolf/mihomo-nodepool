package pool

import (
	"context"
	"time"
)

// SessionRecord is the durable part of a login session; tokens are stored as SHA-256 digests.
type SessionRecord struct {
	UserID     int64
	Version    int64
	Expires    time.Time
	Persistent bool
}

func (s *Store) SaveSession(ctx context.Context, key [32]byte, v SessionRecord) error {
	if err := s.Ensure(ctx); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO np_sessions(token_hash,user_id,auth_version,persistent,expires_at,created_at) VALUES(?,?,?,?,?,?)
 ON DUPLICATE KEY UPDATE auth_version=VALUES(auth_version),persistent=VALUES(persistent),expires_at=VALUES(expires_at)`, key[:], v.UserID, v.Version, v.Persistent, v.Expires.UTC(), utcNow())
	return err
}

func (s *Store) LoadSession(ctx context.Context, key [32]byte) (SessionRecord, error) {
	var v SessionRecord
	if err := s.Ensure(ctx); err != nil {
		return v, err
	}
	err := s.DB.QueryRowContext(ctx, `SELECT user_id,auth_version,persistent,expires_at FROM np_sessions WHERE token_hash=? AND expires_at>UTC_TIMESTAMP(6)`, key[:]).Scan(&v.UserID, &v.Version, &v.Persistent, &v.Expires)
	return v, err
}

func (s *Store) DeleteSession(ctx context.Context, key [32]byte) error {
	if err := s.Ensure(ctx); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM np_sessions WHERE token_hash=?`, key[:])
	return err
}

func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM np_sessions WHERE expires_at<=UTC_TIMESTAMP(6)`)
	return err
}
