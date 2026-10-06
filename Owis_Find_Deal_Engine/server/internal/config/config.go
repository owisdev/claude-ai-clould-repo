// Package config loads service settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Port string

	// Search providers, tried in order until one succeeds.
	SearchProviders        []string // "searxng", "serpapi"
	SearXNGURL             string
	SearXNGOneQuery        bool // one OR-ed site: query instead of one per market
	SearXNGMaxPages        int
	SerpAPIKey             string
	SerpAPIEngine          string // "google_shopping" (prices) or "google" (web)
	SerpAPIBaseURL         string // empty = SerpApi; for tests / outbound proxies
	ProviderAttemptTimeout time.Duration

	// Apify scrapers for marketplaces without an official API (Temu, SHEIN).
	ApifyToken            string
	ApifyMarkets          []string          // e.g. temu,shein
	ApifyActors           map[string]string // market -> "owner~actor-name"
	ApifyInputs           map[string]string // market -> input JSON template with {{query}}
	ApifyMaxItems         int
	ApifyTimeout          time.Duration
	ApifyBaseURL          string // empty = Apify; for tests / outbound proxies
	ProviderFailThreshold int
	ProviderCooldown      time.Duration

	// End-user JWTs from the auth provider (Firebase, Supabase, Clerk, ...).
	AuthJWKSURL   string
	AuthIssuer    string
	AuthAudience  string
	AuthPlanClaim string

	Plans       string // "free:20,pro:500" daily searches per plan
	DefaultPlan string
	RedisURL    string // empty: in-memory counters (single instance only)

	// Search cache (stale-while-revalidate).
	CacheEnabled              bool
	CacheFreshTTL             time.Duration
	CachePartialTTL           time.Duration
	CacheEmptyTTL             time.Duration
	CacheStaleTTL             time.Duration
	CacheMinRefresh           time.Duration
	CacheMaxEntries           int
	CacheMaxBackgroundRefresh int

	SearchCombined   bool // one provider call per search instead of one per market
	ResultsPerMarket int
	MarketsFile      string
	CORSOrigins      []string
	RateLimitRPS     float64 // per user, burst smoothing
	RateLimitBurst   int
	SearchTimeout    time.Duration
	MaxConcurrency   int
	ShutdownTimeout  time.Duration
	LogLevel         string
}

