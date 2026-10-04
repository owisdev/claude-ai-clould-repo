package config

import (
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
	if cfg.Port != "3002" || !cfg.SearchCombined || cfg.SearchTimeout != 15*time.Second {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.SearchProviders) != 2 || cfg.SearchProviders[0] != "searxng" || cfg.ProviderAttemptTimeout != 8*time.Second {
		t.Errorf("unexpected provider defaults: %+v", cfg)
	}
	if cfg.SerpAPIEngine != "google_shopping" || len(cfg.ApifyMarkets) != 0 {
		t.Errorf("unexpected engine/apify defaults: %+v", cfg)
	}
	if !cfg.CacheEnabled || cfg.CacheFreshTTL != 2*time.Hour || cfg.CacheStaleTTL != 24*time.Hour {
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

	t.Setenv("CACHE_FRESH_TTL", "48h") // longer than the 24h stale TTL
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
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ApifyActors["temu"] != "someone~temu" || cfg.ApifyInputs["shein"] == "" {
		t.Errorf("apify config = %+v %+v", cfg.ApifyActors, cfg.ApifyInputs)
	}
	// 25s Apify + 8s web fallback + 2s margin.
	if cfg.SearchTimeout != 35*time.Second {
		t.Errorf("search timeout = %v, want raised to 35s", cfg.SearchTimeout)
	}

	t.Setenv("SERPAPI_ENGINE", "bing")
	if _, err := Load(); err == nil {
		t.Error("unknown SerpApi engine accepted")
	}
}
