package server

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func registerModeration(business func(string, func(http.ResponseWriter, *http.Request) error), store *pool.Store) {
	business("GET /api/v1/moderation/sources", func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		owner, _ := strconv.ParseInt(q.Get("owner_id"), 10, 64)
		page, _ := strconv.Atoi(q.Get("page"))
		size, _ := strconv.Atoi(q.Get("page_size"))
		v, err := store.ModerationSources(r.Context(), pool.ModerationFilter{OwnerID: owner, Status: q.Get("status"), Q: q.Get("q")}, page, size)
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	moderate := func(action string) func(http.ResponseWriter, *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			id, err := idOf(r)
			if err != nil {
				return err
			}
			var in struct {
				Reason string `json:"reason"`
				Detail string `json:"detail"`
				Notify *bool  `json:"notify"`
			}
			if err = decode(w, r, &in); err != nil {
				return err
			}
			res, err := store.ModerateSource(r.Context(), id, action, in.Reason, in.Detail)
			if err != nil {
				return err
			}
			notified := false
			if in.Notify == nil || *in.Notify {
				notified = notifyModeration(r, store, res, action == "restore")
			}
			writeJSON(w, 200, map[string]any{"source": res.Source, "notified": notified})
			return nil
		}
	}
	business("POST /api/v1/sources/{id}/takedown", moderate("takedown"))
	business("POST /api/v1/sources/{id}/restore", moderate("restore"))
	business("GET /api/v1/sources/{id}/moderation", func(w http.ResponseWriter, r *http.Request) error {
		id, err := idOf(r)
		if err != nil {
			return err
		}
		src, err := store.Source(r.Context(), id)
		if err != nil {
			return err
		}
		actor := pool.CurrentUser(r.Context())
		if src.OwnerID != actor.ID && !pool.CanModerate(actor, src.OwnerID, src.ProviderRole) {
			return pool.ErrForbidden
		}
		items, err := store.ModerationHistory(r.Context(), id)
		if err == nil {
			writeJSON(w, 200, map[string]any{"items": items, "takedown": src.Takedown})
		}
		return err
	})
	business("GET /api/v1/users/{id}/detail", func(w http.ResponseWriter, r *http.Request) error {
		id, err := idOf(r)
		if err != nil {
			return err
		}
		v, err := store.UserDetail(r.Context(), id)
		if err == nil {
			writeJSON(w, 200, v)
		}
		return err
	})
	business("DELETE /api/v1/users/{id}/identities/{provider}", func(w http.ResponseWriter, r *http.Request) error {
		id, err := idOf(r)
		if err != nil {
			return err
		}
		if err = store.AdminUnlinkIdentity(r.Context(), id, r.PathValue("provider")); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

// notifyModeration 有已验证邮箱且邮件服务可用时异步通知提供方
func notifyModeration(r *http.Request, store *pool.Store, res pool.ModerationResult, restored bool) bool {
	owner := res.Owner
	if owner.Email == "" || !owner.EmailVerified || owner.ID == pool.CurrentUser(r.Context()).ID {
		return false
	}
	login, err := store.LoginNow(r.Context()).Unsealed()
	if err != nil || !login.SMTP.Ready() {
		return false
	}
	settings, _ := store.Settings(r.Context())
	link := publicOrigin(r, settings) + "/sources"
	operator := pool.CurrentUser(r.Context())
	name := operator.DisplayName
	if name == "" {
		name = operator.Username
	}
	reason, detail := "", ""
	if t := res.Source.Takedown; t != nil {
		reason, detail = t.ReasonLabel, t.Detail
	}
	m := takedownMail(res.Source.Name, reason, detail, name, link, restored)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := sendMail(ctx, login.SMTP, owner.Email, m); err != nil {
			log.Printf("moderation mail to user %d failed: %v", owner.ID, err)
		}
	}()
	return true
}
