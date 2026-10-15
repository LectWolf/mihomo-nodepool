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
