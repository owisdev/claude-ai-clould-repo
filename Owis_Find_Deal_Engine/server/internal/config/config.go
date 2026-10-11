// Package config loads service settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Port string

	// Settings store (bootstrap, environment only). With DatabaseURL set,
	// the runtime settings (see Runtime) come from the database and can be
	// changed without a restart.
	DatabaseURL          string
	SettingsKey          string        // AES-256 key for secret settings (base64 or hex)
	SettingsPollInterval time.Duration // how often the database is checked for changes
	AdminEmail           string        // first admin, added once to an empty admin list

	// Search providers, tried in order until one succeeds.
	SearchProviders        []string // "searxng", "serpapi"
	SearXNGURL             string
	SearXNGOneQuery        bool // one OR-ed site: query instead of one per market
	SearXNGMaxPages        int
	SerpAPIKey             string
	SerpAPIEngine          string // "google_shopping" (prices) or "google" (web)
	SerpAPIBaseURL         string // empty = SerpApi; for tests / outbound proxies
	ProviderAttemptTimeout time.Duration
	SerpAPITimeout         time.Duration // Google Shopping is slow
	// SerpAPIMarkets use a dedicated SerpApi engine ("amazon", "ebay": the
	// Amazon Search API) instead of the web providers, which stay as the
	// fallback.
	SerpAPIMarkets []string

	// Apify scrapers for marketplaces without an official API (Temu, SHEIN).
	ApifyToken            string
	ApifyMarkets          []string          // e.g. temu,shein
	ApifyActors           map[string]string // market -> "owner~actor-name"
	ApifyInputs           map[string]string // market -> input JSON template with {{query}}
	ApifyCurrencies       map[string]string // market -> currency for prices given without one
	ApifyMarketMaxItems   map[string]int    // market -> results per run (default ApifyMaxItems)
	ApifyMaxItems         int
	ApifyTimeout          time.Duration
	ApifyMaxChargeUSD     float64 // max cost of one pay-per-event run; 0 = not sent
	ApifyBaseURL          string  // empty = Apify; for tests / outbound proxies
	ApifyEmptyFallback    bool    // empty scraper result -> try the web providers
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
func Load() (Config, error) { return LoadFrom(os.LookupEnv) }

// Lookup returns a setting's value and whether it is set (os.LookupEnv).
type Lookup func(key string) (string, bool)

