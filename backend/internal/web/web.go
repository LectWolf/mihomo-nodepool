package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

func Handler() http.Handler {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return handler(assets)
}

func handler(assets fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		for _, part := range strings.Split(rel, "/") {
			if strings.HasPrefix(part, ".") {
				http.NotFound(w, r)
				return
			}
		}
		if rel == "" {
			rel = "index.html"
		}
		info, err := fs.Stat(assets, rel)
		if err != nil || info.IsDir() {
			if path.Ext(rel) != "" {
				http.NotFound(w, r)
				return
			}
			rel = "index.html"
		}
		if _, err := fs.Stat(assets, rel); err != nil {
			http.Error(w, "前端尚未构建，请先运行 node scripts/build.mjs。", http.StatusServiceUnavailable)
			return
		}
		if rel == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(rel, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(w, r, assets, rel)
	})
}
