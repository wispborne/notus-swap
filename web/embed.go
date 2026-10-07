// Package web holds the built frontend. Run `npm run build` in this folder
// before `go build`, or the binary serves only a placeholder page.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
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
		}
		files.ServeHTTP(w, r)
	}))
}
