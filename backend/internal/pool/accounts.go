package pool

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	Title                 string     `json:"title"`
	AvatarVersion         int        `json:"avatar_version"`
	RiskFrozenUntil       *time.Time `json:"risk_frozen_until,omitempty"`
	Email                 string     `json:"email"`
	EmailVerified         bool       `json:"email_verified"`
	ProbeUUID             string     `json:"-"`
	PasswordHash          string     `json:"-"`
	HasPassword           bool       `json:"has_password"`
	BeaconEnabled         bool       `json:"beacon_enabled"`
	AuthVersion           int64      `json:"-"`
}
type UserInput struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	DisplayName       string `json:"display_name"`
	NodeLimit         *int   `json:"node_limit"`
	SourceLimit       *int   `json:"source_limit"`
	SubscriptionLimit *int   `json:"subscription_limit"`
	Role              string `json:"role"`
	// NoPassword 用于第三方登录注册，用户之后自行设置密码
	NoPassword bool `json:"-"`
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
	Role              *string `json:"role"`
	Email             *string `json:"email"`
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

// GuestUser 未登录访客，只能访问只读的公开接口
var GuestUser = User{ID: 0, Username: "guest", DisplayName: "访客", Role: "guest"}

// IsGuest 判断当前请求是否来自未登录访客
func IsGuest(u User) bool { return u.ID == 0 && u.Role == "guest" }

func CurrentUser(ctx context.Context) User {
	if u, ok := ctx.Value(userContextKey{}).(User); ok {
		return u
	}
	return User{ID: 1, Username: "admin", Role: "admin", Enabled: true}
}

const userColumns = `id,username,display_name,role,enabled,node_limit,source_limit,subscription_limit,created_at,password_hash,auth_version,xp,points,checkin_on,checkin_streak,subscription_expires_at,COALESCE(probe_uuid,''),title,avatar_version,risk_frozen_until,COALESCE(email,''),email_verified_at IS NOT NULL,beacon_disabled=0`

