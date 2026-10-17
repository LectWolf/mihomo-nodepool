package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestClientIPTrustsOnlyConfiguredProxies(t *testing.T) {
	_, proxy, _ := net.ParseCIDR("10.0.0.0/8")
	r := httptest.NewRequest("GET", "/sub/x", nil)
	r.RemoteAddr = "203.0.113.5:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := clientIP(r, []*net.IPNet{proxy}); got != "203.0.113.5" {
		t.Fatalf("untrusted remote used header: %s", got)
	}
	r.RemoteAddr = "10.1.2.3:443"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 198.51.100.7, 10.9.9.9")
	if got := clientIP(r, []*net.IPNet{proxy}); got != "198.51.100.7" {
		t.Fatalf("forwarded client: %s", got)
	}
	r.Header.Set("X-Forwarded-For", "garbage")
	if got := clientIP(r, []*net.IPNet{proxy}); got != "10.1.2.3" {
		t.Fatalf("invalid header: %s", got)
	}
}

func TestMySQLEconomyHTTPContract(t *testing.T) {
	s := apiStore(t)
	a, err := auth.New(t.TempDir(), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Service: pool.NewService(s, core.New()), Auth: a, Updates: testUpdates{}})
	call := func(method, path string, body []byte, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
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
	admin := call("POST", "/api/v1/auth/login", []byte(`{"password":"test-password"}`), nil, 200).Result().Cookies()[0]
	created := call("POST", "/api/v1/users", []byte(`{"username":"carol","password":"carol-password"}`), admin, 201)
	var carol pool.User
	_ = json.Unmarshal(created.Body.Bytes(), &carol)
	cc := call("POST", "/api/v1/auth/login", []byte(`{"username":"carol","password":"carol-password"}`), nil, 200).Result().Cookies()[0]
	me := call("GET", "/api/v1/me", nil, cc, 200)
	if !strings.Contains(me.Body.String(), `"limits":{"nodes":200`) {
		t.Fatalf("me limits: %s", me.Body.String())
	}
	wallet := call("GET", "/api/v1/me/wallet", nil, cc, 200)
	if !strings.Contains(wallet.Body.String(), `"makeup_price":10`) {
		t.Fatalf("wallet rules: %s", wallet.Body.String())
	}
	call("POST", "/api/v1/me/boosts", []byte(`{"kind":"node"}`), cc, 409)
	call("POST", "/api/v1/me/boosts", []byte(`{"kind":"nope"}`), cc, 400)
	call("GET", "/api/v1/me/ledger?page=1", nil, cc, 200)
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	call("POST", "/api/v1/me/avatar", []byte("not an image"), cc, 400)
	up := call("POST", "/api/v1/me/avatar", buf.Bytes(), cc, 200)
	if !strings.Contains(up.Body.String(), `"avatar_version":1`) {
		t.Fatalf("avatar version: %s", up.Body.String())
	}
	got := call("GET", "/api/v1/avatars/"+jsonID(carol.ID)+"?v=1", nil, admin, 200)
	if got.Header().Get("Content-Type") != "image/png" || !strings.Contains(got.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("avatar headers %v", got.Header())
	}
	call("DELETE", "/api/v1/me/avatar", nil, cc, 200)
	call("GET", "/api/v1/avatars/"+jsonID(carol.ID), nil, admin, 404)
	call("GET", "/api/v1/feedback", nil, cc, 400)
	call("POST", "/api/v1/feedback", []byte(`{"target":"source","source_id":999999,"kind":"ok"}`), cc, 404)
	call("POST", "/api/v1/users/"+jsonID(carol.ID)+"/risk", []byte(`{"freeze_hours":1}`), cc, 403)
	risk := call("POST", "/api/v1/users/"+jsonID(carol.ID)+"/risk", []byte(`{"freeze_hours":1}`), admin, 200)
	if !strings.Contains(risk.Body.String(), `"frozen"`) {
		t.Fatalf("freeze flag: %s", risk.Body.String())
	}
	users := call("GET", "/api/v1/users", nil, admin, 200)
	if !strings.Contains(users.Body.String(), `"risk":{`) || !strings.Contains(users.Body.String(), `"limits":{`) {
		t.Fatalf("users risk: %s", users.Body.String())
	}
	system := call("GET", "/api/v1/system", nil, admin, 200)
	if !strings.Contains(system.Body.String(), `"data_encryption":false`) {
		t.Fatalf("system encryption: %s", system.Body.String())
	}
}

func jsonID(id int64) string {
	raw, _ := json.Marshal(id)
	return string(raw)
}
