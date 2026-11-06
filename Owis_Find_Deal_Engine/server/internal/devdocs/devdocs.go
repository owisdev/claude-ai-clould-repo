// Package devdocs serves the documentation for developers: the OpenAPI 3.1
// description of the public API (GET /api/v1/openapi.json) and a static
// documentation page (GET /docs/). Both are embedded in the binary and
// public: they hold no secrets and no per-server data.
package devdocs

import (
	"embed"
	"net/http"
)

//go:embed static
var files embed.FS

// csp: only the page's own stylesheet; no scripts at all.
const csp = "default-src 'none'; style-src 'self'; img-src 'self' data:; " +
	"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// OpenAPI serves the OpenAPI document.
func OpenAPI() http.Handler { return file("static/openapi.json", "application/json; charset=utf-8") }

// Page serves the documentation under /docs/: the page itself and its
// stylesheet; anything else is 404.
func Page() http.Handler {
	page := file("static/index.html", "text/html; charset=utf-8")
	css := file("static/docs.css", "text/css; charset=utf-8")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs/", "/docs/index.html":
			page.ServeHTTP(w, r)
		case "/docs/docs.css":
			css.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// file serves one embedded file with security headers and a short cache.
func file(name, contentType string) http.Handler {
	b, err := files.ReadFile(name)
	if err != nil {
		panic("devdocs: " + err.Error()) // a build error: the file is embedded
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(b)
	})
}

// Bytes returns an embedded file (for tests).
func Bytes(name string) ([]byte, error) { return files.ReadFile("static/" + name) }
