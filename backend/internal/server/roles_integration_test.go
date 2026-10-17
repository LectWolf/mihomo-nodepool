package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLRolesMaintenanceAndPersistentSessions(t *testing.T) {
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
	login := func(body string) *http.Cookie {
		t.Helper()
		return call("POST", "/api/v1/auth/login", body, nil, 200).Result().Cookies()[0]
	}
	admin := login(`{"password":"test-password"}`)
	call("POST", "/api/v1/users", `{"username":"mgr","password":"manager-pass","role":"manager"}`, admin, 201)
	call("POST", "/api/v1/users", `{"username":"plain","password":"plain-pass1"}`, admin, 201)
	call("POST", "/api/v1/users", `{"username":"boss","password":"boss-pass1","role":"admin"}`, admin, 201)
	call("POST", "/api/v1/users", `{"username":"bad","password":"bad-pass11","role":"root"}`, admin, 400)

	// 默认会话不持久，确认“保持登录”后 Cookie 才有 30 天有效期
	mgr := login(`{"username":"mgr","password":"manager-pass"}`)
	if mgr.MaxAge != 0 {
		t.Fatalf("session cookie should not persist: %d", mgr.MaxAge)
	}
	persisted := call("POST", "/api/v1/auth/persist", `{"remember":true}`, mgr, 200)
	if c := persisted.Result().Cookies(); len(c) == 0 || c[0].MaxAge != 30*24*3600 || !strings.Contains(persisted.Body.String(), `"persistent":true`) {
		t.Fatalf("persist cookie: %+v %s", c, persisted.Body.String())
	}
	// 进程重启后从数据库恢复会话
	restarted, e := auth.New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	h = New(Options{Service: pool.NewService(s, core.New()), Auth: restarted, Updates: testUpdates{}})
	if !strings.Contains(call("GET", "/api/v1/auth", "", mgr, 200).Body.String(), `"username":"mgr"`) {
		t.Fatal("persistent session lost after restart")
	}
	admin = login(`{"password":"test-password","remember":true}`)
	if admin.MaxAge != 30*24*3600 {
		t.Fatal("remember login should persist")
	}

	users := s.DB
	var plainID, bossID int64
	_ = users.QueryRow(`SELECT id FROM np_users WHERE username='plain'`).Scan(&plainID)
	_ = users.QueryRow(`SELECT id FROM np_users WHERE username='boss'`).Scan(&bossID)
	id := func(v int64) string { return strconv.FormatInt(v, 10) }
	// 用户管理员能管理普通用户，但不能改角色、不能管理管理员、不能访问系统设置
	call("GET", "/api/v1/users", "", mgr, 200)
	call("PATCH", "/api/v1/users/"+id(plainID), `{"display_name":"Plain"}`, mgr, 200)
	call("PATCH", "/api/v1/users/"+id(plainID), `{"role":"manager"}`, mgr, 403)
	call("PATCH", "/api/v1/users/"+id(bossID), `{"display_name":"x"}`, mgr, 403)
	call("POST", "/api/v1/users", `{"username":"sneaky","password":"sneaky-pass","role":"admin"}`, mgr, 403)
	call("GET", "/api/v1/settings", "", mgr, 403)
	call("GET", "/api/v1/statistics", "", mgr, 200)
	call("PATCH", "/api/v1/users/1", `{"role":"user"}`, admin, 400)
	call("PATCH", "/api/v1/users/"+id(plainID), `{"node_limit":0,"source_limit":0,"subscription_limit":0}`, admin, 200)

	// 维护模式：读取正常，普通写操作返回 503，管理员与登录不受影响
	settings, e := s.Settings(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	settings.Maintenance = pool.Maintenance{Enabled: true, Message: "升级数据库"}
	if e = s.SaveSettings(t.Context(), settings); e != nil {
		t.Fatal(e)
	}
	plain := login(`{"username":"plain","password":"plain-pass1"}`)
	if !strings.Contains(call("GET", "/api/v1/system", "", plain, 200).Body.String(), `"enabled":true`) {
		t.Fatal("system maintenance flag")
	}
	call("GET", "/api/v1/subscriptions", "", plain, 200)
	blocked := call("POST", "/api/v1/subscriptions", `{"name":"x","nodes":[]}`, plain, 503)
	if !strings.Contains(blocked.Body.String(), `"maintenance":true`) || !strings.Contains(blocked.Body.String(), "升级数据库") {
		t.Fatal(blocked.Body.String())
	}
	call("PATCH", "/api/v1/users/"+id(plainID), `{"display_name":"Plain2"}`, admin, 200)
	call("POST", "/api/v1/auth/logout", "", plain, 200)
	settings.Maintenance.Enabled = false
	if e = s.SaveSettings(t.Context(), settings); e != nil {
		t.Fatal(e)
	}
	call("GET", "/api/v1/titles", "", mgr, 200)
	call("GET", "/api/v1/me/provider-trend?days=7", "", mgr, 200)
}
