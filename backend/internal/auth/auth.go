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
}
type Manager struct {
	mu           sync.Mutex
	digest       [32]byte
	hash         string
	sessions     map[[32]byte]session
	failures     int
	window       time.Time
	PasswordFile string
	store        *pool.Store
}

func New(dataDir, password string) (*Manager, error) {
	m := &Manager{sessions: map[[32]byte]session{}}
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
func (m *Manager) user(ctx context.Context, id int64) (pool.User, error) {
	if m.store == nil {
		return pool.User{ID: 1, Username: "admin", DisplayName: "管理员", Role: "admin", Enabled: true, NodeLimit: 200, SourceLimit: 3, SubscriptionLimit: 1, PasswordHash: m.hash, AuthVersion: 1}, nil
	}
	if e := m.store.BootstrapAdmin(ctx, m.hash); e != nil {
		return pool.User{}, e
	}
	return m.store.User(ctx, id)
}
func (m *Manager) Identity(r *http.Request) (pool.User, bool) {
	cookie, e := r.Cookie("nodepool_session")
	if e != nil {
		return pool.User{}, false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	m.mu.Lock()
	v, ok := m.sessions[key]
	m.mu.Unlock()
	if !ok {
		return pool.User{}, false
	}
	if time.Now().Before(v.Expires) {
		u, e := m.user(r.Context(), v.UserID)
		if e == nil && u.Enabled && u.AuthVersion == v.Version {
			return u, true
		}
	}
	m.mu.Lock()
	delete(m.sessions, key)
	m.mu.Unlock()
	return pool.User{}, false
}
func (m *Manager) Authenticated(r *http.Request) bool { _, ok := m.Identity(r); return ok }

// Keep the current browser signed in after its own password change. The database
// version invalidates every other session, including sessions in another process.
func (m *Manager) KeepSession(r *http.Request, u pool.User) {
	if c, e := r.Cookie("nodepool_session"); e == nil {
		key := sha256.Sum256([]byte(c.Value))
		m.mu.Lock()
		v, ok := m.sessions[key]
		if ok && v.UserID == u.ID {
			v.Version = u.AuthVersion
			m.sessions[key] = v
		}
		m.mu.Unlock()
	}
}
func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func authReply(w http.ResponseWriter, u pool.User, ok bool) {
	var user any
	name := ""
	if ok {
		user = u
		name = u.Username
	}
	reply(w, 200, map[string]any{"authenticated": ok, "username": name, "user": user})
}
func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth", func(w http.ResponseWriter, r *http.Request) { u, ok := m.Identity(r); authReply(w, u, ok) })
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
			u, e = m.store.UserByName(r.Context(), in.Username)
		} else if in.Username != "admin" {
			e = pool.ErrNotFound
		}
		// An unknown account still pays the bcrypt cost to avoid a timing oracle.
		hash := u.PasswordHash
		if e != nil {
			hash = m.hash
		}
		valid := pool.CheckPassword(hash, in.Password)
		if e != nil || !u.Enabled || !valid {
			m.mu.Lock()
			m.failures++
			m.mu.Unlock()
			reply(w, 401, map[string]string{"error": "用户名或密码不正确，或账号已停用"})
			return
		}
		token, e := random()
		if e != nil {
			reply(w, 500, map[string]string{"error": "无法创建登录会话"})
			return
		}
		key := sha256.Sum256([]byte(token))
		m.mu.Lock()
		for k, v := range m.sessions {
			if time.Now().After(v.Expires) {
				delete(m.sessions, k)
			}
		}
		if len(m.sessions) >= 10000 {
			m.mu.Unlock()
			reply(w, 503, map[string]string{"error": "登录会话已满"})
			return
		}
		m.sessions[key] = session{time.Now().Add(12 * time.Hour), u.ID, u.AuthVersion}
		m.failures = 0
		m.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "nodepool_session", Value: token, Path: "/", MaxAge: 43200, HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode})
		authReply(w, u, true)
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, e := r.Cookie("nodepool_session"); e == nil {
			key := sha256.Sum256([]byte(c.Value))
			m.mu.Lock()
			delete(m.sessions, key)
			m.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "nodepool_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		authReply(w, pool.User{}, false)
	})
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
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/auth" && r.URL.Path != "/api/v1/auth/login" && r.URL.Path != "/api/v1/auth/logout" {
			u, ok := m.Identity(r)
			if !ok {
				reply(w, 401, map[string]string{"error": "请先登录"})
				return
			}
			r = r.WithContext(pool.WithUser(r.Context(), u))
		}
		next.ServeHTTP(w, r)
	})
}
