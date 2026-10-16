package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/go-sql-driver/mysql"
)

type testUpdates struct{}

func (testUpdates) Register(mux *http.ServeMux) {
	for _, p := range []string{"GET /api/v1/releases", "POST /api/v1/system/update", "POST /api/v1/system/rollback", "POST /api/v1/system/restart", "GET /api/v1/new-updater-route"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	}
}
func apiStore(t *testing.T) *pool.Store {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set MYSQL_TEST_DSN for isolated MySQL HTTP regression")
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(cfg.DBName, "nodepool_test") {
		t.Fatal("requires dedicated nodepool_test database")
	}
	cfg.DBName = fmt.Sprintf("nodepool_test_http_%d", time.Now().UnixNano())
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	admin := *cfg
	admin.DBName = ""
	adminDB, e := sql.Open("mysql", admin.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = adminDB.Exec("CREATE DATABASE `" + cfg.DBName + "` DEFAULT CHARACTER SET utf8mb4"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(); _, _ = adminDB.Exec("DROP DATABASE `" + cfg.DBName + "`"); adminDB.Close() })
	s := pool.NewStore(db)
	if e = s.Ensure(context.Background()); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestMySQLMultiuserHTTPContractAndPermissions(t *testing.T) {
	s := apiStore(t)
	a, e := auth.New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	h := New(Options{Service: pool.NewService(s, core.New()), Auth: a, Updates: testUpdates{}})
	call := func(method, path, body string, cookie *http.Cookie, ua string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if ua != "" {
			r.Header.Set("User-Agent", ua)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	anon := call("GET", "/api/v1/auth", "", nil, "", 200)
	if !strings.Contains(anon.Body.String(), `"user":null`) {
		t.Fatal("anonymous auth contract")
	}
	admin := call("POST", "/api/v1/auth/login", `{"password":"test-password"}`, nil, "", 200).Result().Cookies()[0]
	create := call("POST", "/api/v1/users", `{"username":"alice","password":"alice-password","display_name":"Alice"}`, admin, "", 201)
	var alice pool.User
	if e = json.Unmarshal(create.Body.Bytes(), &alice); e != nil {
		t.Fatal(e)
	}
	if alice.Role != "user" || alice.NodeLimit != 200 || alice.SourceLimit != 3 || alice.SubscriptionLimit != 1 {
		t.Fatalf("default user contract: %+v", alice)
	}
	call("POST", "/api/v1/users", `{"username":"bob","password":"bob-password","display_name":"Bob"}`, admin, "", 201)
	login := call("POST", "/api/v1/auth/login", `{"username":"alice","password":"alice-password"}`, nil, "", 200)
	ac := login.Result().Cookies()[0]
	if !strings.Contains(login.Body.String(), `"user":{"id":`) || !strings.Contains(login.Body.String(), `"username":"alice"`) {
		t.Fatal("login identity contract")
	}
	ac2 := call("POST", "/api/v1/auth/login", `{"username":"alice","password":"alice-password"}`, nil, "", 200).Result().Cookies()[0]
	bc := call("POST", "/api/v1/auth/login", `{"username":"bob","password":"bob-password"}`, nil, "", 200).Result().Cookies()[0]
	me := call("GET", "/api/v1/me", "", ac, "", 200)
	if !strings.Contains(me.Body.String(), `"provider_path":"/nodes?provider_id=`) || !strings.Contains(me.Body.String(), `"subscriptions":0`) {
		t.Fatal("me contract")
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/settings"}, {"PUT", "/api/v1/settings"}, {"GET", "/api/v1/users"}, {"POST", "/api/v1/users"}, {"PATCH", "/api/v1/users/1"}, {"POST", "/api/v1/nodes/test"}, {"GET", "/api/v1/releases"}, {"POST", "/api/v1/system/update"}, {"POST", "/api/v1/system/rollback"}, {"POST", "/api/v1/system/restart"}} {
		call(route.method, route.path, `{}`, ac, "", 403)
	}
	call("GET", "/api/v1/releases", "", admin, "", 204)
	call("GET", "/api/v1/new-updater-route", "", ac, "", 403)
	call("GET", "/api/v1/new-updater-route", "", admin, "", 204)
	call("PATCH", "/api/v1/users/1", `{"enabled":false}`, admin, "", 400)
	call("PATCH", "/api/v1/me", `{"enabled":true}`, ac, "", 400)
	system := call("GET", "/api/v1/system", "", ac, "", 200)
	if strings.Contains(system.Body.String(), "database") || strings.Contains(system.Body.String(), "tasks") {
		t.Fatal("ordinary system leaked admin status")
	}
	sourceResponse := call("POST", "/api/v1/sources", `{"name":"Alice source","kind":"url","url":"https://example.com/private?token=never-show","max_users":2}`, ac, "", 202)
	var src pool.Source
	_ = json.Unmarshal(sourceResponse.Body.Bytes(), &src)
	if src.OwnerID != alice.ID || src.MaxUsers != 2 || strings.Contains(sourceResponse.Body.String(), "never-show") {
		t.Fatal("source owner/redaction contract")
	}
	sourcePath := "/api/v1/sources/" + strconv.FormatInt(src.ID, 10)
	call("PATCH", sourcePath, `{"name":"stolen"}`, bc, "", 403)
	call("DELETE", sourcePath, "", bc, "", 403)
	call("POST", sourcePath+"/refresh", "", bc, "", 403)
	src, e = s.Source(context.Background(), src.ID)
	if e != nil {
		t.Fatal(e)
	}
	body := `proxies: [{name: shared, type: ss, server: example.com, port: 443, cipher: aes-128-gcm, password: secret}]`
	parsed, _ := pool.Parse(body, nil)
	if e = s.ApplySource(context.Background(), src, body, parsed); e != nil {
		t.Fatal(e)
	}
	page, _ := s.Nodes(context.Background(), pool.Filter{})
	nodePath := fmt.Sprintf("/api/v1/nodes/%d/test", page.Items[0].ID)
	call("POST", nodePath, "", bc, "", 403)
	call("POST", nodePath, "", ac, "", 202)
	subBody := fmt.Sprintf(`{"name":"my subscription","nodes":[{"node_id":%d,"source_id":%d}],"source_ids":[]}`, page.Items[0].ID, src.ID)
	savedResponse := call("POST", "/api/v1/subscriptions", subBody, ac, "", 201)
	var saved pool.SavedSubscription
	_ = json.Unmarshal(savedResponse.Body.Bytes(), &saved)
	if saved.SelectedCount != 1 || saved.Subscription.URL == "" {
		t.Fatal("subscription contract")
	}
	path := fmt.Sprintf("/api/v1/subscriptions/%d", saved.Subscription.ID)
	call("DELETE", path, "", bc, "", 404)
	call("POST", path+"/rotate", "", bc, "", 404)
	downloaded := call("GET", saved.Subscription.URL, "", nil, "v2rayN", 200)
	decoded, e := base64.StdEncoding.DecodeString(downloaded.Body.String())
	if e != nil || !strings.HasPrefix(string(decoded), "ss://") {
		t.Fatal("UA v2ray export")
	}
	if downloaded.Header().Get("Vary") != "User-Agent" || downloaded.Header().Get("X-Subscription-Skipped") != "0" || !strings.Contains(downloaded.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("download headers")
	}
	clash := call("GET", saved.Subscription.URL+"?format=clash", "", nil, "v2rayN", 200)
	if !strings.Contains(clash.Body.String(), "proxy-groups:") || !strings.Contains(clash.Body.String(), "rules:") {
		t.Fatal("explicit clash priority")
	}
	call("PATCH", "/api/v1/me", `{"display_name":"Alice Updated","password":"alice-new-password"}`, ac, "", 200)
	call("GET", "/api/v1/me", "", ac, "", 200)
	call("GET", "/api/v1/me", "", ac2, "", 401)
	call("POST", "/api/v1/auth/login", `{"username":"alice","password":"alice-password"}`, nil, "", 401)
	persisted, e := s.User(context.Background(), alice.ID)
	if e != nil || persisted.PasswordHash == "alice-new-password" || !strings.HasPrefix(persisted.PasswordHash, "$2") || !pool.CheckPassword(persisted.PasswordHash, "alice-new-password") {
		t.Fatal("persistent bcrypt hash")
	}
	rotatedResponse := call("POST", path+"/rotate", "", ac, "", 200)
	var rotated pool.Subscription
	_ = json.Unmarshal(rotatedResponse.Body.Bytes(), &rotated)
	call("GET", saved.Subscription.URL, "", nil, "", 404)
	call("GET", rotated.URL, "", nil, "", 200)
	_, e = s.DB.Exec(`UPDATE np_nodes SET config=JSON_SET(config,'$.smux',JSON_OBJECT('enabled',true))`)
	if e != nil {
		t.Fatal(e)
	}
	call("GET", rotated.URL+"?format=v2ray", "", nil, "", 422)
	call("PATCH", fmt.Sprintf("/api/v1/users/%d", alice.ID), `{"enabled":false}`, admin, "", 200)
	call("GET", "/api/v1/me", "", ac, "", 401)
	call("GET", rotated.URL, "", nil, "", 404)
	call("GET", "/api/v1/auth", "", ac, "", 200)
	// A fresh manager reads the persisted password, rather than overwriting it
	// with the environment bootstrap value on every process restart.
	restarted, e := auth.New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	restarted.BindStore(s)
	enabled := true
	_, _ = s.PatchUser(context.Background(), alice.ID, pool.UserPatch{Enabled: &enabled})
	mux := http.NewServeMux()
	restarted.Register(mux)
	w := httptest.NewRecorder()
	restarted.Wrap(mux).ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"alice","password":"alice-new-password"}`)))
	if w.Code != 200 {
		t.Fatalf("persistent login: %d %s", w.Code, w.Body.String())
	}
}
