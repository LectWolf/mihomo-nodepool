package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLEmailRegistrationBindingAndOAuth(t *testing.T) {
	s := apiStore(t)
	a, e := auth.New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	codes := map[string]string{}
	oldSend := sendMail
	sendMail = func(_ context.Context, cfg pool.SMTPSettings, to string, m mailContent) error {
		body := m.Text
		if !strings.Contains(m.HTML, "<!DOCTYPE html>") || !strings.Contains(m.Subject, "MCII节点共享") {
			t.Errorf("mail should carry html and branded subject: %q", m.Subject)
		}
		if cfg.Password != "smtp-secret" {
			t.Errorf("smtp password not unsealed: %q", cfg.Password)
		}
		mu.Lock()
		defer mu.Unlock()
		if i := strings.Index(body, "验证码："); i >= 0 {
			codes[to] = body[i+len("验证码：") : i+len("验证码：")+6]
		}
		return nil
	}
	defer func() { sendMail = oldSend }()
	code := func(to string) string {
		mu.Lock()
		defer mu.Unlock()
		v := codes[to]
		delete(codes, to)
		return v
	}
	h := New(Options{Service: pool.NewService(s, core.New()), Auth: a, Updates: testUpdates{}})
	call := func(method, path, body string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	session := func(w *httptest.ResponseRecorder) *http.Cookie {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == "nodepool_session" && c.Value != "" {
				return c
			}
		}
		t.Fatalf("no session cookie: %v", w.Result().Cookies())
		return nil
	}

	// 管理员配置 SMTP 与 GitHub，密钥不会回显
	admin := session(call("POST", "/api/v1/auth/login", `{"password":"test-password"}`, nil, 200))
	var settings map[string]any
	_ = json.Unmarshal(call("GET", "/api/v1/settings", "", admin, 200).Body.Bytes(), &settings)
	login := settings["login"].(map[string]any)
	if suffixes := login["email_suffixes"].([]any); len(suffixes) != len(pool.DefaultEmailSuffixes) || suffixes[0] != "deepseek.com" {
		t.Fatalf("default suffixes: %v", suffixes)
	}
	login["smtp"] = map[string]any{"host": "smtp.test", "port": 465, "security": "ssl", "username": "bot", "password": "smtp-secret", "from": "noreply@deepseek.com", "from_name": "MCII节点共享"}
	login["github"] = map[string]any{"enabled": true, "client_id": "gh-id", "client_secret": "gh-secret"}
	raw, _ := json.Marshal(settings)
	if body := call("PUT", "/api/v1/settings", string(raw), admin, 200).Body.String(); strings.Contains(body, "smtp-secret") || strings.Contains(body, "gh-secret") || !strings.Contains(body, `"password_set":true`) {
		t.Fatalf("secrets leaked or missing: %s", body)
	}
	// 再次保存空密钥沿用旧值
	_ = json.Unmarshal(call("GET", "/api/v1/settings", "", admin, 200).Body.Bytes(), &settings)
	raw, _ = json.Marshal(settings)
	call("PUT", "/api/v1/settings", string(raw), admin, 200)
	call("POST", "/api/v1/settings/smtp-test", `{"to":"ops@qq.com"}`, admin, 200)
	if body := call("GET", "/api/v1/auth", "", nil, 200).Body.String(); !strings.Contains(body, `"email":true`) || !strings.Contains(body, `"github":true`) || !strings.Contains(body, `"linuxdo":false`) {
		t.Fatalf("login options: %s", body)
	}

	// 邮箱注册：后缀、验证码与用户名规则
	call("POST", "/api/v1/auth/email/code", `{"email":"x@example.com","purpose":"register"}`, nil, 400)
	call("POST", "/api/v1/auth/email/code", `{"email":"Alice@QQ.com","purpose":"register"}`, nil, 200)
	call("POST", "/api/v1/auth/email/code", `{"email":"alice@qq.com","purpose":"register"}`, nil, 429)
	aliceCode := code("alice@qq.com")
	if len(aliceCode) != 6 {
		t.Fatalf("code not delivered: %q", aliceCode)
	}
	call("POST", "/api/v1/auth/register", `{"username":"1alice","email":"alice@qq.com","code":"`+aliceCode+`","password":"alice-pass1"}`, nil, 400)
	call("POST", "/api/v1/auth/register", `{"username":"alice","email":"alice@qq.com","password":"alice-pass1"}`, nil, 400)
	call("POST", "/api/v1/auth/register", `{"username":"alice","email":"alice@qq.com","code":"000000x","password":"alice-pass1"}`, nil, 400)
	alice := session(call("POST", "/api/v1/auth/register", `{"username":"alice","email":"alice@qq.com","code":"`+aliceCode+`","password":"alice-pass1","display_name":"爱丽丝"}`, nil, 200))
	if body := call("GET", "/api/v1/me", "", alice, 200).Body.String(); !strings.Contains(body, `"email":"alice@qq.com"`) || !strings.Contains(body, `"email_verified":true`) {
		t.Fatalf("registered email: %s", body)
	}
	call("POST", "/api/v1/auth/email/code", `{"email":"alice@qq.com","purpose":"register"}`, nil, 409)

	// 邮箱密码登录、验证码登录与找回密码
	session(call("POST", "/api/v1/auth/login", `{"username":"ALICE@qq.com","password":"alice-pass1"}`, nil, 200))
	call("POST", "/api/v1/auth/email/code", `{"email":"nobody@qq.com","purpose":"login"}`, nil, 200)
	if code("nobody@qq.com") != "" {
		t.Fatal("code sent to unknown email")
	}
	call("POST", "/api/v1/auth/email/code", `{"email":"alice@qq.com","purpose":"login"}`, nil, 200)
	session(call("POST", "/api/v1/auth/login/code", `{"email":"alice@qq.com","code":"`+code("alice@qq.com")+`"}`, nil, 200))
	call("POST", "/api/v1/auth/email/code", `{"email":"alice@qq.com","purpose":"reset"}`, nil, 200)
	call("POST", "/api/v1/auth/password/reset", `{"email":"alice@qq.com","code":"`+code("alice@qq.com")+`","password":"alice-pass2"}`, nil, 200)
	call("GET", "/api/v1/me", "", alice, 401)
	alice = session(call("POST", "/api/v1/auth/login", `{"username":"alice","password":"alice-pass2"}`, nil, 200))

	// 没有邮箱的旧账号必须先补齐邮箱才能修改数据
	call("POST", "/api/v1/users", `{"username":"legacy","password":"legacy-pass"}`, admin, 201)
	legacy := session(call("POST", "/api/v1/auth/login", `{"username":"legacy","password":"legacy-pass"}`, nil, 200))
	if body := call("GET", "/api/v1/auth", "", legacy, 200).Body.String(); !strings.Contains(body, `"email_required":true`) {
		t.Fatalf("email requirement missing: %s", body)
	}
	if body := call("POST", "/api/v1/me/checkin", ``, legacy, 403).Body.String(); !strings.Contains(body, "email_required") {
		t.Fatalf("gate body: %s", body)
	}
	call("PATCH", "/api/v1/me", `{"display_name":"老用户"}`, legacy, 200)
	call("POST", "/api/v1/me/email/code", `{"email":"alice@qq.com"}`, legacy, 409)
	call("POST", "/api/v1/me/email/code", `{"email":"legacy@example.com"}`, legacy, 400)
	call("POST", "/api/v1/me/email/code", `{"email":"legacy@163.com"}`, legacy, 200)
	call("PUT", "/api/v1/me/email", `{"email":"legacy@163.com","code":"`+code("legacy@163.com")+`"}`, legacy, 200)
	if w := call("POST", "/api/v1/me/checkin", `{}`, legacy, 200); strings.Contains(w.Body.String(), "email_required") {
		t.Fatal("gate still active after binding")
	}

	// GitHub 首次登录创建账号，之后再次登录复用；已登录用户可以绑定
	subject := "42"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("client_secret") != "gh-secret" || r.Form.Get("code") != "good" {
				_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"tok"}`))
		case "/user":
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"id":` + subject + `,"login":"octo-cat","name":"Octo"}`))
		}
	}))
	defer fake.Close()
	oldProviders := oauthProviders["github"]
	oauthProviders["github"] = oauthProvider{Name: "GitHub", AuthURL: fake.URL + "/authorize", TokenURL: fake.URL + "/token", UserURL: fake.URL + "/user", Prefix: "gh"}
	defer func() { oauthProviders["github"] = oldProviders }()
	oauth := func(start string, cookie *http.Cookie, codeValue string) *httptest.ResponseRecorder {
		t.Helper()
		w := call("GET", start, "", cookie, 302)
		loc, _ := url.Parse(w.Header().Get("Location"))
		if !strings.HasPrefix(loc.String(), fake.URL+"/authorize") || loc.Query().Get("client_id") != "gh-id" {
			t.Fatalf("authorize redirect: %s", loc)
		}
		var state *http.Cookie
		for _, c := range w.Result().Cookies() {
			if c.Name == oauthCookie {
				state = c
			}
		}
		r := httptest.NewRequest("GET", "/api/v1/auth/oauth/github/callback?code="+codeValue+"&state="+loc.Query().Get("state"), nil)
		r.AddCookie(state)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, r)
		if res.Code != 302 {
			t.Fatalf("callback status %d: %s", res.Code, res.Body.String())
		}
		return res
	}
	if loc := oauth("/api/v1/auth/oauth/github/start", nil, "bad").Header().Get("Location"); !strings.Contains(loc, "oauth_error=") {
		t.Fatalf("bad code should fail: %s", loc)
	}
	first := oauth("/api/v1/auth/oauth/github/start", nil, "good")
	if loc := first.Header().Get("Location"); loc != "/?oauth=github&welcome=1" {
		t.Fatalf("first login redirect: %s", loc)
	}
	octo := session(first)
	if body := call("GET", "/api/v1/auth", "", octo, 200).Body.String(); !strings.Contains(body, `"username":"octo-cat"`) || !strings.Contains(body, `"email_required":true`) {
		t.Fatalf("oauth user: %s", body)
	}
	if loc := oauth("/api/v1/auth/oauth/github/start", nil, "good").Header().Get("Location"); loc != "/?oauth=github" {
		t.Fatalf("second login redirect: %s", loc)
	}
	if loc := oauth("/api/v1/auth/oauth/github/start?mode=bind", alice, "good").Header().Get("Location"); !strings.HasPrefix(loc, "/account?oauth_error=") {
		t.Fatalf("binding someone else's identity: %s", loc)
	}
	subject = "43"
	if loc := oauth("/api/v1/auth/oauth/github/start?mode=bind", alice, "good").Header().Get("Location"); loc != "/account?bound=github" {
		t.Fatalf("bind redirect: %s", loc)
	}
	if body := call("GET", "/api/v1/me/identities", "", alice, 200).Body.String(); !strings.Contains(body, `"provider":"github"`) || !strings.Contains(body, `"login":"octo-cat"`) {
		t.Fatalf("identities: %s", body)
	}
	call("DELETE", "/api/v1/me/identities/github", "", alice, 200)
	// 伪造 state 被拒绝
	r := httptest.NewRequest("GET", "/api/v1/auth/oauth/github/callback?code=good&state=forged", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, r)
	if !strings.Contains(res.Header().Get("Location"), "oauth_error=") {
		t.Fatalf("forged state: %s", res.Header().Get("Location"))
	}
}
