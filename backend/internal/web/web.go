package web

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
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
	zipped := &gzipCache{files: map[string][]byte{}}
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
		if compressible(rel) {
			w.Header().Add("Vary", "Accept-Encoding")
			if acceptsGzip(r.Header.Get("Accept-Encoding")) {
				if data := zipped.get(assets, rel); data != nil {
					if t := mime.TypeByExtension(path.Ext(rel)); t != "" {
						w.Header().Set("Content-Type", t)
					}
					w.Header().Set("Content-Encoding", "gzip")
					http.ServeContent(w, r, rel, time.Time{}, bytes.NewReader(data))
					return
				}
			}
		}
		http.ServeFileFS(w, r, assets, rel)
	})
}

// gzipCache 首次请求时压缩文本资源并常驻内存，嵌入的文件不会变化
type gzipCache struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (c *gzipCache) get(assets fs.FS, rel string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data, ok := c.files[rel]; ok {
		return data
	}
	raw, err := fs.ReadFile(assets, rel)
	var data []byte
	if err == nil && len(raw) >= 1024 {
		var b bytes.Buffer
		z, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
		_, _ = z.Write(raw)
		if z.Close() == nil && b.Len() < len(raw) {
			data = b.Bytes()
		}
	}
	c.files[rel] = data
	return data
}

func compressible(rel string) bool {
	switch path.Ext(rel) {
	case ".html", ".js", ".mjs", ".css", ".svg", ".json", ".txt", ".webmanifest", ".map":
		return true
	}
	return false
}

func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}
	return false
}
