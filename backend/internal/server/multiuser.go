package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
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
	guard := newPasswordGuard()
	business("PATCH /api/v1/me", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			DisplayName     *string `json:"display_name"`
			Password        *string `json:"password"`
			CurrentPassword string  `json:"current_password"`
			BeaconEnabled   *bool   `json:"beacon_enabled"`
			AutoPause       *bool   `json:"auto_pause_dead_sources"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		id := pool.CurrentUser(r.Context()).ID
		if in.Password != nil {
			// 已有密码的账号必须先验证当前密码，没有密码的账号（旧站迁移、第三方注册）可直接设置
			cur, e := s.User(r.Context(), id)
			if e != nil {
				return e
			}
			if cur.PasswordHash != "" {
				if !guard.allow(id) {
					return pool.ErrTooManyAttempts
				}
				if !pool.CheckPassword(cur.PasswordHash, in.CurrentPassword) {
					guard.fail(id)
					return errors.New("当前密码不正确")
				}
				guard.reset(id)
			}
		}
		if in.BeaconEnabled != nil {
			if e := s.SetBeacon(r.Context(), id, *in.BeaconEnabled); e != nil {
				return e
			}
		}
		if in.AutoPause != nil {
			if e := s.SetAutoPause(r.Context(), id, *in.AutoPause); e != nil {
				return e
			}
		}
		u, e := s.PatchUser(r.Context(), id, pool.UserPatch{DisplayName: in.DisplayName, Password: in.Password})
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
			ids, exclude, e := s.RiskUserIDs(r.Context(), uf, risk)
			if e != nil {
				return e
			}
			if risk == "clean" {
				uf.ExcludeIDs = exclude
			} else {
				uf.IDs = ids
			}
		}
		v, e := s.Users(r.Context(), uf, f.Page, f.PageSize)
		if e != nil {
			return e
		}
		items, _ := v["items"].([]pool.User)
		ids := make([]int64, 0, len(items))
		for _, u := range items {
			ids = append(ids, u.ID)
		}
		limits, e := s.BatchLimits(r.Context(), items)
		if e != nil {
			return e
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
	countries := []string{}
	for _, c := range strings.Split(strings.ToUpper(q.Get("countries")), ",") {
		if len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z' && len(countries) < 20 {
			countries = append(countries, c)
		}
	}
	return pool.CatalogFilter{Q: q.Get("q"), ProviderID: provider, Following: q.Get("following") == "1", Exclusive: q.Get("exclusive") == "1", Sort: q.Get("sort"), Available: q.Get("available") == "1", Countries: countries, Page: page, PageSize: size}
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
		traffic.TagRequest(r, traffic.KindSub, format)
		user, e := s.SubscriptionDeliveryInfo(ctx, r.PathValue("token"))
		if e != nil {
			writeError(w, e)
			return
		}
		settings, e := s.Settings(ctx)
		if e != nil {
			writeError(w, e)
			return
		}
		origin := publicOrigin(r, settings)
		active := user.SubscriptionExpiresAt != nil && user.SubscriptionExpiresAt.After(time.Now().UTC())
		// 只缓存 clash 大订阅；v2ray 体积小，且节点配置可能被外部直接改写，保持实时生成。
		cacheable := format == "clash" && active
		cacheKey := subscriptionCacheKey(r, format, user, trusted)
		if cacheable {
			if entry, ok := s.CachedSubscription(cacheKey); ok {
				writeSubscriptionHeaders(w, entry, origin, user)
				_ = s.TouchSubscriptionPull(ctx, r.PathValue("token"))
				serveSubscription(w, r, entry)
				return
			}
		}
		raw, skipped, routingSkipped, nodes, e := s.ExportSavedSubscriptionNodes(ctx, r.PathValue("token"), format)
		if format == "v2ray" && errors.Is(e, pool.ErrUnexportable) {
			raw, e = v2rayNotice(v2rayEmpty), nil
		}
		state := "active"
		if errors.Is(e, pool.ErrSubscriptionInactive) {
			raw, e = inactiveSubscription(format)
			state = "expired"
		} else if e == nil {
			raw, e = attachKeepalive(raw, format, user, origin)
			if e == nil && format == "clash" && user.BeaconEnabled {
				raw = attachBeacons(ctx, s, raw, r.PathValue("token"), nodes, origin, clientIP(r, trusted))
			}
			if e == nil {
				e = s.TouchSubscriptionPull(ctx, r.PathValue("token"))
			}
		}
		if e != nil {
			writeError(w, e)
			return
		}
		entry := pool.SubscriptionCacheEntry{
			Body:           raw,
			ETag:           subscriptionETag(raw),
			State:          state,
			Format:         format,
			Skipped:        skipped,
			RoutingSkipped: routingSkipped,
		}
		if cacheable {
			s.CacheSubscription(cacheKey, entry)
		}
		writeSubscriptionHeaders(w, entry, origin, user)
		serveSubscription(w, r, entry)
	})
}

func subscriptionCacheKey(r *http.Request, format string, u pool.User, trusted []*net.IPNet) string {
	key := r.PathValue("token") + "|" + format + "|" + strconv.FormatInt(u.ID, 10)
	if format == "clash" && u.BeaconEnabled {
		// 回报节点与客户端 IP 相关，按 IP 分桶避免串数据。
		sum := sha256.Sum256([]byte(clientIP(r, trusted)))
		key += "|" + hex.EncodeToString(sum[:8])
	}
	return key
}

func subscriptionETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func writeSubscriptionHeaders(w http.ResponseWriter, e pool.SubscriptionCacheEntry, origin string, u pool.User) {
	setProfileName(w)
	w.Header().Set("Profile-Web-Page-Url", origin+"/subscriptions")
	if u.SubscriptionExpiresAt != nil {
		w.Header().Set("Subscription-Userinfo", "expire="+strconv.FormatInt(u.SubscriptionExpiresAt.Unix(), 10))
	}
	w.Header().Set("X-Subscription-State", e.State)
	w.Header().Set("X-Subscription-Routing-Skipped", strconv.Itoa(e.RoutingSkipped))
	w.Header().Set("X-Subscription-Skipped", strconv.Itoa(e.Skipped))
	w.Header().Set("X-Subscription-Format", e.Format)
	w.Header().Set("ETag", e.ETag)
	contentType := "application/yaml; charset=utf-8"
	if e.Format == "v2ray" {
		contentType = "text/plain; charset=utf-8"
		w.Header().Set("X-Subscription-Compatibility", "v2ray-compatible URI import only")
	}
	w.Header().Set("Content-Type", contentType)
}

func serveSubscription(w http.ResponseWriter, r *http.Request, e pool.SubscriptionCacheEntry) {
	if matchETag(r.Header.Get("If-None-Match"), e.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(e.Body)
}

func matchETag(header, etag string) bool {
	if header == "" || etag == "" {
		return false
	}
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == etag || v == "*" {
			return true
		}
	}
	return false
}

// passwordGuard 限制修改密码时当前密码的连续错误次数
type passwordGuard struct {
	mu    sync.Mutex
	fails map[int64]passwordFails
}
type passwordFails struct {
	count int
	since time.Time
}

func newPasswordGuard() *passwordGuard { return &passwordGuard{fails: map[int64]passwordFails{}} }
func (g *passwordGuard) allow(id int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	f := g.fails[id]
	if time.Since(f.since) > 15*time.Minute {
		delete(g.fails, id)
		return true
	}
	return f.count < 5
}
func (g *passwordGuard) fail(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f := g.fails[id]
	if f.count == 0 || time.Since(f.since) > 15*time.Minute {
		f = passwordFails{since: time.Now()}
	}
	f.count++
	g.fails[id] = f
}
func (g *passwordGuard) reset(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, id)
}
