package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/web"
)

type databaseStatus struct {
	Configured bool `json:"configured"`
	Connected  bool `json:"connected"`
}

func Handler(db *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		status := checkDatabase(r.Context(), db)
		code := http.StatusOK
		if !status.Connected {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, status)
	})
	mux.HandleFunc("GET /api/v1/system", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			Name      string         `json:"name"`
			Version   string         `json:"version"`
			Commit    string         `json:"commit"`
			BuildTime string         `json:"build_time"`
			Database  databaseStatus `json:"database"`
			Core      struct {
				State string `json:"state"`
			} `json:"core"`
		}{
			Name: "Mihomo NodePool", Version: buildinfo.Version,
			Commit: buildinfo.Commit, BuildTime: buildinfo.BuildTime,
			Database: checkDatabase(r.Context(), db),
			Core: struct {
				State string `json:"state"`
			}{State: "not_integrated"},
		})
	})
	mux.Handle("/api/", http.NotFoundHandler())
	mux.Handle("/", web.Handler())
	return mux
}

func checkDatabase(parent context.Context, db *sql.DB) databaseStatus {
	status := databaseStatus{Configured: db != nil}
	if db == nil {
		return status
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	status.Connected = db.PingContext(ctx) == nil
	return status
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
