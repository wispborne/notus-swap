// Package web holds the built frontend. Run `npm run build` in this folder
// before `go build`, or the binary serves only a placeholder page.
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

	"github.com/wispborne/notus-swap/internal/compress"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the frontend under /notus/. Paths without a file extension
// get index.html, so the app's own page URLs (/notus/requests) load on refresh.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	files := http.FileServerFS(root)
	return http.StripPrefix("/notus", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || path.Ext(p) == "" {
			index, err := fs.ReadFile(root, "index.html")
			if err != nil {
				http.Error(w, "The web UI was not built into this binary. Run npm run build in web/ before go build.", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(index)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			// Vite puts a content hash in these file names.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			if gz := zipped(root, p); gz != nil && compress.AcceptsGzip(r) {
				w.Header().Set("Vary", "Accept-Encoding")
				w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(p)))
				w.Header().Set("Content-Encoding", "gzip")
				w.Write(gz)
				return
			}
		}
		files.ServeHTTP(w, r)
	}))
}

// gzipped caches each asset's compressed bytes by path.
var gzipped sync.Map // path -> []byte, or nil when not worth compressing

// zipped returns an asset's gzip bytes, or nil for files that don't
// compress (images, fonts) or are missing.
func zipped(root fs.FS, p string) []byte {
	if v, ok := gzipped.Load(p); ok {
		b, _ := v.([]byte)
		return b
	}
	var out []byte
	switch path.Ext(p) {
	case ".js", ".css", ".svg", ".json", ".map":
		if raw, err := fs.ReadFile(root, p); err == nil {
			var buf bytes.Buffer
			gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			gz.Write(raw)
			gz.Close()
			out = buf.Bytes()
		}
	}
	gzipped.Store(p, out)
	return out
}