// LoadFrom reads the settings from lookup: the environment, or the
// environment for bootstrap keys and the database for runtime keys
// (see Runtime).
func LoadFrom(lookup Lookup) (Config, error) {
	var errs []error
	getenv := func(key string) string {
		v, _ := lookup(key)
		return v
	}
	env := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	cfg := Config{
		Port:                      env("PORT", "3002"),
		DatabaseURL:               getenv("DATABASE_URL"),
		SettingsKey:               getenv("SETTINGS_ENCRYPTION_KEY"),
		SettingsPollInterval:      parse(getenv, &errs, "SETTINGS_POLL_INTERVAL", 10*time.Second, time.ParseDuration),
		AdminEmail:                strings.ToLower(strings.TrimSpace(getenv("ADMIN_EMAIL"))),
		AuthJWKSURL:               getenv("AUTH_JWKS_URL"),
		AuthIssuer:                getenv("AUTH_ISSUER"),
		AuthAudience:              getenv("AUTH_AUDIENCE"),
		AuthPlanClaim:             env("AUTH_PLAN_CLAIM", "plan"),
		Plans:                     env("PLANS", "free:20,pro:500"),
		DefaultPlan:               env("DEFAULT_PLAN", "free"),
		RedisURL:                  getenv("REDIS_URL"),
		SearchProviders:           splitList(env("SEARCH_PROVIDERS", "searxng,serpapi")),
		SearXNGURL:                getenv("SEARXNG_URL"),
		SearXNGOneQuery:           parse(getenv, &errs, "SEARXNG_ONE_QUERY", false, strconv.ParseBool),
		SearXNGMaxPages:           parse(getenv, &errs, "SEARXNG_MAX_PAGES", 2, strconv.Atoi),
		SerpAPIKey:                getenv("SERPAPI_KEY"),
		SerpAPIEngine:             env("SERPAPI_ENGINE", "google_shopping"),
		SerpAPIBaseURL:            getenv("SERPAPI_BASE_URL"),
		ApifyBaseURL:              getenv("APIFY_BASE_URL"),
		ApifyToken:                getenv("APIFY_TOKEN"),
		ApifyMarkets:              splitList(strings.ToLower(getenv("APIFY_MARKETS"))),
		ApifyActors:               map[string]string{},
		ApifyInputs:               map[string]string{},
		ApifyCurrencies:           map[string]string{},
		ApifyMarketMaxItems:       map[string]int{},
		ApifyMaxItems:             parse(getenv, &errs, "APIFY_MAX_ITEMS", 10, strconv.Atoi),
		ApifyEmptyFallback:        parse(getenv, &errs, "APIFY_EMPTY_FALLBACK", false, strconv.ParseBool),
		ApifyTimeout:              parse(getenv, &errs, "APIFY_TIMEOUT", 60*time.Second, time.ParseDuration),
		ApifyMaxChargeUSD:         parse(getenv, &errs, "APIFY_MAX_CHARGE_USD", 0.20, parseFloat),
		ProviderAttemptTimeout:    parse(getenv, &errs, "PROVIDER_ATTEMPT_TIMEOUT", 8*time.Second, time.ParseDuration),
		SerpAPITimeout:            parse(getenv, &errs, "SERPAPI_TIMEOUT", 25*time.Second, time.ParseDuration),
		ProviderFailThreshold:     parse(getenv, &errs, "PROVIDER_FAILURE_THRESHOLD", 3, strconv.Atoi),
		ProviderCooldown:          parse(getenv, &errs, "PROVIDER_COOLDOWN", time.Minute, time.ParseDuration),
		CacheEnabled:              parse(getenv, &errs, "CACHE_ENABLED", true, strconv.ParseBool),
		CacheFreshTTL:             parse(getenv, &errs, "CACHE_FRESH_TTL", 48*time.Hour, time.ParseDuration),
		CachePartialTTL:           parse(getenv, &errs, "CACHE_PARTIAL_TTL", 10*time.Minute, time.ParseDuration),
		CacheEmptyTTL:             parse(getenv, &errs, "CACHE_EMPTY_TTL", 30*time.Minute, time.ParseDuration),
		CacheStaleTTL:             parse(getenv, &errs, "CACHE_STALE_TTL", 72*time.Hour, time.ParseDuration),
		CacheMinRefresh:           parse(getenv, &errs, "CACHE_MIN_REFRESH", 10*time.Minute, time.ParseDuration),
		CacheMaxEntries:           parse(getenv, &errs, "CACHE_MAX_ENTRIES", 10000, strconv.Atoi),
		CacheMaxBackgroundRefresh: parse(getenv, &errs, "CACHE_MAX_BACKGROUND_REFRESH", 4, strconv.Atoi),
		MarketsFile:               getenv("MARKETS_FILE"),
		CORSOrigins:               splitList(getenv("CORS_ALLOWED_ORIGINS")),
		LogLevel:                  env("LOG_LEVEL", "info"),
		SearchCombined:            parse(getenv, &errs, "SEARCH_COMBINED", true, strconv.ParseBool),
		RateLimitRPS:              parse(getenv, &errs, "RATE_LIMIT_RPS", 1.0, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) }),
		RateLimitBurst:            parse(getenv, &errs, "RATE_LIMIT_BURST", 5, strconv.Atoi),
		MaxConcurrency:            parse(getenv, &errs, "SEARCH_MAX_CONCURRENCY", 4, strconv.Atoi),
		ResultsPerMarket:          parse(getenv, &errs, "RESULTS_PER_MARKET", 10, strconv.Atoi),
		SearchTimeout:             parse(getenv, &errs, "SEARCH_TIMEOUT", 15*time.Second, time.ParseDuration),
		ShutdownTimeout:           parse(getenv, &errs, "SHUTDOWN_TIMEOUT", 10*time.Second, time.ParseDuration),
	}

	if cfg.AuthJWKSURL == "" || cfg.AuthIssuer == "" || cfg.AuthAudience == "" {
		errs = append(errs, errors.New("AUTH_JWKS_URL, AUTH_ISSUER and AUTH_AUDIENCE are required"))
	}
	if cfg.DatabaseURL != "" && cfg.SettingsKey == "" {
		errs = append(errs, errors.New("SETTINGS_ENCRYPTION_KEY is required when DATABASE_URL is set"))
	}
	if cfg.SettingsPollInterval < time.Second {
		errs = append(errs, errors.New("SETTINGS_POLL_INTERVAL must be at least 1s"))
	}
	if cfg.AdminEmail != "" && !strings.Contains(cfg.AdminEmail, "@") {
		errs = append(errs, fmt.Errorf("ADMIN_EMAIL: %q is not an email address", cfg.AdminEmail))
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
		if cfg.ApifyMaxChargeUSD < 0 || cfg.ApifyMaxChargeUSD > 5 {
			errs = append(errs, errors.New("APIFY_MAX_CHARGE_USD must be between 0 and 5"))
		}
		for _, m := range cfg.ApifyMarkets {
			key := "APIFY_" + strings.ToUpper(m)
			cfg.ApifyActors[m] = getenv(key + "_ACTOR")
			cfg.ApifyInputs[m] = getenv(key + "_INPUT")
			cfg.ApifyCurrencies[m] = strings.TrimSpace(getenv(key + "_CURRENCY"))
			// Fewer results = lower cost for scrapers billed per result.
			cfg.ApifyMarketMaxItems[m] = parse(getenv, &errs, key+"_MAX_ITEMS", cfg.ApifyMaxItems, strconv.Atoi)
			if cfg.ApifyMarketMaxItems[m] <= 0 {
				errs = append(errs, fmt.Errorf("%s_MAX_ITEMS must be positive", key))
			}
			in := cfg.ApifyInputs[m]
			if cfg.ApifyActors[m] == "" || !(strings.Contains(in, "{{query}}") || strings.Contains(in, "{{query_url}}") || strings.Contains(in, "{{query_path}}") || strings.Contains(in, "{{query_slug}}")) {
				errs = append(errs, fmt.Errorf("%s_ACTOR and %s_INPUT (JSON containing {{query}}, {{query_url}}, {{query_path}} or {{query_slug}}) are required for %s", key, key, m))
			}
		}
	}
	// Amazon and eBay go to SerpApi's own engines by default once a key is
	// set (direct product links and prices instead of Google Shopping).
	defaultSerpMarkets := ""
	if cfg.SerpAPIKey != "" {
		defaultSerpMarkets = "amazon,ebay"
	}
	// Unlike other settings, an empty SERPAPI_MARKETS= means "none".
	serpMarkets, set := lookup("SERPAPI_MARKETS")
	if !set {
		serpMarkets = defaultSerpMarkets
	}
	cfg.SerpAPIMarkets = splitList(strings.ToLower(serpMarkets))
	for _, m := range cfg.SerpAPIMarkets {
		switch {
		case m != "amazon" && m != "ebay":
			errs = append(errs, fmt.Errorf("SERPAPI_MARKETS: %q has no dedicated SerpApi engine (supported: amazon, ebay)", m))
		case cfg.SerpAPIKey == "":
			errs = append(errs, errors.New("SERPAPI_KEY is required when SERPAPI_MARKETS is set"))
		case slices.Contains(cfg.ApifyMarkets, m):
			errs = append(errs, fmt.Errorf("%s is in both SERPAPI_MARKETS and APIFY_MARKETS", m))
		}
	}

	// Give the whole search enough time for every provider in the chain to
	// get its full attempt: a dedicated source (Apify run or SerpApi
	// engine), then each web provider.
	// Shops are searched in parallel, so the longest path counts: the web
	// chain, or a dedicated source plus one web attempt after it fails.
	var chain time.Duration
	for _, p := range cfg.SearchProviders {
		if p == "serpapi" {
			chain += cfg.SerpAPITimeout
		} else {
			chain += cfg.ProviderAttemptTimeout
		}
	}
	var dedicated time.Duration
	if len(cfg.ApifyMarkets) > 0 {
		dedicated = cfg.ApifyTimeout
	}
	if len(cfg.SerpAPIMarkets) > 0 {
		dedicated = max(dedicated, cfg.SerpAPITimeout)
	}
	if dedicated > 0 {
		dedicated += cfg.ProviderAttemptTimeout
	}
	need := 2*time.Second + max(chain, dedicated)
	cfg.SearchTimeout = max(cfg.SearchTimeout, need)
	if cfg.ProviderAttemptTimeout <= 0 || cfg.SerpAPITimeout <= 0 || cfg.SearchTimeout <= 0 {
		errs = append(errs, errors.New("PROVIDER_ATTEMPT_TIMEOUT, SERPAPI_TIMEOUT and SEARCH_TIMEOUT must be positive"))
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

func parse[T any](getenv func(string) string, errs *[]error, key string, def T, fn func(string) (T, error)) T {
	v := getenv(key)
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

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
