// Command owis_find_deal_engine runs the product search API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"owis_find_deal_engine/internal/api"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/cache"
	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/metering"
	"owis_find_deal_engine/internal/providers/apify"
	"owis_find_deal_engine/internal/providers/searxng"
	"owis_find_deal_engine/internal/providers/serpapi"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"

	"github.com/redis/go-redis/v9"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	// "healthcheck" lets the container check the server without a shell or
	// curl in the image (distroless).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// healthcheck returns 0 when the local server answers /api/v1/health.
func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3002"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/api/v1/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return fmt.Errorf("LOG_LEVEL: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	catalog, err := markets.Load(cfg.MarketsFile)
	if err != nil {
		return err
	}
	web, err := buildWebProvider(cfg, log)
	if err != nil {
		return err
	}
	log.Info("search providers", "chain", web.Name())

	providers := map[string]search.Provider{"web": web}
	catalog, err = addApifyProviders(cfg, catalog, providers, web, log)
	if err != nil {
		return err
	}

	searcher := search.NewService(catalog,
		providers,
		search.Options{Timeout: cfg.SearchTimeout, MaxConcurrency: cfg.MaxConcurrency},
		log)

	// ctx is cancelled on SIGINT/SIGTERM and stops background goroutines.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	limiter := ratelimit.New(cfg.RateLimitRPS, cfg.RateLimitBurst, 10*time.Minute)
	go limiter.Cleanup(ctx, time.Minute)

	// Auth: download the provider's public keys once, then keep them fresh.
	jwks := auth.NewJWKS(cfg.AuthJWKSURL, log)
	if err := jwks.Refresh(ctx); err != nil {
		return fmt.Errorf("load auth keys: %w", err)
	}
	go jwks.Run(ctx, time.Hour)
	verifier, err := auth.NewVerifier(jwks, auth.VerifierConfig{
		Issuer:    cfg.AuthIssuer,
		Audience:  cfg.AuthAudience,
		PlanClaim: cfg.AuthPlanClaim,
	})
	if err != nil {
		return err
	}

	// Redis (when configured) holds usage counters and the search cache so
	// several instances share them; otherwise both live in memory.
	plans, err := usage.ParsePlans(cfg.Plans, cfg.DefaultPlan)
	if err != nil {
		return err
	}
	var (
		usageStore usage.Store
		cacheStore cache.Store
	)
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("REDIS_URL: %w", err)
		}
		rdb := redis.NewClient(opts)
		defer rdb.Close()
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = rdb.Ping(pingCtx).Err()
		cancel()
		if err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		usageStore = usage.NewRedisStore(rdb)
		cacheStore = cache.NewRedisStore(rdb)
		log.Info("usage counters and search cache in redis")
	} else {
		mem := usage.NewMemoryStore()
		go mem.Cleanup(ctx, 10*time.Minute)
		usageStore = mem
		cacheStore = cache.NewMemoryStore(cfg.CacheMaxEntries)
		log.Warn("REDIS_URL not set: usage counters and search cache in memory, reset on restart")
	}

	cacheOrLive := metering.WithoutCache(searcher)
	var searchCache *cache.Cache
	if cfg.CacheEnabled {
		searchCache = cache.New(searcher, cacheStore, catalog, cache.Options{
			FreshTTL:             cfg.CacheFreshTTL,
			PartialTTL:           cfg.CachePartialTTL,
			EmptyTTL:             cfg.CacheEmptyTTL,
			StaleTTL:             cfg.CacheStaleTTL,
			MinRefresh:           cfg.CacheMinRefresh,
			FetchTimeout:         cfg.SearchTimeout + 5*time.Second,
			MaxBackgroundRefresh: cfg.CacheMaxBackgroundRefresh,
		}, log)
		cacheOrLive = searchCache
		log.Info("search cache enabled", "fresh", cfg.CacheFreshTTL.String(), "stale", cfg.CacheStaleTTL.String())
	}

	srv := &http.Server{
		Addr: net.JoinHostPort("", cfg.Port),
		Handler: api.NewRouter(api.Deps{
			Catalog:     catalog,
			Searcher:    metering.New(cacheOrLive, usage.NewQuota(usageStore, plans), log),
			Verifier:    verifier,
			Limiter:     limiter,
			CORSOrigins: cfg.CORSOrigins,
			Log:         log,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.SearchTimeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server started", "addr", srv.Addr, "version", version, "countries", len(catalog.Countries()))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	log.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if searchCache != nil {
		// Let running background refreshes finish (they write to the cache).
		if err := searchCache.Close(shutdownCtx); err != nil {
			log.Warn("cache refreshes still running at shutdown", "err", err)
		}
		hits, stale, misses := searchCache.Stats()
		log.Info("search cache stats", "hits", hits, "stale", stale, "misses", misses)
	}
	log.Info("server stopped cleanly")
	return nil
}

// buildWebProvider creates the configured providers in SEARCH_PROVIDERS
// order and chains them: the first that succeeds answers the search.
func buildWebProvider(cfg config.Config, log *slog.Logger) (search.Provider, error) {
	providers := make([]search.Provider, 0, len(cfg.SearchProviders))
	for _, name := range cfg.SearchProviders {
		var (
			p   search.Provider
			err error
		)
		switch name {
		case "searxng":
			p, err = searxng.New(searxng.Config{BaseURL: cfg.SearXNGURL, Combined: cfg.SearchCombined,
				OneQuery: cfg.SearXNGOneQuery})
		case "serpapi":
			p, err = serpapi.New(serpapi.Config{APIKey: cfg.SerpAPIKey, Engine: cfg.SerpAPIEngine,
				BaseURL: cfg.SerpAPIBaseURL, Combined: cfg.SearchCombined})
		default:
			err = fmt.Errorf("unknown search provider %q", name)
		}
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return search.NewFallback(providers, search.FallbackOptions{
		AttemptTimeout:   cfg.ProviderAttemptTimeout,
		FailureThreshold: cfg.ProviderFailThreshold,
		Cooldown:         cfg.ProviderCooldown,
	}, log)
}

// addApifyProviders routes each marketplace in APIFY_MARKETS to its Apify
// scraper, with the web providers as fallback when the scraper fails or is
// too slow. Other marketplaces keep using the web providers.
func addApifyProviders(cfg config.Config, catalog *markets.Catalog, providers map[string]search.Provider,
	web search.Provider, log *slog.Logger) (*markets.Catalog, error) {
	if len(cfg.ApifyMarkets) == 0 {
		return catalog, nil
	}
	overrides := make(map[string]string, len(cfg.ApifyMarkets))
	for _, m := range cfg.ApifyMarkets {
		scraper, err := apify.New(apify.Config{
			Token:         cfg.ApifyToken,
			Actor:         cfg.ApifyActors[m],
			InputTemplate: cfg.ApifyInputs[m],
			MaxItems:      cfg.ApifyMaxItems,
			Timeout:       cfg.ApifyTimeout,
			Combined:      cfg.SearchCombined,
			BaseURL:       cfg.ApifyBaseURL,
		})
		if err != nil {
			return nil, fmt.Errorf("APIFY_%s: %w", strings.ToUpper(m), err)
		}
		chain, err := search.NewFallback([]search.Provider{scraper, web}, search.FallbackOptions{
			AttemptTimeout:   cfg.ApifyTimeout,
			FailureThreshold: cfg.ProviderFailThreshold,
			Cooldown:         cfg.ProviderCooldown,
		}, log)
		if err != nil {
			return nil, err
		}
		name := "apify-" + m
		providers[name] = chain
		overrides[m] = name
		log.Info("apify scraper enabled", "market", m, "actor", cfg.ApifyActors[m],
			"timeout", cfg.ApifyTimeout.String(), "search_timeout", cfg.SearchTimeout.String())
	}
	return catalog.WithProviders(overrides)
}
