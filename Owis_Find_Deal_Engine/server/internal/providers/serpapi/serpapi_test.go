package serpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

var jorTargets = []markets.Target{
	{Market: "amazon", Name: "Amazon", Domain: "amazon.com"},
	{Market: "temu", Name: "Temu", Domain: "temu.com"},
	{Market: "shein", Name: "SHEIN", Domain: "ar.shein.com"},
}

const sampleResponse = `{
  "organic_results": [
    {"position": 1, "title": "S Pen - Amazon", "link": "https://www.amazon.com/dp/B1",
     "snippet": "Stylus", "rich_snippet": {"top": {"extensions": ["4.5 stars", 12],
     "detected_extensions": {"price": 29.99, "currency": "$"}}}},
    {"position": 2, "title": "S Pen - Temu", "link": "https://www.temu.com/s-pen.html"},
    {"position": 3, "title": "S Pen - Amazon again", "link": "https://amazon.com/dp/B2"},
    {"position": 4, "title": "No link"},
    {"position": 5, "title": "Elsewhere", "link": "https://example.com/x"},
    {"position": 6, "title": "S Pen case - SHEIN SA", "link": "https://sa.shein.com/p-1.html"}
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

func TestWebSearchMapsResults(t *testing.T) {
	srv := newServer(t, http.StatusOK, sampleResponse, func(r *http.Request) {
		q := r.URL.Query()
		if got, want := q.Get("q"), "s pen (site:amazon.com OR site:temu.com OR site:ar.shein.com)"; got != want {
			t.Errorf("q = %q, want %q", got, want)
		}
		if q.Get("gl") != "jo" || q.Get("engine") != "google" || q.Get("api_key") != "secret" {
			t.Errorf("unexpected params %v", q)
		}
	})
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Engine: EngineWeb, Combined: true})

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
			for _, engine := range []string{EngineWeb, EngineShopping} {
				c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Engine: engine})
				_, err := c.Search(context.Background(), search.Query{Title: "x", Targets: jorTargets})
				if (err != nil) != tt.wantErr {
					t.Fatalf("%s: err = %v, wantErr %v", engine, err, tt.wantErr)
				}
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

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("expected error without API key")
	}
	if _, err := New(Config{APIKey: "k", Engine: "bing"}); err == nil {
		t.Error("unknown engine accepted")
	}
	c, err := New(Config{APIKey: "k"})
	if err != nil || c.cfg.Engine != EngineShopping {
		t.Errorf("default engine = %q, %v; want google_shopping", c.cfg.Engine, err)
	}
}

const sampleShopping = `{
  "shopping_results": [
    {"position": 1, "title": "Samsung S Pen", "product_link": "https://www.google.com/shopping/product/1",
     "source": "Amazon.com", "price": "$29.99", "extracted_price": 29.99,
     "thumbnail": "https://img/1.jpg", "delivery": "Free delivery", "rating": 4.6, "reviews": 1200},
    {"position": 2, "title": "S Pen Pro", "link": "https://www.temu.com/goods-2.html",
     "product_link": "https://www.google.com/shopping/product/2",
     "source": "Temu", "price": "SAR 45.00", "extracted_price": 45},
    {"position": 3, "title": "Stylus", "product_link": "https://www.google.com/shopping/product/3",
     "source": "Best Buy", "price": "$35.00", "extracted_price": 35},
    {"position": 4, "title": "Pen case", "link": "https://sa.shein.com/p-4.html",
     "source": "SHEIN", "price": "12,50 €", "extracted_price": 12.5},
    {"position": 5, "title": "Second S Pen", "product_link": "https://www.google.com/shopping/product/5",
     "source": "Amazon.com - Seller", "price": "$19.00"},
    {"position": 6, "title": "", "source": "Amazon.com", "product_link": "https://x"}
  ]
}`

func TestShoppingSearchMapsResults(t *testing.T) {
	srv := newServer(t, http.StatusOK, sampleShopping, func(r *http.Request) {
		q := r.URL.Query()
		if q.Get("engine") != EngineShopping || q.Get("q") != "s pen" || q.Get("gl") != "sa" {
			t.Errorf("unexpected params %v", q)
		}
	})
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Combined: true})

	got, err := c.Search(context.Background(), search.Query{Title: "s pen", Region: "sa", Language: "en", Targets: jorTargets})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []struct {
		market, link, currency string
		pos                    int
		price                  float64
	}{
		{"amazon", "https://www.google.com/shopping/product/1", "$", 1, 29.99},
		{"temu", "https://www.temu.com/goods-2.html", "SAR", 1, 45},
		{"shein", "https://sa.shein.com/p-4.html", "€", 1, 12.5},
		{"amazon", "https://www.google.com/shopping/product/5", "", 2, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d products, want %d (Best Buy and untitled must be dropped): %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Market != w.market || g.Link != w.link || g.Position != w.pos || g.Currency != w.currency {
			t.Errorf("product %d = %+v, want %+v", i, g, w)
		}
		if w.price != 0 && (g.Price == nil || *g.Price != w.price) {
			t.Errorf("product %d price = %v, want %v", i, g.Price, w.price)
		}
	}
	if len(got[0].Extensions) != 2 || got[0].Extensions[1] != "rating 4.6 (1200 reviews)" {
		t.Errorf("extensions = %v", got[0].Extensions)
	}
	if got[3].Price != nil {
		t.Error("price set without extracted_price")
	}
}

func TestShoppingPerMarketNamesTheShop(t *testing.T) {
	srv := newServer(t, http.StatusOK, sampleShopping, func(r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "s pen Temu" {
			t.Errorf("q = %q, want %q", got, "s pen Temu")
		}
	})
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL})
	got, err := c.Search(context.Background(), search.Query{Title: "s pen", Targets: jorTargets[1:2]})
	if err != nil || len(got) != 1 || got[0].Market != "temu" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestShoppingFallsBackToWebWhenNothingMatches(t *testing.T) {
	var engines []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		engine := r.URL.Query().Get("engine")
		engines = append(engines, engine)
		if engine == EngineShopping {
			// Only shops we do not cover.
			_, _ = w.Write([]byte(`{"shopping_results":[{"title":"x","source":"Best Buy","product_link":"https://g/1"}]}`))
			return
		}
		_, _ = w.Write([]byte(sampleResponse))
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Combined: true})

	got, err := c.Search(context.Background(), search.Query{Title: "s pen", Region: "jo", Targets: jorTargets})
	if err != nil || len(got) != 4 {
		t.Fatalf("got %d products, %v", len(got), err)
	}
	if strings.Join(engines, ",") != "google_shopping,google" {
		t.Errorf("engines called = %v", engines)
	}
}

func TestShoppingErrorDoesNotFallBack(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Invalid API key."}`))
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL})
	if _, err := c.Search(context.Background(), search.Query{Title: "x", Targets: jorTargets}); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (a hard error must not cost a second search)", calls)
	}
}

