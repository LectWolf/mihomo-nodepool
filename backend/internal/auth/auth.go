package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

type session struct {
	Expires         time.Time
	UserID, Version int64
	Persistent      bool
}

const (
	sessionTTL    = 12 * time.Hour
	persistentTTL = 30 * 24 * time.Hour
	cookieName    = "nodepool_session"
)

type Manager struct {
	mu           sync.Mutex
	digest       [32]byte
	hash         string
	sessions     map[[32]byte]session
	users        map[int64]userCacheEntry
	userMu       sync.Mutex
	failures     int
	window       time.Time
	PasswordFile string
	store        *pool.Store
}

func New(dataDir, password string) (*Manager, error) {
	m := &Manager{sessions: map[[32]byte]session{}, users: map[int64]userCacheEntry{}}
	if password == "" {
		if e := os.MkdirAll(dataDir, 0700); e != nil {
			return nil, e
		}
		path := filepath.Join(dataDir, "admin-password")
		b, e := os.ReadFile(path)
		if errors.Is(e, os.ErrNotExist) {
			token, e := random()
			if e != nil {
				return nil, e
			}
			f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return nil, e
			}
			_, we := f.WriteString(token + "\n")
			ce := f.Close()
			if we != nil {
				return nil, we
			}
			if ce != nil {
				return nil, ce
			}
			password = token
		} else if e != nil {
			return nil, e
		} else {
			password = strings.TrimSpace(string(b))
		}
		m.PasswordFile = path
	}
	hash, e := pool.HashPassword(password)
	if e != nil {
		return nil, e
	}
	m.hash = hash
	m.digest = sha256.Sum256([]byte(password))
	return m, nil
}
func (m *Manager) BindStore(s *pool.Store) {
	if s != nil && s.DB != nil {
		m.store = s
	}
}
func random() (string, error) {
	var b [32]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}

type userCacheEntry struct {
	u  pool.User
	at time.Time
}

const userCacheTTL = 5 * time.Second

func (m *Manager) user(ctx context.Context, id int64) (pool.User, error) {
	if m.store == nil {
		return pool.User{ID: 1, Username: "admin", DisplayName: "管理员", Role: "admin", Enabled: true, NodeLimit: 200, SourceLimit: 3, SubscriptionLimit: 1, PasswordHash: m.hash, AuthVersion: 1}, nil
	}
	m.userMu.Lock()
	if e, ok := m.users[id]; ok && time.Since(e.at) < userCacheTTL {
		u := e.u
		m.userMu.Unlock()
		return u, nil
	}
	m.userMu.Unlock()
	if e := m.store.BootstrapAdmin(ctx, m.hash); e != nil {
		return pool.User{}, e
	}
	u, e := m.store.User(ctx, id)
	if e == nil {
		m.userMu.Lock()
		if len(m.users) > 10000 {
			m.users = map[int64]userCacheEntry{}
		}
		if m.users == nil {
			m.users = map[int64]userCacheEntry{}
		}
		m.users[id] = userCacheEntry{u: u, at: time.Now()}
		m.userMu.Unlock()
	}
	return u, e
}
func (m *Manager) Identity(r *http.Request) (pool.User, bool) {
	u, _, _, ok := m.identify(r)
	return u, ok
}

// identify 先查内存缓存，未命中时读取数据库中的持久会话
func (m *Manager) identify(r *http.Request) (pool.User, [32]byte, session, bool) {
	var key [32]byte
	cookie, e := r.Cookie(cookieName)
	if e != nil {
		return pool.User{}, key, session{}, false
	}
	key = sha256.Sum256([]byte(cookie.Value))
	m.mu.Lock()
	v, ok := m.sessions[key]
	m.mu.Unlock()
	if !ok && m.store != nil {
		if rec, e := m.store.LoadSession(r.Context(), key); e == nil {
			v, ok = session{rec.Expires, rec.UserID, rec.Version, rec.Persistent}, true
			m.cache(key, v)
		}
	}
	if !ok {
		return pool.User{}, key, v, false
	}
	if time.Now().Before(v.Expires) {
		u, e := m.user(r.Context(), v.UserID)
		if e == nil && u.Enabled && u.AuthVersion == v.Version {
			return u, key, v, true
		}
	}
	m.forget(r.Context(), key)
	return pool.User{}, key, v, false
}
func (m *Manager) cache(key [32]byte, v session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= 10000 {
		for k, s := range m.sessions {
			if time.Now().After(s.Expires) {
				delete(m.sessions, k)
			}
		}
		if len(m.sessions) >= 10000 && m.store != nil {
			// 数据库仍保存会话，缓存满时直接清空重建
			m.sessions = map[[32]byte]session{}
		}
	}
	m.sessions[key] = v
}
func (m *Manager) forget(ctx context.Context, key [32]byte) {
	m.mu.Lock()
	delete(m.sessions, key)
	m.mu.Unlock()
	if m.store != nil {
		_ = m.store.DeleteSession(context.WithoutCancel(ctx), key)
	}
}
func (m *Manager) persist(ctx context.Context, key [32]byte, v session) error {
	if m.store != nil {
		if e := m.store.SaveSession(ctx, key, pool.SessionRecord{UserID: v.UserID, Version: v.Version, Expires: v.Expires, Persistent: v.Persistent}); e != nil {
			return e
		}
	}
	m.cache(key, v)
	return nil
}
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, persistent bool) {
	c := &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode}
	if persistent {
		c.MaxAge = int(persistentTTL / time.Second)
	}
	http.SetCookie(w, c)
}
func (m *Manager) Authenticated(r *http.Request) bool { _, ok := m.Identity(r); return ok }

