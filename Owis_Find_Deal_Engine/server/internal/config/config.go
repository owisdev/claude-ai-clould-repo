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
	SerpAPIKey             string
	ProviderAttemptTimeout time.Duration
	ProviderFailThreshold  int
	ProviderCooldown       time.Duration

	// End-user JWTs from the auth provider (Firebase, Supabase, Clerk, ...).
	AuthJWKSURL   string
	AuthIssuer    string
	AuthAudience  string
	AuthPlanClaim string

	Plans       string // "free:20,pro:500" daily searches per plan
	DefaultPlan string
	RedisURL    string // empty: in-memory counters (single instance only)

	SearchCombined  bool // one provider call per search instead of one per market
	MarketsFile     string
	CORSOrigins     []string
	RateLimitRPS    float64 // per user, burst smoothing
	RateLimitBurst  int
	SearchTimeout   time.Duration
	MaxConcurrency  int
	ShutdownTimeout time.Duration
	LogLevel        string
}

// Load reads the environment. Required: the AUTH_* settings and the
// settings of every provider in SEARCH_PROVIDERS.
func Load() (Config, error) {
	var errs []error
	cfg := Config{
		Port:                   env("PORT", "3002"),
		AuthJWKSURL:            os.Getenv("AUTH_JWKS_URL"),
		AuthIssuer:             os.Getenv("AUTH_ISSUER"),
		AuthAudience:           os.Getenv("AUTH_AUDIENCE"),
		AuthPlanClaim:          env("AUTH_PLAN_CLAIM", "plan"),
		Plans:                  env("PLANS", "free:20,pro:500"),
		DefaultPlan:            env("DEFAULT_PLAN", "free"),
		RedisURL:               os.Getenv("REDIS_URL"),
		SearchProviders:        splitList(env("SEARCH_PROVIDERS", "searxng,serpapi")),
		SearXNGURL:             os.Getenv("SEARXNG_URL"),
		SerpAPIKey:             os.Getenv("SERPAPI_KEY"),
		ProviderAttemptTimeout: parse(&errs, "PROVIDER_ATTEMPT_TIMEOUT", 8*time.Second, time.ParseDuration),
		ProviderFailThreshold:  parse(&errs, "PROVIDER_FAILURE_THRESHOLD", 3, strconv.Atoi),
		ProviderCooldown:       parse(&errs, "PROVIDER_COOLDOWN", time.Minute, time.ParseDuration),
		MarketsFile:            os.Getenv("MARKETS_FILE"),
		CORSOrigins:            splitList(os.Getenv("CORS_ALLOWED_ORIGINS")),
		LogLevel:               env("LOG_LEVEL", "info"),
		SearchCombined:         parse(&errs, "SEARCH_COMBINED", true, strconv.ParseBool),
		RateLimitRPS:           parse(&errs, "RATE_LIMIT_RPS", 1.0, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) }),
		RateLimitBurst:         parse(&errs, "RATE_LIMIT_BURST", 5, strconv.Atoi),
		MaxConcurrency:         parse(&errs, "SEARCH_MAX_CONCURRENCY", 4, strconv.Atoi),
		SearchTimeout:          parse(&errs, "SEARCH_TIMEOUT", 15*time.Second, time.ParseDuration),
		ShutdownTimeout:        parse(&errs, "SHUTDOWN_TIMEOUT", 10*time.Second, time.ParseDuration),
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
	if cfg.RateLimitRPS <= 0 || cfg.RateLimitBurst <= 0 || cfg.MaxConcurrency <= 0 || cfg.ProviderFailThreshold <= 0 {
		errs = append(errs, errors.New("RATE_LIMIT_RPS, RATE_LIMIT_BURST, SEARCH_MAX_CONCURRENCY and PROVIDER_FAILURE_THRESHOLD must be positive"))
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