func scanUser(row scanner) (User, error) {
	var u User
	e := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &u.NodeLimit, &u.SourceLimit, &u.SubscriptionLimit, &u.CreatedAt, &u.PasswordHash, &u.AuthVersion, &u.XP, &u.Points, &u.CheckinOn, &u.CheckinStreak, &u.SubscriptionExpiresAt, &u.ProbeUUID, &u.Title, &u.AvatarVersion, &u.RiskFrozenUntil, &u.Email, &u.EmailVerified, &u.BeaconEnabled)
	u.Level = LevelForXP(u.XP)
	u.HasPassword = u.PasswordHash != ""
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
	// 0 表示跟随等级默认额度
	if !between(u.NodeLimit, 0, 100000) || !between(u.SourceLimit, 0, 10000) || !between(u.SubscriptionLimit, 0, 1000) {
		return errors.New("单独额度须为 0（跟随等级）或节点 1–100000、来源 1–10000、订阅 1–1000")
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
	u := User{Username: in.Username, DisplayName: strings.TrimSpace(in.DisplayName), Role: "user"}
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
	if in.Role != "" {
		u.Role = in.Role
	}
	if e := checkRoleChange(CurrentUser(ctx), User{Role: "user"}, u.Role); e != nil {
		return u, e
	}
	if e := validateLimits(u); e != nil {
		return u, e
	}
	hash := ""
	if !in.NoPassword {
		var e error
		if hash, e = HashPassword(in.Password); e != nil {
			return u, e
		}
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		return u, e
	}
	until := utcNow().Add(time.Duration(settings.SubscriptionLeaseDays) * 24 * time.Hour)
	res, e := s.DB.ExecContext(ctx, `INSERT INTO np_users(username,display_name,role,enabled,node_limit,source_limit,subscription_limit,created_at,password_hash,subscription_expires_at) VALUES(?,?,?,1,?,?,?,?,?,?)`, u.Username, u.DisplayName, u.Role, u.NodeLimit, u.SourceLimit, u.SubscriptionLimit, utcNow(), hash, until)
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
	defer s.InvalidateIndex()
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
	actor := CurrentUser(ctx)
	if actor.Role == "manager" && u.Role != "user" {
		return u, fmt.Errorf("%w：用户管理员只能管理普通用户", ErrForbidden)
	}
	if p.Role != nil && *p.Role != u.Role {
		if id == 1 || id == actor.ID {
			return u, errors.New("不能修改初始管理员或自己的角色")
		}
		if e = checkRoleChange(actor, u, *p.Role); e != nil {
			return u, e
		}
		u.Role = *p.Role
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
	oldXP, oldPoints := u.XP, u.Points
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
		u.HasPassword = hash != ""
	}
	if revoke {
		u.AuthVersion++
	}
	_, e = tx.ExecContext(ctx, `UPDATE np_users SET display_name=?,role=?,enabled=?,node_limit=?,source_limit=?,subscription_limit=?,password_hash=?,auth_version=?,xp=?,points=? WHERE id=?`, u.DisplayName, u.Role, u.Enabled, u.NodeLimit, u.SourceLimit, u.SubscriptionLimit, u.PasswordHash, u.AuthVersion, u.XP, u.Points, id)
	if e != nil {
		return u, e
	}
	if p.Email != nil {
		// 管理员代为设置的邮箱视为已验证，不受后缀限制；留空表示解除绑定
		if v := strings.TrimSpace(*p.Email); v == "" {
			_, e = tx.ExecContext(ctx, `UPDATE np_users SET email=NULL,email_verified_at=NULL WHERE id=?`, id)
			u.Email, u.EmailVerified = "", false
		} else {
			if v, e = NormalizeEmail(v); e != nil {
				return u, e
			}
			if v != u.Email || !u.EmailVerified {
				_, e = tx.ExecContext(ctx, `UPDATE np_users SET email=?,email_verified_at=? WHERE id=?`, v, utcNow(), id)
				u.Email, u.EmailVerified = v, true
			}
		}
		if IsDuplicate(e) {
			return u, ErrEmailTaken
		}
		if e != nil {
			return u, e
		}
	}
	if u.XP != oldXP || u.Points != oldPoints {
		if e = logLedger(ctx, tx, id, "admin", int(u.XP-oldXP), int(u.Points-oldPoints), "管理员调整", ""); e != nil {
			return u, e
		}
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
	if e = tx.Commit(); e != nil {
		return u, e
	}
	if p.Enabled != nil && !*p.Enabled {
		// 提供方被停用后，其他人订阅里来自其来源的节点切换到别的来源
		if owned, err := ownedSources(ctx, s.DB, id); err == nil {
			s.rehomeSources(ctx, owned...)
		}
	}
	return u, nil
}

func ownedSources(ctx context.Context, q queryer, ownerID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM np_sources WHERE owner_id=?`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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

// UserFilter 用户管理列表的筛选条件；IDs 非 nil 时只在这些用户中查找
type UserFilter struct {
	Q      string
	Role   string
	Status string // enabled / disabled
	Quota  string // custom / level
	IDs    []int64
}

func (f UserFilter) where() (string, []any) {
	where := `(username LIKE ? OR display_name LIKE ? OR email LIKE ?)`
	args := []any{"%" + f.Q + "%", "%" + f.Q + "%", "%" + f.Q + "%"}
	if slices.Contains(Roles, f.Role) {
		where += ` AND role=?`
		args = append(args, f.Role)
	}
	switch f.Status {
	case "enabled":
		where += ` AND enabled=1`
	case "disabled":
		where += ` AND enabled=0`
	}
	switch f.Quota {
	case "custom":
		where += ` AND (node_limit>0 OR source_limit>0 OR subscription_limit>0)`
	case "level":
		where += ` AND node_limit=0 AND source_limit=0 AND subscription_limit=0`
	}
	if f.IDs != nil {
		if len(f.IDs) == 0 {
			return where + ` AND 1=0`, args
		}
		where += ` AND id IN (?` + strings.Repeat(",?", len(f.IDs)-1) + `)`
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	return where, args
}

// UserIDs 返回符合条件的全部用户ID，供风控等需要计算后再筛选的条件使用
func (s *Store) UserIDs(ctx context.Context, f UserFilter) ([]int64, error) {
	where, args := f.where()
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM np_users WHERE `+where+` ORDER BY id LIMIT 20000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) Users(ctx context.Context, f UserFilter, page, size int) (map[string]any, error) {
	page, size = pagination(page, size)
	items := []User{}
	var total int
	where, args := f.where()
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

// Roles: admin 拥有全部权限；manager 只管理普通用户；user 为普通账号。
var Roles = []string{"user", "manager", "admin"}

// CanManageUsers reports whether the account may open user management.
func CanManageUsers(u User) bool { return u.Role == "admin" || u.Role == "manager" }

func checkRoleChange(actor, target User, role string) error {
	if !slices.Contains(Roles, role) {
		return errors.New("角色须为 user、manager 或 admin")
	}
	if role == target.Role {
		return nil
	}
	if actor.Role != "admin" {
		return fmt.Errorf("%w：只有管理员可以设置角色", ErrForbidden)
	}
	return nil
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
