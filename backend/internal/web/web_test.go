package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestSPAFallbackDoesNotHideMissingAssets(t *testing.T) {
	assets := fstest.MapFS{"index.html": {Data: []byte("<html>nodepool</html>")}}
	h := handler(assets)
	for path, want := range map[string]int{
		"/":                  http.StatusOK,
		"/settings":          http.StatusOK,
		"/assets/missing.js": http.StatusNotFound,
		"/.env":              http.StatusNotFound,
	} {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			if rr.Code != want {
				t.Fatalf("status = %d, want %d", rr.Code, want)
			}
		})
	}
}
