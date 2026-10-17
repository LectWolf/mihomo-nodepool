package pool

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrEmailTaken    = errors.New("该邮箱已被其他账号使用")
	ErrEmailSuffix   = errors.New("暂不支持该邮箱后缀")
	ErrEmailCode     = errors.New("验证码不正确或已过期")
	ErrCodeCooldown  = errors.New("验证码发送过于频繁，请稍后再试")
	ErrIdentityTaken = errors.New("该第三方账号已绑定其他用户")
)

const (
	emailCodeTTL      = 10 * time.Minute
	emailCodeCooldown = time.Minute
	emailCodeAttempts = 5
	purposeSecret     = "np_settings.secret"
)

// DefaultEmailSuffixes 默认允许注册的邮箱后缀
var DefaultEmailSuffixes = []string{"deepseek.com", "edu.mo", "edu.hk", "edu.tw", "foxmail.com", "163.com", "126.com", "139.com", "189.cn", "edu.cn", "gmail.com", "aliyun.com", "apache.org", "sohu.com", "privaterelay.appleid.com", "xiaomi.com", "yahoo.com", "sina.cn", "sina.com", "qq.com", "gov.cn"}

// LoginSettings 邮箱验证与第三方登录配置，密钥只写不读
type LoginSettings struct {
	RequireEmail  bool          `json:"require_email"`
	EmailSuffixes []string      `json:"email_suffixes"`
	SMTP          SMTPSettings  `json:"smtp"`
	GitHub        OAuthSettings `json:"github"`
	LinuxDo       OAuthSettings `json:"linuxdo"`
}
type SMTPSettings struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	PasswordSet bool   `json:"password_set"`
	From        string `json:"from"`
	FromName    string `json:"from_name"`
}
type OAuthSettings struct {
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	SecretSet    bool   `json:"secret_set"`
}

var loginCache atomic.Pointer[LoginSettings]

// LoginNow 读取缓存的登录设置（密钥仍为加密形式）
func (s *Store) LoginNow(ctx context.Context) LoginSettings {
	if c := loginCache.Load(); c != nil {
		return *c
	}
	if v, err := s.Settings(ctx); err == nil {
		return v.Login
	}
	return DefaultLogin()
}

func DefaultLogin() LoginSettings {
	return LoginSettings{RequireEmail: true, EmailSuffixes: append([]string{}, DefaultEmailSuffixes...), SMTP: SMTPSettings{Port: 465, Security: "ssl", FromName: "MCII节点共享"}}
}

func (s SMTPSettings) Ready() bool  { return s.Host != "" && s.Port > 0 && s.From != "" }
func (o OAuthSettings) Ready() bool { return o.Enabled && o.ClientID != "" && o.ClientSecret != "" }

func (l *LoginSettings) normalize() error {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range l.EmailSuffixes {
		v = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(v), "@"))
		v = strings.TrimPrefix(v, ".")
		if v == "" || seen[v] {
			continue
		}
		if !regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*$`).MatchString(v) || len(v) > 100 {
			return fmt.Errorf("邮箱后缀无效：%s", v)
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) > 200 {
		return errors.New("邮箱后缀最多 200 个")
	}
	l.EmailSuffixes = out
	m := &l.SMTP
	m.Host, m.Username, m.From, m.FromName = strings.TrimSpace(m.Host), strings.TrimSpace(m.Username), strings.TrimSpace(m.From), strings.TrimSpace(m.FromName)
	switch m.Security {
	case "ssl", "starttls", "none":
	case "":
		m.Security = "ssl"
	default:
		return errors.New("SMTP 加密方式须为 ssl、starttls 或 none")
	}
	if m.Port < 0 || m.Port > 65535 {
		return errors.New("SMTP 端口无效")
	}
	if m.From != "" {
		if _, err := mail.ParseAddress(m.From); err != nil {
			return errors.New("发件邮箱格式无效")
		}
	}
	for _, o := range []*OAuthSettings{&l.GitHub, &l.LinuxDo} {
		o.ClientID = strings.TrimSpace(o.ClientID)
		o.ClientSecret = strings.TrimSpace(o.ClientSecret)
	}
	return nil
}

// mergeSecrets 前端未重新填写密钥时沿用旧值，并加密保存
func (l *LoginSettings) mergeSecrets(old LoginSettings) error {
	pairs := []struct{ next, prev *string }{{&l.SMTP.Password, &old.SMTP.Password}, {&l.GitHub.ClientSecret, &old.GitHub.ClientSecret}, {&l.LinuxDo.ClientSecret, &old.LinuxDo.ClientSecret}}
	for _, p := range pairs {
		if *p.next == "" {
			*p.next = *p.prev
			continue
		}
		v, err := seal(purposeSecret, *p.next)
		if err != nil {
			return err
		}
		*p.next = v
	}
	l.SMTP.PasswordSet, l.GitHub.SecretSet, l.LinuxDo.SecretSet = false, false, false
	return nil
}

// Masked 返回给前端的设置，不包含任何密钥
func (l LoginSettings) Masked() LoginSettings {
	l.EmailSuffixes = append([]string{}, l.EmailSuffixes...)
	l.SMTP.PasswordSet, l.SMTP.Password = l.SMTP.Password != "", ""
	l.GitHub.SecretSet, l.GitHub.ClientSecret = l.GitHub.ClientSecret != "", ""
	l.LinuxDo.SecretSet, l.LinuxDo.ClientSecret = l.LinuxDo.ClientSecret != "", ""
	return l
}

// Unsealed 返回解密后的密钥，仅供发信和 OAuth 交换使用
func (l LoginSettings) Unsealed() (LoginSettings, error) {
	var err error
	for _, p := range []*string{&l.SMTP.Password, &l.GitHub.ClientSecret, &l.LinuxDo.ClientSecret} {
		if *p, err = unseal(purposeSecret, *p); err != nil {
			return l, err
		}
	}
	return l, nil
}

// EmailRequired 普通用户在邮件服务可用且开启强制绑定时必须补齐邮箱
func (l LoginSettings) EmailRequired(u User) bool {
	return u.ID > 0 && u.Role != "admin" && u.Email == "" && l.RequireEmail && l.SMTP.Ready()
}

// NormalizeEmail 统一小写并校验格式
func NormalizeEmail(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	a, err := mail.ParseAddress(v)
	if err != nil || a.Address != v || len(v) > 254 || strings.Count(v, "@") != 1 {
		return "", errors.New("邮箱格式不正确")
	}
	return v, nil
}

func (l LoginSettings) EmailAllowed(email string) bool {
	if len(l.EmailSuffixes) == 0 {
		return true
	}
	domain := email[strings.LastIndex(email, "@")+1:]
	for _, s := range l.EmailSuffixes {
		if domain == s || strings.HasSuffix(domain, "."+s) {
			return true
		}
	}
	return false
}

// ValidUsername 自助注册与第三方登录使用的用户名规则
func ValidUsername(v string) bool {
	return regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{2,19}$`).MatchString(v)
}

