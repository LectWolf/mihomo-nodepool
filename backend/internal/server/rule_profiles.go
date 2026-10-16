package server

import (
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"net/http"
)

func registerRuleProfiles(business businessRegistrar, s *pool.Store) {
	business("GET /api/v1/rule-profiles", func(w http.ResponseWriter, r *http.Request) error {
		items, e := s.RuleProfiles(r.Context())
		if e == nil {
			writeJSON(w, 200, map[string]any{"items": items})
		}
		return e
	})
	business("GET /api/v1/rule-profiles/default", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, pool.DefaultRuleProfile())
		return nil
	})
	save := func(create bool) func(http.ResponseWriter, *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			var id int64
			var e error
			if !create {
				id, e = idOf(r)
				if e != nil {
					return e
				}
			}
			var in pool.RuleProfileInput
			if e = decode(w, r, &in); e != nil {
				return e
			}
			p, e := s.SaveRuleProfile(r.Context(), id, in)
			if e == nil {
				code := 200
				if create {
					code = 201
				}
				writeJSON(w, code, p)
			}
			return e
		}
	}
	business("POST /api/v1/rule-profiles", save(true))
	business("PUT /api/v1/rule-profiles/{id}", save(false))
	business("DELETE /api/v1/rule-profiles/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		if e = s.DeleteRuleProfile(r.Context(), id); e == nil {
			writeJSON(w, 200, map[string]bool{"deleted": true})
		}
		return e
	})
}
