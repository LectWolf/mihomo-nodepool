package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnconfiguredSystemReportsActualState(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/system", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("system status: %d", rr.Code)
	}
	var result struct {
		Database databaseStatus `json:"database"`
		Core     struct {
			State string `json:"state"`
		} `json:"core"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Database.Configured || result.Database.Connected || result.Core.State != "not_integrated" {
		t.Fatal("preparation build must not claim a working database or core")
	}
}

func TestHealthAndReadinessAreSeparate(t *testing.T) {
	handler := Handler(nil)
	for path, want := range map[string]int{
		"/healthz":        http.StatusOK,
		"/readyz":         http.StatusServiceUnavailable,
		"/api/v1/missing": http.StatusNotFound,
	} {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			if rr.Code != want {
				t.Fatalf("status = %d, want %d", rr.Code, want)
			}
		})
	}
}
