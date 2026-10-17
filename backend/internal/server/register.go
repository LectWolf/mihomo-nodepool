package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// rateLimiter 按小时限制同一 IP 和全站的请求次数
type rateLimiter struct {
	mu            sync.Mutex
	window        time.Time
	total         int
	byIP          map[string]int
	perIP, perAll int
}

func newLimiter(perIP, perAll int) *rateLimiter {
	return &rateLimiter{byIP: map[string]int{}, perIP: perIP, perAll: perAll}
}

func (l *rateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.window) > time.Hour {
		l.window, l.total, l.byIP = time.Now(), 0, map[string]int{}
	}
	if l.total >= l.perAll || l.byIP[ip] >= l.perIP {
		return false
	}
	l.total++
	l.byIP[ip]++
	return true
}

func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "请求格式无效"})
		return false
	}
	return true
}

// deliverCode 生成并发送验证码，发信失败时撤销以便重试
func deliverCode(ctx context.Context, store *pool.Store, purpose, email string, userID int64) error {
	login, err := store.LoginNow(ctx).Unsealed()
	if err != nil {
		return err
	}
	if !login.SMTP.Ready() {
		return errors.New("管理员尚未配置邮件服务")
	}
	code, err := store.IssueEmailCode(ctx, purpose, email, userID)
	if err != nil {
		return err
	}
	if err = sendMail(ctx, login.SMTP, email, codeMail(purpose, code)); err != nil {
		store.DiscardEmailCode(ctx, purpose, email)
		return fmt.Errorf("验证码发送失败：%w", err)
	}
	return nil
}

