package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web
var webFS embed.FS

// newWebHandler serves the embedded SPA. Static assets live under /static/ and
// the SPA entry point is index.html. Unknown API paths fall through here from
// the root mux pattern and are answered with a JSON 404 instead of HTML.
func newWebHandler() (http.Handler, error) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasPrefix(path, "/static/") {
			http.StripPrefix("/static/", fileServer).ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/auth/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}

		// SPA fallback: every other path renders the app.
		fileServer.ServeHTTP(w, r)
	}), nil
}
