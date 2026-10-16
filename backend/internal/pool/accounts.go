package pool

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ErrForbidden = errors.New("没有权限执行此操作")
var ErrQuota = errors.New("超过账号额度或来源领取人数上限")

type User struct {
	ID                    int64      `json:"id"`
	Username              string     `json:"username"`
	DisplayName           string     `json:"display_name"`
	Role                  string     `json:"role"`
	Enabled               bool       `json:"enabled"`
	NodeLimit             int        `json:"node_limit"`
	SourceLimit           int        `json:"source_limit"`
	SubscriptionLimit     int        `json:"subscription_limit"`
	CreatedAt             time.Time  `json:"created_at"`
	XP                    int64      `json:"xp"`
	Points                int64      `json:"points"`
	Level                 int        `json:"level"`
	CheckinOn             string     `json:"checkin_on"`
	CheckinStreak         int        `json:"checkin_streak"`
	SubscriptionExpiresAt *time.Time `json:"subscription_expires_at"`
	ProbeUUID             string     `json:"-"`
	PasswordHash          string     `json:"-"`
	AuthVersion           int64      `json:"-"`
}
type UserInput struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	DisplayName       string `json:"display_name"`
	NodeLimit         *int   `json:"node_limit"`
	SourceLimit       *int   `json:"source_limit"`
	SubscriptionLimit *int   `json:"subscription_limit"`
}
type UserPatch struct {
	DisplayName       *string `json:"display_name"`
	Password          *string `json:"password"`
	Enabled           *bool   `json:"enabled"`
	NodeLimit         *int    `json:"node_limit"`
	SourceLimit       *int    `json:"source_limit"`
	SubscriptionLimit *int    `json:"subscription_limit"`
	XP                *int64  `json:"xp"`
	Points            *int64  `json:"points"`
}
type Usage struct {
	Nodes         int `json:"nodes"`
	Sources       int `json:"sources"`
	Subscriptions int `json:"subscriptions"`
}
type userContextKey struct{}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userContextKey{}, u)
}
func CurrentUser(ctx context.Context) User {
	if u, ok := ctx.Value(userContextKey{}).(User); ok {
		return u
	}
	return User{ID: 1, Username: "admin", Role: "admin", Enabled: true}
}

const userColumns = `id,username,display_name,role,enabled,node_limit,source_limit,subscription_limit,created_at,password_hash,auth_version,xp,points,checkin_on,checkin_streak,subscription_expires_at,COALESCE(probe_uuid,'')`

func scanUser(row scanner) (User, error) {
	var u User
	e := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &u.NodeLimit, &u.SourceLimit, &u.SubscriptionLimit, &u.CreatedAt, &u.PasswordHash, &u.AuthVersion, &u.XP, &u.Points, &u.CheckinOn, &u.CheckinStreak, &u.SubscriptionExpiresAt, &u.ProbeUUID)
	u.Level = LevelForXP(u.XP)
	return u, e
}
func (s *Store) User(ctx context.Context, id int64) (User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=?`, id))
}
func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE username=?`, name))
}

