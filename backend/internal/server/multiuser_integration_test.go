package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"
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
	if alice.Role != "user" || alice.NodeLimit != 0 || alice.SourceLimit != 0 || alice.SubscriptionLimit != 0 {
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
	if pub := call("GET", "/api/v1/statistics", "", ac, "", 200).Body.String(); !strings.Contains(pub, `"heap_bytes":0`) {
		t.Fatal("public statistics should hide process internals")
	}
	stats := call("GET", "/api/v1/statistics", "", admin, "", 200)
	if !strings.Contains(stats.Body.String(), `"runtime"`) || !strings.Contains(stats.Body.String(), `"sampled_at"`) {
		t.Fatal("statistics response contract")
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
	call("POST", nodePath, "", ac, "", 403)
	call("POST", nodePath, "", admin, "", 202)
	subBody := fmt.Sprintf(`{"name":"my subscription","nodes":[{"node_id":%d,"source_id":%d}],"source_ids":[]}`, page.Items[0].ID, src.ID)
	savedResponse := call("POST", "/api/v1/subscriptions", subBody, ac, "", 201)
	var saved pool.SavedSubscription
	_ = json.Unmarshal(savedResponse.Body.Bytes(), &saved)
	if saved.SelectedCount != 1 || saved.Subscription.URL == "" || saved.Subscription.RuleProfileID == nil || *saved.Subscription.RuleProfileID != 0 {
		t.Fatal("subscription contract")
	}
	// Explicit null preserves the legacy export, including through omitted edits.
	nullBody := fmt.Sprintf(`{"name":"unbound","source_ids":[%d],"rule_profile_id":null}`, src.ID)
	nullResponse := call("POST", "/api/v1/subscriptions", nullBody, bc, "", 201)
	var unbound pool.SavedSubscription
	if e = json.Unmarshal(nullResponse.Body.Bytes(), &unbound); e != nil || unbound.Subscription.RuleProfileID != nil {
		t.Fatal("explicit null creation bound official rules")
	}
	nullPath := fmt.Sprintf("/api/v1/subscriptions/%d", unbound.Subscription.ID)
	nullEdit := call("PUT", nullPath, subBody, bc, "", 200)
	if e = json.Unmarshal(nullEdit.Body.Bytes(), &unbound); e != nil || unbound.Subscription.RuleProfileID != nil {
		t.Fatal("omitted edit migrated legacy null")
	}
	legacyDownload := call("GET", unbound.Subscription.URL+"?format=clash", "", nil, "", 200)
	if strings.Contains(legacyDownload.Body.String(), "dns:") || strings.Contains(legacyDownload.Body.String(), "🌩️ Cloudflare") {
		t.Fatal("explicit null changed legacy export")
	}
	call("POST", "/api/v1/subscriptions", fmt.Sprintf(`{"name":"invalid","source_ids":[%d],"rule_profile_id":-1}`, src.ID), ac, "", 400)
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
	var clashConfig struct {
		Groups []struct {
			Name string `yaml:"name"`
		} `yaml:"proxy-groups"`
		DNS struct {
			Nameserver []string `yaml:"nameserver"`
		} `yaml:"dns"`
	}
	if e = yaml.Unmarshal(clash.Body.Bytes(), &clashConfig); e != nil {
		t.Fatal(e)
	}
	hasCloudflare := false
	for _, group := range clashConfig.Groups {
		if group.Name == "🌩️ Cloudflare" {
			hasCloudflare = true
		}
	}
	if !hasCloudflare || len(clashConfig.DNS.Nameserver) != 3 || clashConfig.DNS.Nameserver[0] != "223.5.5.5" || clash.Header().Get("X-Subscription-Routing-Skipped") != "0" {
		t.Fatal("explicit clash priority and official routing/DNS")
	}
	profileDefault := call("GET", "/api/v1/rule-profiles/default", "", ac, "", 200)
	var template pool.RuleProfile
	if e = json.Unmarshal(profileDefault.Body.Bytes(), &template); e != nil || template.ID != 0 || template.Name != "官方默认规则" || !template.Official || !template.Readonly || len(template.Rules) != 9735 || !template.RoutingEnabled || !template.DNSEnabled {
		t.Fatal("default profile contract")
	}
	checkProfiles := func(cookie *http.Cookie, privateIDs ...int64) {
		t.Helper()
		response := call("GET", "/api/v1/rule-profiles", "", cookie, "", 200)
		var list struct {
			Items []pool.RuleProfile `json:"items"`
		}
		if e := json.Unmarshal(response.Body.Bytes(), &list); e != nil || len(list.Items) != len(privateIDs)+1 {
			t.Fatalf("profile list count: %s %v", response.Body.String(), e)
		}
		first := list.Items[0]
		if first.ID != 0 || !first.Official || !first.Readonly || len(first.Rules) != 9735 || first.Rules[0] != template.Rules[0] {
			t.Fatal("official profile not first or template changed")
		}
		for i, id := range privateIDs {
			if p := list.Items[i+1]; p.ID != id || p.Official || p.Readonly {
				t.Fatal("private profile metadata or ownership")
			}
		}
	}
	for _, cookie := range []*http.Cookie{admin, ac, bc} {
		checkProfiles(cookie)
		for _, method := range []string{"PUT", "DELETE"} {
			denied := call(method, "/api/v1/rule-profiles/0", `{}`, cookie, "", 403)
			if !strings.Contains(denied.Body.String(), "官方默认规则只读") {
				t.Fatal("missing official readonly error")
			}
		}
	}
	profileBody := `{"name":"my rules","routing_enabled":true,"dns_enabled":true,"dns_servers":[],"rules":["DOMAIN-SUFFIX,example.com,DIRECT","MATCH,NodePool"]}`
	profileResponse := call("POST", "/api/v1/rule-profiles", profileBody, ac, "", 201)
	var profile pool.RuleProfile
	if e = json.Unmarshal(profileResponse.Body.Bytes(), &profile); e != nil || profile.ID <= 0 || profile.Official || profile.Readonly {
		t.Fatal("created profile contract")
	}
	checkProfiles(ac, profile.ID)
	checkProfiles(bc)
	checkProfiles(admin)
	profilePath := fmt.Sprintf("/api/v1/rule-profiles/%d", profile.ID)
	call("PUT", profilePath, profileBody, bc, "", 404)
	call("DELETE", profilePath, "", bc, "", 404)
	call("POST", "/api/v1/rule-profiles", `{"name":"bad","rules":["MATCH,unsafe"]}`, ac, "", 400)
	// 每人最多 10 份规则组合
	extras := []int64{}
	for i := 2; i <= pool.RuleProfileLimit; i++ {
		var extra pool.RuleProfile
		_ = json.Unmarshal(call("POST", "/api/v1/rule-profiles", fmt.Sprintf(`{"name":"extra %d","rules":["MATCH,NodePool"]}`, i), ac, "", 201).Body.Bytes(), &extra)
		extras = append(extras, extra.ID)
	}
	if limited := call("POST", "/api/v1/rule-profiles", `{"name":"too many","rules":["MATCH,NodePool"]}`, ac, "", 400); !strings.Contains(limited.Body.String(), "最多保存 10 份") {
		t.Fatalf("rule profile limit: %s", limited.Body.String())
	}
	for _, id := range extras {
		call("DELETE", fmt.Sprintf("/api/v1/rule-profiles/%d", id), "", ac, "", 200)
	}
	foreignBind := fmt.Sprintf(`{"name":"foreign","source_ids":[%d],"rule_profile_id":%d}`, src.ID, profile.ID)
	call("POST", "/api/v1/subscriptions", foreignBind, bc, "", 404)
	bindBody := fmt.Sprintf(`{"name":"bound","source_ids":[%d],"rule_profile_id":%d}`, src.ID, profile.ID)
	call("PUT", path, bindBody, ac, "", 200)
	conflict := call("DELETE", profilePath, "", ac, "", 409)
	if !strings.Contains(conflict.Body.String(), "规则配置仍被订阅引用") {
		t.Fatal("profile conflict message")
	}
	legacyEdit := call("PUT", path, subBody, ac, "", 200)
	var retained pool.SavedSubscription
	if e = json.Unmarshal(legacyEdit.Body.Bytes(), &retained); e != nil || retained.Subscription.RuleProfileID == nil || *retained.Subscription.RuleProfileID != profile.ID {
		t.Fatal("legacy edit lost profile")
	}
	boundDownload := call("GET", saved.Subscription.URL+"?format=clash", "", nil, "", 200)
	if cd := boundDownload.Header().Get("Content-Disposition"); !strings.Contains(cd, "filename*=UTF-8''"+url.PathEscape(pool.SubscriptionDisplayName)) || strings.Contains(cd, "subscription.yaml") {
		t.Fatalf("profile filename: %s", cd)
	}
	// 历史组合可以恢复到原订阅
	var archives struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if e = json.Unmarshal(call("GET", "/api/v1/subscriptions/archives", "", ac, "", 200).Body.Bytes(), &archives); e != nil || len(archives.Items) == 0 {
		t.Fatalf("archives: %v", e)
	}
	restored := call("POST", fmt.Sprintf("/api/v1/subscriptions/archives/%d/restore", archives.Items[0].ID), fmt.Sprintf(`{"subscription_id":%d}`, saved.Subscription.ID), ac, "", 200)
	if !strings.Contains(restored.Body.String(), `"skipped":0`) {
		t.Fatalf("restore: %s", restored.Body.String())
	}
	call("POST", fmt.Sprintf("/api/v1/subscriptions/archives/%d/restore", archives.Items[0].ID), `{}`, bc, "", 404)
	call("PUT", path, subBody, ac, "", 200)
	for i := 0; i < pool.SubscriptionArchiveKeep+2; i++ {
		call("PUT", path, subBody, ac, "", 200)
	}
	var kept struct {
		Total int `json:"total"`
	}
	if _ = json.Unmarshal(call("GET", "/api/v1/subscriptions/archives", "", ac, "", 200).Body.Bytes(), &kept); kept.Total != pool.SubscriptionArchiveKeep {
		t.Fatalf("archives kept: %d", kept.Total)
	}
	if !strings.Contains(boundDownload.Body.String(), "223.5.5.5") || boundDownload.Header().Get("X-Subscription-Routing-Skipped") != "0" {
		t.Fatal("bound profile export contract")
	}
	patchBinding := fmt.Sprintf(`{"rule_profile_id":%d}`, profile.ID)
	call("PATCH", path, patchBinding, bc, "", 404)
	call("PATCH", path, `{}`, ac, "", 400)
	call("PATCH", path, `{"rule_profile_id":-1}`, ac, "", 400)
	call("PATCH", path, `{"rule_profile_id":"0"}`, ac, "", 400)
	call("PUT", path, fmt.Sprintf(`{"name":"invalid","source_ids":[%d],"rule_profile_id":-1}`, src.ID), ac, "", 400)
	call("PATCH", path, patchBinding, ac, "", 200)
	call("PATCH", fmt.Sprintf("/api/v1/sources/%d", src.ID), `{"enabled":false}`, ac, "", 200)
	// Official binding remains available to both users with an unavailable source.
	for _, binding := range []struct {
		path   string
		cookie *http.Cookie
		saved  pool.Subscription
	}{{path, ac, saved.Subscription}, {nullPath, bc, unbound.Subscription}} {
		response := call("PATCH", binding.path, `{"rule_profile_id":0}`, binding.cookie, "", 200)
		var changed pool.Subscription
		if e = json.Unmarshal(response.Body.Bytes(), &changed); e != nil || changed.RuleProfileID == nil || *changed.RuleProfileID != 0 || changed.URL != binding.saved.URL || changed.Name != binding.saved.Name || len(changed.Nodes) != len(binding.saved.Nodes) {
			t.Fatalf("official rule-only binding changed snapshot: %+v %v", changed, e)
		}
		for i, node := range changed.Nodes {
			if node != binding.saved.Nodes[i] {
				t.Fatal("official binding changed snapshot membership")
			}
		}
	}
	detached := call("PATCH", path, `{"rule_profile_id":null}`, ac, "", 200)
	var snapshot pool.Subscription
	if e = json.Unmarshal(detached.Body.Bytes(), &snapshot); e != nil || snapshot.RuleProfileID != nil || snapshot.URL != saved.Subscription.URL || len(snapshot.Nodes) != len(saved.Subscription.Nodes) {
		t.Fatal("rule-only update changed snapshot")
	}
	call("PATCH", fmt.Sprintf("/api/v1/sources/%d", src.ID), `{"enabled":true}`, ac, "", 200)
	call("DELETE", profilePath, "", ac, "", 200)
	checkProfiles(ac)
	checkProfiles(bc)
	checkProfiles(admin)
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
