package config

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "https://securetoken.google.com/p")
	t.Setenv("AUTH_AUDIENCE", "p")
	t.Setenv("SERPAPI_KEY", "k")
	t.Setenv("SEARXNG_URL", "http://searxng:8080")
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://a.com, ,https://b.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Plans != "free:20,pro:500" || cfg.DefaultPlan != "free" || cfg.AuthPlanClaim != "plan" {
		t.Errorf("unexpected plan defaults: %+v", cfg)
	}
	// 15s default raised to fit the chain: 8s SearXNG + 25s SerpApi + 2s.
	if cfg.Port != "3002" || !cfg.SearchCombined || cfg.SearchTimeout != 35*time.Second || cfg.SerpAPITimeout != 25*time.Second {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.SearchProviders) != 2 || cfg.SearchProviders[0] != "searxng" || cfg.ProviderAttemptTimeout != 8*time.Second {
		t.Errorf("unexpected provider defaults: %+v", cfg)
	}
	if cfg.SerpAPIEngine != "google_shopping" || len(cfg.ApifyMarkets) != 0 {
		t.Errorf("unexpected engine/apify defaults: %+v", cfg)
	}
	if !cfg.CacheEnabled || cfg.CacheFreshTTL != 48*time.Hour || cfg.CacheStaleTTL != 72*time.Hour {
		t.Errorf("unexpected cache defaults: %+v", cfg)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Errorf("CORSOrigins = %v", cfg.CORSOrigins)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "")
	t.Setenv("SERPAPI_KEY", "")
	t.Setenv("SEARCH_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadProviderValidation(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "i")
	t.Setenv("AUTH_AUDIENCE", "a")

	tests := []struct {
		providers, searxng, serpapi string
		ok                          bool
	}{
		{"searxng", "http://s", "", true},          // free only, no SerpApi key needed
		{"serpapi", "", "k", true},                 // paid only
		{"searxng,serpapi", "http://s", "", false}, // missing key
		{"searxng,serpapi", "", "k", false},        // missing URL
		{"bing", "", "", false},
		{"serpapi,serpapi", "", "k", false},
		{" , ", "", "", false},
	}
	for _, tt := range tests {
		t.Setenv("SEARCH_PROVIDERS", tt.providers)
		t.Setenv("SEARXNG_URL", tt.searxng)
		t.Setenv("SERPAPI_KEY", tt.serpapi)
		if _, err := Load(); (err == nil) != tt.ok {
			t.Errorf("providers=%q searxng=%q serpapi=%q: err = %v, want ok=%v", tt.providers, tt.searxng, tt.serpapi, err, tt.ok)
		}
	}
}

func TestLoadCacheValidation(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "i")
	t.Setenv("AUTH_AUDIENCE", "a")
	t.Setenv("SEARCH_PROVIDERS", "searxng")
	t.Setenv("SEARXNG_URL", "http://s")

	t.Setenv("CACHE_FRESH_TTL", "96h") // longer than the 72h stale TTL
	if _, err := Load(); err == nil {
		t.Error("fresh TTL longer than stale TTL accepted")
	}
	t.Setenv("CACHE_ENABLED", "false")
	if _, err := Load(); err != nil {
		t.Errorf("disabled cache still validated: %v", err)
	}
}

