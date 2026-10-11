package config

import (
	"regexp"
	"slices"
	"strings"
)

// Settings come in two kinds:
//
//   - bootstrap: read from the environment only, needed before the database
//     is open or fixed for the life of the process (port, database URL,
//     encryption key, auth issuer, Redis, service addresses inside Docker);
//   - runtime: everything about how searches run (sources, shops, timeouts,
//     cache times, plans, rate limits, CORS). With a database they live
//     there and are applied without a restart.

// runtimeKeys are the runtime settings with a fixed name.
var runtimeKeys = []string{
	"SEARCH_PROVIDERS",
	"SEARXNG_ONE_QUERY",
	"SEARXNG_MAX_PAGES",
	"SERPAPI_KEY",
	"SERPAPI_ENGINE",
	"SERPAPI_MARKETS",
	"SERPAPI_TIMEOUT",
	"PROVIDER_ATTEMPT_TIMEOUT",
	"PROVIDER_FAILURE_THRESHOLD",
	"PROVIDER_COOLDOWN",
	"APIFY_TOKEN",
	"APIFY_MARKETS",
	"APIFY_MAX_ITEMS",
	"APIFY_TIMEOUT",
	"APIFY_MAX_CHARGE_USD",
	"APIFY_EMPTY_FALLBACK",
	"SEARCH_COMBINED",
	"RESULTS_PER_MARKET",
	"SEARCH_TIMEOUT",
	"SEARCH_MAX_CONCURRENCY",
	"CACHE_ENABLED",
	"CACHE_FRESH_TTL",
	"CACHE_PARTIAL_TTL",
	"CACHE_EMPTY_TTL",
	"CACHE_STALE_TTL",
	"CACHE_MIN_REFRESH",
	"CACHE_MAX_BACKGROUND_REFRESH",
	"CACHE_GENERATION",
	"PLANS",
	"DEFAULT_PLAN",
	"RATE_LIMIT_RPS",
	"RATE_LIMIT_BURST",
	"CORS_ALLOWED_ORIGINS",
	"MARKETS",
	"SERPAPI_SEARCH_COST_USD",
}

// apifyMarketKey matches the per-shop Apify settings, e.g. APIFY_TEMU_INPUT.
var apifyMarketKey = regexp.MustCompile(`^APIFY_[A-Z0-9][A-Z0-9_]*_(ACTOR|INPUT|CURRENCY|MAX_ITEMS|RUN_COST_USD)$`)

// secretKeys are runtime settings that are stored encrypted and never
// shown again once set.
var secretKeys = []string{"SERPAPI_KEY", "APIFY_TOKEN"}

// Runtime reports whether key is a runtime setting (kept in the database
// when there is one) rather than a bootstrap one (environment only).
func Runtime(key string) bool {
	return slices.Contains(runtimeKeys, key) || apifyMarketKey.MatchString(key)
}

// Secret reports whether the runtime setting key holds a secret.
func Secret(key string) bool { return slices.Contains(secretKeys, key) }

// RuntimeKeys returns the runtime settings with a fixed name (the per-shop
// APIFY_<SHOP>_* settings are not listed).
func RuntimeKeys() []string { return slices.Clone(runtimeKeys) }

// RuntimeFromEnv returns the runtime settings found in environ
// (os.Environ format). Empty values are skipped, except SERPAPI_MARKETS
// where empty means "none".
func RuntimeFromEnv(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !Runtime(k) || (v == "" && k != "SERPAPI_MARKETS") {
			continue
		}
		out[k] = v
	}
	return out
}

// Layered looks runtime keys up in runtime and every other key in env.
func Layered(runtime map[string]string, env Lookup) Lookup {
	return func(key string) (string, bool) {
		if Runtime(key) {
			v, ok := runtime[key]
			return v, ok
		}
		return env(key)
	}
}
