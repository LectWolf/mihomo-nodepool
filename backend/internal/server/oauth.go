package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

type oauthProvider struct {
	Name, AuthURL, TokenURL, UserURL, Scope, Prefix string
}

// oauthProviders 端点可在测试中替换
var oauthProviders = map[string]oauthProvider{
	"github":  {Name: "GitHub", AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", UserURL: "https://api.github.com/user", Scope: "read:user", Prefix: "gh"},
	"linuxdo": {Name: "LINUX DO", AuthURL: "https://connect.linux.do/oauth2/authorize", TokenURL: "https://connect.linux.do/oauth2/token", UserURL: "https://connect.linux.do/api/user", Scope: "", Prefix: "ld"},
}

var oauthClient = &http.Client{Timeout: 15 * time.Second}

type oauthState struct {
	provider, mode string
	userID         int64
	remember       bool
	expires        time.Time
}

type oauthStates struct {
	mu sync.Mutex
	m  map[string]oauthState
}

func (s *oauthStates) put(v oauthState) (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	key := hex.EncodeToString(b[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, old := range s.m {
		if time.Now().After(old.expires) {
			delete(s.m, k)
		}
	}
	if len(s.m) >= 10000 {
		return "", errors.New("登录请求过多，请稍后再试")
	}
	s.m[key] = v
	return key, nil
}

func (s *oauthStates) take(key string) (oauthState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	delete(s.m, key)
	return v, ok && time.Now().Before(v.expires)
}

const oauthCookie = "nodepool_oauth"

type oauthProfile struct{ Subject, Login, Name string }

func oauthSettings(l pool.LoginSettings, name string) pool.OAuthSettings {
	if name == "github" {
		return l.GitHub
	}
	return l.LinuxDo
}

// fetchProfile 用授权码换取令牌并读取第三方用户信息
func fetchProfile(ctx context.Context, p oauthProvider, cfg pool.OAuthSettings, code, redirect string) (oauthProfile, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var token struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := doJSON(req, &token); err != nil {
		return oauthProfile{}, err
	}
	if token.AccessToken == "" {
		return oauthProfile{}, fmt.Errorf("授权失败：%s", strings.TrimSpace(token.Error+" "+token.Description))
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, p.UserURL, nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")
	var raw struct {
		ID       json.Number `json:"id"`
		Login    string      `json:"login"`
		Username string      `json:"username"`
		Name     string      `json:"name"`
		Active   *bool       `json:"active"`
	}
	if err := doJSON(req, &raw); err != nil {
		return oauthProfile{}, err
	}
	if raw.ID.String() == "" || raw.ID.String() == "0" {
		return oauthProfile{}, errors.New("第三方平台没有返回用户 ID")
	}
	if raw.Active != nil && !*raw.Active {
		return oauthProfile{}, errors.New("第三方账号未激活")
	}
	login := raw.Login
	if login == "" {
		login = raw.Username
	}
	return oauthProfile{Subject: raw.ID.String(), Login: login, Name: strings.TrimSpace(raw.Name)}, nil
}

func doJSON(req *http.Request, out any) error {
	res, err := oauthClient.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接第三方平台：%w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("第三方平台返回 %d", res.StatusCode)
	}
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.UseNumber()
	if err = d.Decode(out); err != nil {
		return errors.New("第三方平台返回格式无效")
	}
	return nil
}

func registerOAuth(mux *http.ServeMux, store *pool.Store, manager *auth.Manager) {
	states := &oauthStates{m: map[string]oauthState{}}
	back := func(w http.ResponseWriter, r *http.Request, path string, params url.Values) {
		target := path
		if len(params) > 0 {
			target += "?" + params.Encode()
		}
		http.Redirect(w, r, target, http.StatusFound)
	}
	fail := func(w http.ResponseWriter, r *http.Request, mode string, err error) {
		path := "/"
		if mode == "bind" {
			path = "/account"
		}
		back(w, r, path, url.Values{"oauth_error": {err.Error()}})
	}
	mux.HandleFunc("GET /api/v1/auth/oauth/{provider}/start", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("provider")
		p, ok := oauthProviders[name]
		mode := r.URL.Query().Get("mode")
		if mode != "bind" {
			mode = "login"
		}
		if !ok || store == nil || manager == nil || store.Ensure(r.Context()) != nil {
			fail(w, r, mode, errors.New("暂不支持该登录方式"))
			return
		}
		login, err := store.LoginNow(r.Context()).Unsealed()
		cfg := oauthSettings(login, name)
		if err != nil || !cfg.Ready() {
			fail(w, r, mode, fmt.Errorf("管理员尚未启用 %s 登录", p.Name))
			return
		}
		st := oauthState{provider: name, mode: mode, remember: r.URL.Query().Get("remember") == "1", expires: time.Now().Add(10 * time.Minute)}
		if mode == "bind" {
			u, ok := manager.Identity(r)
			if !ok {
				fail(w, r, mode, errors.New("请先登录再绑定"))
				return
			}
			st.userID = u.ID
		}
		key, err := states.put(st)
		if err != nil {
			fail(w, r, mode, err)
			return
		}
		settings, _ := store.Settings(r.Context())
		q := url.Values{"response_type": {"code"}, "client_id": {cfg.ClientID}, "redirect_uri": {publicOrigin(r, settings) + "/api/v1/auth/oauth/" + name + "/callback"}, "state": {key}}
		if p.Scope != "" {
			q.Set("scope", p.Scope)
		}
		// 回调来自第三方站点的跳转，需要 Lax 才能带上 Cookie
		http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: key, Path: "/api/v1/auth/oauth/", MaxAge: 600, HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, p.AuthURL+"?"+q.Encode(), http.StatusFound)
	})
	mux.HandleFunc("GET /api/v1/auth/oauth/{provider}/callback", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		name := r.PathValue("provider")
		p, known := oauthProviders[name]
		http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: "", Path: "/api/v1/auth/oauth/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		key := r.URL.Query().Get("state")
		cookie, _ := r.Cookie(oauthCookie)
		st, ok := states.take(key)
		if !known || !ok || cookie == nil || cookie.Value != key || st.provider != name {
			fail(w, r, st.mode, errors.New("登录请求已失效，请重新发起"))
			return
		}
		if msg := r.URL.Query().Get("error"); msg != "" {
			fail(w, r, st.mode, errors.New("已取消第三方授权"))
			return
		}
		if store == nil || store.Ensure(ctx) != nil {
			fail(w, r, st.mode, errors.New("数据库暂不可用"))
			return
		}
		login, err := store.LoginNow(ctx).Unsealed()
		cfg := oauthSettings(login, name)
		if err != nil || !cfg.Ready() {
			fail(w, r, st.mode, fmt.Errorf("管理员尚未启用 %s 登录", p.Name))
			return
		}
		settings, _ := store.Settings(ctx)
		profile, err := fetchProfile(ctx, p, cfg, r.URL.Query().Get("code"), publicOrigin(r, settings)+"/api/v1/auth/oauth/"+name+"/callback")
		if err != nil {
			fail(w, r, st.mode, err)
			return
		}
		if st.mode == "bind" {
			if err = store.LinkIdentity(ctx, st.userID, name, profile.Subject, profile.Login); err != nil {
				fail(w, r, st.mode, err)
				return
			}
			back(w, r, "/account", url.Values{"bound": {name}})
			return
		}
		u, err := store.UserByIdentity(ctx, name, profile.Subject)
		created := false
		if err != nil {
			if !store.CommunityNow(ctx).RegistrationEnabled {
				fail(w, r, st.mode, errors.New("该第三方账号尚未绑定，且管理员暂未开放注册"))
				return
			}
			if pool.CurrentMaintenance().Enabled {
				fail(w, r, st.mode, errors.New("维护期间暂停注册"))
				return
			}
			if u, err = createOAuthUser(ctx, store, p, name, profile); err != nil {
				fail(w, r, st.mode, err)
				return
			}
			created = true
		}
		if !u.Enabled {
			fail(w, r, st.mode, errors.New("账号已停用"))
			return
		}
		if err = manager.Issue(w, r, u, st.remember); err != nil {
			fail(w, r, st.mode, err)
			return
		}
		params := url.Values{"oauth": {name}}
		if created {
			params.Set("welcome", "1")
		}
		back(w, r, "/", params)
	})
}

// createOAuthUser 第三方首次登录时创建账号，邮箱稍后由用户补齐验证
func createOAuthUser(ctx context.Context, store *pool.Store, p oauthProvider, name string, profile oauthProfile) (pool.User, error) {
	username, err := store.AvailableUsername(ctx, profile.Login, p.Prefix)
	if err != nil {
		return pool.User{}, err
	}
	var b [24]byte
	if _, err = rand.Read(b[:]); err != nil {
		return pool.User{}, err
	}
	display := profile.Name
	if display == "" {
		display = profile.Login
	}
	if r := []rune(display); len(r) > 60 {
		display = string(r[:60])
	}
	u, err := store.CreateUser(pool.WithUser(ctx, pool.GuestUser), pool.UserInput{Username: username, DisplayName: display, Password: hex.EncodeToString(b[:])})
	if err != nil {
		return u, err
	}
	if err = store.LinkIdentity(ctx, u.ID, name, profile.Subject, profile.Login); err != nil {
		return u, err
	}
	return u, nil
}
