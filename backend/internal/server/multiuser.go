package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

type businessRegistrar func(string, func(http.ResponseWriter, *http.Request) error)

func registerMultiuser(business businessRegistrar, s *pool.Store, a *auth.Manager) {
	registerRuleProfiles(business, s)
	registerCommunity(business, s)
	registerEconomy(business, s)
	business("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) error {
		u := pool.CurrentUser(r.Context())
		usage, e := s.Usage(r.Context(), u.ID)
		if e != nil {
			return e
		}
		limits, e := s.EffectiveLimits(r.Context(), u)
		if e == nil {
			writeJSON(w, 200, map[string]any{"user": u, "usage": usage, "limits": limits, "provider_path": pool.ProviderPath(u.ID)})
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
		q := r.URL.Query()
		uf := pool.UserFilter{Q: f.Q, Role: q.Get("role"), Status: q.Get("status"), Quota: q.Get("quota")}
		// 风控标记需要实时计算，先算出命中的用户再分页
		if risk := q.Get("risk"); risk == "flagged" || risk == "frozen" || risk == "clean" {
			all, e := s.UserIDs(r.Context(), uf)
			if e != nil {
				return e
			}
			summaries, e := s.RiskSummaries(r.Context(), all)
			if e != nil {
				return e
			}
			uf.IDs = []int64{}
			for _, id := range all {
				flags := summaries[id].Flags
				if (risk == "flagged" && len(flags) > 0) || (risk == "frozen" && slices.Contains(flags, "frozen")) || (risk == "clean" && len(flags) == 0) {
					uf.IDs = append(uf.IDs, id)
				}
			}
		}
		v, e := s.Users(r.Context(), uf, f.Page, f.PageSize)
		if e != nil {
			return e
		}
		items, _ := v["items"].([]pool.User)
		ids := []int64{}
		limits := map[int64]pool.EffectiveLimits{}
		for _, u := range items {
			ids = append(ids, u.ID)
			if limits[u.ID], e = s.EffectiveLimits(r.Context(), u); e != nil {
				return e
			}
		}
		risks, e := s.RiskSummaries(r.Context(), ids)
		if e != nil {
			return e
		}
		v["limits"], v["risk"] = limits, risks
		writeJSON(w, 200, v)
		return nil
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
	business("PATCH /api/v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		var in struct {
			RuleProfileID json.RawMessage `json:"rule_profile_id"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if len(in.RuleProfileID) == 0 {
			return errors.New("请提供rule_profile_id：0选择官方默认规则，正整数选择私人规则，null解除绑定")
		}
		var profileID *int64
		if e = json.Unmarshal(in.RuleProfileID, &profileID); e != nil {
			return errors.New("规则配置ID必须为0（官方默认规则）、正整数（私人规则）或null（不绑定）")
		}
		item, e := s.BindSubscriptionRule(r.Context(), id, profileID)
		if e == nil {
			writeJSON(w, 200, item)
		}
		return e
	})
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
	return pool.CatalogFilter{Q: q.Get("q"), ProviderID: provider, Following: q.Get("following") == "1", Sort: q.Get("sort"), Available: q.Get("available") == "1", Page: page, PageSize: size}
}
func registerSubscriptionDownload(mux *http.ServeMux, s *pool.Store, trusted []*net.IPNet) {
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
		if e := s.RecordPull(ctx, r.PathValue("token"), clientIP(r, trusted)); errors.Is(e, pool.ErrRateLimited) {
			w.Header().Set("Retry-After", "600")
			writeError(w, e)
			return
		}
		format, e := pool.ExportFormat(r.URL.Query().Get("format"), r.UserAgent())
		if e != nil {
			writeError(w, e)
			return
		}
		raw, skipped, routingSkipped, e := s.ExportSavedSubscription(ctx, r.PathValue("token"), format)
		user, infoErr := s.SubscriptionDeliveryInfo(ctx, r.PathValue("token"))
		if infoErr != nil {
			writeError(w, infoErr)
			return
		}
		settings, settingsErr := s.Settings(ctx)
		if settingsErr != nil {
			writeError(w, settingsErr)
			return
		}
		origin := publicOrigin(r, settings)
		setProfileName(w)
		w.Header().Set("Profile-Web-Page-Url", origin+"/subscriptions")
		if user.SubscriptionExpiresAt != nil {
			w.Header().Set("Subscription-Userinfo", "expire="+strconv.FormatInt(user.SubscriptionExpiresAt.Unix(), 10))
		}
		if errors.Is(e, pool.ErrSubscriptionInactive) {
			raw, e = inactiveSubscription(format)
			w.Header().Set("X-Subscription-State", "expired")
		} else if e == nil {
			raw, e = attachKeepalive(raw, format, user, origin)
			if e == nil {
				e = s.TouchSubscriptionPull(ctx, r.PathValue("token"))
			}
			w.Header().Set("X-Subscription-State", "active")
		}
		w.Header().Set("X-Subscription-Routing-Skipped", strconv.Itoa(routingSkipped))
		w.Header().Set("X-Subscription-Skipped", strconv.Itoa(skipped))
		w.Header().Set("X-Subscription-Format", format)
		if e != nil {
			writeError(w, e)
			return
		}
		contentType := "application/yaml; charset=utf-8"
		if format == "v2ray" {
			contentType = "text/plain; charset=utf-8"
			w.Header().Set("X-Subscription-Compatibility", "v2ray-compatible URI import only")
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(200)
		_, _ = w.Write(raw)
	})
}
