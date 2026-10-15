package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestAuthenticatedSystemAndDatabaseUnavailable(t *testing.T) {
	a, e := auth.New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	h := New(Options{Service: pool.NewService(pool.NewStore(nil), core.New()), Auth: a})
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 503, "/api/v1/system": 401, "/api/v1/nodes": 401} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("%s = %d", path, w.Code)
		}
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"password":"test-password"}`)))
	if login.Code != 200 {
		t.Fatal("login failed")
	}
	for path, want := range map[string]int{"/api/v1/system": 200, "/api/v1/nodes": 503, "/api/v1/missing": 404} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(login.Result().Cookies()[0])
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s = %d", path, w.Code)
		}
		if path == "/api/v1/system" && !strings.Contains(w.Body.String(), `"state":"ready"`) {
			t.Fatal("embedded engine missing")
		}
	}
}
