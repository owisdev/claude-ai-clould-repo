// Package adminui serves the admin web app (a React build embedded in the
// binary) under /admin/, with the public sign-in settings it needs.
// The app talks to the admin API (/admin/v1) on the same origin.
package adminui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is the app build (npm run build in web/admin writes it here). In a
// checkout without a build it only holds .gitkeep; the app then answers
// with a hint instead.
//
//go:embed all:dist
var dist embed.FS

// Config is what the browser needs to sign in with Firebase. The Web API
// key identifies the Firebase project; it is not a secret.
type Config struct {
	FirebaseAPIKey     string `json:"firebase_api_key"`
	FirebaseAuthDomain string `json:"firebase_auth_domain"`
	FirebaseProjectID  string `json:"firebase_project_id"`
	Require2FA         bool   `json:"require_2fa"`
}

// csp allows only the app's own files and the Firebase sign-in endpoints.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; " +
	"connect-src 'self' https://identitytoolkit.googleapis.com https://securetoken.googleapis.com; " +
	"frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

// Handler serves the app under /admin/ (the admin API under /admin/v1/ is
// routed elsewhere).
func Handler(cfg Config) http.Handler {
	files, _ := fs.Sub(dist, "dist")
	return handler(files, cfg)
}

func handler(files fs.FS, cfg Config) http.Handler {
	_, err := fs.Stat(files, "index.html")
	built := err == nil
	static := http.FileServerFS(files)
	configJSON, _ := json.Marshal(cfg)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/admin" {
			http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
			return
		}
		rest := strings.TrimPrefix(path.Clean(r.URL.Path), "/admin")
		switch {
		case rest == "/config.json":
			h.Set("Content-Type", "application/json")
			h.Set("Cache-Control", "no-store")
			_, _ = w.Write(configJSON)
			return
		case !built:
			h.Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("The admin web app is not in this build. Build it with: cd web/admin && npm ci && npm run build (the Docker image does this).\n"))
			return
		case strings.HasPrefix(rest, "/assets/"):
			// File names carry a content hash: cache for a year.
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// The app uses #/ routes: every other path is the app itself.
			if rest == "" || rest == "/" || rest == "/index.html" || !hasFile(files, rest) {
				h.Set("Cache-Control", "no-cache")
				http.ServeFileFS(w, r, files, "index.html")
				return
			}
			h.Set("Cache-Control", "no-cache")
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = rest
		static.ServeHTTP(w, r2)
	})
}

func hasFile(files fs.FS, name string) bool {
	st, err := fs.Stat(files, strings.TrimPrefix(name, "/"))
	return err == nil && !st.IsDir()
}
