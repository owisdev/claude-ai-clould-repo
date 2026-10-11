package adminui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestServesApp(t *testing.T) {
	files := fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><title>Admin</title>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":       {Data: []byte("<svg/>")},
	}
	h := handler(files, Config{FirebaseAPIKey: "key", FirebaseProjectID: "p"})

	rec := get(t, h, "GET", "/admin/")
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != 200 || !strings.Contains(string(body), "<title>Admin") || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("index = %d %q %v", rec.Code, body, rec.Header())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("security headers = %v", rec.Header())
	}
	if rec := get(t, h, "GET", "/admin/assets/app-abc.js"); rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset = %d %v", rec.Code, rec.Header())
	}
	if rec := get(t, h, "GET", "/admin/settings"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Admin") {
		t.Errorf("app path = %d", rec.Code)
	}
	if rec := get(t, h, "GET", "/admin"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/admin/" {
		t.Errorf("redirect = %d %v", rec.Code, rec.Header())
	}
	if rec := get(t, h, "POST", "/admin/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	// Only embedded files can be served (the server's mux also cleans paths).
	if rec := get(t, h, "GET", "/admin/../../etc/passwd"); rec.Code != http.StatusBadRequest {
		t.Errorf("traversal = %d %q", rec.Code, rec.Body)
	}
	rec = get(t, h, "GET", "/admin/config.json")
	if rec.Code != 200 || rec.Body.String() != `{"firebase_api_key":"key","firebase_auth_domain":"","firebase_project_id":"p","require_2fa":false}` {
		t.Errorf("config = %d %s", rec.Code, rec.Body)
	}
}

func TestNotBuilt(t *testing.T) {
	h := handler(fstest.MapFS{".gitkeep": {}}, Config{})
	if rec := get(t, h, "GET", "/admin/"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Errorf("not built = %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "GET", "/admin/config.json"); rec.Code != 200 {
		t.Errorf("config without build = %d", rec.Code)
	}
}