// Keep the current browser signed in after its own password change. The database
// version invalidates every other session, including sessions in another process.
func (m *Manager) KeepSession(r *http.Request, u pool.User) {
	if c, e := r.Cookie(cookieName); e == nil {
		key := sha256.Sum256([]byte(c.Value))
		m.mu.Lock()
		v, ok := m.sessions[key]
		m.mu.Unlock()
		if ok && v.UserID == u.ID {
			v.Version = u.AuthVersion
			_ = m.persist(r.Context(), key, v)
		}
	}
}

// passwordUnset 返回可用于验证身份的方式，邮箱只给出打码提示
func (m *Manager) passwordUnset(ctx context.Context, u pool.User) map[string]any {
	l := m.store.LoginNow(ctx)
	hint := ""
	if u.Email != "" && u.EmailVerified && l.SMTP.Ready() {
		hint = MaskEmail(u.Email)
	}
	providers := []string{}
	if ids, e := m.store.Identities(ctx, u.ID); e == nil {
		for _, id := range ids {
			if (id.Provider == "github" && l.GitHub.Ready()) || (id.Provider == "linuxdo" && l.LinuxDo.Ready()) {
				providers = append(providers, id.Provider)
			}
		}
	}
	return map[string]any{"error": "该账号还没有设置登录密码，请先通过邮箱验证码或已绑定的第三方账号验证身份，再设置密码", "password_unset": true, "email_hint": hint, "providers": providers}
}

// MaskEmail 只保留邮箱名的首尾字符，例如 22****85@qq.com
func MaskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	name := []rune(email[:at])
	keep := 2
	if len(name) <= 4 {
		keep = 1
	}
	if len(name) <= 2 {
		return string(name[:1]) + "*" + email[at:]
	}
	return string(name[:keep]) + "****" + string(name[len(name)-keep:]) + email[at:]
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// authReply 附带登录方式开关，前端据此显示邮箱和第三方登录入口
func (m *Manager) authReply(w http.ResponseWriter, r *http.Request, u pool.User, ok bool, persistent ...bool) {
	out := map[string]any{"authenticated": ok, "username": "", "user": nil, "persistent": len(persistent) > 0 && persistent[0]}
	if ok {
		out["user"], out["username"] = u, u.Username
	}
	if m.store != nil && m.store.DB != nil {
		c, l := m.store.CommunityNow(r.Context()), m.store.LoginNow(r.Context())
		out["guest_browse"], out["registration"] = c.GuestBrowse, c.RegistrationEnabled
		out["login_options"] = map[string]any{"email": l.SMTP.Ready(), "github": l.GitHub.Ready(), "linuxdo": l.LinuxDo.Ready(), "require_email": l.RequireEmail && l.SMTP.Ready(), "email_suffixes": l.EmailSuffixes}
		out["email_required"] = ok && l.EmailRequired(u)
	}
	reply(w, 200, out)
}
func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth", func(w http.ResponseWriter, r *http.Request) {
		u, _, v, ok := m.identify(r)
		m.authReply(w, r, u, ok, ok && v.Persistent)
	})
	// 类似“保持登录状态”：登录后再确认，把当前会话延长为 30 天
	mux.HandleFunc("POST /api/v1/auth/persist", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Remember bool `json:"remember"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
			reply(w, 400, map[string]string{"error": "请求格式无效"})
			return
		}
		u, key, v, ok := m.identify(r)
		if !ok {
			reply(w, 401, map[string]string{"error": "请先登录"})
			return
		}
		c, _ := r.Cookie(cookieName)
		v.Persistent = in.Remember
		if in.Remember {
			v.Expires = time.Now().Add(persistentTTL)
		} else if time.Until(v.Expires) > sessionTTL {
			v.Expires = time.Now().Add(sessionTTL)
		}
		if e := m.persist(r.Context(), key, v); e != nil {
			reply(w, 503, map[string]string{"error": "无法保存登录状态"})
			return
		}
		setSessionCookie(w, r, c.Value, v.Persistent)
		m.authReply(w, r, u, true, v.Persistent)
	})
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		if time.Since(m.window) > time.Minute {
			m.failures = 0
			m.window = time.Now()
		}
		limited := m.failures >= 10
		m.mu.Unlock()
		if limited {
			reply(w, 429, map[string]string{"error": "尝试过于频繁，请一分钟后重试"})
			return
		}
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Remember bool   `json:"remember"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
			reply(w, 400, map[string]string{"error": "请求格式无效"})
			return
		}
		if in.Username == "" {
			in.Username = "admin"
		}
		u, e := m.user(r.Context(), 1)
		if e != nil {
			reply(w, 503, map[string]string{"error": "数据库暂不可用"})
			return
		}
		if m.store != nil {
			u, e = m.store.UserByLogin(r.Context(), in.Username)
		} else if in.Username != "admin" {
			e = pool.ErrNotFound
		}
		// An unknown account still pays the bcrypt cost to avoid a timing oracle.
		hash := u.PasswordHash
		if e != nil {
			hash = m.hash
		}
		valid := pool.CheckPassword(hash, in.Password)
		if e == nil && u.Enabled && u.PasswordHash == "" && m.store != nil {
			// 旧站迁移等没有密码的账号：引导改用邮箱验证码或已绑定的第三方账号
			m.mu.Lock()
			m.failures++
			m.mu.Unlock()
			reply(w, 409, m.passwordUnset(r.Context(), u))
			return
		}
		if e != nil || !u.Enabled || !valid {
			m.mu.Lock()
			m.failures++
			m.mu.Unlock()
			reply(w, 401, map[string]string{"error": "账号或密码不正确，或账号已停用"})
			return
		}
		m.mu.Lock()
		m.failures = 0
		m.mu.Unlock()
		m.StartSession(w, r, u, in.Remember)
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, e := r.Cookie(cookieName); e == nil {
			m.forget(r.Context(), sha256.Sum256([]byte(c.Value)))
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		m.authReply(w, r, pool.User{}, false)
	})
}

