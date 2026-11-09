// Package config loads service settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/markets"
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
	// Admin API login (bootstrap): two-factor sign-in required, and how old
	// the sign-in may be before the admin has to sign in again.
	// Two-factor is off by default for now (owner, 2026-10-11); turn it on
	// (ADMIN_REQUIRE_2FA=true) before production.
	AdminRequire2FA    bool
	AdminSessionMaxAge time.Duration
	// AdminRequireVerifiedEmail: an admin's email must be verified before
	// the first sign-in links it to the admin list. Off by default during
	// development (owner, 2026-10-11, test accounts with made-up emails);
	// turn it on (true) before production.
	AdminRequireVerifiedEmail bool
	// WebhookAllowPrivate lets the webhooks of the asynchronous search use
	// plain http and internal addresses (localhost, 10.x, 192.168.x,
	// host.docker.internal). Development only: off, a client cannot make
	// the server call Redis, Postgres or a cloud metadata service.
	WebhookAllowPrivate bool
	// Admin web app sign-in (bootstrap): the Firebase project's Web API key
	// (public) and auth domain (default <project>.firebaseapp.com).
	AdminFirebaseAPIKey     string
	AdminFirebaseAuthDomain string

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
	// SerpAPIMarkets are the shops whose source is serpapi: SerpApi's own
	// engine for that shop ("amazon", "ebay"). Worked out from Shops.
	SerpAPIMarkets []string

	// Shops holds the rules of every shop in the catalog (SHOP_<SHOP>_*
	// settings, step 14b): its source, time limit and group.
	Shops map[string]ShopRule

	// Apify scrapers for marketplaces without an official API (Temu, SHEIN).
	ApifyToken          string
	ApifyMarkets        []string           // shops whose source is apify (worked out from Shops)
	ApifyActors         map[string]string  // market -> "owner~actor-name"
	ApifyInputs         map[string]string  // market -> input JSON template with {{query}}
	ApifyCurrencies     map[string]string  // market -> currency for prices given without one
	ApifyMarketMaxItems map[string]int     // market -> results per run (default ApifyMaxItems)
	ApifyRunCostUSD     map[string]float64 // market -> estimated cost of one run (0 = unknown)
	ApifyMemoryMB       map[string]int     // market -> run memory in MB (0 = the Actor's default)
	// ApifyStopMargin: Apify is asked to stop a run this long before the
	// shop's time limit, so what it found still comes back (default 5s).
	ApifyStopMargin       time.Duration
	ApifyMaxItems         int
	ApifyTimeout          time.Duration
	ApifyMaxChargeUSD     float64 // max cost of one pay-per-event run; 0 = not sent
	ApifyBaseURL          string  // empty = Apify; for tests / outbound proxies
	ApifyEmptyFallback    bool    // empty scraper result -> try the web providers
	ProviderFailThreshold int
	ProviderCooldown      time.Duration
	// SerpAPISearchCostUSD is the estimated cost of one SerpApi search
	// (0 = free plan / unknown), shown by the admin test search.
	SerpAPISearchCostUSD float64

	// Monthly budget for the estimated cost of sources (0 = none; default
	// $5 = the Apify free credit, owner decision D5) and the share of it
	// that raises an alert (usage dashboard, step 9).
	BudgetMonthlyUSD   float64
	BudgetAlertPercent int

	// End-user JWTs from the auth provider (Firebase, Supabase, Clerk, ...).
	AuthJWKSURL   string
	AuthIssuer    string
	AuthAudience  string
	AuthPlanClaim string
	// AuthAllowedSignIn: the sign-in methods accepted for app users
	// (Firebase's firebase.sign_in_provider: "google.com", "apple.com",
	// "password", ...). Tokens made another way, e.g. Firebase anonymous
	// accounts, are refused (401). Empty = any method (AUTH_ALLOWED_SIGN_IN=*,
	// needed for providers other than Firebase). Owner, 2026-11-06 (E1).
	AuthAllowedSignIn []string

	// TrustedProxies: connections from these addresses may name the
	// caller's own address in the CF-Connecting-IP header (the Cloudflare
	// Tunnel's cloudflared). Empty = the header is never believed.
	TrustedProxies []netip.Prefix

	Plans       string // "free:20,pro:500" daily searches per plan
	DefaultPlan string
	RedisURL    string // empty: in-memory counters (single instance only)

	// Search cache (stale-while-revalidate).
	CacheEnabled    bool
	CacheFreshTTL   time.Duration
	CachePartialTTL time.Duration
	CacheEmptyTTL   time.Duration
	CacheStaleTTL   time.Duration
	// CacheLastKnownTTL: how long a shop's latest results are kept to be
	// shown, dated, when a live search of that shop fails (default 7 days).
	CacheLastKnownTTL         time.Duration
	CacheMinRefresh           time.Duration
	CacheMaxEntries           int
	CacheMaxBackgroundRefresh int
	// CacheGeneration is part of every cache key: raising it (admin tool
	// "bump results version") makes every cached answer a miss.
	CacheGeneration int

	SearchCombined   bool // one provider call per search instead of one per market
	ResultsPerMarket int
	MarketsFile      string
	// Catalog is the countries and shops: the MARKETS setting (JSON, edited
	// in the admin panel), else MARKETS_FILE, else the built-in markets.json.
	Catalog        *markets.Catalog
	MarketsSource  string // "setting", "file" or "built-in"
	MarketsJSON    []byte // the catalog as read
	CORSOrigins    []string
	RateLimitRPS   float64 // per user, burst smoothing
	RateLimitBurst int
	SearchTimeout  time.Duration
	MaxConcurrency int
	// Queue of the shops' live searches (step 14b part 3): runs at once
	// per source and how long a search may wait for a free run.
	QueueApifyMaxRuns   int
	QueueSerpAPIMaxRuns int
	QueueWebMaxRuns     int
	QueueMaxWait        time.Duration
	ShutdownTimeout     time.Duration
	LogLevel            string
}