func TestLoadApify(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "i")
	t.Setenv("AUTH_AUDIENCE", "a")
	t.Setenv("SEARCH_PROVIDERS", "searxng")
	t.Setenv("SEARXNG_URL", "http://s")

	t.Setenv("APIFY_MARKETS", "Temu, shein")
	if _, err := Load(); err == nil {
		t.Fatal("missing token/actors accepted")
	}

	t.Setenv("APIFY_TOKEN", "tok")
	t.Setenv("APIFY_TEMU_ACTOR", "someone~temu")
	t.Setenv("APIFY_TEMU_INPUT", `{"keyword":"{{query}}"}`)
	t.Setenv("APIFY_SHEIN_ACTOR", "someone~shein")
	t.Setenv("APIFY_SHEIN_INPUT", `{"searchQueries":["fixed"]}`)
	if _, err := Load(); err == nil {
		t.Fatal("input without {{query}} accepted")
	}

	t.Setenv("APIFY_SHEIN_INPUT", `{"searchQueries":["{{query}}"]}`)
	t.Setenv("APIFY_SHEIN_MAX_ITEMS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("APIFY_SHEIN_MAX_ITEMS=0 accepted")
	}
	t.Setenv("APIFY_SHEIN_MAX_ITEMS", "5")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ApifyActors["temu"] != "someone~temu" || cfg.ApifyInputs["shein"] == "" {
		t.Errorf("apify config = %+v %+v", cfg.ApifyActors, cfg.ApifyInputs)
	}
	if cfg.ApifyMarketMaxItems["shein"] != 5 || cfg.ApifyMarketMaxItems["temu"] != 10 {
		t.Errorf("max items per market = %v, want shein 5, temu 10 (APIFY_MAX_ITEMS)", cfg.ApifyMarketMaxItems)
	}
	// The slowest shop (Temu, 45s by default) + 2s margin.
	if cfg.SearchTimeout != 47*time.Second {
		t.Errorf("search timeout = %v, want raised to 47s", cfg.SearchTimeout)
	}

	t.Setenv("SERPAPI_ENGINE", "bing")
	if _, err := Load(); err == nil {
		t.Error("unknown SerpApi engine accepted")
	}
}

func TestLoadSerpAPIMarkets(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "i")
	t.Setenv("AUTH_AUDIENCE", "a")
	t.Setenv("SEARXNG_URL", "http://s")

	// No key: no dedicated engine.
	t.Setenv("SEARCH_PROVIDERS", "searxng")
	cfg, err := Load()
	if err != nil || len(cfg.SerpAPIMarkets) != 0 {
		t.Fatalf("without key: %v, %v", cfg.SerpAPIMarkets, err)
	}

	// With a key, Amazon uses the Amazon engine by default.
	t.Setenv("SERPAPI_KEY", "k")
	t.Setenv("SEARCH_PROVIDERS", "serpapi,searxng")
	cfg, err = Load()
	if err != nil || len(cfg.SerpAPIMarkets) != 2 || cfg.SerpAPIMarkets[0] != "amazon" || cfg.SerpAPIMarkets[1] != "ebay" {
		t.Fatalf("default: %v, %v", cfg.SerpAPIMarkets, err)
	}
	// Longest path: SerpApi 25s + SearXNG 8s + 2s.
	if cfg.SearchTimeout != 35*time.Second {
		t.Errorf("search timeout = %v", cfg.SearchTimeout)
	}

	t.Setenv("SERPAPI_MARKETS", "")
	if cfg, _ = Load(); len(cfg.SerpAPIMarkets) != 0 {
		t.Errorf("SERPAPI_MARKETS= did not disable: %v", cfg.SerpAPIMarkets)
	}
	t.Setenv("SERPAPI_MARKETS", "temu")
	if _, err := Load(); err == nil {
		t.Error("market without a SerpApi engine accepted")
	}
}

