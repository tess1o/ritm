package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const notBuiltPage = `<!doctype html><html><head><meta charset="utf-8"><meta name="color-scheme" content="dark">
<title>Ritm</title></head><body style="background:#0f1115;color:#e6e8eb;font-family:system-ui;padding:2rem">
<h1>Ritm</h1><p>The web UI is not built into this binary. Run <code>make build</code>, or use
<code>make dev</code> and open <a style="color:#7aa2ff" href="http://localhost:5173">http://localhost:5173</a>.</p></body></html>`

// spa serves the embedded Vue build: hashed assets are immutable, unknown non-API paths fall
// back to index.html (FE-2).
func (s *Server) spa() http.Handler {
	files := s.SPA
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if fi, err := fs.Stat(files, p); err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(p, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		index, err := fs.ReadFile(files, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if err != nil {
			w.Write([]byte(notBuiltPage))
			return
		}
		w.Write(index)
	})
}
