package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLGuestBrowseRegistrationAndFilters(t *testing.T) {
	s := apiStore(t)
	a, e := auth.New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
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
	// 访客只能读公开接口
	if body := call("GET", "/api/v1/auth", "", nil, 200).Body.String(); !strings.Contains(body, `"guest_browse":true`) || !strings.Contains(body, `"registration":true`) {
		t.Fatalf("auth flags: %s", body)
	}
	for _, p := range []string{"/api/v1/nodes", "/api/v1/sources/catalog", "/api/v1/providers", "/api/v1/levels", "/api/v1/titles"} {
		call("GET", p, "", nil, 200)
	}
	if body := call("GET", "/api/v1/system", "", nil, 200).Body.String(); strings.Contains(body, "database") {
		t.Fatalf("guest system leaks details: %s", body)
	}
	for _, p := range []string{"/api/v1/me", "/api/v1/subscriptions", "/api/v1/sources", "/api/v1/users", "/api/v1/settings", "/api/v1/rule-profiles"} {
		call("GET", p, "", nil, 401)
	}
	call("POST", "/api/v1/subscriptions", `{"name":"x","nodes":[]}`, nil, 401)

	// 自助注册后直接登录，重名与关闭注册会被拒绝
	reg := call("POST", "/api/v1/auth/register", `{"username":"newbie","password":"newbie-pass","display_name":"新人"}`, nil, 200)
	cookie := reg.Result().Cookies()[0]
	if !strings.Contains(call("GET", "/api/v1/me", "", cookie, 200).Body.String(), `"role":"user"`) {
		t.Fatal("registered user should be a normal user")
	}
	call("POST", "/api/v1/auth/register", `{"username":"newbie","password":"other-pass1"}`, nil, 409)
	call("POST", "/api/v1/auth/register", `{"username":"x","password":"short"}`, nil, 400)
	call("POST", "/api/v1/auth/register", `{"username":"sneaky","password":"sneaky-pass","role":"admin"}`, nil, 400)

	admin := call("POST", "/api/v1/auth/login", `{"password":"test-password"}`, nil, 200).Result().Cookies()[0]
	var settings map[string]any
	_ = json.Unmarshal(call("GET", "/api/v1/settings", "", admin, 200).Body.Bytes(), &settings)
	community := settings["community"].(map[string]any)
	if community["risk_pulls_per_hour"] != float64(120) {
		t.Fatalf("default pulls per hour: %v", community["risk_pulls_per_hour"])
	}
	community["guest_browse"], community["registration_enabled"] = false, false
	raw, _ := json.Marshal(settings)
	call("PUT", "/api/v1/settings", string(raw), admin, 200)
	call("GET", "/api/v1/nodes", "", nil, 401)
	call("POST", "/api/v1/auth/register", `{"username":"late","password":"late-pass1"}`, nil, 403)

	// 用户管理筛选
	call("POST", "/api/v1/users", `{"username":"off","password":"off-pass11"}`, admin, 201)
	if _, err := s.DB.Exec(`UPDATE np_users SET enabled=0 WHERE username='off'`); err != nil {
		t.Fatal(err)
	}
	count := func(query string) int {
		var v struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(call("GET", "/api/v1/users?"+query, "", admin, 200).Body.Bytes(), &v)
		return v.Total
	}
	if count("status=disabled") != 1 || count("role=admin") != 1 || count("risk=frozen") != 0 || count("risk=clean")+count("risk=flagged") != count("") {
		t.Fatalf("user filters: disabled=%d admin=%d", count("status=disabled"), count("role=admin"))
	}
}