// Load reads the environment. Required: the AUTH_* settings and the
// settings of every provider in SEARCH_PROVIDERS.
func Load() (Config, error) {
	var errs []error
	cfg := Config{
		Port:                      env("PORT", "3002"),
		AuthJWKSURL:               os.Getenv("AUTH_JWKS_URL"),
		AuthIssuer:                os.Getenv("AUTH_ISSUER"),
		AuthAudience:              os.Getenv("AUTH_AUDIENCE"),
		AuthPlanClaim:             env("AUTH_PLAN_CLAIM", "plan"),
		Plans:                     env("PLANS", "free:20,pro:500"),
		DefaultPlan:               env("DEFAULT_PLAN", "free"),
		RedisURL:                  os.Getenv("REDIS_URL"),
		SearchProviders:           splitList(env("SEARCH_PROVIDERS", "searxng,serpapi")),
		SearXNGURL:                os.Getenv("SEARXNG_URL"),
		SearXNGOneQuery:           parse(&errs, "SEARXNG_ONE_QUERY", false, strconv.ParseBool),
		SearXNGMaxPages:           parse(&errs, "SEARXNG_MAX_PAGES", 2, strconv.Atoi),
		SerpAPIKey:                os.Getenv("SERPAPI_KEY"),
		SerpAPIEngine:             env("SERPAPI_ENGINE", "google_shopping"),
		SerpAPIBaseURL:            os.Getenv("SERPAPI_BASE_URL"),
		ApifyBaseURL:              os.Getenv("APIFY_BASE_URL"),
		ApifyToken:                os.Getenv("APIFY_TOKEN"),
		ApifyMarkets:              splitList(strings.ToLower(os.Getenv("APIFY_MARKETS"))),
		ApifyActors:               map[string]string{},
		ApifyInputs:               map[string]string{},
		ApifyMaxItems:             parse(&errs, "APIFY_MAX_ITEMS", 10, strconv.Atoi),
		ApifyTimeout:              parse(&errs, "APIFY_TIMEOUT", 25*time.Second, time.ParseDuration),
		ProviderAttemptTimeout:    parse(&errs, "PROVIDER_ATTEMPT_TIMEOUT", 8*time.Second, time.ParseDuration),
		ProviderFailThreshold:     parse(&errs, "PROVIDER_FAILURE_THRESHOLD", 3, strconv.Atoi),
		ProviderCooldown:          parse(&errs, "PROVIDER_COOLDOWN", time.Minute, time.ParseDuration),
		CacheEnabled:              parse(&errs, "CACHE_ENABLED", true, strconv.ParseBool),
		CacheFreshTTL:             parse(&errs, "CACHE_FRESH_TTL", 2*time.Hour, time.ParseDuration),
		CachePartialTTL:           parse(&errs, "CACHE_PARTIAL_TTL", 10*time.Minute, time.ParseDuration),
		CacheEmptyTTL:             parse(&errs, "CACHE_EMPTY_TTL", 30*time.Minute, time.ParseDuration),
		CacheStaleTTL:             parse(&errs, "CACHE_STALE_TTL", 24*time.Hour, time.ParseDuration),
		CacheMinRefresh:           parse(&errs, "CACHE_MIN_REFRESH", 10*time.Minute, time.ParseDuration),
		CacheMaxEntries:           parse(&errs, "CACHE_MAX_ENTRIES", 10000, strconv.Atoi),
		CacheMaxBackgroundRefresh: parse(&errs, "CACHE_MAX_BACKGROUND_REFRESH", 4, strconv.Atoi),
		MarketsFile:               os.Getenv("MARKETS_FILE"),
		CORSOrigins:               splitList(os.Getenv("CORS_ALLOWED_ORIGINS")),
		LogLevel:                  env("LOG_LEVEL", "info"),
		SearchCombined:            parse(&errs, "SEARCH_COMBINED", true, strconv.ParseBool),
		RateLimitRPS:              parse(&errs, "RATE_LIMIT_RPS", 1.0, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) }),
		RateLimitBurst:            parse(&errs, "RATE_LIMIT_BURST", 5, strconv.Atoi),
		MaxConcurrency:            parse(&errs, "SEARCH_MAX_CONCURRENCY", 4, strconv.Atoi),
		ResultsPerMarket:          parse(&errs, "RESULTS_PER_MARKET", 10, strconv.Atoi),
		SearchTimeout:             parse(&errs, "SEARCH_TIMEOUT", 15*time.Second, time.ParseDuration),
		ShutdownTimeout:           parse(&errs, "SHUTDOWN_TIMEOUT", 10*time.Second, time.ParseDuration),
	}

	if cfg.AuthJWKSURL == "" || cfg.AuthIssuer == "" || cfg.AuthAudience == "" {
		errs = append(errs, errors.New("AUTH_JWKS_URL, AUTH_ISSUER and AUTH_AUDIENCE are required"))
	}
	if len(cfg.SearchProviders) == 0 {
		errs = append(errs, errors.New("SEARCH_PROVIDERS must list at least one provider"))
	}
	seen := map[string]bool{}
	for _, p := range cfg.SearchProviders {
		switch {
		case seen[p]:
			errs = append(errs, fmt.Errorf("SEARCH_PROVIDERS: %q listed twice", p))
		case p == "searxng" && cfg.SearXNGURL == "":
			errs = append(errs, errors.New("SEARXNG_URL is required when searxng is in SEARCH_PROVIDERS"))
		case p == "serpapi" && cfg.SerpAPIKey == "":
			errs = append(errs, errors.New("SERPAPI_KEY is required when serpapi is in SEARCH_PROVIDERS"))
		case p != "searxng" && p != "serpapi":
			errs = append(errs, fmt.Errorf("SEARCH_PROVIDERS: unknown provider %q", p))
		}
		seen[p] = true
	}
	if cfg.SerpAPIEngine != "google_shopping" && cfg.SerpAPIEngine != "google" {
		errs = append(errs, fmt.Errorf("SERPAPI_ENGINE: %q must be google_shopping or google", cfg.SerpAPIEngine))
	}
	if len(cfg.ApifyMarkets) > 0 {
		if cfg.ApifyToken == "" {
			errs = append(errs, errors.New("APIFY_TOKEN is required when APIFY_MARKETS is set"))
		}
		if cfg.ApifyMaxItems <= 0 || cfg.ApifyTimeout <= 0 || cfg.ApifyTimeout > 300*time.Second {
			errs = append(errs, errors.New("APIFY_MAX_ITEMS must be positive and APIFY_TIMEOUT between 1s and 300s"))
		}
		for _, m := range cfg.ApifyMarkets {
			key := "APIFY_" + strings.ToUpper(m)
			cfg.ApifyActors[m] = os.Getenv(key + "_ACTOR")
			cfg.ApifyInputs[m] = os.Getenv(key + "_INPUT")
			if cfg.ApifyActors[m] == "" || !strings.Contains(cfg.ApifyInputs[m], "{{query}}") {
				errs = append(errs, fmt.Errorf("%s_ACTOR and %s_INPUT (JSON containing {{query}}) are required for %s", key, key, m))
			}
		}
		// An Actor run can take much longer than a web search: give the whole
		// search enough time for it plus a web fallback.
		cfg.SearchTimeout = max(cfg.SearchTimeout, cfg.ApifyTimeout+cfg.ProviderAttemptTimeout+2*time.Second)
	}
	if cfg.RateLimitRPS <= 0 || cfg.RateLimitBurst <= 0 || cfg.MaxConcurrency <= 0 || cfg.ProviderFailThreshold <= 0 || cfg.ResultsPerMarket <= 0 {
		errs = append(errs, errors.New("RATE_LIMIT_RPS, RATE_LIMIT_BURST, SEARCH_MAX_CONCURRENCY, PROVIDER_FAILURE_THRESHOLD and RESULTS_PER_MARKET must be positive"))
	}
	if cfg.CacheEnabled {
		if cfg.CacheFreshTTL <= 0 || cfg.CachePartialTTL <= 0 || cfg.CacheEmptyTTL <= 0 || cfg.CacheStaleTTL <= 0 {
			errs = append(errs, errors.New("CACHE_*_TTL must be positive"))
		} else if cfg.CacheFreshTTL > cfg.CacheStaleTTL {
			errs = append(errs, errors.New("CACHE_FRESH_TTL must not be longer than CACHE_STALE_TTL"))
		}
		if cfg.CacheMaxEntries <= 0 || cfg.CacheMaxBackgroundRefresh <= 0 {
			errs = append(errs, errors.New("CACHE_MAX_ENTRIES and CACHE_MAX_BACKGROUND_REFRESH must be positive"))
		}
	}
	return cfg, errors.Join(errs...)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parse[T any](errs *[]error, key string, def T, fn func(string) (T, error)) T {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	out, err := fn(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: invalid value %q", key, v))
		return def
	}
	return out
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
