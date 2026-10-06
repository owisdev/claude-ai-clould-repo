package apify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

var temu = markets.Target{Market: "temu", Name: "Temu", Domain: "temu.com"}

const temuTemplate = `{"searchQueries":["{{query}}"],"maxItems":{{max}},"country":"{{COUNTRY}}"}`

func newServer(t *testing.T, status int, body string, check func(*http.Request, map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			raw, _ := io.ReadAll(r.Body)
			var input map[string]any
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Errorf("input is not JSON: %s", raw)
			}
			check(r, input)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(Config{Token: "tok", Actor: "someone~temu-scraper", InputTemplate: temuTemplate,
		MaxItems: 3, Timeout: 5 * time.Second, BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSearchRunsActorAndMapsItems(t *testing.T) {
	items := `[
	  {"title": "S Pen", "url": "https://www.temu.com/goods-1.html", "price": 9.99, "currency": "USD",
	   "image": "https://img/1.jpg"},
	  {"goods_name": "Stylus", "goods_url": "https://www.temu.com/goods-2.html", "salePrice": "$4.50",
	   "images": ["https://img/2a.jpg", "https://img/2b.jpg"]},
	  {"name": "Pen", "link": "https://www.temu.com/goods-3.html",
	   "price": {"amount": 3, "currency": "SAR"}, "thumbnail": {"url": "https://img/3.jpg"}},
	  {"title": "Elsewhere", "url": "https://example.com/x", "price": 1},
	  {"title": "No link", "price": 2},
	  {"title": "Fourth valid", "url": "https://temu.com/goods-4.html"}
	]`
	srv := newServer(t, http.StatusCreated, items, func(r *http.Request, input map[string]any) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/actors/someone~temu-scraper/run-sync-get-dataset-items" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" || strings.Contains(r.URL.RawQuery, "tok") {
			t.Error("token must be sent as a Bearer header, not in the URL")
		}
		q := r.URL.Query()
		if q.Get("maxItems") != "3" || q.Get("timeout") != "5" {
			t.Errorf("query = %v", q)
		}
		if input["searchQueries"].([]any)[0] != `s pen "pro"` || input["maxItems"].(float64) != 3 || input["country"] != "SA" {
			t.Errorf("input = %v", input)
		}
	})
	c := newClient(t, srv.URL)

	got, err := c.Search(context.Background(), search.Query{Title: `s pen "pro"`, Region: "sa", Targets: []markets.Target{temu}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d products, want 3 (limit; foreign and link-less items skipped): %+v", len(got), got)
	}
	want := []struct {
		title, thumb, currency string
		price                  float64
	}{
		{"S Pen", "https://img/1.jpg", "USD", 9.99},
		{"Stylus", "https://img/2a.jpg", "$", 4.5},
		{"Pen", "https://img/3.jpg", "SAR", 3},
	}
	for i, w := range want {
		g := got[i]
		if g.Title != w.title || g.Thumbnail != w.thumb || g.Currency != w.currency ||
			g.Price == nil || *g.Price != w.price || g.Position != i+1 || g.Market != "temu" || g.Provider != "apify" {
			t.Errorf("product %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestSearchErrors(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
	}{
		"timeout":      {http.StatusRequestTimeout, `{"error":{"type":"run-timeout-exceeded","message":"Actor run exceeded the timeout"}}`},
		"no credit":    {http.StatusPaymentRequired, `{"error":{"type":"not-enough-usage","message":"Monthly usage limit exceeded"}}`},
		"bad token":    {http.StatusUnauthorized, `{"error":{"type":"token-not-valid","message":"Authentication token is not valid"}}`},
		"not an array": {http.StatusCreated, `{"items":[]}`},
		"invalid json": {http.StatusCreated, `<html>`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := newServer(t, tt.status, tt.body, nil)
			_, err := newClient(t, srv.URL).Search(context.Background(),
				search.Query{Title: "x", Targets: []markets.Target{temu}})
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "tok") && !strings.Contains(err.Error(), "token") {
				t.Errorf("error leaks token: %v", err)
			}
		})
	}
}

func TestEmptyResultIsNotAnError(t *testing.T) {
	srv := newServer(t, http.StatusCreated, `[]`, nil)
	got, err := newClient(t, srv.URL).Search(context.Background(), search.Query{Title: "x", Targets: []markets.Target{temu}})
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestNewValidation(t *testing.T) {
	base := Config{Token: "t", Actor: "a~b", InputTemplate: temuTemplate}
	cases := map[string]func(*Config){
		"no token":       func(c *Config) { c.Token = "" },
		"no actor":       func(c *Config) { c.Actor = "" },
		"no placeholder": func(c *Config) { c.InputTemplate = `{"q":"fixed"}` },
		"not json":       func(c *Config) { c.InputTemplate = `{"q":"{{query}}"` },
	}
	for name, mutate := range cases {
		cfg := base
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := New(base); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestRenderInputEscapesQuery(t *testing.T) {
	out, err := renderInput(`{"q":"{{query}}"}`, `a"b\c`, 5, "jo")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(out), &v); err != nil || v["q"] != `a"b\c` {
		t.Errorf("out = %s (%v)", out, err)
	}
}

func TestParsePriceLabel(t *testing.T) {
	cases := map[string]struct {
		price float64
		cur   string
		ok    bool
	}{
		"$12.99":       {12.99, "$", true},
		"SAR 1,245.50": {1245.5, "SAR", true},
		"12,99 €":      {12.99, "€", true},
		"1,299":        {1299, "", true},
		"free":         {0, "", false},
		"":             {0, "", false},
	}
	for in, w := range cases {
		p, c, ok := parsePriceLabel(in)
		if ok != w.ok || (ok && (p != w.price || c != w.cur)) {
			t.Errorf("parsePriceLabel(%q) = %v %q %v, want %v %q %v", in, p, c, ok, w.price, w.cur, w.ok)
		}
	}
}

func TestRenderInputURLPlaceholders(t *testing.T) {
	tmpl := `{"startUrls":[{"url":"https://www.aliexpress.com/w/wholesale-{{query_slug}}.html"},` +
		`{"url":"https://www.aliexpress.com/wholesale?SearchText={{query_url}}"}],"maxItems":{{max}}}`
	out, err := renderInput(tmpl, `Samsung  S-Pen "pro"`, 10, "us")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		StartUrls []struct{ URL string } `json:"startUrls"`
		MaxItems  int                    `json:"maxItems"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if v.StartUrls[0].URL != "https://www.aliexpress.com/w/wholesale-samsung-s-pen-pro.html" ||
		v.StartUrls[1].URL != "https://www.aliexpress.com/wholesale?SearchText=Samsung++S-Pen+%22pro%22" || v.MaxItems != 10 {
		t.Errorf("out = %s", out)
	}
	if got := slug("سماعة بلوتوث"); got != "%D8%B3%D9%85%D8%A7%D8%B9%D8%A9-%D8%A8%D9%84%D9%88%D8%AA%D9%88%D8%AB" {
		t.Errorf("arabic slug = %s", got)
	}
}

func TestNewAcceptsURLPlaceholderOnly(t *testing.T) {
	if _, err := New(Config{Token: "t", Actor: "piotrv1001/aliexpress-listings-scraper",
		InputTemplate: `{"startUrls":[{"url":"https://www.aliexpress.com/w/wholesale-{{query_slug}}.html"}]}`}); err != nil {
		t.Errorf("template with {{query_slug}} rejected: %v", err)
	}
}

func TestAliExpressListingsOutput(t *testing.T) {
	// Output of piotrv1001/aliexpress-listings-scraper from the local test.
	items := []map[string]any{{
		"imageUrl": "https://ae-pic-a1.aliexpress-media.com/kf/S40b.jpg",
		"title":    "For Samsung Galaxy S25 Ultra Stylus Pen, S Pen Replacement",
		"price":    1.33, "originalPrice": 11.21, "currency": "USD", "rating": 4.9,
		"productType": "natural", "id": "3256812288634547",
		"productUrl": "https://www.aliexpress.com/item/3256812288634547.html",
	}}
	got := toProducts(items, markets.Target{Market: "aliexpress", Domain: "aliexpress.com"}, 10)
	if len(got) != 1 || got[0].Link != "https://www.aliexpress.com/item/3256812288634547.html" ||
		got[0].Price == nil || *got[0].Price != 1.33 || got[0].Currency != "USD" ||
		got[0].Thumbnail != "https://ae-pic-a1.aliexpress-media.com/kf/S40b.jpg" {
		t.Errorf("got %+v", got)
	}
}
