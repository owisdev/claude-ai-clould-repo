package serpapi

import (
	"context"
	"net/http"
	"testing"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

const sampleEbay = `{
  "organic_results": [
    {"position": 1, "title": "Shop on eBay", "link": "https://www.ebay.com/itm/123456",
     "price": {"raw": "$20.00", "extracted": 20.0}},
    {"position": 2, "title": "2.5\" SATA USB 3.0 Hard Drive SSD Enclosure Tool-Free",
     "link": "https://www.ebay.com/itm/387654321098?_skw=ssd+enclosure&hash=item5a&epid=123",
     "thumbnail": "https://i.ebayimg.com/images/g/abc/s-l500.jpg", "condition": "Brand New",
     "price": {"raw": "$6.99", "extracted": 6.99}, "shipping": "Free delivery",
     "rating": 4.5, "reviews": 120},
    {"position": 3, "title": "USB 3.0 SSD Enclosure 2.5 inch, several colors",
     "link": "https://www.ebay.com/itm/Usb-Enclosure/276543210987",
     "price": {"from": {"raw": "$4.49", "extracted": 4.49}, "to": {"raw": "$8.99", "extracted": 8.99}},
     "shipping": {"raw": "+$2.50 delivery"}},
    {"position": 4, "title": "USB 3.0 SSD Enclosure duplicate", "link": "https://www.ebay.com/itm/387654321098"},
    {"position": 5, "title": "SSD USB 3.0 Enclosure store page", "link": "https://www.ebay.com/str/somestore"},
    {"position": 6, "title": "Laptop charger 65W", "link": "https://www.ebay.com/itm/111111111111"}
  ]
}`

func TestEbaySearch(t *testing.T) {
	srv := newServer(t, http.StatusOK, sampleEbay, func(r *http.Request) {
		q := r.URL.Query()
		if q.Get("engine") != "ebay" || q.Get("_nkw") != "ssd usb 3.0 enclosure" || q.Get("ebay_domain") != "ebay.com" {
			t.Errorf("unexpected params %v", q)
		}
	})
	e, err := NewEbay(Config{APIKey: "secret", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := markets.Load("")
	for _, code := range []string{"usa", "ksa"} {
		country, _ := cat.Country(code)
		var ebay markets.Target
		for _, tg := range country.Targets {
			if tg.Market == "ebay" {
				ebay = tg
			}
		}
		got, err := e.Search(context.Background(), search.Query{Title: "ssd usb 3.0 enclosure", Targets: []markets.Target{ebay}})
		if err != nil {
			t.Fatalf("%s: Search: %v", code, err)
		}
		if len(got) != 2 {
			t.Fatalf("%s: got %d products, want 2 (placeholder, duplicate, store page, irrelevant dropped): %+v", code, len(got), got)
		}
		p := got[0]
		if p.Link != "https://www.ebay.com/itm/387654321098" || p.Price == nil || *p.Price != 6.99 || p.Currency != "$" ||
			p.Provider != "serpapi-ebay" || p.Market != "ebay" || p.Thumbnail != "https://i.ebayimg.com/images/g/abc/s-l500.jpg" {
			t.Errorf("%s: product 0 = %+v", code, p)
		}
		if len(p.Extensions) != 3 || p.Extensions[0] != "Brand New" || p.Extensions[1] != "Free delivery" ||
			p.Extensions[2] != "rating 4.5 (120 reviews)" {
			t.Errorf("%s: extensions = %v", code, p.Extensions)
		}
		// Price range: the lowest price; shipping given as an object.
		p = got[1]
		if p.Link != "https://www.ebay.com/itm/276543210987" || p.Price == nil || *p.Price != 4.49 || p.Position != 2 ||
			len(p.Extensions) != 1 || p.Extensions[0] != "+$2.50 delivery" {
			t.Errorf("%s: product 1 = %+v", code, p)
		}
	}
}

func TestEbayRejectsOtherMarkets(t *testing.T) {
	e, _ := NewEbay(Config{APIKey: "secret", BaseURL: "http://unused"})
	_, err := e.Search(context.Background(), search.Query{Title: "x",
		Targets: []markets.Target{{Market: "amazon", Domain: "amazon.com"}}})
	if err == nil {
		t.Error("amazon target accepted")
	}
}
