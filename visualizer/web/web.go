// Package web serves the built Svelte UI (web/dist, see task web:build)
// embedded in the binary, at / on the API port.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded UI: files as they are, index.html for every
// other path, and 503 while the UI has not been built.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed directive guarantees the directory
	}
	return handler(sub)
}

func handler(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(root, "index.html"); err != nil {
			http.Error(w, "web UI not built: run task web:build", http.StatusServiceUnavailable)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" && path != "index.html" {
			if st, err := fs.Stat(root, path); err == nil && !st.IsDir() {
				if strings.HasPrefix(path, "assets/") { // Vite hashes these names
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// not ServeFileFS: it answers /index.html with a redirect
		index, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
}
