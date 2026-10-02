package searxng

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

var jorTargets = []markets.Target{
	{Market: "amazon", Domain: "amazon.com"},
	{Market: "temu", Domain: "temu.com"},
	{Market: "shein", Domain: "ar.shein.com"},
}

const sampleResponse = `{
  "query": "s pen",
  "results": [
    {"url": "https://www.amazon.com/dp/B1", "title": "S Pen - Amazon", "content": "Stylus", "engine": "google",
     "thumbnail": "https://img/a.jpg"},
    {"url": "https://www.temu.com/s-pen.html", "title": "S Pen - Temu", "engine": "bing", "img_src": "https://img/t.jpg"},
    {"url": "https://www.amazon.com/dp/B1", "title": "duplicate", "engine": "brave"},
    {"url": "https://amazon.com/dp/B2", "title": "Amazon again", "engine": "duckduckgo"},
    {"url": "https://example.com/x", "title": "Elsewhere", "engine": "duckduckgo"},
    {"title": "No URL"},
    {"url": "https://sa.shein.com/p-1.html", "title": "SHEIN SA", "engine": "google"}
  ],
  "unresponsive_engines": [["startpage", "timeout"]]
}`

func newServer(t *testing.T, status int, body string, check func(*http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchMapsResults(t *testing.T) {
	srv := newServer(t, http.StatusOK, sampleResponse, func(r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if got, want := q.Get("q"), "s pen (site:amazon.com OR site:temu.com OR site:ar.shein.com)"; got != want {
			t.Errorf("q = %q, want %q", got, want)
		}
		if q.Get("format") != "json" || q.Get("language") != "en-JO" {
			t.Errorf("unexpected params %v", q)
		}
	})
	c, err := New(Config{BaseURL: srv.URL + "/", Combined: true})
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.Search(context.Background(), search.Query{Title: "s pen", Region: "jo", Language: "en", Targets: jorTargets})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []struct {
		market string
		pos    int
		thumb  string
	}{
		{"amazon", 1, "https://img/a.jpg"},
		{"temu", 1, "https://img/t.jpg"},
		{"amazon", 2, ""},
		{"shein", 1, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d products, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Market != w.market || got[i].Position != w.pos || got[i].Thumbnail != w.thumb || got[i].Provider != "searxng" {
			t.Errorf("product %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{"json disabled", http.StatusForbidden, `Forbidden`, true},
		{"limiter", http.StatusTooManyRequests, `Too Many Requests`, true},
		{"bad json", http.StatusOK, `<html>`, true},
		{"engines failed", http.StatusOK, `{"results":[],"unresponsive_engines":[["google","CAPTCHA"]]}`, true},
		{"nothing found", http.StatusOK, `{"results":[],"unresponsive_engines":[]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, tt.status, tt.body, nil)
			c, _ := New(Config{BaseURL: srv.URL})
			_, err := c.Search(context.Background(), search.Query{Title: "x", Targets: jorTargets})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewValidatesURL(t *testing.T) {
	for _, u := range []string{"", "searxng:8080", "ftp://x", "http://"} {
		if _, err := New(Config{BaseURL: u}); err == nil {
			t.Errorf("New(%q): expected error", u)
		}
	}
}

func TestLocale(t *testing.T) {
	if got := locale("en", "sa"); got != "en-SA" {
		t.Errorf("got %q", got)
	}
	if got := locale("", "sa"); got != "" {
		t.Errorf("got %q", got)
	}
}
