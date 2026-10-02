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
	Port            string
	APIKeys         string // "name:sha256hex,..." of client apps
	SerpAPIKey      string
	SearchCombined  bool // one SerpApi call per search instead of one per market
	MarketsFile     string
	CORSOrigins     []string
	RateLimitRPS    float64
	RateLimitBurst  int
	SearchTimeout   time.Duration
	MaxConcurrency  int
	ShutdownTimeout time.Duration
	LogLevel        string
}

// Load reads the environment. Required: API_KEYS, SERPAPI_KEY.
func Load() (Config, error) {
	var errs []error
	cfg := Config{
		Port:            env("PORT", "3002"),
		APIKeys:         os.Getenv("API_KEYS"),
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

	if cfg.APIKeys == "" {
		errs = append(errs, errors.New("API_KEYS is required (generate one with: go run ./cmd/keygen)"))
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
