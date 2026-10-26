package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"owis_find_deal_engine/internal/markets"
)

func probeService(t *testing.T) (*Service, *stubProvider, *stubProvider, *stubProvider) {
	t.Helper()
	cat, err := markets.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cat, err = cat.WithProviders(map[string]string{"temu": "apify-temu"})
	if err != nil {
		t.Fatal(err)
	}
	free := &stubProvider{name: "searxng", batch: true, err: errors.New("blocked")}
	paid := &stubProvider{name: "serpapi", batch: true}
	scraper := &stubProvider{name: "apify", batch: true}
	web, _ := NewFallback([]Provider{free, paid}, FallbackOptions{}, discard)
	temu, _ := NewFallback([]Provider{scraper, web}, FallbackOptions{}, discard)
	svc := NewService(cat, map[string]Provider{"web": web, "apify-temu": temu}, Options{Timeout: time.Second}, discard)
	return svc, free, paid, scraper
}

func TestProbeFirstSourceOnly(t *testing.T) {
	svc, free, paid, scraper := probeService(t)
	cost := func(provider, market string) float64 {
		if provider == "apify" && market == "temu" {
			return 0.10
		}
		return 0
	}
	res, err := svc.Probe(context.Background(), ProbeRequest{Title: " usb  hub ", Country: "jor", Markets: []string{"temu", "amazon"}}, cost)
	if err != nil {
		t.Fatal(err)
	}
	if res.Query != "usb hub" || len(res.Sources) != 2 || res.CostUSD != 0.10 {
		t.Fatalf("result = %+v", res)
	}
	temu, amazon := res.Sources[0], res.Sources[1]
	if temu.Market != "temu" || temu.Provider != "apify" || temu.Status != StatusOK || len(temu.Products) != 1 || temu.Fallback {
		t.Errorf("temu = %+v", temu)
	}
	// Amazon's first source fails; the probe reports it and does not fall back.
	if amazon.Provider != "searxng" || amazon.Status != StatusError || amazon.Error == "" || len(amazon.Products) != 0 {
		t.Errorf("amazon = %+v", amazon)
	}
	if paid.calls.Load() != 0 || free.calls.Load() != 1 || scraper.calls.Load() != 1 {
		t.Errorf("calls: searxng %d serpapi %d apify %d", free.calls.Load(), paid.calls.Load(), scraper.calls.Load())
	}
}

func TestProbeAllSources(t *testing.T) {
	svc, free, paid, scraper := probeService(t)
	res, err := svc.Probe(context.Background(), ProbeRequest{Title: "usb hub", Country: "jor", Markets: []string{"temu"}, AllSources: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range res.Sources {
		got = append(got, s.Provider)
		if s.Fallback != (s.Provider != "apify") {
			t.Errorf("%s fallback = %v", s.Provider, s.Fallback)
		}
	}
	if len(got) != 3 || got[0] != "apify" || got[1] != "searxng" || got[2] != "serpapi" {
		t.Errorf("sources = %v", got)
	}
	if free.calls.Load() != 1 || paid.calls.Load() != 1 || scraper.calls.Load() != 1 {
		t.Error("every source must be asked once")
	}
}

func TestProbeValidation(t *testing.T) {
	svc, _, _, _ := probeService(t)
	ctx := context.Background()
	if _, err := svc.Probe(ctx, ProbeRequest{Title: "x", Country: "jor"}, nil); !errors.Is(err, ErrInvalidTitle) {
		t.Errorf("short title: %v", err)
	}
	if _, err := svc.Probe(ctx, ProbeRequest{Title: "hub", Country: "xx"}, nil); !errors.Is(err, ErrUnknownCountry) {
		t.Errorf("country: %v", err)
	}
	if _, err := svc.Probe(ctx, ProbeRequest{Title: "hub", Country: "jor", Markets: []string{"ebay"}}, nil); !errors.Is(err, ErrUnknownMarket) {
		t.Errorf("shop not in jor: %v", err)
	}
}

func TestServiceHealth(t *testing.T) {
	svc, _, _, _ := probeService(t)
	for range 3 {
		_, _ = svc.Search(context.Background(), Request{Title: "usb hub", Country: "jor"})
	}
	h := svc.Health()
	if len(h) != 2 || h[0].Name != "apify-temu" || h[1].Name != "web" || h[1].Chain != "searxng>serpapi" {
		t.Fatalf("health = %+v", h)
	}
	web := h[1].Sources
	if len(web) != 2 || web[0].Provider != "searxng" || web[0].State != "open" || web[0].OpenUntil == nil ||
		web[1].State != "closed" || !web[1].LastResort {
		t.Errorf("web sources = %+v", web)
	}
}