func registerSignup(mux *http.ServeMux, store *pool.Store, manager *auth.Manager, trusted []*net.IPNet) {
	signups, codes, attempts := newLimiter(5, 60), newLimiter(10, 300), newLimiter(30, 1000)
	ready := func(w http.ResponseWriter, r *http.Request) bool {
		if store == nil || manager == nil || store.Ensure(r.Context()) != nil {
			writeJSON(w, 503, map[string]string{"error": "数据库暂不可用"})
			return false
		}
		return true
	}
	mux.HandleFunc("POST /api/v1/auth/email/code", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !ready(w, r) {
			return
		}
		var in struct {
			Email   string `json:"email"`
			Purpose string `json:"purpose"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		email, err := pool.NormalizeEmail(in.Email)
		if err != nil {
			writeError(w, err)
			return
		}
		if in.Purpose != "register" && in.Purpose != "login" && in.Purpose != "reset" {
			writeJSON(w, 400, map[string]string{"error": "验证码用途无效"})
			return
		}
		if !codes.allow(clientIP(r, trusted)) {
			writeJSON(w, 429, map[string]string{"error": "验证码请求过于频繁，请稍后再试"})
			return
		}
		_, lookup := store.UserByEmail(ctx, email)
		switch {
		case in.Purpose == "register" && !store.CommunityNow(ctx).RegistrationEnabled:
			writeJSON(w, 403, map[string]string{"error": "管理员暂未开放注册"})
			return
		case in.Purpose == "register" && !store.LoginNow(ctx).EmailAllowed(email):
			writeError(w, pool.ErrEmailSuffix)
			return
		case in.Purpose == "register" && lookup == nil:
			writeJSON(w, 409, map[string]string{"error": "该邮箱已注册，请直接登录"})
			return
		case in.Purpose != "register" && lookup != nil:
			// 登录与找回密码不暴露邮箱是否存在
			writeJSON(w, 200, map[string]any{"ok": true, "cooldown": 60})
			return
		}
		if err = deliverCode(ctx, store, in.Purpose, email, 0); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "cooldown": 60})
	})
	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !ready(w, r) {
			return
		}
		if !store.CommunityNow(ctx).RegistrationEnabled {
			writeJSON(w, 403, map[string]string{"error": "管理员暂未开放注册"})
			return
		}
		if pool.CurrentMaintenance().Enabled {
			writeJSON(w, 503, map[string]any{"error": "维护期间暂停注册", "maintenance": true})
			return
		}
		var in struct {
			Username    string `json:"username"`
			DisplayName string `json:"display_name"`
			Password    string `json:"password"`
			Email       string `json:"email"`
			Code        string `json:"code"`
			Remember    bool   `json:"remember"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		in.Username = strings.TrimSpace(in.Username)
		if !pool.ValidUsername(in.Username) {
			writeJSON(w, 400, map[string]string{"error": "用户名须为 3–20 位，以字母开头，只能包含字母、数字、下划线或横线"})
			return
		}
		login := store.LoginNow(ctx)
		email := ""
		// 配置了邮件服务时注册必须验证邮箱
		if login.SMTP.Ready() {
			var err error
			if email, err = pool.NormalizeEmail(in.Email); err != nil {
				writeError(w, err)
				return
			}
			if !login.EmailAllowed(email) {
				writeError(w, pool.ErrEmailSuffix)
				return
			}
		}
		if !signups.allow(clientIP(r, trusted)) {
			writeJSON(w, 429, map[string]string{"error": "注册过于频繁，请稍后再试"})
			return
		}
		if email != "" {
			if _, err := store.UserByEmail(ctx, email); err == nil {
				writeJSON(w, 409, map[string]string{"error": "该邮箱已注册，请直接登录"})
				return
			}
			if err := store.ConsumeEmailCode(ctx, "register", email, in.Code, 0); err != nil {
				writeError(w, err)
				return
			}
		}
		u, err := store.CreateUser(pool.WithUser(ctx, pool.GuestUser), pool.UserInput{Username: in.Username, DisplayName: strings.TrimSpace(in.DisplayName), Password: in.Password})
		if pool.IsDuplicate(err) {
			writeJSON(w, 409, map[string]string{"error": "用户名已被使用"})
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		if email != "" {
			if u, err = store.SetEmail(ctx, u.ID, email); err != nil {
				writeError(w, err)
				return
			}
		}
		manager.StartSession(w, r, u, in.Remember)
	})
	mux.HandleFunc("POST /api/v1/auth/login/code", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !ready(w, r) {
			return
		}
		var in struct {
			Email    string `json:"email"`
			Code     string `json:"code"`
			Remember bool   `json:"remember"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if !attempts.allow(clientIP(r, trusted)) {
			writeJSON(w, 429, map[string]string{"error": "尝试过于频繁，请稍后再试"})
			return
		}
		email, err := pool.NormalizeEmail(in.Email)
		if err == nil {
			err = store.ConsumeEmailCode(ctx, "login", email, in.Code, 0)
		}
		var u pool.User
		if err == nil {
			u, err = store.UserByEmail(ctx, email)
		}
		if err != nil || !u.Enabled {
			writeJSON(w, 401, map[string]string{"error": "验证码不正确或已过期，或账号已停用"})
			return
		}
		manager.StartSession(w, r, u, in.Remember)
	})
	mux.HandleFunc("POST /api/v1/auth/password/reset", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !ready(w, r) {
			return
		}
		var in struct {
			Email    string `json:"email"`
			Code     string `json:"code"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if !attempts.allow(clientIP(r, trusted)) {
			writeJSON(w, 429, map[string]string{"error": "尝试过于频繁，请稍后再试"})
			return
		}
		email, err := pool.NormalizeEmail(in.Email)
		if err == nil {
			_, err = pool.HashPassword(in.Password)
		}
		if err != nil {
			writeError(w, err)
			return
		}
		if err = store.ConsumeEmailCode(ctx, "reset", email, in.Code, 0); err != nil {
			writeError(w, err)
			return
		}
		u, err := store.UserByEmail(ctx, email)
		if err == nil {
			err = store.ResetPassword(ctx, u.ID, in.Password)
		}
		if err != nil {
			writeError(w, pool.ErrEmailCode)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}

// registerAccountSecurity 已登录用户的邮箱与第三方账号绑定
func registerAccountSecurity(business func(string, func(http.ResponseWriter, *http.Request) error), store *pool.Store, trusted []*net.IPNet) {
	codes := newLimiter(10, 300)
	business("POST /api/v1/me/email/code", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Email string `json:"email"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		email, err := pool.NormalizeEmail(in.Email)
		if err != nil {
			return err
		}
		u := pool.CurrentUser(r.Context())
		if email == u.Email {
			return errors.New("新邮箱与当前邮箱相同")
		}
		if !store.LoginNow(r.Context()).EmailAllowed(email) {
			return pool.ErrEmailSuffix
		}
		if other, e := store.UserByEmail(r.Context(), email); e == nil && other.ID != u.ID {
			return pool.ErrEmailTaken
		}
		if !codes.allow(clientIP(r, trusted)) {
			return pool.ErrCodeCooldown
		}
		if err = deliverCode(r.Context(), store, "bind", email, u.ID); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"ok": true, "cooldown": 60})
		return nil
	})
	business("PUT /api/v1/me/email", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Email string `json:"email"`
			Code  string `json:"code"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		email, err := pool.NormalizeEmail(in.Email)
		if err != nil {
			return err
		}
		u := pool.CurrentUser(r.Context())
		if err = store.ConsumeEmailCode(r.Context(), "bind", email, in.Code, u.ID); err != nil {
			return err
		}
		v, err := store.SetEmail(r.Context(), u.ID, email)
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	business("GET /api/v1/me/identities", func(w http.ResponseWriter, r *http.Request) error {
		v, err := store.Identities(r.Context(), pool.CurrentUser(r.Context()).ID)
		if err == nil {
			l := store.LoginNow(r.Context())
			writeJSON(w, 200, map[string]any{"items": v, "providers": map[string]bool{"github": l.GitHub.Ready(), "linuxdo": l.LinuxDo.Ready()}})
		}
		return err
	})
	business("DELETE /api/v1/me/identities/{provider}", func(w http.ResponseWriter, r *http.Request) error {
		p := r.PathValue("provider")
		if _, ok := oauthProviders[p]; !ok {
			return pool.ErrNotFound
		}
		if err := store.UnlinkIdentity(r.Context(), pool.CurrentUser(r.Context()).ID, p); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
	business("POST /api/v1/settings/smtp-test", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			To string `json:"to"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		to, err := pool.NormalizeEmail(in.To)
		if err != nil {
			return err
		}
		login, err := store.LoginNow(r.Context()).Unsealed()
		if err != nil {
			return err
		}
		if err = sendMail(r.Context(), login.SMTP, to, testMail(login.SMTP.Host)); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

// emailGate 必须补齐邮箱的用户只能浏览、绑定邮箱和修改资料
func emailGate(r *http.Request, store *pool.Store, u pool.User) bool {
	if store == nil || store.DB == nil || u.ID == 0 || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return false
	}
	p := r.URL.Path
	if !strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/api/v1/auth") || strings.HasPrefix(p, "/api/v1/me/email") || strings.HasPrefix(p, "/api/v1/me/identities") || (p == "/api/v1/me" && r.Method == http.MethodPatch) || strings.HasPrefix(p, "/api/v1/me/avatar") {
		return false
	}
	return store.LoginNow(r.Context()).EmailRequired(u)
}
