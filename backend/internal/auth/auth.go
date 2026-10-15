package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
)

type Manager struct {
	mu           sync.Mutex
	digest       [32]byte
	sessions     map[[32]byte]time.Time
	failures     int
	window       time.Time
	PasswordFile string
}

func New(dataDir, password string) (*Manager, error) {
	m := &Manager{sessions: map[[32]byte]time.Time{}}
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
			_, writeErr := f.WriteString(token + "\n")
			closeErr := f.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			password = token
		} else if e != nil {
			return nil, e
		} else {
			password = strings.TrimSpace(string(b))
		}
		m.PasswordFile = path
	}
	if len(password) < 8 || len(password) > 1024 {
		return nil, errors.New("管理员密码须为 8–1024 字节")
	}
	m.digest = sha256.Sum256([]byte(password))
	return m, nil
}
func random() (string, error) {
	var b [32]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func (m *Manager) Authenticated(r *http.Request) bool {
	cookie, e := r.Cookie("nodepool_session")
	if e != nil {
		return false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	m.mu.Lock()
	defer m.mu.Unlock()
	expiry, ok := m.sessions[key]
	if ok && time.Now().Before(expiry) {
		return true
	}
	delete(m.sessions, key)
	return false
}
func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"authenticated": m.Authenticated(r), "username": "admin"})
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
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			reply(w, 400, map[string]string{"error": "请求格式无效"})
			return
		}
		sum := sha256.Sum256([]byte(in.Password))
		if subtle.ConstantTimeCompare(sum[:], m.digest[:]) != 1 {
			m.mu.Lock()
			m.failures++
			m.mu.Unlock()
			reply(w, 401, map[string]string{"error": "密码不正确"})
			return
		}
		token, e := random()
		if e != nil {
			reply(w, 500, map[string]string{"error": "无法创建登录会话"})
			return
		}
		key := sha256.Sum256([]byte(token))
		m.mu.Lock()
		for key, expiry := range m.sessions {
			if time.Now().After(expiry) {
				delete(m.sessions, key)
			}
		}
		if len(m.sessions) >= 1000 {
			m.sessions = map[[32]byte]time.Time{}
		}
		m.sessions[key] = time.Now().Add(12 * time.Hour)
		m.failures = 0
		m.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "nodepool_session", Value: token, Path: "/", MaxAge: 43200, HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode})
		reply(w, 200, map[string]bool{"authenticated": true})
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if cookie, e := r.Cookie("nodepool_session"); e == nil {
			key := sha256.Sum256([]byte(cookie.Value))
			m.mu.Lock()
			delete(m.sessions, key)
			m.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "nodepool_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		reply(w, 200, map[string]bool{"authenticated": false})
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
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/auth" && r.URL.Path != "/api/v1/auth/login" && r.URL.Path != "/api/v1/auth/logout" && !m.Authenticated(r) {
			reply(w, 401, map[string]string{"error": "请先登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
