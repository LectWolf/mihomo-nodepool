package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

type businessRegistrar func(string, func(http.ResponseWriter, *http.Request) error)

func registerMultiuser(business businessRegistrar, s *pool.Store, a *auth.Manager) {
	business("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) error {
		u := pool.CurrentUser(r.Context())
		usage, e := s.Usage(r.Context(), u.ID)
		if e == nil {
			writeJSON(w, 200, map[string]any{"user": u, "usage": usage, "provider_path": pool.ProviderPath(u.ID)})
		}
		return e
	})
	business("PATCH /api/v1/me", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			DisplayName *string `json:"display_name"`
			Password    *string `json:"password"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		u, e := s.PatchUser(r.Context(), pool.CurrentUser(r.Context()).ID, pool.UserPatch{DisplayName: in.DisplayName, Password: in.Password})
		if e == nil {
			a.KeepSession(r, u)
			writeJSON(w, 200, u)
		}
		return e
	})
	business("GET /api/v1/users", func(w http.ResponseWriter, r *http.Request) error {
		f := catalogFilter(r)
		v, e := s.Users(r.Context(), f.Q, f.Page, f.PageSize)
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("POST /api/v1/users", func(w http.ResponseWriter, r *http.Request) error {
		var in pool.UserInput
		if e := decode(w, r, &in); e != nil {
			return e
		}
		u, e := s.CreateUser(r.Context(), in)
		if e == nil {
			writeJSON(w, 201, u)
		}
		return e
	})
	business("PATCH /api/v1/users/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		var in pool.UserPatch
		if e = decode(w, r, &in); e != nil {
			return e
		}
		u, e := s.PatchUser(r.Context(), id, in)
		if e == nil {
			if u.ID == pool.CurrentUser(r.Context()).ID {
				a.KeepSession(r, u)
			}
			writeJSON(w, 200, u)
		}
		return e
	})
	business("GET /api/v1/sources/catalog", func(w http.ResponseWriter, r *http.Request) error {
		v, e := s.Catalog(r.Context(), catalogFilter(r))
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("GET /api/v1/providers", func(w http.ResponseWriter, r *http.Request) error {
		v, e := s.Providers(r.Context(), catalogFilter(r))
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	follow := func(follow bool) func(http.ResponseWriter, *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			id, e := idOf(r)
			if e != nil {
				return e
			}
			if e = s.Follow(r.Context(), id, follow); e == nil {
				writeJSON(w, 200, map[string]bool{"followed": follow})
			}
			return e
		}
	}
	business("POST /api/v1/providers/{id}/follow", follow(true))
	business("DELETE /api/v1/providers/{id}/follow", follow(false))
	business("GET /api/v1/subscriptions", func(w http.ResponseWriter, r *http.Request) error {
		items, e := s.Subscriptions(r.Context())
		if e == nil {
			writeJSON(w, 200, map[string]any{"items": items})
		}
		return e
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
			var in pool.SubscriptionInput
			if e = decode(w, r, &in); e != nil {
				return e
			}
			v, e := s.SaveSubscription(r.Context(), id, in)
			if e == nil {
				code := 200
				if create {
					code = 201
				}
				writeJSON(w, code, v)
			}
			return e
		}
	}
	business("POST /api/v1/subscriptions", save(true))
	business("PUT /api/v1/subscriptions/{id}", save(false))
	business("DELETE /api/v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		_, e = s.MutateSubscription(r.Context(), id, false)
		if e == nil {
			writeJSON(w, 200, map[string]bool{"deleted": true})
		}
		return e
	})
	business("POST /api/v1/subscriptions/{id}/rotate", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		v, e := s.MutateSubscription(r.Context(), id, true)
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
}
func catalogFilter(r *http.Request) pool.CatalogFilter {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("page_size"))
	provider, _ := strconv.ParseInt(q.Get("provider_id"), 10, 64)
	return pool.CatalogFilter{Q: q.Get("q"), ProviderID: provider, Following: q.Get("following") == "1", Page: page, PageSize: size}
}
func registerSubscriptionDownload(mux *http.ServeMux, s *pool.Store) {
	mux.HandleFunc("GET /sub/{token}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, private, max-age=0")
		w.Header().Set("Vary", "User-Agent")
		w.Header().Set("Referrer-Policy", "no-referrer")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if e := s.Ensure(ctx); e != nil {
			writeError(w, e)
			return
		}
		format, e := pool.ExportFormat(r.URL.Query().Get("format"), r.UserAgent())
		if e != nil {
			writeError(w, e)
			return
		}
		nodes, e := s.SubscriptionNodes(ctx, r.PathValue("token"))
		if e != nil {
			writeError(w, e)
			return
		}
		raw, skipped, e := pool.ExportSubscription(nodes, format)
		w.Header().Set("X-Subscription-Skipped", strconv.Itoa(skipped))
		w.Header().Set("X-Subscription-Format", format)
		if e != nil {
			writeError(w, e)
			return
		}
		filename := "subscription.yaml"
		contentType := "application/yaml; charset=utf-8"
		if format == "v2ray" {
			filename = "subscription.txt"
			contentType = "text/plain; charset=utf-8"
			w.Header().Set("X-Subscription-Compatibility", "v2ray-compatible URI import only")
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
		w.WriteHeader(200)
		_, _ = w.Write(raw)
	})
}