// Prehash allows the existing 8–1024 byte administrator passwords without bcrypt's 72-byte truncation.
func passwordBytes(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	return []byte(base64.RawStdEncoding.EncodeToString(sum[:]))
}
func HashPassword(password string) (string, error) {
	if len(password) < 8 || len(password) > 1024 {
		return "", errors.New("密码须为 8–1024 字节")
	}
	b, e := bcrypt.GenerateFromPassword(passwordBytes(password), bcrypt.DefaultCost)
	return string(b), e
}
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), passwordBytes(password)) == nil
}
func (s *Store) BootstrapAdmin(ctx context.Context, hash string) error {
	if e := s.Ensure(ctx); e != nil {
		return e
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE np_users SET password_hash=? WHERE id=1 AND password_hash=''`, hash)
	return e
}
func validateLimits(u User) error {
	if u.XP < 0 || u.Points < 0 || u.XP > 1000000000 || u.Points > 1000000000 {
		return errors.New("经验和积分须为0–1000000000")
	}
	if u.NodeLimit < 1 || u.SourceLimit < 1 || u.SubscriptionLimit < 1 {
		return errors.New("额度须为正整数")
	}
	if len(u.DisplayName) > 200 {
		return errors.New("显示名称最多200字节")
	}
	return nil
}
func (s *Store) CreateUser(ctx context.Context, in UserInput) (User, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`).MatchString(in.Username) {
		return User{}, errors.New("用户名须为1–64位字母、数字、点、下划线或横线")
	}
	u := User{Username: in.Username, DisplayName: strings.TrimSpace(in.DisplayName), NodeLimit: 200, SourceLimit: 3, SubscriptionLimit: 1}
	if u.DisplayName == "" {
		u.DisplayName = u.Username
	}
	if in.NodeLimit != nil {
		u.NodeLimit = *in.NodeLimit
	}
	if in.SourceLimit != nil {
		u.SourceLimit = *in.SourceLimit
	}
	if in.SubscriptionLimit != nil {
		u.SubscriptionLimit = *in.SubscriptionLimit
	}
	if e := validateLimits(u); e != nil {
		return u, e
	}
	hash, e := HashPassword(in.Password)
	if e != nil {
		return u, e
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		return u, e
	}
	until := utcNow().Add(time.Duration(settings.SubscriptionLeaseDays) * 24 * time.Hour)
	res, e := s.DB.ExecContext(ctx, `INSERT INTO np_users(username,display_name,role,enabled,node_limit,source_limit,subscription_limit,created_at,password_hash,subscription_expires_at) VALUES(?,?,'user',1,?,?,?,?,?,?)`, u.Username, u.DisplayName, u.NodeLimit, u.SourceLimit, u.SubscriptionLimit, utcNow(), hash, until)
	if e != nil {
		return u, e
	}
	id, e := res.LastInsertId()
	if e != nil {
		return u, e
	}
	return s.User(ctx, id)
}
func (s *Store) PatchUser(ctx context.Context, id int64, p UserPatch) (User, error) {
	var hash string
	var e error
	if p.Password != nil {
		hash, e = HashPassword(*p.Password)
		if e != nil {
			return User{}, e
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return User{}, e
	}
	defer tx.Rollback()
	u, e := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE id=? FOR UPDATE`, id))
	if e != nil {
		return u, e
	}
	if p.DisplayName != nil {
		u.DisplayName = strings.TrimSpace(*p.DisplayName)
	}
	if p.Enabled != nil {
		if id == 1 && !*p.Enabled {
			return u, errors.New("不能停用管理员")
		}
		u.Enabled = *p.Enabled
	}
	if p.NodeLimit != nil {
		u.NodeLimit = *p.NodeLimit
	}
	if p.SourceLimit != nil {
		u.SourceLimit = *p.SourceLimit
	}
	if p.SubscriptionLimit != nil {
		u.SubscriptionLimit = *p.SubscriptionLimit
	}
	if p.XP != nil {
		u.XP = *p.XP
	}
	if p.Points != nil {
		u.Points = *p.Points
	}
	u.Level = LevelForXP(u.XP)
	if e = validateLimits(u); e != nil {
		return u, e
	}
	revoke := p.Password != nil || (p.Enabled != nil && !*p.Enabled)
	if p.Password != nil {
		u.PasswordHash = hash
	}
	if revoke {
		u.AuthVersion++
	}
	_, e = tx.ExecContext(ctx, `UPDATE np_users SET display_name=?,enabled=?,node_limit=?,source_limit=?,subscription_limit=?,password_hash=?,auth_version=?,xp=?,points=? WHERE id=?`, u.DisplayName, u.Enabled, u.NodeLimit, u.SourceLimit, u.SubscriptionLimit, u.PasswordHash, u.AuthVersion, u.XP, u.Points, id)
	if e != nil {
		return u, e
	}
	if p.Enabled != nil && !*p.Enabled { // Re-enabling must never resurrect a token issued before suspension.
		if e = releaseDisabledUserSeats(ctx, tx, id); e != nil {
			return u, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE np_users SET probe_uuid=NULL WHERE id=?`, id); e != nil {
			return u, e
		}
		rows, e := tx.QueryContext(ctx, `SELECT id FROM np_subscriptions WHERE user_id=? ORDER BY id FOR UPDATE`, id)
		if e != nil {
			return u, e
		}
		var ids []int64
		for rows.Next() {
			var sid int64
			if e = rows.Scan(&sid); e != nil {
				rows.Close()
				return u, e
			}
			ids = append(ids, sid)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return u, e
		}
		for _, sid := range ids {
			token, e := newToken()
			if e != nil {
				return u, e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE np_subscriptions SET token=? WHERE id=?`, token, sid); e != nil {
				return u, e
			}
		}
	}
	return u, tx.Commit()
}
func pagination(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		page = 1000000
	}
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	return page, size
}
func (s *Store) Users(ctx context.Context, q string, page, size int) (map[string]any, error) {
	page, size = pagination(page, size)
	items := []User{}
	var total int
	where := `username LIKE ? OR display_name LIKE ?`
	args := []any{"%" + q + "%", "%" + q + "%"}
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_users WHERE `+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE `+where+` ORDER BY id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, u)
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, rows.Err()
}
func ProviderPath(id int64) string { return fmt.Sprintf("/nodes?provider_id=%d", id) }
func (s *Store) SourcePermission(ctx context.Context, id int64) error {
	u := CurrentUser(ctx)
	if u.Role == "admin" {
		return nil
	}
	var owner int64
	if e := s.DB.QueryRowContext(ctx, `SELECT owner_id FROM np_sources WHERE id=?`, id).Scan(&owner); e != nil {
		return e
	}
	if owner != u.ID {
		return ErrForbidden
	}
	return nil
}
func (s *Store) NodePermission(ctx context.Context, id int64) error {
	if CurrentUser(ctx).Role != "admin" {
		return ErrForbidden
	}
	return nil
}