func TestLoadSignInAndProxies(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "https://securetoken.google.com/p")
	t.Setenv("AUTH_AUDIENCE", "p")
	t.Setenv("SEARCH_PROVIDERS", "searxng")
	t.Setenv("SEARXNG_URL", "http://searxng:8080")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.AuthAllowedSignIn, ",") != "google.com,apple.com,password" || len(cfg.TrustedProxies) != 0 {
		t.Errorf("defaults: sign-in %v, proxies %v", cfg.AuthAllowedSignIn, cfg.TrustedProxies)
	}

	t.Setenv("AUTH_ALLOWED_SIGN_IN", " Google.com , password")
	t.Setenv("TRUSTED_PROXIES", "10.203.20.10, 10.0.0.0/8, ::ffff:192.0.2.1, 10.203.20.17/28")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.AuthAllowedSignIn, ",") != "google.com,password" {
		t.Errorf("sign-in = %v", cfg.AuthAllowedSignIn)
	}
	if fmt.Sprint(cfg.TrustedProxies) != "[10.203.20.10/32 10.0.0.0/8 192.0.2.1/32 10.203.20.16/28]" {
		t.Errorf("proxies = %v", cfg.TrustedProxies)
	}

	t.Setenv("AUTH_ALLOWED_SIGN_IN", "*")
	if cfg, err = Load(); err != nil || cfg.AuthAllowedSignIn != nil {
		t.Errorf("* = %v, %v", cfg.AuthAllowedSignIn, err)
	}

	for _, bad := range []string{"cloudflared", "10.0.0.0/33", "10.0.0.1:80"} {
		t.Setenv("TRUSTED_PROXIES", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestLoadShopRules(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "i")
	t.Setenv("AUTH_AUDIENCE", "a")
	t.Setenv("SEARCH_PROVIDERS", "serpapi,searxng")
	t.Setenv("SEARXNG_URL", "http://s")
	t.Setenv("SERPAPI_KEY", "k")
	t.Setenv("APIFY_TOKEN", "tok")
	t.Setenv("APIFY_MARKETS", "aliexpress,temu")
	t.Setenv("APIFY_TIMEOUT", "90s")
	for _, m := range []string{"ALIEXPRESS", "TEMU", "SHEIN"} {
		t.Setenv("APIFY_"+m+"_ACTOR", "someone~"+m)
		t.Setenv("APIFY_"+m+"_INPUT", `{"q":"{{query}}"}`)
	}

	// Defaults: the old lists decide the source, the agreed time limits
	// apply; shein (in no list) goes to the web providers.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]ShopRule{
		"amazon":     {SourceSerpAPI, 15 * time.Second, GroupFast},
		"ebay":       {SourceSerpAPI, 15 * time.Second, GroupFast},
		"aliexpress": {SourceApify, 60 * time.Second, GroupSlow},
		"temu":       {SourceApify, 45 * time.Second, GroupSlow},
		"shein":      {SourceWeb, 33 * time.Second, GroupSlow}, // SerpApi 25s + SearXNG 8s
	}
	for m, w := range want {
		if got := cfg.Shops[m]; got != w {
			t.Errorf("%s = %+v, want %+v", m, got, w)
		}
	}
	if cfg.SearchTimeout != 62*time.Second {
		t.Errorf("search timeout = %v, want 62s (aliexpress 60s + 2s)", cfg.SearchTimeout)
	}
	if !slices.Equal(cfg.ApifyMarkets, []string{"aliexpress", "temu"}) || !slices.Equal(cfg.SerpAPIMarkets, []string{"amazon", "ebay"}) {
		t.Errorf("apify %v serpapi %v", cfg.ApifyMarkets, cfg.SerpAPIMarkets)
	}

	// SHOP_* settings win over the old lists.
	t.Setenv("SHOP_SHEIN_SOURCE", "off")
	t.Setenv("SHOP_AMAZON_SOURCE", "web")
	t.Setenv("SHOP_TEMU_TIMEOUT", "20s")
	t.Setenv("SHOP_TEMU_GROUP", "fast")
	t.Setenv("SHOP_ALIEXPRESS_SOURCE", "Off")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r := cfg.Shops["shein"]; r.Source != SourceOff {
		t.Errorf("shein = %+v, want off", r)
	}
	if r := cfg.Shops["temu"]; r.Timeout != 20*time.Second || r.Group != GroupFast {
		t.Errorf("temu = %+v", r)
	}
	if !slices.Equal(cfg.ApifyMarkets, []string{"temu"}) || !slices.Equal(cfg.SerpAPIMarkets, []string{"ebay"}) {
		t.Errorf("apify %v serpapi %v", cfg.ApifyMarkets, cfg.SerpAPIMarkets)
	}
	// amazon on the web chain (33s) is now the slowest shop.
	if cfg.SearchTimeout != 35*time.Second {
		t.Errorf("search timeout = %v, want 35s", cfg.SearchTimeout)
	}

	for key, bad := range map[string]string{
		"SHOP_TEMU_SOURCE":  "scraper",
		"SHOP_TEMU_TIMEOUT": "0s",
		"SHOP_TEMU_GROUP":   "medium",
		"SHOP_SHEIN_SOURCE": "serpapi", // no SerpApi engine for SHEIN
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)
			if _, err := Load(); err == nil {
				t.Errorf("%s=%s accepted", key, bad)
			}
		})
	}
	if !Runtime("SHOP_TEMU_SOURCE") || !Runtime("CACHE_LAST_KNOWN_TTL") || Runtime("SHOP_TEMU_COLOR") {
		t.Error("SHOP_* runtime keys not recognised as expected")
	}
}