// StartSession 为已验证的用户创建会话并写入 Cookie，登录和注册共用
func (m *Manager) StartSession(w http.ResponseWriter, r *http.Request, u pool.User, remember bool) {
	if e := m.Issue(w, r, u, remember); e != nil {
		reply(w, 503, map[string]string{"error": e.Error()})
		return
	}
	m.authReply(w, r, u, true, remember)
}

// Issue 只写入会话 Cookie，供 OAuth 回调等需要自行跳转的场景使用
func (m *Manager) Issue(w http.ResponseWriter, r *http.Request, u pool.User, remember bool) error {
	token, e := random()
	if e != nil {
		return errors.New("无法创建登录会话")
	}
	key := sha256.Sum256([]byte(token))
	m.mu.Lock()
	for k, v := range m.sessions {
		if time.Now().After(v.Expires) {
			delete(m.sessions, k)
		}
	}
	full := len(m.sessions) >= 10000 && m.store == nil
	m.mu.Unlock()
	if full {
		return errors.New("登录会话已满")
	}
	v := session{time.Now().Add(sessionTTL), u.ID, u.AuthVersion, remember}
	if remember {
		v.Expires = time.Now().Add(persistentTTL)
	}
	if e := m.persist(r.Context(), key, v); e != nil {
		return errors.New("无法创建登录会话")
	}
	setSessionCookie(w, r, token, remember)
	return nil
}

// guestPaths 未登录访客可以只读访问的接口
func guestPath(p string) bool {
	switch p {
	case "/api/v1/nodes", "/api/v1/options", "/api/v1/sources/catalog", "/api/v1/providers", "/api/v1/levels", "/api/v1/titles", "/api/v1/system", "/api/v1/rule-profiles/default", "/api/v1/statistics":
		return true
	}
	return strings.HasPrefix(p, "/api/v1/avatars/")
}

func (m *Manager) guestAllowed(r *http.Request) bool {
	if m.store == nil || (r.Method != "GET" && r.Method != "HEAD") || !guestPath(r.URL.Path) {
		return false
	}
	return m.store.CommunityNow(r.Context()).GuestBrowse
}

func (m *Manager) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != r.Host {
					reply(w, 403, map[string]string{"error": "不允许跨站修改"})
					return
				}
			}
		}
		// /api/v1/auth 下的接口自行识别会话
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/auth" && !strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			u, key, v, ok := m.identify(r)
			if !ok && m.guestAllowed(r) {
				next.ServeHTTP(w, r.WithContext(pool.WithUser(r.Context(), pool.GuestUser)))
				return
			}
			if !ok {
				reply(w, 401, map[string]string{"error": "请先登录"})
				return
			}
			// 持久会话滑动续期：剩余不足 29 天时延长并刷新 Cookie
			if v.Persistent && time.Until(v.Expires) < persistentTTL-24*time.Hour {
				v.Expires = time.Now().Add(persistentTTL)
				if m.persist(r.Context(), key, v) == nil {
					if c, e := r.Cookie(cookieName); e == nil {
						setSessionCookie(w, r, c.Value, true)
					}
				}
			}
			r = r.WithContext(pool.WithUser(r.Context(), u))
		}
		next.ServeHTTP(w, r)
	})
}
