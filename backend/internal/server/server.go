package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/web"
	"github.com/go-sql-driver/mysql"
)

type Registrar interface{ Register(*http.ServeMux) }
type Options struct {
	Service *pool.Service
	Auth    *auth.Manager
	Updates Registrar
}
type databaseStatus struct {
	Configured bool `json:"configured"`
	Connected  bool `json:"connected"`
}

func New(o Options) http.Handler {
	mux := http.NewServeMux()
	store := o.Service.Store
	o.Auth.BindStore(store)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		e := store.Ensure(ctx)
		status := databaseStatus{Configured: store.DB != nil, Connected: e == nil}
		code := 200
		if e != nil {
			code = 503
		}
		writeJSON(w, code, status)
	})
	mux.HandleFunc("GET /api/v1/system", func(w http.ResponseWriter, r *http.Request) {
		if pool.CurrentUser(r.Context()).Role != "admin" {
			writeJSON(w, 200, map[string]any{"name": "Mihomo NodePool", "version": buildinfo.Version})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		connected := store.Ensure(ctx) == nil
		tasks := pool.Tasks{}
		if connected {
			tasks, _ = store.TaskStatus(ctx)
		}
		writeJSON(w, 200, map[string]any{"name": "Mihomo NodePool", "version": buildinfo.Version, "commit": buildinfo.Commit, "build_time": buildinfo.BuildTime, "database": databaseStatus{store.DB != nil, connected}, "core": map[string]string{"state": "ready", "version": o.Service.Prober.Version()}, "tasks": tasks})
	})
	business := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			if e := store.Ensure(ctx); e != nil {
				writeJSON(w, 503, map[string]string{"error": "MySQL 尚未配置、不可达或没有建表权限"})
				return
			}
			if e := fn(w, r); e != nil {
				writeError(w, e)
			}
		})
	}
	business("GET /api/v1/sources", func(w http.ResponseWriter, r *http.Request) error {
		v, e := store.Sources(r.Context())
		if e == nil {
			writeJSON(w, 200, map[string]any{"items": v})
		}
		return e
	})
	business("POST /api/v1/sources", func(w http.ResponseWriter, r *http.Request) error {
		var in pool.SourceInput
		if e := decode(w, r, &in); e != nil {
			return e
		}
		v, e := store.AddSource(r.Context(), in)
		if e == nil {
			writeJSON(w, 202, v)
		}
		return e
	})
	business("PATCH /api/v1/sources/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		var in pool.SourcePatch
		if e = decode(w, r, &in); e != nil {
			return e
		}
		v, e := store.PatchSource(r.Context(), id, in)
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("DELETE /api/v1/sources/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		if e = store.DeleteSource(r.Context(), id); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
		return nil
	})
	business("POST /api/v1/sources/{id}/refresh", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		if e = store.QueueSource(r.Context(), id); e != nil {
			return e
		}
		writeJSON(w, 202, map[string]bool{"queued": true})
		return nil
	})
	business("GET /api/v1/nodes", func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		size, _ := strconv.Atoi(q.Get("page_size"))
		source, _ := strconv.ParseInt(q.Get("source_id"), 10, 64)
		max, _ := strconv.Atoi(q.Get("max_latency"))
		if len(q.Get("q")) > 512 {
			return errors.New("搜索内容过长")
		}
		provider, _ := strconv.ParseInt(q.Get("provider_id"), 10, 64)
		var countries []string
		if q.Get("countries") != "" {
			countries = strings.Split(q.Get("countries"), ",")
		}
		var minLevel *int
		if value := q.Get("min_level"); value != "" {
			v, err := strconv.Atoi(value)
			if err != nil || v < 0 || v > 6 {
				return errors.New("等级筛选须为Lv0–Lv6")
			}
			minLevel = &v
		}
		v, e := store.Nodes(r.Context(), pool.Filter{Page: page, PageSize: size, Q: q.Get("q"), Type: q.Get("type"), Country: q.Get("country"), Countries: countries, ProviderID: provider, Following: q.Get("following") == "1", UserID: pool.CurrentUser(r.Context()).ID, Status: q.Get("status"), SourceID: source, Sort: q.Get("sort"), MaxLatency: max, MinLevel: minLevel})
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("POST /api/v1/nodes/{id}/test", func(w http.ResponseWriter, r *http.Request) error {
		id, e := idOf(r)
		if e != nil {
			return e
		}
		if e = store.NodePermission(r.Context(), id); e != nil {
			return e
		}
		if e = store.QueueNodes(r.Context(), []int64{id}); e != nil {
			return e
		}
		writeJSON(w, 202, map[string]bool{"queued": true})
		return nil
	})
	business("POST /api/v1/nodes/test", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			IDs []int64 `json:"ids"`
		}
		if r.ContentLength != 0 {
			if e := decode(w, r, &in); e != nil {
				return e
			}
		}
		if e := store.QueueNodes(r.Context(), in.IDs); e != nil {
			return e
		}
		writeJSON(w, 202, map[string]bool{"queued": true})
		return nil
	})
	business("GET /api/v1/options", func(w http.ResponseWriter, r *http.Request) error {
		v, e := store.Options(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) error {
		v, e := store.Settings(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	business("PUT /api/v1/settings", func(w http.ResponseWriter, r *http.Request) error {
		var in pool.Settings
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if e := store.SaveSettings(r.Context(), in); e != nil {
			return e
		}
		in.RefreshHours, in.CheckHours = 6, 6
		writeJSON(w, 200, in)
		return nil
	})
	registerMultiuser(business, store, o.Auth)
	registerSubscriptionDownload(mux, store)
	registerSourceLink(mux, store)
	registerLeaseProbe(mux, store)
	o.Auth.Register(mux)
	updatesMux := http.NewServeMux()
	if o.Updates != nil {
		o.Updates.Register(updatesMux)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "接口不存在"})
	})
	mux.Handle("/", web.Handler())
	return o.Auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		adminOnly := p == "/api/v1/settings" || strings.HasPrefix(p, "/api/v1/users") || p == "/api/v1/nodes/test" || (strings.HasPrefix(p, "/api/v1/nodes/") && strings.HasSuffix(p, "/test")) || strings.HasPrefix(p, "/api/v1/releases") || strings.HasPrefix(p, "/api/v1/system/") || strings.HasPrefix(p, "/api/v1/updates")
		if adminOnly && pool.CurrentUser(r.Context()).Role != "admin" {
			writeError(w, pool.ErrForbidden)
			return
		}
		// The registrar may add routes in future; guard its entire mux as a unit.
		if _, pattern := updatesMux.Handler(r); pattern != "" {
			if pool.CurrentUser(r.Context()).Role != "admin" {
				writeError(w, pool.ErrForbidden)
				return
			}
			updatesMux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
}
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON 请求格式无效或内容过大")
	}
	return nil
}
func idOf(r *http.Request) (int64, error) {
	v, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || v <= 0 {
		return 0, errors.New("ID 无效")
	}
	return v, nil
}
func writeError(w http.ResponseWriter, e error) {
	code := 400
	message := e.Error()
	var me *mysql.MySQLError
	switch {
	case errors.Is(e, pool.ErrForbidden):
		code = 403
	case errors.Is(e, pool.ErrQuota), errors.Is(e, pool.ErrProfileInUse):
		code = 409
	case errors.Is(e, pool.ErrUnexportable):
		code = 422
	case errors.Is(e, pool.ErrNotFound), errors.Is(e, sql.ErrNoRows):
		code = 404
		message = "记录不存在"
	case errors.Is(e, context.DeadlineExceeded), errors.Is(e, pool.ErrUnavailable):
		code = 503
		message = "数据库暂不可用，请稍后重试"
	case errors.As(e, &me):
		code = 500
		message = "数据库操作失败，请检查数据库配置与权限"
	}
	writeJSON(w, code, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
