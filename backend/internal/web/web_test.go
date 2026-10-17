package web

import (
	"bytes"
	"compress/gzip"
	"io"
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

func TestGzipAssets(t *testing.T) {
	js := bytes.Repeat([]byte("console.log('nodepool');\n"), 200)
	h := handler(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}, "assets/app.js": {Data: js}})
	r := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r.Header.Set("Accept-Encoding", "br, gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Header().Get("Content-Encoding") != "gzip" || rr.Header().Get("Vary") != "Accept-Encoding" || rr.Header().Get("Cache-Control") == "" {
		t.Fatalf("headers: %v", rr.Header())
	}
	z, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(z); !bytes.Equal(got, js) {
		t.Fatal("gzip body mismatch")
	}
	for _, accept := range []string{"", "gzip;q=0", "br"} {
		r = httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		r.Header.Set("Accept-Encoding", accept)
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		if rr.Header().Get("Content-Encoding") != "" || !bytes.Equal(rr.Body.Bytes(), js) {
			t.Fatalf("%q should be identity", accept)
		}
	}
}