func (s *Store) ensureIdentity(ctx context.Context) error {
	columns := []struct{ name, definition string }{
		{"email", "VARCHAR(254) NULL"},
		{"email_verified_at", "DATETIME(6) NULL"},
	}
	for _, c := range columns {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_users' AND COLUMN_NAME=?`, c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_users ADD COLUMN `+c.name+` `+c.definition); err != nil {
				return err
			}
		}
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_users' AND INDEX_NAME='uk_users_email'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_users ADD UNIQUE KEY uk_users_email(email)`); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS np_user_identities (
 provider VARCHAR(16) NOT NULL, subject VARCHAR(64) NOT NULL, user_id BIGINT NOT NULL, login VARCHAR(100) NOT NULL DEFAULT '',
 created_at DATETIME(6) NOT NULL, PRIMARY KEY(provider,subject), UNIQUE KEY(user_id,provider),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
		`CREATE TABLE IF NOT EXISTS np_email_codes (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, purpose VARCHAR(16) NOT NULL, email VARCHAR(254) NOT NULL, user_id BIGINT NOT NULL DEFAULT 0,
 code_hash CHAR(64) NOT NULL, attempts INT NOT NULL DEFAULT 0, expires_at DATETIME(6) NOT NULL, created_at DATETIME(6) NOT NULL,
 INDEX(email,purpose), INDEX(expires_at)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func codeHash(purpose, email, code string) string {
	sum := sha256.Sum256([]byte(purpose + "\x00" + email + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

// IssueEmailCode 生成 6 位验证码，同一邮箱同一用途一分钟内只能发送一次
func (s *Store) IssueEmailCode(ctx context.Context, purpose, email string, userID int64) (string, error) {
	now := utcNow()
	var last sql.NullTime
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(created_at) FROM np_email_codes WHERE email=? AND purpose=?`, email, purpose).Scan(&last); err != nil {
		return "", err
	}
	if last.Valid && now.Sub(last.Time) < emailCodeCooldown {
		return "", ErrCodeCooldown
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM np_email_codes WHERE (email=? AND purpose=?) OR expires_at<?`, email, purpose, now.Add(-time.Hour)); err != nil {
		return "", err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO np_email_codes(purpose,email,user_id,code_hash,expires_at,created_at) VALUES(?,?,?,?,?,?)`, purpose, email, userID, codeHash(purpose, email, code), now.Add(emailCodeTTL), now)
	return code, err
}

// DiscardEmailCode 发信失败时撤销验证码，允许立即重试
func (s *Store) DiscardEmailCode(ctx context.Context, purpose, email string) {
	_, _ = s.DB.ExecContext(context.WithoutCancel(ctx), `DELETE FROM np_email_codes WHERE email=? AND purpose=?`, email, purpose)
}

