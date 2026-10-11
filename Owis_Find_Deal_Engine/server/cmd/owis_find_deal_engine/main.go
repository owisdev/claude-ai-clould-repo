// Command owis_find_deal_engine runs the product search API.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
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
	"owis_find_deal_engine/internal/settings"
	"owis_find_deal_engine/internal/store"
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
	if len(os.Args) > 1 && (os.Args[1] == "settings" || os.Args[1] == "admins") {
		os.Exit(adminCommand(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
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
	var level slog.Level
	if err := level.UnmarshalText([]byte(cmp.Or(os.Getenv("LOG_LEVEL"), "info"))); err != nil {
		return fmt.Errorf("LOG_LEVEL: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	// ctx is cancelled on SIGINT/SIGTERM and stops background goroutines.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Settings: from the database when DATABASE_URL is set (changes are
	// applied without a restart), otherwise from the environment.
	var (
		cfg         config.Config
		settingsVer int64
		mgr         *settings.Manager
	)
	if os.Getenv("DATABASE_URL") != "" {
		db, m, err := openSettings(ctx, log)
		if err != nil {
			return err
		}
		defer db.Close()
		mgr = m
		if cfg, settingsVer, err = mgr.Config(ctx); err != nil {
			return fmt.Errorf("settings: %w", err)
		}
		if cfg.AdminEmail != "" {
			added, err := db.EnsureFirstAdmin(ctx, cfg.AdminEmail)
			if err != nil {
				return fmt.Errorf("admins: %w", err)
			}
			if added {
				log.Info("first admin added", "email", cfg.AdminEmail)
			}
		}
		log.Info("settings loaded from the database", "settings_version", settingsVer)
	} else {
		var err error
		if cfg, err = config.Load(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		log.Warn("DATABASE_URL not set: settings come from the environment and need a restart to change")
	}

	baseCatalog, err := markets.Load(cfg.MarketsFile)
	if err != nil {
		return err
	}

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
	sh := shared{log: log, catalog: baseCatalog, verifier: verifier}
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
		sh.usageStore = usage.NewRedisStore(rdb)
		sh.cacheStore = cache.NewRedisStore(rdb)
		log.Info("usage counters and search cache in redis")
	} else {
		mem := usage.NewMemoryStore()
		go mem.Cleanup(ctx, 10*time.Minute)
		sh.usageStore = mem
		sh.cacheStore = cache.NewMemoryStore(cfg.CacheMaxEntries)
		log.Warn("REDIS_URL not set: usage counters and search cache in memory, reset on restart")
	}

	first, err := buildApp(ctx, cfg, sh)
	if err != nil {
		return err
	}
	var (
		current  atomic.Pointer[app]
		retiring sync.WaitGroup
	)
	current.Store(first)
	if mgr != nil {
		// A settings change builds a new provider chain, cache front and
		// router and swaps them in at once. Requests already running finish
		// on the old ones, which are closed afterwards.
		apply := func(next config.Config, v int64) error {
			a, err := buildApp(ctx, next, sh)
			if err != nil {
				return err
			}
			old := current.Swap(a)
			log.Info("settings applied", "settings_version", v)
			retiring.Go(func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), old.cfg.SearchTimeout+15*time.Second)
				defer cancel()
				old.close(closeCtx)
			})
			return nil
		}
		go mgr.Watch(ctx, cfg.SettingsPollInterval, settingsVer, apply, log)
	}

	srv := &http.Server{
		Addr: net.JoinHostPort("", cfg.Port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current.Load().handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// The write timeout follows the search timeout, which is a runtime
		// setting: the router sets it per request.
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server started", "addr", srv.Addr, "version", version, "countries", len(baseCatalog.Countries()))
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
	current.Load().close(shutdownCtx)
	retiring.Wait()
	log.Info("server stopped cleanly")
	return nil
}

// openSettings connects to the database, applies migrations and, on the
// first start, moves the runtime settings of the environment (.env) into
// the database.
func openSettings(ctx context.Context, log *slog.Logger) (*store.DB, *settings.Manager, error) {
	db, mgr, err := connectSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	imported, err := mgr.ImportEnv(ctx, os.Environ())
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	if len(imported) > 0 {
		log.Info("runtime settings imported from the environment into the database", "keys", imported)
	}
	values, _, err := mgr.Values(ctx)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	if ignored := settings.IgnoredEnv(values, os.Environ()); len(ignored) > 0 {
		log.Warn("runtime settings in the environment differ from the database and are ignored; change them with the settings command",
			"keys", ignored)
	}
	return db, mgr, nil
}

// connectSettings opens the database (migrated) and the settings manager.
func connectSettings(ctx context.Context) (*store.DB, *settings.Manager, error) {
	key := os.Getenv("SETTINGS_ENCRYPTION_KEY")
	if key == "" {
		return nil, nil, errors.New("SETTINGS_ENCRYPTION_KEY is required when DATABASE_URL is set")
	}
	cipher, err := settings.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	db, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, nil, err
	}
	if _, err := db.Migrate(ctx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("database migrations: %w", err)
	}
	return db, settings.NewManager(db, cipher, os.LookupEnv), nil
}

// shared are the parts that stay when the runtime settings change.
type shared struct {
	log        *slog.Logger
	catalog    *markets.Catalog // countries and shops, before source overrides
	verifier   *auth.Verifier
	usageStore usage.Store
	cacheStore cache.Store
}

// app is everything built from the runtime settings: providers, search
// service, cache front, quotas, rate limiter and router.
type app struct {
	cfg     config.Config
	handler http.Handler
	cache   *cache.Cache // nil when the cache is off
	stop    context.CancelFunc
	log     *slog.Logger
}

func buildApp(ctx context.Context, cfg config.Config, sh shared) (*app, error) {
	log := sh.log
	web, err := buildWebProvider(cfg, log)
	if err != nil {
		return nil, err
	}
	log.Info("search providers", "chain", web.Name())

	providers := map[string]search.Provider{"web": web}
	catalog, err := addSerpAPIMarkets(cfg, sh.catalog, providers, web, log)
	if err != nil {
		return nil, err
	}
	catalog, err = addApifyProviders(cfg, catalog, providers, web, log)
	if err != nil {
		return nil, err
	}
	plans, err := usage.ParsePlans(cfg.Plans, cfg.DefaultPlan)
	if err != nil {
		return nil, err
	}

	searcher := search.NewService(catalog,
		providers,
		search.Options{Timeout: cfg.SearchTimeout, MaxConcurrency: cfg.MaxConcurrency,
			PerMarket: cfg.ResultsPerMarket},
		log)

	ctx, stop := context.WithCancel(ctx)
	limiter := ratelimit.New(cfg.RateLimitRPS, cfg.RateLimitBurst, 10*time.Minute)
	go limiter.Cleanup(ctx, time.Minute)

	a := &app{cfg: cfg, stop: stop, log: log}
	cacheOrLive := metering.WithoutCache(searcher)
	if cfg.CacheEnabled {
		a.cache = cache.New(searcher, sh.cacheStore, catalog, cache.Options{
			FreshTTL:             cfg.CacheFreshTTL,
			PartialTTL:           cfg.CachePartialTTL,
			EmptyTTL:             cfg.CacheEmptyTTL,
			StaleTTL:             cfg.CacheStaleTTL,
			MinRefresh:           cfg.CacheMinRefresh,
			FetchTimeout:         cfg.SearchTimeout + 5*time.Second,
			MaxBackgroundRefresh: cfg.CacheMaxBackgroundRefresh,
			Variant: fmt.Sprintf("providers=%s;apify=%s;apify_empty_fallback=%t;serpapi_markets=%s;per_market=%d;searxng_one_query=%t;serpapi=%s",
				web.Name(), strings.Join(cfg.ApifyMarkets, ","), cfg.ApifyEmptyFallback, strings.Join(cfg.SerpAPIMarkets, ","),
				cfg.ResultsPerMarket, cfg.SearXNGOneQuery, cfg.SerpAPIEngine),
		}, log)
		cacheOrLive = a.cache
		log.Info("search cache enabled", "fresh", cfg.CacheFreshTTL.String(), "stale", cfg.CacheStaleTTL.String())
	}

	a.handler = api.NewRouter(api.Deps{
		Catalog:      catalog,
		Searcher:     metering.New(cacheOrLive, usage.NewQuota(sh.usageStore, plans), log),
		Verifier:     sh.verifier,
		Limiter:      limiter,
		CORSOrigins:  cfg.CORSOrigins,
		WriteTimeout: cfg.SearchTimeout + 10*time.Second,
		Log:          log,
	})
	return a, nil
}

// close stops the app's background work and lets running cache refreshes
// finish until ctx ends.
func (a *app) close(ctx context.Context) {
	a.stop()
	if a.cache == nil {
		return
	}
	if err := a.cache.Close(ctx); err != nil {
		a.log.Warn("cache refreshes still running at shutdown", "err", err)
	}
	hits, stale, misses := a.cache.Stats()
	a.log.Info("search cache stats", "hits", hits, "stale", stale, "misses", misses)
}

// buildWebProvider creates the configured providers in SEARCH_PROVIDERS
// order and chains them: the first that succeeds answers the search.
func buildWebProvider(cfg config.Config, log *slog.Logger) (search.Provider, error) {
	providers := make([]search.Provider, 0, len(cfg.SearchProviders))
	for i, name := range cfg.SearchProviders {
		last := i == len(cfg.SearchProviders)-1
		var (
			p   search.Provider
			err error
		)
		switch name {
		case "searxng":
			p, err = searxng.New(searxng.Config{BaseURL: cfg.SearXNGURL, Combined: cfg.SearchCombined,
				OneQuery: cfg.SearXNGOneQuery, MaxPages: cfg.SearXNGMaxPages, Logger: log})
		case "serpapi":
			p, err = serpapi.New(serpapi.Config{APIKey: cfg.SerpAPIKey, Engine: cfg.SerpAPIEngine,
				BaseURL: cfg.SerpAPIBaseURL, Combined: cfg.SearchCombined,
				HTTPClient: &http.Client{Timeout: cfg.SerpAPITimeout},
				// A free provider after SerpApi does the web search instead.
				NoWebRetry: !last})
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
		Timeouts:         map[string]time.Duration{"serpapi": cfg.SerpAPITimeout},
		FailureThreshold: cfg.ProviderFailThreshold,
		Cooldown:         cfg.ProviderCooldown,
	}, log)
}

// addSerpAPIMarkets routes each marketplace in SERPAPI_MARKETS to its
// dedicated SerpApi engine (Amazon / eBay Search API), with the web
// providers as fallback when it fails.
func addSerpAPIMarkets(cfg config.Config, catalog *markets.Catalog, providers map[string]search.Provider,
	web search.Provider, log *slog.Logger) (*markets.Catalog, error) {
	if len(cfg.SerpAPIMarkets) == 0 {
		return catalog, nil
	}
	overrides := make(map[string]string, len(cfg.SerpAPIMarkets))
	for _, m := range cfg.SerpAPIMarkets {
		sc := serpapi.Config{APIKey: cfg.SerpAPIKey, BaseURL: cfg.SerpAPIBaseURL,
			Combined: cfg.SearchCombined, HTTPClient: &http.Client{Timeout: cfg.SerpAPITimeout}}
		var (
			engine search.Provider
			err    error
		)
		switch m {
		case "amazon":
			engine, err = serpapi.NewAmazon(sc)
		case "ebay":
			engine, err = serpapi.NewEbay(sc)
		default:
			err = fmt.Errorf("SERPAPI_MARKETS: no dedicated engine for %q", m)
		}
		if err != nil {
			return nil, err
		}
		chain, err := search.NewFallback([]search.Provider{engine, web}, search.FallbackOptions{
			AttemptTimeout:   cfg.SerpAPITimeout,
			Timeouts:         map[string]time.Duration{web.Name(): cfg.SearchTimeout},
			FailureThreshold: cfg.ProviderFailThreshold,
			Cooldown:         cfg.ProviderCooldown,
		}, log)
		if err != nil {
			return nil, err
		}
		name := engine.Name()
		providers[name] = chain
		overrides[m] = name
		log.Info("dedicated SerpApi engine enabled", "market", m, "provider", name)
	}
	return catalog.WithProviders(overrides)
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
			MaxItems:      cfg.ApifyMarketMaxItems[m],
			Timeout:       cfg.ApifyTimeout,
			MaxChargeUSD:  cfg.ApifyMaxChargeUSD,
			Currency:      cfg.ApifyCurrencies[m],
			Combined:      cfg.SearchCombined,
			BaseURL:       cfg.ApifyBaseURL,
			EmptyFallback: cfg.ApifyEmptyFallback,
		})
		if err != nil {
			return nil, fmt.Errorf("APIFY_%s: %w", strings.ToUpper(m), err)
		}
		chain, err := search.NewFallback([]search.Provider{scraper, web}, search.FallbackOptions{
			AttemptTimeout: cfg.ApifyTimeout,
			// The web chain has its own per-provider timeouts; the search
			// timeout still bounds the whole search.
			Timeouts:         map[string]time.Duration{web.Name(): cfg.SearchTimeout},
			FailureThreshold: cfg.ProviderFailThreshold,
			Cooldown:         cfg.ProviderCooldown,
		}, log)
		if err != nil {
			return nil, err
		}
		name := "apify-" + m
		providers[name] = chain
		overrides[m] = name
		log.Info("apify scraper enabled", "market", m, "actor", cfg.ApifyActors[m], "max_items", cfg.ApifyMarketMaxItems[m],
			"timeout", cfg.ApifyTimeout.String(), "search_timeout", cfg.SearchTimeout.String())
	}
	return catalog.WithProviders(overrides)
}