func TestLoadQueueAndApifyRun(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "i")
	t.Setenv("AUTH_AUDIENCE", "a")
	t.Setenv("SEARCH_PROVIDERS", "searxng")
	t.Setenv("SEARXNG_URL", "http://s")
	t.Setenv("APIFY_TOKEN", "tok")
	t.Setenv("APIFY_MARKETS", "aliexpress,temu")
	for _, m := range []string{"ALIEXPRESS", "TEMU"} {
		t.Setenv("APIFY_"+m+"_ACTOR", "someone~"+m)
		t.Setenv("APIFY_"+m+"_INPUT", `{"q":"{{query}}"}`)
	}
	t.Setenv("SHOP_ALIEXPRESS_TIMEOUT", "90s")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.QueueApifyMaxRuns != 4 || cfg.QueueSerpAPIMaxRuns != 10 || cfg.QueueWebMaxRuns != 10 || cfg.QueueMaxWait != time.Minute {
		t.Errorf("queue defaults = %d %d %d %v", cfg.QueueApifyMaxRuns, cfg.QueueSerpAPIMaxRuns, cfg.QueueWebMaxRuns, cfg.QueueMaxWait)
	}
	if got := cfg.ApifyRunTimeout("aliexpress"); got != 85*time.Second {
		t.Errorf("apify run timeout = %v, want 85s (90s - 5s)", got)
	}
	if cfg.ApifyMemoryMB["aliexpress"] != 0 || cfg.LongestShopTimeout() != 90*time.Second {
		t.Errorf("memory %v longest %v", cfg.ApifyMemoryMB, cfg.LongestShopTimeout())
	}

	t.Setenv("APIFY_ALIEXPRESS_MEMORY_MB", "4096")
	t.Setenv("APIFY_STOP_MARGIN", "10s")
	if cfg, err = Load(); err != nil || cfg.ApifyMemoryMB["aliexpress"] != 4096 || cfg.ApifyRunTimeout("aliexpress") != 80*time.Second {
		t.Errorf("memory / margin = %v %v %v", cfg.ApifyMemoryMB, cfg.ApifyRunTimeout("aliexpress"), err)
	}
	for key, bad := range map[string]string{
		"APIFY_ALIEXPRESS_MEMORY_MB": "3000",
		"QUEUE_APIFY_MAX_RUNS":       "0",
		"QUEUE_MAX_WAIT":             "0s",
		"APIFY_STOP_MARGIN":          "-1s",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)
			if _, err := Load(); err == nil {
				t.Errorf("%s=%s accepted", key, bad)
			}
		})
	}
	if !Runtime("QUEUE_MAX_WAIT") || !Runtime("APIFY_TEMU_MEMORY_MB") || !Runtime("APIFY_STOP_MARGIN") {
		t.Error("new runtime keys not recognised")
	}
}
