package server

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func registerEconomy(business businessRegistrar, s *pool.Store) {
	wallet := func(fn func(r *http.Request) (pool.Wallet, error)) func(http.ResponseWriter, *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			v, e := fn(r)
			if e == nil {
				writeJSON(w, 200, v)
			}
			return e
		}
	}
	business("GET /api/v1/me/wallet", wallet(func(r *http.Request) (pool.Wallet, error) { return s.Wallet(r.Context()) }))
	business("POST /api/v1/me/makeup", wallet(func(r *http.Request) (pool.Wallet, error) { return s.Makeup(r.Context()) }))
	business("POST /api/v1/me/leave", wallet(func(r *http.Request) (pool.Wallet, error) { return s.Leave(r.Context()) }))
	business("POST /api/v1/me/boosts", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Kind string `json:"kind"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		return wallet(func(r *http.Request) (pool.Wallet, error) { return s.Boost(r.Context(), in.Kind) })(w, r)
	})
	business("POST /api/v1/me/titles", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Name string `json:"name"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		return wallet(func(r *http.Request) (pool.Wallet, error) { return s.BuyTitle(r.Context(), in.Name) })(w, r)
	})
	business("PUT /api/v1/me/title", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Name string `json:"name"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		return wallet(func(r *http.Request) (pool.Wallet, error) { return s.EquipTitle(r.Context(), in.Name) })(w, r)
	})
	business("POST /api/v1/providers/{id}/tip", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		var in struct {
			Amount int `json:"amount"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		return wallet(func(r *http.Request) (pool.Wallet, error) { return s.Tip(r.Context(), id, in.Amount) })(w, r)
	})
	business("GET /api/v1/me/ledger", func(w http.ResponseWriter, r *http.Request) error {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		v, e := s.Ledger(r.Context(), page, size)
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("POST /api/v1/me/avatar", func(w http.ResponseWriter, r *http.Request) error {
		data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, pool.MaxAvatarBytes+1))
		if e != nil {
			return errors.New("头像须为 200KB 以内的图片")
		}
		u, e := s.SetAvatar(r.Context(), data)
		if e == nil {
			writeJSON(w, 200, u)
		}
		return e
	})
	business("DELETE /api/v1/me/avatar", func(w http.ResponseWriter, r *http.Request) error {
		u, e := s.DeleteAvatar(r.Context())
		if e == nil {
			writeJSON(w, 200, u)
		}
		return e
	})
	business("GET /api/v1/avatars/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		data, kind, e := s.Avatar(r.Context(), id)
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
		} else {
			w.Header().Set("Cache-Control", "private, no-cache")
		}
		_, _ = w.Write(data)
		return nil
	})
	business("POST /api/v1/feedback", func(w http.ResponseWriter, r *http.Request) error {
		var in pool.FeedbackInput
		if e := decode(w, r, &in); e != nil {
			return e
		}
		v, e := s.SubmitFeedback(r.Context(), in)
		if e == nil {
			writeJSON(w, 201, v)
		}
		return e
	})
	business("GET /api/v1/feedback", func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		node, _ := strconv.ParseInt(q.Get("node_id"), 10, 64)
		source, _ := strconv.ParseInt(q.Get("source_id"), 10, 64)
		page, _ := strconv.Atoi(q.Get("page"))
		size, _ := strconv.Atoi(q.Get("page_size"))
		v, e := s.Feedback(r.Context(), pool.FeedbackFilter{Target: q.Get("target"), NodeID: node, SourceID: source, Page: page, PageSize: size})
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("PATCH /api/v1/feedback/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		var in struct {
			Resolved bool `json:"resolved"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if e = s.ResolveFeedback(r.Context(), id, in.Resolved); e == nil {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
		return e
	})
	business("DELETE /api/v1/feedback/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		if e = s.DeleteFeedback(r.Context(), id); e == nil {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
		return e
	})
	business("POST /api/v1/users/{id}/risk", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		var in struct {
			FreezeHours   *int `json:"freeze_hours"`
			ResetExposure bool `json:"reset_exposure"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if actor := pool.CurrentUser(r.Context()); actor.Role != "admin" {
			target, e := s.User(r.Context(), id)
			if e != nil {
				return e
			}
			if target.Role != "user" {
				return pool.ErrForbidden
			}
		}
		if in.ResetExposure {
			if e = s.ResetExposure(r.Context(), id); e != nil {
				return e
			}
		}
		if in.FreezeHours != nil {
			if _, e = s.FreezeUser(r.Context(), id, *in.FreezeHours); e != nil {
				return e
			}
		}
		risks, e := s.RiskSummaries(r.Context(), []int64{id})
		if e == nil {
			writeJSON(w, 200, risks[id])
		}
		return e
	})
}

// clientIP 只在直连地址属于可信代理时读取 X-Forwarded-For
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	isTrusted := func(raw string) bool {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			return false
		}
		for _, n := range trusted {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !isTrusted(host) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		item := strings.TrimSpace(parts[i])
		if item == "" {
			continue
		}
		if !isTrusted(item) {
			if net.ParseIP(item) == nil {
				return host
			}
			return item
		}
	}
	return host
}
