package searxng

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
    {"url": "https://www.amazon.com/dp/B1", "title": "S Pen duplicate", "engine": "brave"},
    {"url": "https://amazon.com/dp/B2", "title": "S Pen - Amazon again", "engine": "duckduckgo"},
    {"url": "https://example.com/x", "title": "Elsewhere", "engine": "duckduckgo"},
    {"title": "No URL"},
    {"url": "https://sa.shein.com/p-1.html", "title": "S Pen case - SHEIN SA", "engine": "google"}
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
	c, err := New(Config{BaseURL: srv.URL + "/", Combined: true, OneQuery: true})
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

// perSiteServer answers each "title site:domain" query from bodies, keyed by
// domain; a missing domain gets HTTP 500.
func perSiteServer(t *testing.T, bodies map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query().Get("q")
		_, domain, ok := strings.Cut(q, " site:")
		if !ok || strings.Contains(q, " OR ") {
			t.Errorf("not a single-site query: %q", q)
		}
		body, ok := bodies[domain]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestSearchQueriesEachMarket(t *testing.T) {
	srv, calls := perSiteServer(t, map[string]string{
		"amazon.com":   `{"results":[{"url":"https://www.amazon.com/dp/B1","title":"S Pen A1"},{"url":"https://www.amazon.com/dp/B2","title":"S Pen A2"}]}`,
		"temu.com":     `{"results":[{"url":"https://www.temu.com/x.html","title":"S Pen T1"}]}`,
		"ar.shein.com": `{"results":[]}`,
	})
	c, _ := New(Config{BaseURL: srv.URL, Combined: true})

	got, err := c.Search(context.Background(), search.Query{Title: "s pen", Targets: jorTargets})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("queries = %d, want one per market", calls.Load())
	}
	perMarket := map[string]int{}
	for _, p := range got {
		perMarket[p.Market]++
	}
	if perMarket["amazon"] != 2 || perMarket["temu"] != 1 || len(got) != 3 {
		t.Errorf("got %+v", got)
	}
}

func TestSearchSomeMarketsFail(t *testing.T) {
	srv, _ := perSiteServer(t, map[string]string{
		"amazon.com":   `{"results":[{"url":"https://www.amazon.com/dp/B1","title":"S Pen A1"}]}`,
		"ar.shein.com": `{"results":[],"unresponsive_engines":[["google","CAPTCHA"]]}`,
		// temu.com: HTTP 500
	})
	c, _ := New(Config{BaseURL: srv.URL, Combined: true})

	_, err := c.Search(context.Background(), search.Query{Title: "s pen", Targets: jorTargets})
	var pe *search.PartialError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *search.PartialError", err)
	}
	if len(pe.Products) != 1 || pe.Products[0].Market != "amazon" {
		t.Errorf("products = %+v", pe.Products)
	}
	if len(pe.Failed) != 2 || pe.Failed["temu"] == nil || pe.Failed["shein"] == nil {
		t.Errorf("failed = %v", pe.Failed)
	}
}

func TestSearchAllMarketsFail(t *testing.T) {
	srv, _ := perSiteServer(t, nil)
	c, _ := New(Config{BaseURL: srv.URL, Combined: true})

	_, err := c.Search(context.Background(), search.Query{Title: "s pen", Targets: jorTargets})
	var pe *search.PartialError
	if err == nil || errors.As(err, &pe) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

func TestSearchKeepsOnlyProductPages(t *testing.T) {
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor")
	// The amazon results of the first local test, plus duplicates.
	srv, _ := perSiteServer(t, map[string]string{
		"amazon.com": `{"results":[
			{"url":"https://www.amazon.com/stores/SSK/page/3FFC2A12-0613-42C4-9CC7-C0A4025BCD41","title":"SSK store"},
			{"url":"https://www.amazon.com/s?i=merchant-items&me=A41S1C1L96T2O","title":"SSK Direct"},
			{"url":"https://www.amazon.com/clp/B07MNFH1PX","title":"Amazon.com: SSK M.2 enclosure"},
			{"url":"https://www.amazon.com/SSK-Enclosure/dp/B07MNFH1PX/ref=sr_1_1","title":"duplicate of the clp page"},
			{"url":"https://www.amazon.com/samsung-s-pen/s?k=samsung+s+pen","title":"search page"},
			{"url":"https://www.amazon.com/dp/B0FD38XB93?th=1","title":"SSK 20Gbps"}]}`,
		"aliexpress.com": `{"results":[{"url":"https://www.aliexpress.com/item/1005008495498271.html?spm=1","title":"SSK cloner"}]}`,
		"temu.com":       `{"results":[{"url":"https://www.temu.com/","title":"Temu home"}]}`,
		"ar.shein.com":   `{"results":[]}`,
	})
	c, _ := New(Config{BaseURL: srv.URL, Combined: true})

	got, err := c.Search(context.Background(), search.Query{Title: "ssk enclosure", Targets: jor.Targets})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := map[string]bool{
		"https://www.amazon.com/dp/B07MNFH1PX":                  true,
		"https://www.amazon.com/dp/B0FD38XB93":                  true,
		"https://www.aliexpress.com/item/1005008495498271.html": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d products, want %d: %+v", len(got), len(want), got)
	}
	for _, p := range got {
		if !want[p.Link] {
			t.Errorf("unexpected link %s", p.Link)
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

func TestSearchDropsIrrelevantPages(t *testing.T) {
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor")
	shein := []markets.Target{jor.Targets[3]}
	// From the local test: SHEIN pages returned for an SSD search.
	srv := newServer(t, http.StatusOK, `{"results":[
		{"url":"https://ar.shein.com/SHEIN-Young-Girl-Ruffle-Trim-Tee-Sunflower-Print-Belted-Shorts-Summer-Holiday-p-12821272.html","title":"Shein قميص فتاة صغيرة بطيات"},
		{"url":"https://ar.shein.com/Hair-Ties-Home-Beauty-Women-Accessory-Gifts-p-145507524.html","title":"ربطات الشعر"}],
		"unresponsive_engines":[["brave","timeout"]]}`, nil)
	c, _ := New(Config{BaseURL: srv.URL})

	got, err := c.Search(context.Background(), search.Query{Title: "SSK ssd m3 enclosure", Targets: shein})
	if err != nil || len(got) != 0 {
		// The engines answered (one timed out): nothing relevant is "no
		// results", not an error.
		t.Fatalf("got %+v, %v; want no products and no error", got, err)
	}
}
