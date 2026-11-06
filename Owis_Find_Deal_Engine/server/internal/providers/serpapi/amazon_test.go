package serpapi

import (
	"context"
	"net/http"
	"testing"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

const sampleAmazon = `{
  "organic_results": [
    {"position": 1, "asin": "B07MNFH1PX", "title": "SSK M.2 NVMe SATA SSD Enclosure USB 3.2 Gen2 10Gbps",
     "link": "https://www.amazon.com/SSK-Enclosure/dp/B07MNFH1PX/ref=sr_1_1?keywords=ssk",
     "link_clean": "https://www.amazon.com/dp/B07MNFH1PX",
     "thumbnail": "https://m.media-amazon.com/images/I/1.jpg",
     "price": "$29.99", "extracted_price": 29.99, "rating": 4.5, "reviews": 7123,
     "delivery": ["FREE delivery Thu, Oct 9", "Ships to Jordan"]},
    {"position": 2, "asin": "B0FD38XB93", "title": "SSK 20Gbps NVMe SSD Enclosure Aluminum", "sponsored": true,
     "link": "https://www.amazon.com/sspa/click?ie=UTF8&spc=x", "price": "$45.99", "extracted_price": 45.99},
    {"position": 3, "asin": "B0AAAAAAAA", "title": "USB C Cable 6ft", "link_clean": "https://www.amazon.com/dp/B0AAAAAAAA"},
    {"position": 4, "asin": "B07MNFH1PX", "title": "SSK M.2 NVMe Enclosure duplicate", "link_clean": "https://www.amazon.com/dp/B07MNFH1PX"},
    {"position": 5, "title": "SSK NVMe enclosure without asin or link"}
  ]
}`

func TestAmazonSearch(t *testing.T) {
	srv := newServer(t, http.StatusOK, sampleAmazon, func(r *http.Request) {
		q := r.URL.Query()
		if q.Get("engine") != "amazon" || q.Get("k") != "ssk nvme enclosure" || q.Get("amazon_domain") != "amazon.com" {
			t.Errorf("unexpected params %v", q)
		}
	})
	a, err := NewAmazon(Config{APIKey: "secret", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor") // Jordan's Amazon is amazon.com

	got, err := a.Search(context.Background(), search.Query{Title: "ssk nvme enclosure", Targets: jor.Targets[:1]})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d products, want 2 (cable irrelevant, duplicate and link-less dropped): %+v", len(got), got)
	}
	if got[0].Link != "https://www.amazon.com/dp/B07MNFH1PX" {
		t.Errorf("link = %s", got[0].Link)
	}
	if got[0].Price == nil || *got[0].Price != 29.99 || got[0].Currency != "$" || got[0].Provider != "serpapi-amazon" {
		t.Errorf("product 0 = %+v", got[0])
	}
	if len(got[0].Extensions) != 2 || got[0].Extensions[0] != "rating 4.5 (7123 reviews)" ||
		got[0].Extensions[1] != "FREE delivery Thu, Oct 9, Ships to Jordan" {
		t.Errorf("extensions = %v", got[0].Extensions)
	}
	// Sponsored link is an ad redirect: rebuilt from the ASIN, marked.
	if got[1].Link != "https://www.amazon.com/dp/B0FD38XB93" || got[1].Extensions[0] != "Sponsored" || got[1].Position != 2 {
		t.Errorf("product 1 = %+v", got[1])
	}
}

func TestAmazonErrors(t *testing.T) {
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor")

	srv := newServer(t, http.StatusOK, `{"error":"Amazon hasn't returned any results for this query."}`, nil)
	a, _ := NewAmazon(Config{APIKey: "secret", BaseURL: srv.URL})
	if got, err := a.Search(context.Background(), search.Query{Title: "zzqx", Targets: jor.Targets[:1]}); err != nil || len(got) != 0 {
		t.Errorf("no results: got %v, %v", got, err)
	}

	srv = newServer(t, http.StatusUnauthorized, `{"error":"Invalid API key."}`, nil)
	a, _ = NewAmazon(Config{APIKey: "secret", BaseURL: srv.URL})
	if _, err := a.Search(context.Background(), search.Query{Title: "x y", Targets: jor.Targets[:1]}); err == nil {
		t.Error("API error not reported")
	}

	if _, err := a.Search(context.Background(), search.Query{Title: "x y", Targets: jor.Targets[1:2]}); err == nil {
		t.Error("non-amazon marketplace accepted")
	}
}
