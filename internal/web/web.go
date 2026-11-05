// Package web serves the compiled single-page application embedded in the
// binary. The frontend build (web/) writes its output into internal/web/dist.
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

const fallbackHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Harbor Pilot</title></head>
<body style="background:#0C141A;color:#EEE6D2;font-family:system-ui;padding:2rem">
<h1>Harbor Pilot</h1><p>The web UI has not been built into this binary. Run <code>make web</code> and rebuild.
The API is available under <code>/api/v1</code>.</p></body></html>`

// Handler serves static assets and falls back to index.html for client-side
// routes. Hashed assets are cached immutably; index.html is never cached.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	index, idxErr := fs.ReadFile(sub, "index.html")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if p != "/" && !strings.HasSuffix(p, "/") {
			if st, err := fs.Stat(sub, strings.TrimPrefix(p, "/")); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(p, "/assets/") || path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if idxErr != nil {
			_, _ = w.Write([]byte(fallbackHTML))
			return
		}
		_, _ = w.Write(index)
	})
}
