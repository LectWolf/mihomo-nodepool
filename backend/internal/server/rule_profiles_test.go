package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestOfficialRuleProfileReadonlyHTTP(t *testing.T) {
	mux := http.NewServeMux()
	registerRuleProfiles(func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				writeError(w, err)
			}
		})
	}, pool.NewStore(nil))

	for _, method := range []string{"PUT", "DELETE"} {
		for _, id := range []string{"0", "00"} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/rule-profiles/"+id, strings.NewReader(`{"name":"changed","rules":["MATCH,REJECT"]}`)))
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "官方默认规则只读") {
				t.Fatalf("%s official profile: %d %s", method, w.Code, w.Body.String())
			}
		}
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/rule-profiles/default", nil))
	var got pool.RuleProfile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || !reflect.DeepEqual(got, pool.DefaultRuleProfile()) || got.ID != 0 || !got.Official || !got.Readonly || len(got.Rules) != 9735 {
		t.Fatal("official mutation changed the default template")
	}
}
