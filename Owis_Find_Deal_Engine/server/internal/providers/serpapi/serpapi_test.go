package serpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
  "organic_results": [
    {"position": 1, "title": "S Pen - Amazon", "link": "https://www.amazon.com/dp/B1",
     "snippet": "Stylus", "rich_snippet": {"top": {"extensions": ["4.5 stars", 12],
     "detected_extensions": {"price": 29.99, "currency": "$"}}}},
    {"position": 2, "title": "S Pen - Temu", "link": "https://www.temu.com/s-pen.html"},
    {"position": 3, "title": "Amazon again", "link": "https://amazon.com/dp/B2"},
    {"position": 4, "title": "No link"},
    {"position": 5, "title": "Elsewhere", "link": "https://example.com/x"},
    {"position": 6, "title": "SHEIN SA", "link": "https://sa.shein.com/p-1.html"}
  ]
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
		q := r.URL.Query()
		if got, want := q.Get("q"), "s pen (site:amazon.com OR site:temu.com OR site:ar.shein.com)"; got != want {
			t.Errorf("q = %q, want %q", got, want)
		}
		if q.Get("gl") != "jo" || q.Get("engine") != "google" || q.Get("api_key") != "secret" {
			t.Errorf("unexpected params %v", q)
		}
	})
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Combined: true})

	got, err := c.Search(context.Background(), search.Query{Title: "s pen", Region: "jo", Language: "en", Targets: jorTargets})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := []struct {
		market string
		pos    int
	}{{"amazon", 1}, {"temu", 1}, {"amazon", 2}, {"shein", 1}}
	if len(got) != len(want) {
		t.Fatalf("got %d products, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Market != w.market || got[i].Position != w.pos {
			t.Errorf("product %d = %s/%d, want %s/%d", i, got[i].Market, got[i].Position, w.market, w.pos)
		}
	}
	if got[0].Price == nil || *got[0].Price != 29.99 || got[0].Currency != "$" {
		t.Errorf("price not parsed: %+v", got[0])
	}
	if len(got[0].Extensions) != 1 {
		t.Errorf("extensions = %v, want only string values", got[0].Extensions)
	}
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{"api error", http.StatusUnauthorized, `{"error":"Invalid API key."}`, true},
		{"bad json", http.StatusOK, `not json`, true},
		{"no results", http.StatusOK, `{"error":"Google hasn't returned any results for this query."}`, false},
		{"empty", http.StatusOK, `{}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, tt.status, tt.body, nil)
			c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL})
			_, err := c.Search(context.Background(), search.Query{Title: "x", Targets: jorTargets})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSearchErrorDoesNotLeakKey(t *testing.T) {
	c, _ := New(Config{APIKey: "supersecret", BaseURL: "http://127.0.0.1:1"})
	_, err := c.Search(context.Background(), search.Query{Title: "x", Targets: jorTargets})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("error leaks API key: %v", err)
	}
}

func TestNewRequiresKey(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("expected error without API key")
	}
}