// ConsumeEmailCode 校验并作废验证码；userID 非零时要求与发送时一致
func (s *Store) ConsumeEmailCode(ctx context.Context, purpose, email, code string, userID int64) error {
	code = strings.TrimSpace(code)
	var id, owner int64
	var hash string
	var attempts int
	err := s.DB.QueryRowContext(ctx, `SELECT id,user_id,code_hash,attempts FROM np_email_codes WHERE email=? AND purpose=? AND expires_at>? ORDER BY id DESC LIMIT 1`, email, purpose, utcNow()).Scan(&id, &owner, &hash, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEmailCode
	}
	if err != nil {
		return err
	}
	if attempts >= emailCodeAttempts || owner != userID || hash != codeHash(purpose, email, code) {
		_, _ = s.DB.ExecContext(ctx, `UPDATE np_email_codes SET attempts=attempts+1 WHERE id=?`, id)
		return ErrEmailCode
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM np_email_codes WHERE id=?`, id)
	return err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM np_users WHERE email=?`, email))
}

// UserByLogin 登录名可以是用户名或邮箱
func (s *Store) UserByLogin(ctx context.Context, login string) (User, error) {
	login = strings.TrimSpace(login)
	if strings.Contains(login, "@") {
		email, err := NormalizeEmail(login)
		if err != nil {
			return User{}, sql.ErrNoRows
		}
		return s.UserByEmail(ctx, email)
	}
	return s.UserByName(ctx, login)
}

// IsDuplicate 判断是否违反唯一约束
func IsDuplicate(err error) bool {
	var dup *mysql.MySQLError
	return errors.As(err, &dup) && dup.Number == 1062
}

// SetEmail 写入已验证的邮箱
func (s *Store) SetEmail(ctx context.Context, userID int64, email string) (User, error) {
	if _, err := s.DB.ExecContext(ctx, `UPDATE np_users SET email=?,email_verified_at=? WHERE id=?`, email, utcNow(), userID); err != nil {
		if IsDuplicate(err) {
			return User{}, ErrEmailTaken
		}
		return User{}, err
	}
	return s.User(ctx, userID)
}

func (s *Store) ResetPassword(ctx context.Context, userID int64, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE np_users SET password_hash=?,auth_version=auth_version+1 WHERE id=?`, hash, userID)
	return err
}

type Identity struct {
	Provider  string    `json:"provider"`
	Login     string    `json:"login"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) Identities(ctx context.Context, userID int64) ([]Identity, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT provider,login,created_at FROM np_user_identities WHERE user_id=? ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Identity{}
	for rows.Next() {
		var v Identity
		if err = rows.Scan(&v.Provider, &v.Login, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) UserByIdentity(ctx context.Context, provider, subject string) (User, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM np_user_identities WHERE provider=? AND subject=?`, provider, subject).Scan(&id); err != nil {
		return User{}, err
	}
	return s.User(ctx, id)
}

// LinkIdentity 绑定第三方账号；同一用户每个平台只能绑定一个
func (s *Store) LinkIdentity(ctx context.Context, userID int64, provider, subject, login string) error {
	if len(login) > 100 {
		login = login[:100]
	}
	var owner int64
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM np_user_identities WHERE provider=? AND subject=?`, provider, subject).Scan(&owner)
	if err == nil {
		if owner != userID {
			return ErrIdentityTaken
		}
		_, err = s.DB.ExecContext(ctx, `UPDATE np_user_identities SET login=? WHERE provider=? AND subject=?`, login, provider, subject)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO np_user_identities(provider,subject,user_id,login,created_at) VALUES(?,?,?,?,?)`, provider, subject, userID, login, utcNow())
	if IsDuplicate(err) {
		return errors.New("已绑定该平台的其他账号，请先解绑")
	}
	return err
}

func (s *Store) UnlinkIdentity(ctx context.Context, userID int64, provider string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM np_user_identities WHERE user_id=? AND provider=?`, userID, provider)
	return err
}

// AvailableUsername 根据第三方登录名生成符合规则且未被占用的用户名
func (s *Store) AvailableUsername(ctx context.Context, hint, prefix string) (string, error) {
	base := regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(hint, "")
	if base == "" || !regexp.MustCompile(`^[A-Za-z]`).MatchString(base) {
		base = prefix + base
	}
	if len(base) > 16 {
		base = base[:16]
	}
	for len(base) < 3 {
		base += "0"
	}
	for i := 0; i < 50; i++ {
		name := base
		if i > 0 {
			n, err := rand.Int(rand.Reader, big.NewInt(9000))
			if err != nil {
				return "", err
			}
			name = fmt.Sprintf("%s%d", base, n.Int64()+1000)
		}
		var exists bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM np_users WHERE username=?)`, name).Scan(&exists); err != nil {
			return "", err
		}
		if !exists && ValidUsername(name) {
			return name, nil
		}
	}
	return "", errors.New("无法生成可用的用户名")
}
