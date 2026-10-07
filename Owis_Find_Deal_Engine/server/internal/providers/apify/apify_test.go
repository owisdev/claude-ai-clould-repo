package apify

import (
	"context"
	"encoding/json"
	"errors"
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
	  {"title": "S Pen Pro", "url": "https://www.temu.com/goods-1.html", "price": 9.99, "currency": "USD",
	   "image": "https://img/1.jpg"},
	  {"goods_name": "Stylus Pen Pro", "goods_url": "https://www.temu.com/goods-2.html", "salePrice": "$4.50",
	   "images": ["https://img/2a.jpg", "https://img/2b.jpg"]},
	  {"name": "Pen Pro", "link": "https://www.temu.com/goods-3.html",
	   "price": {"amount": 3, "currency": "SAR"}, "thumbnail": {"url": "https://img/3.jpg"}},
	  {"title": "Pen Pro elsewhere", "url": "https://example.com/x", "price": 1},
	  {"title": "Pen Pro no link", "price": 2},
	  {"title": "Fourth Pen Pro", "url": "https://temu.com/goods-4.html"}
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
		{"S Pen Pro", "https://img/1.jpg", "USD", 9.99},
		{"Stylus Pen Pro", "https://img/2a.jpg", "$", 4.5},
		{"Pen Pro", "https://img/3.jpg", "SAR", 3},
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

func TestEmptyResult(t *testing.T) {
	srv := newServer(t, http.StatusCreated, `[]`, nil)

	// Default: nothing found is a final "no results" (no paid fallback).
	got, err := newClient(t, srv.URL).Search(context.Background(), search.Query{Title: "x", Targets: []markets.Target{temu}})
	if err != nil || len(got) != 0 {
		t.Errorf("default: got %v, %v; want no results, no error", got, err)
	}

	// EmptyFallback: the next provider gets the market.
	c, _ := New(Config{Token: "tok", Actor: "someone~temu-scraper", InputTemplate: temuTemplate,
		BaseURL: srv.URL, EmptyFallback: true})
	_, err = c.Search(context.Background(), search.Query{Title: "x", Targets: []markets.Target{temu}})
	var pe *search.PartialError
	if !errors.As(err, &pe) || !pe.Uncovered["temu"] || len(pe.Failed) != 0 {
		t.Errorf("EmptyFallback: err = %v, want temu uncovered", err)
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
	got := toProducts(items, "samsung s pen", markets.Target{Market: "aliexpress", Domain: "aliexpress.com"}, 10)
	if len(got) != 1 || got[0].Link != "https://www.aliexpress.com/item/3256812288634547.html" ||
		got[0].Price == nil || *got[0].Price != 1.33 || got[0].Currency != "USD" ||
		got[0].Thumbnail != "https://ae-pic-a1.aliexpress-media.com/kf/S40b.jpg" {
		t.Errorf("got %+v", got)
	}
}

func TestActorSlashForm(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{Token: "t", Actor: "piotrv1001/aliexpress-listings-scraper", BaseURL: srv.URL,
		InputTemplate: `{"maxResults":{{max}},"searchQueries":["{{query}}"],"proxyConfiguration":{"useApifyProxy":true}}`})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Search(context.Background(), search.Query{Title: "s pen", Targets: []markets.Target{temu}})
	if !strings.Contains(path, "/v2/actors/piotrv1001~aliexpress-listings-scraper/") {
		t.Errorf("request path = %s", path)
	}
}

func TestTemuProductsScraperOutput(t *testing.T) {
	// Output of crw/temu-products-scraper from the local test: price in
	// cents next to a formatted label, link in "link_url" with tracking.
	items := []map[string]any{{
		"goods_id":  606284493507175.0,
		"title":     "EAGET JHL7440 40Gbps M.2 NVMe SSD Enclosure",
		"price":     10361.0,
		"price_str": "$103.61",
		"currency":  "USD",
		"thumb_url": "https://img.kwcdn.com/product/open/b365-goods.jpeg",
		"image_url": "https://img.kwcdn.com/product/open/b365-goods.jpeg",
		"link_url":  "https://www.temu.com/goods.html?_bg_fs=1&goods_id=606284493507175&_oak_mp_inf=EOeU%2Bd6&refer_page_sn=10009",
	}}
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor")
	var temuJor markets.Target
	for _, tg := range jor.Targets {
		if tg.Market == "temu" {
			temuJor = tg
		}
	}
	got := toProducts(items, "m.2 enclosure", temuJor, 10)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	p := got[0]
	if p.Link != "https://www.temu.com/goods.html?goods_id=606284493507175" {
		t.Errorf("link = %s", p.Link)
	}
	if p.Price == nil || *p.Price != 103.61 || p.Currency != "$" {
		t.Errorf("price = %v %q, want 103.61 $", p.Price, p.Currency)
	}
	if p.Thumbnail != "https://img.kwcdn.com/product/open/b365-goods.jpeg" {
		t.Errorf("thumbnail = %s", p.Thumbnail)
	}
}

func TestToProductsDropsIrrelevantItems(t *testing.T) {
	// From the local test: "EAGET JHL7440" also returned flash drives.
	items := []map[string]any{
		{"title": "KODAK K113 Mini USB Flash Drive USB3.2", "productUrl": "https://www.aliexpress.com/item/1.html", "price": 1.09},
		{"title": "EAGET 8GB Mini Car USB2.0 Flash Drive", "productUrl": "https://www.aliexpress.com/item/2.html", "price": 1.09},
		{"title": "EAGET JHL7440 40Gbps M.2 NVMe SSD Enclosure", "productUrl": "https://www.aliexpress.com/item/3.html", "price": 99.0},
	}
	got := toProducts(items, "EAGET JHL7440", markets.Target{Market: "aliexpress", Domain: "aliexpress.com"}, 10)
	if len(got) != 1 || got[0].Link != "https://www.aliexpress.com/item/3.html" || got[0].Position != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestSheinProductScraperOutput(t *testing.T) {
	// Output of clearpath/shein-product-scraper (search "ssd usb 3.0
	// enclosure", site "us"): sale price in "price.current" next to the
	// price before discount. The API may return the same fields nested.
	link := "https://us.shein.com/2-5-SATA-To-USB-3-1-10Gbps-Hard-Drive-Enclosure-For-SSD-HDD-USB-C-3-1-Gen-2-Interface-External-Hard-Drive-Enclosure-Hard-Drive-Not-Included-Christmas-New-Year-Holiday-Gift-Christmas-Special-p-30396385.html"
	image := "https://img.ltwebstatic.com/images3_spmp/2024/02/22/9e/17085748121263b2f8947b0035b101543655b1fa4f_square_thumbnail_405x552.jpg"
	flat := map[string]any{
		"productId":                "30396385",
		"name":                     "2.5\" SATA To USB 3.1 10Gbps Hard Drive Enclosure, For SSD/HDD, USB-C 3.1 Gen 2 Interface, External Hard Drive Enclosure (Hard Drive Not Included)",
		"url":                      link,
		"price.current":            6.7,
		"price.original":           8.3,
		"price.currency":           "USD",
		"price.discountPercent":    19.0,
		"image":                    image,
		"images":                   []any{"https://img.ltwebstatic.com/images3_spmp/other.jpg"},
		"inStock":                  true,
		"store.businessModelLabel": "SHEIN-fulfilled",
	}
	nested := map[string]any{
		"productId": "30396385",
		"name":      flat["name"],
		"url":       link,
		"price":     map[string]any{"current": 6.7, "original": 8.3, "currency": "USD", "discountPercent": 19.0},
		"image":     image,
	}
	cat, _ := markets.Load("")
	for _, code := range []string{"usa", "ksa", "jor"} {
		country, _ := cat.Country(code)
		var shein markets.Target
		for _, tg := range country.Targets {
			if tg.Market == "shein" {
				shein = tg
			}
		}
		for name, item := range map[string]map[string]any{"flat": flat, "nested": nested} {
			got := toProducts([]map[string]any{item}, "ssd usb 3.0 enclosure", shein, 10)
			if len(got) != 1 {
				t.Fatalf("%s/%s: got %+v", code, name, got)
			}
			p := got[0]
			if p.Link != link || p.Thumbnail != image || p.Market != "shein" {
				t.Errorf("%s/%s: product = %+v", code, name, p)
			}
			if p.Price == nil || *p.Price != 6.7 || p.Currency != "USD" {
				t.Errorf("%s/%s: price = %v %q, want 6.7 USD (sale price)", code, name, p.Price, p.Currency)
			}
		}
	}
}