// Shop sources (SHOP_<SHOP>_SOURCE).
const (
	SourceSerpAPI = "serpapi" // SerpApi's engine for the shop (amazon, ebay)
	SourceApify   = "apify"   // the shop's Apify scraper (APIFY_<SHOP>_*)
	SourceWeb     = "web"     // the web providers (SEARCH_PROVIDERS)
	SourceOff     = "off"     // not searched
)

// Shop groups (SHOP_<SHOP>_GROUP): the asynchronous search answers first
// when the fast shops are done (step 14b part 3).
const (
	GroupFast = "fast"
	GroupSlow = "slow"
)

// ShopRule is how one shop is searched.
type ShopRule struct {
	Source  string
	Timeout time.Duration // how long one search of this shop may take
	Group   string
}

type shopDefault struct {
	timeout time.Duration
	group   string
}

// shopDefaults are the time limits and groups agreed with the owner
// (2026-11-09) for the known shops; SHOP_<SHOP>_TIMEOUT / _GROUP change
// them. A shop searched through the web providers gets the time of the
// whole web chain instead.
var shopDefaults = map[string]shopDefault{
	"amazon":     {15 * time.Second, GroupFast},
	"ebay":       {15 * time.Second, GroupFast},
	"aliexpress": {60 * time.Second, GroupSlow},
	"temu":       {45 * time.Second, GroupSlow},
	"shein":      {30 * time.Second, GroupSlow},
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
		AdminRequire2FA:           parse(getenv, &errs, "ADMIN_REQUIRE_2FA", false, strconv.ParseBool),
		AdminSessionMaxAge:        parse(getenv, &errs, "ADMIN_SESSION_MAX_AGE", 12*time.Hour, time.ParseDuration),
		AdminRequireVerifiedEmail: parse(getenv, &errs, "ADMIN_REQUIRE_VERIFIED_EMAIL", false, strconv.ParseBool),
		WebhookAllowPrivate:       parse(getenv, &errs, "WEBHOOK_ALLOW_PRIVATE", false, strconv.ParseBool),
		AdminFirebaseAPIKey:       strings.TrimSpace(getenv("ADMIN_FIREBASE_API_KEY")),
		AdminFirebaseAuthDomain:   strings.TrimSpace(getenv("ADMIN_FIREBASE_AUTH_DOMAIN")),
		AuthJWKSURL:               getenv("AUTH_JWKS_URL"),
		AuthIssuer:                getenv("AUTH_ISSUER"),
		AuthAudience:              getenv("AUTH_AUDIENCE"),
		AuthPlanClaim:             env("AUTH_PLAN_CLAIM", "plan"),
		AuthAllowedSignIn:         parseSignIn(env("AUTH_ALLOWED_SIGN_IN", "google.com,apple.com,password")),
		TrustedProxies:            parse(getenv, &errs, "TRUSTED_PROXIES", []netip.Prefix(nil), parsePrefixes),
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
		ApifyRunCostUSD:           map[string]float64{},
		ApifyMemoryMB:             map[string]int{},
		ApifyStopMargin:           parse(getenv, &errs, "APIFY_STOP_MARGIN", 5*time.Second, time.ParseDuration),
		QueueApifyMaxRuns:         parse(getenv, &errs, "QUEUE_APIFY_MAX_RUNS", 4, strconv.Atoi),
		QueueSerpAPIMaxRuns:       parse(getenv, &errs, "QUEUE_SERPAPI_MAX_RUNS", 10, strconv.Atoi),
		QueueWebMaxRuns:           parse(getenv, &errs, "QUEUE_WEB_MAX_RUNS", 10, strconv.Atoi),
		QueueMaxWait:              parse(getenv, &errs, "QUEUE_MAX_WAIT", 60*time.Second, time.ParseDuration),
		SerpAPISearchCostUSD:      parse(getenv, &errs, "SERPAPI_SEARCH_COST_USD", 0, parseFloat),
		CacheGeneration:           parse(getenv, &errs, "CACHE_GENERATION", 0, strconv.Atoi),
		BudgetMonthlyUSD:          parse(getenv, &errs, "BUDGET_MONTHLY_USD", 5, parseFloat),
		BudgetAlertPercent:        parse(getenv, &errs, "BUDGET_ALERT_PERCENT", 80, strconv.Atoi),
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
		CacheLastKnownTTL:         parse(getenv, &errs, "CACHE_LAST_KNOWN_TTL", 7*24*time.Hour, time.ParseDuration),
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
	if cfg.AdminFirebaseAuthDomain == "" && cfg.AuthAudience != "" {
		cfg.AdminFirebaseAuthDomain = cfg.AuthAudience + ".firebaseapp.com"
	}
	if cfg.AdminSessionMaxAge < 5*time.Minute {
		errs = append(errs, errors.New("ADMIN_SESSION_MAX_AGE must be at least 5m"))
	}
	if cfg.BudgetMonthlyUSD < 0 || cfg.BudgetAlertPercent < 1 || cfg.BudgetAlertPercent > 100 {
		errs = append(errs, errors.New("BUDGET_MONTHLY_USD must not be negative and BUDGET_ALERT_PERCENT must be 1-100"))
	}
	if cfg.SerpAPISearchCostUSD < 0 || cfg.CacheGeneration < 0 {
		errs = append(errs, errors.New("SERPAPI_SEARCH_COST_USD and CACHE_GENERATION must not be negative"))
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
	// The old shop lists still say which shops use a dedicated source when
	// no SHOP_<SHOP>_SOURCE is set: APIFY_MARKETS and SERPAPI_MARKETS.
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
	serpList := splitList(strings.ToLower(serpMarkets))
	apifyList := cfg.ApifyMarkets
	for _, m := range serpList {
		if slices.Contains(apifyList, m) {
			errs = append(errs, fmt.Errorf("%s is in both SERPAPI_MARKETS and APIFY_MARKETS", m))
		}
	}

	var err error
	if cfg.Catalog, cfg.MarketsJSON, cfg.MarketsSource, err = loadCatalog(getenv("MARKETS"), cfg.MarketsFile); err != nil {
		errs = append(errs, err)
	} else {
		for _, m := range slices.Concat(apifyList, serpList) {
			if !cfg.Catalog.HasMarket(m) {
				errs = append(errs, fmt.Errorf("%s is in APIFY_MARKETS or SERPAPI_MARKETS but not a shop in MARKETS", m))
			}
		}
	}

	// The web providers are tried one after another, so a shop searched
	// through them needs the time of the whole chain.
	var chain time.Duration
	for _, p := range cfg.SearchProviders {
		if p == "serpapi" {
			chain += cfg.SerpAPITimeout
		} else {
			chain += cfg.ProviderAttemptTimeout
		}
	}

	// Rules per shop (step 14b): source, time limit, group.
	cfg.Shops = map[string]ShopRule{}
	cfg.ApifyMarkets, cfg.SerpAPIMarkets = nil, nil
	var shops []string
	if cfg.Catalog != nil {
		shops = cfg.Catalog.Markets()
	}
	for _, m := range shops {
		key := "SHOP_" + strings.ToUpper(m)
		src := strings.ToLower(strings.TrimSpace(getenv(key + "_SOURCE")))
		if src == "" {
			switch {
			case slices.Contains(apifyList, m):
				src = SourceApify
			case slices.Contains(serpList, m):
				src = SourceSerpAPI
			default:
				src = SourceWeb
			}
		}
		def := shopDefaults[m]
		switch src {
		case SourceSerpAPI:
			switch {
			case m != "amazon" && m != "ebay":
				errs = append(errs, fmt.Errorf("%s_SOURCE: SerpApi has a dedicated engine only for amazon and ebay", key))
			case cfg.SerpAPIKey == "":
				errs = append(errs, fmt.Errorf("SERPAPI_KEY is required when %s's source is serpapi", m))
			}
			if def.timeout == 0 {
				def = shopDefault{timeout: 15 * time.Second, group: GroupFast}
			}
			cfg.SerpAPIMarkets = append(cfg.SerpAPIMarkets, m)
		case SourceApify:
			if def.timeout == 0 {
				def.timeout = cfg.ApifyTimeout
			}
			cfg.ApifyMarkets = append(cfg.ApifyMarkets, m)
		case SourceWeb:
			def.timeout = chain
		case SourceOff:
		default:
			errs = append(errs, fmt.Errorf("%s_SOURCE: %q must be serpapi, apify, web or off", key, src))
		}
		if def.group == "" {
			def.group = GroupSlow
		}
		rule := ShopRule{
			Source:  src,
			Timeout: parse(getenv, &errs, key+"_TIMEOUT", def.timeout, time.ParseDuration),
			Group:   strings.ToLower(strings.TrimSpace(env(key+"_GROUP", def.group))),
		}
		if src != SourceOff && (rule.Timeout < time.Second || rule.Timeout > 300*time.Second) {
			errs = append(errs, fmt.Errorf("%s_TIMEOUT must be between 1s and 300s", key))
		}
		if rule.Group != GroupFast && rule.Group != GroupSlow {
			errs = append(errs, fmt.Errorf("%s_GROUP: %q must be fast or slow", key, rule.Group))
		}
		cfg.Shops[m] = rule
	}

	if len(cfg.ApifyMarkets) > 0 {
		if cfg.ApifyToken == "" {
			errs = append(errs, errors.New("APIFY_TOKEN is required when a shop's source is apify"))
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
			cfg.ApifyRunCostUSD[m] = parse(getenv, &errs, key+"_RUN_COST_USD", 0, parseFloat)
			if cfg.ApifyRunCostUSD[m] < 0 {
				errs = append(errs, fmt.Errorf("%s_RUN_COST_USD must not be negative", key))
			}
			cfg.ApifyMemoryMB[m] = parse(getenv, &errs, key+"_MEMORY_MB", 0, strconv.Atoi)
			if mb := cfg.ApifyMemoryMB[m]; mb != 0 && (mb < 128 || mb > 32768 || mb&(mb-1) != 0) {
				errs = append(errs, fmt.Errorf("%s_MEMORY_MB must be a power of 2 between 128 and 32768 (e.g. 2048, 4096), or empty", key))
			}
			in := cfg.ApifyInputs[m]
			if cfg.ApifyActors[m] == "" || !(strings.Contains(in, "{{query}}") || strings.Contains(in, "{{query_url}}") || strings.Contains(in, "{{query_path}}") || strings.Contains(in, "{{query_slug}}")) {
				errs = append(errs, fmt.Errorf("%s_ACTOR and %s_INPUT (JSON containing {{query}}, {{query_url}}, {{query_path}} or {{query_slug}}) are required for %s", key, key, m))
			}
		}
	}

	// A waiting search ends when the slowest shop's time is up; there is no
	// backup source to wait for any more (the last known results are read
	// from the cache).
	var longest time.Duration
	for _, r := range cfg.Shops {
		if r.Source != SourceOff {
			longest = max(longest, r.Timeout)
		}
	}
	cfg.SearchTimeout = max(cfg.SearchTimeout, longest+2*time.Second)
	if cfg.ProviderAttemptTimeout <= 0 || cfg.SerpAPITimeout <= 0 || cfg.SearchTimeout <= 0 {
		errs = append(errs, errors.New("PROVIDER_ATTEMPT_TIMEOUT, SERPAPI_TIMEOUT and SEARCH_TIMEOUT must be positive"))
	}
	if cfg.QueueApifyMaxRuns <= 0 || cfg.QueueSerpAPIMaxRuns <= 0 || cfg.QueueWebMaxRuns <= 0 {
		errs = append(errs, errors.New("QUEUE_APIFY_MAX_RUNS, QUEUE_SERPAPI_MAX_RUNS and QUEUE_WEB_MAX_RUNS must be positive"))
	}
	if cfg.QueueMaxWait < time.Second || cfg.QueueMaxWait > 10*time.Minute {
		errs = append(errs, errors.New("QUEUE_MAX_WAIT must be between 1s and 10m"))
	}
	if cfg.ApifyStopMargin < 0 || cfg.ApifyStopMargin > time.Minute {
		errs = append(errs, errors.New("APIFY_STOP_MARGIN must be between 0s and 1m"))
	}
	if cfg.RateLimitRPS <= 0 || cfg.RateLimitBurst <= 0 || cfg.MaxConcurrency <= 0 || cfg.ProviderFailThreshold <= 0 || cfg.ResultsPerMarket <= 0 {
		errs = append(errs, errors.New("RATE_LIMIT_RPS, RATE_LIMIT_BURST, SEARCH_MAX_CONCURRENCY, PROVIDER_FAILURE_THRESHOLD and RESULTS_PER_MARKET must be positive"))
	}
	if cfg.CacheEnabled {
		if cfg.CacheFreshTTL <= 0 || cfg.CachePartialTTL <= 0 || cfg.CacheEmptyTTL <= 0 || cfg.CacheStaleTTL <= 0 {
			errs = append(errs, errors.New("CACHE_*_TTL must be positive"))
		} else if cfg.CacheFreshTTL > cfg.CacheStaleTTL {
			errs = append(errs, errors.New("CACHE_FRESH_TTL must not be longer than CACHE_STALE_TTL"))
		} else if cfg.CacheLastKnownTTL < cfg.CacheStaleTTL {
			errs = append(errs, errors.New("CACHE_LAST_KNOWN_TTL must not be shorter than CACHE_STALE_TTL"))
		}
		if cfg.CacheMaxEntries <= 0 || cfg.CacheMaxBackgroundRefresh <= 0 {
			errs = append(errs, errors.New("CACHE_MAX_ENTRIES and CACHE_MAX_BACKGROUND_REFRESH must be positive"))
		}
	}
	return cfg, errors.Join(errs...)
}

// loadCatalog parses the countries and shops from the MARKETS setting,
// else from file, else the built-in catalog.
func loadCatalog(setting, file string) (*markets.Catalog, []byte, string, error) {
	data, source := []byte(setting), "setting"
	switch {
	case strings.TrimSpace(setting) != "":
	case file != "":
		var err error
		if data, err = os.ReadFile(file); err != nil {
			return nil, nil, "", fmt.Errorf("read markets file: %w", err)
		}
		source = "file"
	default:
		data, source = markets.Default(), "built-in"
	}
	c, err := markets.Parse(data)
	if err != nil {
		if source == "setting" {
			err = fmt.Errorf("MARKETS: %w", err)
		}
		return nil, nil, "", err
	}
	return c, data, source, nil
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

// parseSignIn reads AUTH_ALLOWED_SIGN_IN; "*" = any method (nil).
func parseSignIn(s string) []string {
	list := splitList(strings.ToLower(s))
	if slices.Contains(list, "*") {
		return nil
	}
	return list
}

// parsePrefixes reads a list of addresses ("10.203.20.10") and ranges
// ("10.203.20.0/28").
func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range splitList(s) {
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, err
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// ApifyRunTimeout is the run time limit sent to Apify for a shop: its
// time limit less ApifyStopMargin (at least 1s, at most APIFY_TIMEOUT's
// 300 s ceiling).
func (c Config) ApifyRunTimeout(market string) time.Duration {
	t := c.Shops[market].Timeout - c.ApifyStopMargin
	return min(max(t, time.Second), 300*time.Second)
}

// LongestShopTimeout is the time limit of the slowest shop that is on.
func (c Config) LongestShopTimeout() time.Duration {
	var longest time.Duration
	for _, r := range c.Shops {
		if r.Source != SourceOff {
			longest = max(longest, r.Timeout)
		}
	}
	return longest
}
