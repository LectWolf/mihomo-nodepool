package pool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchUsagePrefersHead(t *testing.T) {
	var methods []string
	headOK := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodHead && !headOK {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=10")
		_, _ = w.Write([]byte("proxies: []"))
	}))
	defer srv.Close()
	if u := fetchUsage(context.Background(), srv.Client(), srv.URL); u == nil || len(methods) != 1 || methods[0] != http.MethodHead {
		t.Fatalf("head only: %+v %v", u, methods)
	}
	methods, headOK = nil, false
	if u := fetchUsage(context.Background(), srv.Client(), srv.URL); u == nil || len(methods) != 2 || methods[1] != http.MethodGet {
		t.Fatalf("get fallback: %+v %v", u, methods)
	}
}