func TestCurrencyOf(t *testing.T) {
	for in, want := range map[string]string{"$29.99": "$", "SAR 1,200.00": "SAR", "12,50 €": "€", "": "", "JOD 7": "JOD"} {
		if got := currencyOf(in); got != want {
			t.Errorf("currencyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShoppingUsesShoppingRegionAndLeavesUncovered(t *testing.T) {
	srv := newServer(t, http.StatusOK, sampleShopping, func(r *http.Request) {
		// Google Shopping does not cover Jordan: the shopping region is used.
		if q := r.URL.Query(); q.Get("gl") != "us" {
			t.Errorf("gl = %q, want us", q.Get("gl"))
		}
	})
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Combined: true})
	targets := append([]markets.Target{{Market: "aliexpress", Name: "AliExpress", Domain: "aliexpress.com"}}, jorTargets...)

	_, err := c.Search(context.Background(), search.Query{Title: "s pen", Region: "jo", ShoppingRegion: "us", Targets: targets})
	var pe *search.PartialError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *search.PartialError", err)
	}
	if len(pe.Products) != 4 || len(pe.Failed) != 0 || len(pe.Uncovered) != 1 || !pe.Uncovered["aliexpress"] {
		t.Errorf("products %d, failed %v, uncovered %v", len(pe.Products), pe.Failed, pe.Uncovered)
	}
}

func TestShoppingDropsSimilarProductsAndCleansLinks(t *testing.T) {
	// From the local test: Google Shopping listed SSK portable SSDs for an
	// enclosure search, with raw spaces in Google's product link.
	body := `{"shopping_results":[
		{"title":"SSK Portable SSD USB Drive 550MB/S External Solid State Drive","source":"AliExpress - AliExpress-6000993635",
		 "product_link":"https://www.google.com/search?ibp=oshop&q=SSK ssd m3 enclosure&prds=productid:1,pvt:hg&gl=us"},
		{"title":"SSK M.2 NVME SSD Enclosure USB 3.2","source":"AliExpress",
		 "product_link":"https://www.google.com/search?ibp=oshop&q=SSK ssd m3 enclosure&prds=productid:2,pvt:hg&gl=us"}]}`
	srv := newServer(t, http.StatusOK, body, nil)
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Combined: true})
	ali := []markets.Target{{Market: "aliexpress", Name: "AliExpress", Domain: "aliexpress.com"}}

	got, err := c.Search(context.Background(), search.Query{Title: "SSK ssd m3 enclosure", Targets: ali})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v; want only the enclosure", got, err)
	}
	if strings.Contains(got[0].Link, " ") || !strings.Contains(got[0].Link, "q=SSK+ssd+m3+enclosure") {
		t.Errorf("link not cleaned: %s", got[0].Link)
	}
}

func TestShoppingNoWebRetryLeavesAllUncovered(t *testing.T) {
	var engines []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		engines = append(engines, r.URL.Query().Get("engine"))
		_, _ = w.Write([]byte(`{"shopping_results":[{"title":"x","source":"Best Buy","product_link":"https://g/1"}]}`))
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Combined: true, NoWebRetry: true})

	_, err := c.Search(context.Background(), search.Query{Title: "s pen", Targets: jorTargets})
	var pe *search.PartialError
	if !errors.As(err, &pe) || len(pe.Uncovered) != 3 || len(pe.Products) != 0 {
		t.Fatalf("err = %v, want all markets uncovered", err)
	}
	if len(engines) != 1 {
		t.Errorf("SerpApi calls = %v, want only the shopping search", engines)
	}
}

func TestShoppingUsesShopSearchInsteadOfGoogleLink(t *testing.T) {
	// From the local test: Google's oshop links do not open outside Google.
	body := `{"shopping_results":[{"title":"Stylus Pen For Samsung S Pen Galaxy Tab S6 Lite","source":"AliExpress - AliExpress-2673793064",
		"product_link":"https://www.google.com/search?ibp=oshop&prds=productid:1","extracted_price":25.08,"price":"$25.08"}]}`
	srv := newServer(t, http.StatusOK, body, nil)
	c, _ := New(Config{APIKey: "secret", BaseURL: srv.URL, Combined: true})
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor")

	got, err := c.Search(context.Background(), search.Query{Title: "samsung s pen", Targets: jor.Targets[1:2]})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got[0].Link != "https://www.aliexpress.com/w/wholesale-stylus-pen-for-samsung-s-pen-galaxy-tab.html" || got[0].LinkType != "search" {
		t.Errorf("link = %s (%s)", got[0].Link, got[0].LinkType)
	}
}
