package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLoginSessionLogoutAndCrossSite(t *testing.T) {
	m, e := New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	m.Register(mux)
	mux.HandleFunc("GET /api/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := m.Wrap(mux)
	call := func(method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if call("GET", "/api/private", "", nil, "").Code != 401 {
		t.Fatal("unguarded API")
	}
	if call("POST", "/api/v1/auth/login", `{"password":"wrong"}`, nil, "").Code != 401 {
		t.Fatal("wrong password accepted")
	}
	login := call("POST", "/api/v1/auth/login", `{"password":"test-password"}`, nil, "")
	if login.Code != 200 || len(login.Result().Cookies()) == 0 {
		t.Fatal("login failed")
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	if call("GET", "/api/private", "", cookie, "").Code != 204 {
		t.Fatal("session rejected")
	}
	if call("POST", "/api/v1/auth/logout", "{}", cookie, "https://other.example").Code != 403 {
		t.Fatal("cross-site request accepted")
	}
	call("POST", "/api/v1/auth/logout", "{}", cookie, "")
	if call("GET", "/api/private", "", cookie, "").Code != 401 {
		t.Fatal("logout retained session")
	}
}
func TestLoginLockoutIsScopedToIPPair(t *testing.T) {
	m, e := New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	m.Register(mux)
	h := m.Wrap(mux)
	attempt := func(ip, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
		r.RemoteAddr = ip + ":10000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < loginPairRule.limit; i++ {
		if code := attempt("198.51.100.10", `{"password":"bad"}`).Code; code != 401 {
			t.Fatalf("attempt %d: got %d", i, code)
		}
	}
	blocked := attempt("198.51.100.10", `{"password":"bad"}`)
	if blocked.Code != 429 || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("want 429 with Retry-After, got %d %q", blocked.Code, blocked.Header().Get("Retry-After"))
	}
	// 别的出口不受影响：以前全站共用一个计数器，这里会一起被锁。
	if code := attempt("198.51.100.11", `{"password":"test-password"}`).Code; code != 200 {
		t.Fatalf("other IP login: got %d", code)
	}
	// 同一出口的另一个账号也不受牵连。
	if code := attempt("198.51.100.10", `{"username":"ghost","password":"bad"}`).Code; code != 401 {
		t.Fatalf("other account same IP: got %d", code)
	}
}

func TestBootstrapPasswordPersists(t *testing.T) {
	dir := t.TempDir()
	a, e := New(dir, "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := New(dir, "")
	if e != nil {
		t.Fatal(e)
	}
	if a.digest != b.digest {
		t.Fatal("password changed after restart")
	}
	if _, e = os.Stat(a.PasswordFile); e != nil {
		t.Fatal(e)
	}
}
