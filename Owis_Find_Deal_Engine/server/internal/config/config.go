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
	Port       string
	SerpAPIKey string

	// End-user JWTs from the auth provider (Firebase, Supabase, Clerk, ...).
	AuthJWKSURL   string
	AuthIssuer    string
	AuthAudience  string
	AuthPlanClaim string

	Plans       string // "free:20,pro:500" daily searches per plan
	DefaultPlan string
	RedisURL    string // empty: in-memory counters (single instance only)

	SearchCombined  bool // one SerpApi call per search instead of one per market
	MarketsFile     string
	CORSOrigins     []string
	RateLimitRPS    float64 // per user, burst smoothing
	RateLimitBurst  int
	SearchTimeout   time.Duration
	MaxConcurrency  int
	ShutdownTimeout time.Duration
	LogLevel        string
}

// Load reads the environment. Required: SERPAPI_KEY and the AUTH_* settings.
func Load() (Config, error) {
	var errs []error
	cfg := Config{
		Port:            env("PORT", "3002"),
		AuthJWKSURL:     os.Getenv("AUTH_JWKS_URL"),
		AuthIssuer:      os.Getenv("AUTH_ISSUER"),
		AuthAudience:    os.Getenv("AUTH_AUDIENCE"),
		AuthPlanClaim:   env("AUTH_PLAN_CLAIM", "plan"),
		Plans:           env("PLANS", "free:20,pro:500"),
		DefaultPlan:     env("DEFAULT_PLAN", "free"),
		RedisURL:        os.Getenv("REDIS_URL"),
		SerpAPIKey:      os.Getenv("SERPAPI_KEY"),
		MarketsFile:     os.Getenv("MARKETS_FILE"),
		CORSOrigins:     splitList(os.Getenv("CORS_ALLOWED_ORIGINS")),
		LogLevel:        env("LOG_LEVEL", "info"),
		SearchCombined:  parse(&errs, "SEARCH_COMBINED", true, strconv.ParseBool),
		RateLimitRPS:    parse(&errs, "RATE_LIMIT_RPS", 1.0, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) }),
		RateLimitBurst:  parse(&errs, "RATE_LIMIT_BURST", 5, strconv.Atoi),
		MaxConcurrency:  parse(&errs, "SEARCH_MAX_CONCURRENCY", 4, strconv.Atoi),
		SearchTimeout:   parse(&errs, "SEARCH_TIMEOUT", 15*time.Second, time.ParseDuration),
		ShutdownTimeout: parse(&errs, "SHUTDOWN_TIMEOUT", 10*time.Second, time.ParseDuration),
	}

	if cfg.AuthJWKSURL == "" || cfg.AuthIssuer == "" || cfg.AuthAudience == "" {
		errs = append(errs, errors.New("AUTH_JWKS_URL, AUTH_ISSUER and AUTH_AUDIENCE are required"))
	}
	if cfg.SerpAPIKey == "" {
		errs = append(errs, errors.New("SERPAPI_KEY is required"))
	}
	if cfg.RateLimitRPS <= 0 || cfg.RateLimitBurst <= 0 || cfg.MaxConcurrency <= 0 {
		errs = append(errs, errors.New("RATE_LIMIT_RPS, RATE_LIMIT_BURST and SEARCH_MAX_CONCURRENCY must be positive"))
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
