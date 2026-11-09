// Command owis_find_deal_engine runs the product search API.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"owis_find_deal_engine/internal/adminui"
	"owis_find_deal_engine/internal/api"
	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/cache"
	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/jobs"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/metering"
	"owis_find_deal_engine/internal/providers/apify"
	"owis_find_deal_engine/internal/providers/searxng"
	"owis_find_deal_engine/internal/providers/serpapi"
	"owis_find_deal_engine/internal/queue"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/settings"
	"owis_find_deal_engine/internal/stats"
	"owis_find_deal_engine/internal/store"
	"owis_find_deal_engine/internal/usage"
	"owis_find_deal_engine/internal/webhook"

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
	if len(os.Args) > 1 && (os.Args[1] == "settings" || os.Args[1] == "admins" || os.Args[1] == "clients" || os.Args[1] == "audit") {
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
		db          *store.DB
	)
	if os.Getenv("DATABASE_URL") != "" {
		var err error
		if db, mgr, err = openSettings(ctx, log); err != nil {
			return err
		}
		defer db.Close()
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
	sh := shared{log: log, verifier: verifier, db: db}
	if db != nil {
		// Usage counters for the dashboard: saved every 30 s, the last ones
		// after shutdown (before the database closes).
		sh.stats = stats.NewRecorder()
		statsCtx, stopStats := context.WithCancel(context.Background())
		var statsDone sync.WaitGroup
		defer func() {
			stopStats()
			statsDone.Wait()
		}()
		statsDone.Go(func() { sh.stats.Run(statsCtx, db, stats.FlushInterval, log) })

		// API keys of API clients; when each key was last used is saved
		// every minute and at shutdown.
		sh.keys = apikeys.NewAuthenticator(db, log)
		statsDone.Go(func() { sh.keys.Run(statsCtx, time.Minute) })
	}
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
		sh.jobs = jobs.NewRunner(jobs.NewRedisStore(rdb), log)
		sh.queue = queue.NewRedis(rdb, queueOptions(cfg), log)
		log.Info("usage counters, search cache, search jobs and the shops' search queue in redis")
	} else {
		mem := usage.NewMemoryStore()
		go mem.Cleanup(ctx, 10*time.Minute)
		sh.usageStore = mem
		sh.cacheStore = cache.NewMemoryStore(cfg.CacheMaxEntries)
		jobStore := jobs.NewMemoryStore()
		go jobStore.Cleanup(ctx, time.Minute)
		sh.jobs = jobs.NewRunner(jobStore, log)
		sh.queue = queue.NewMemory(queueOptions(cfg), log)
		log.Warn("REDIS_URL not set: usage counters, search cache, search jobs and the search queue in memory, reset on restart")
	}

	// Webhooks of the asynchronous search (API clients, so a database):
	// each client's signing secret is derived from SETTINGS_ENCRYPTION_KEY.
	if db != nil {
		key, err := settings.ParseKey(os.Getenv("SETTINGS_ENCRYPTION_KEY"))
		if err != nil {
			return err
		}
		if sh.webhooks.Secrets, err = webhook.NewSecrets(key); err != nil {
			return err
		}
		sh.webhooks.Sender = webhook.NewSender(webhook.Options{AllowPrivate: cfg.WebhookAllowPrivate})
		sh.webhooks.AllowPrivate = cfg.WebhookAllowPrivate
		if cfg.WebhookAllowPrivate {
			log.Warn("webhooks may call plain http and internal addresses (WEBHOOK_ALLOW_PRIVATE=true); development only")
		}
	}

	first, err := buildApp(ctx, cfg, settingsVer, sh)
	if err != nil {
		return err
	}
	var (
		current  atomic.Pointer[app]
		retiring sync.WaitGroup
	)
	current.Store(first)
	// The queue runs each shop's live search with the current setup.
	sh.queue.SetRunner(func(ctx context.Context, t queue.Task) queue.Outcome {
		a := current.Load()
		if a.cache == nil {
			return queue.Failed("the search cache is off")
		}
		return a.cache.RunTask(ctx, t)
	})
	sh.queue.Start()
	logQueue(log, cfg)
	// Jobs a stopped server left running (Redis) go on.
	api.ResumeJobs(ctx, first.deps)
	if mgr != nil {
		// A settings change builds a new provider chain, cache front and
		// router and swaps them in at once. Requests already running finish
		// on the old ones, which are closed afterwards.
		apply := func(next config.Config, v int64) error {
			a, err := buildApp(ctx, next, v, sh)
			if err != nil {
				return err
			}
			old := current.Swap(a)
			sh.queue.SetOptions(queueOptions(next))
			log.Info("settings applied", "settings_version", v)
			logQueue(log, next)
			retiring.Go(func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), old.cfg.SearchTimeout+15*time.Second)
				defer cancel()
				old.close(closeCtx)
			})
			return nil
		}
		go mgr.Watch(ctx, cfg.SettingsPollInterval, settingsVer, apply, log)
	}

	if db != nil {
		go watchUsage(ctx, db, func() config.Config { return current.Load().cfg }, log)
	}

	// The admin API (with a database only) stays while the settings change;
	// it reaches the running search setup through current.
	var admin http.Handler
	if db != nil {
		admin = api.NewAdminRouter(api.AdminDeps{
			Store:          db,
			Settings:       mgr,
			Clients:        db,
			ClientsChanged: sh.keys.Forget,
			WebhookSecrets: sh.webhooks.Secrets,
			Runtime:        func() api.AdminRuntime { return adminRuntime{a: current.Load(), poll: cfg.SettingsPollInterval} },
			Verifier:       verifier,
			Require2FA:     cfg.AdminRequire2FA,
			SessionMaxAge:  cfg.AdminSessionMaxAge,

			RequireVerifiedEmail: cfg.AdminRequireVerifiedEmail,
			Log:                  log,
		})
		api := admin
		ui := adminui.Handler(adminui.Config{
			FirebaseAPIKey:     cfg.AdminFirebaseAPIKey,
			FirebaseAuthDomain: cfg.AdminFirebaseAuthDomain,
			FirebaseProjectID:  cfg.AuthAudience,
			Require2FA:         cfg.AdminRequire2FA,
			RequireVerified:    cfg.AdminRequireVerifiedEmail,
		})
		admin = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/admin/v1/") {
				api.ServeHTTP(w, r)
				return
			}
			ui.ServeHTTP(w, r)
		})
		if cfg.AdminFirebaseAPIKey == "" {
			log.Warn("ADMIN_FIREBASE_API_KEY not set: the admin web app (/admin/) cannot sign in")
		}
		if !cfg.AdminRequireVerifiedEmail {
			log.Warn("admin email verification is disabled (ADMIN_REQUIRE_VERIFIED_EMAIL=false); set it to true before production")
		}
		if !cfg.AdminRequire2FA {
			log.Warn("two-factor admin login is disabled (ADMIN_REQUIRE_2FA=false); set it to true before production")
		}
	}

	srv := &http.Server{
		Addr: net.JoinHostPort("", cfg.Port),
		// The caller's address: from cloudflared's CF-Connecting-IP header
		// (TRUSTED_PROXIES), else the connection's.
		Handler: api.WithClientIP(cfg.TrustedProxies)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if admin != nil && (r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/")) {
				admin.ServeHTTP(w, r)
				return
			}
			current.Load().handler.ServeHTTP(w, r)
		})),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// The write timeout follows the search timeout, which is a runtime
		// setting: the router sets it per request.
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}

	if len(cfg.AuthAllowedSignIn) == 0 {
		log.Warn("app users may sign in with any method, also Firebase anonymous accounts (AUTH_ALLOWED_SIGN_IN=*)")
	} else if slices.Contains(cfg.AuthAllowedSignIn, "anonymous") {
		log.Warn("Firebase anonymous accounts are accepted (AUTH_ALLOWED_SIGN_IN lists anonymous)")
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server started", "addr", srv.Addr, "version", version, "countries", len(cfg.Catalog.Countries()),
			"markets_source", cfg.MarketsSource, "admin_api", admin != nil,
			"trusted_proxies", fmt.Sprint(cfg.TrustedProxies), "allowed_sign_in", strings.Join(cfg.AuthAllowedSignIn, ","))
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
	// Running search jobs: with Redis they stop at once and the next start
	// resumes them; in memory they get the rest of the shutdown time, then
	// are saved as failed (the caller can start them again).
	sh.jobs.Close(shutdownCtx)
	// Shop searches still running are queued again for the next start.
	sh.queue.Close(shutdownCtx)
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
	stats      *stats.Recorder        // usage counters; nil without a database
	keys       *apikeys.Authenticator // API keys; nil without a database
	log        *slog.Logger
	verifier   *auth.Verifier
	usageStore usage.Store
	cacheStore cache.Store
	jobs       *jobs.Runner // asynchronous searches
	queue      *queue.Queue // the shops' live searches
	webhooks   api.Webhooks // webhooks of the jobs; no Sender without a database
	db         *store.DB    // nil without a database
}

// app is everything built from the runtime settings: providers, search
// service, cache front, quotas, rate limiter and router.
type app struct {
	cfg             config.Config
	settingsVersion int64
	handler         http.Handler
	searcher        *search.Service
	cache           *cache.Cache // nil when the cache is off
	deps            api.Deps     // what the router was built with
	stop            context.CancelFunc
	log             *slog.Logger
}

func buildApp(ctx context.Context, cfg config.Config, settingsVersion int64, sh shared) (*app, error) {
	log := sh.log
	rec := sh.recorder()
	web, err := buildWebProvider(cfg, rec, log)
	if err != nil {
		return nil, err
	}
	log.Info("search providers", "chain", web.Name())

	providers := map[string]search.Provider{"web": web}
	catalog, err := addSerpAPIMarkets(cfg, cfg.Catalog, providers, web, rec, log)
	if err != nil {
		return nil, err
	}
	catalog, err = addApifyProviders(cfg, catalog, providers, web, rec, log)
	if err != nil {
		return nil, err
	}
	catalog, err = turnOffShops(cfg, catalog, log)
	if err != nil {
		return nil, err
	}
	timeouts := make(map[string]time.Duration, len(cfg.Shops))
	for _, m := range slices.Sorted(maps.Keys(cfg.Shops)) {
		r := cfg.Shops[m]
		timeouts[m] = r.Timeout
		if r.Source == config.SourceOff {
			log.Info("shop rules", "market", m, "source", r.Source)
			continue
		}
		log.Info("shop rules", "market", m, "source", r.Source, "timeout", r.Timeout.String(), "group", r.Group)
	}
	plans, err := usage.ParsePlans(cfg.Plans, cfg.DefaultPlan)
	if err != nil {
		return nil, err
	}

	searcher := search.NewService(catalog,
		providers,
		search.Options{Timeout: cfg.SearchTimeout, Timeouts: timeouts, MaxConcurrency: cfg.MaxConcurrency,
			PerMarket: cfg.ResultsPerMarket},
		log)

	ctx, stop := context.WithCancel(ctx)
	limiter := ratelimit.New(cfg.RateLimitRPS, cfg.RateLimitBurst, 10*time.Minute)
	go limiter.Cleanup(ctx, time.Minute)
	// Reading search jobs: generous, so polling does not eat the search limit.
	pollLimiter := ratelimit.New(5, 20, 10*time.Minute)
	go pollLimiter.Cleanup(ctx, time.Minute)

	a := &app{cfg: cfg, settingsVersion: settingsVersion, searcher: searcher, stop: stop, log: log}
	cacheOrLive := metering.WithoutCache(searcher)
	if cfg.CacheEnabled {
		a.cache = cache.New(searcher, sh.cacheStore, catalog, cache.Options{
			FreshTTL:             cfg.CacheFreshTTL,
			EmptyTTL:             cfg.CacheEmptyTTL,
			StaleTTL:             cfg.CacheStaleTTL,
			LastKnownTTL:         cfg.CacheLastKnownTTL,
			MinRefresh:           cfg.CacheMinRefresh,
			FetchTimeout:         cfg.SearchTimeout + 5*time.Second,
			MaxBackgroundRefresh: cfg.CacheMaxBackgroundRefresh,
			Generation:           strconv.Itoa(cfg.CacheGeneration),
			Shops:                cacheShops(cfg, web.Name()),
			Queue:                sh.queue,
		}, log)
		cacheOrLive = a.cache
		log.Info("search cache enabled", "fresh", cfg.CacheFreshTTL.String(), "stale", cfg.CacheStaleTTL.String(),
			"last_known", cfg.CacheLastKnownTTL.String())
	}

	groups := make(map[string]string, len(cfg.Shops))
	for m, r := range cfg.Shops {
		groups[m] = r.Group
	}
	a.deps = api.Deps{
		Catalog:       catalog,
		Searcher:      metering.New(cacheOrLive, usage.NewQuota(sh.usageStore, plans), log).WithRecorder(sh.searchRecorder()),
		Verifier:      sh.verifier,
		Keys:          sh.keyAuthenticator(),
		AllowedSignIn: cfg.AuthAllowedSignIn,
		Limiter:       limiter,
		CORSOrigins:   cfg.CORSOrigins,
		WriteTimeout:  cfg.SearchTimeout + 10*time.Second,
		Log:           log,
		Jobs:          sh.jobs,
		// A job may wait for a free run in the queue, then the slowest
		// shop's time limit.
		JobTimeout:  cfg.LongestShopTimeout() + cfg.QueueMaxWait + 15*time.Second,
		Webhooks:    sh.webhooks,
		PollLimiter: pollLimiter,
		Version:     version,
		ShopGroups:  groups,
		ClientSalt:  sh.clientSalt(),
	}
	a.handler = api.NewRouter(a.deps)
	return a, nil
}

// clientSalt reads an API client's webhook salt (nil without a database).
func (sh shared) clientSalt() func(ctx context.Context, id int64) ([]byte, error) {
	if sh.db == nil {
		return nil
	}
	return func(ctx context.Context, id int64) ([]byte, error) {
		c, err := sh.db.Client(ctx, id)
		return c.WebhookSalt, err
	}
}

// queueOptions are the queue's caps and waits from the settings.
func queueOptions(cfg config.Config) queue.Options {
	return queue.Options{
		Limits: map[string]int{queue.ClassApify: cfg.QueueApifyMaxRuns, queue.ClassSerpAPI: cfg.QueueSerpAPIMaxRuns,
			queue.ClassWeb: cfg.QueueWebMaxRuns},
		MaxWait: cfg.QueueMaxWait,
	}
}

func logQueue(log *slog.Logger, cfg config.Config) {
	log.Info("search queue", "apify_max_runs", cfg.QueueApifyMaxRuns, "serpapi_max_runs", cfg.QueueSerpAPIMaxRuns,
		"web_max_runs", cfg.QueueWebMaxRuns, "max_wait", cfg.QueueMaxWait.String())
}

// adminRuntime gives the admin API the running search setup.
type adminRuntime struct {
	a    *app
	poll time.Duration
}

func (r adminRuntime) Probe(ctx context.Context, req search.ProbeRequest) (*search.ProbeResult, error) {
	cfg := r.a.cfg
	return r.a.searcher.Probe(ctx, req, func(provider, market string) float64 { return sourceCost(cfg, provider, market) })
}

func (r adminRuntime) ForgetCached(ctx context.Context, req search.Request) (bool, error) {
	if r.a.cache == nil {
		return false, api.ErrCacheDisabled
	}
	return r.a.cache.Forget(ctx, req)
}

func (r adminRuntime) Health() []search.ChainHealth { return r.a.searcher.Health() }

func (r adminRuntime) Countries() []string {
	var codes []string
	for _, c := range r.a.cfg.Catalog.Countries() {
		codes = append(codes, c.Code)
	}
	return codes
}

func (r adminRuntime) Status() api.RuntimeStatus {
	c := r.a.cfg
	return api.RuntimeStatus{
		Version:         version,
		SettingsVersion: r.a.settingsVersion,
		PollInterval:    r.poll.String(),
		MarketsSource:   c.MarketsSource,
		MarketsJSON:     c.MarketsJSON,
		CacheEnabled:    c.CacheEnabled,
		CacheGeneration: c.CacheGeneration,

		BudgetMonthlyUSD:   c.BudgetMonthlyUSD,
		BudgetAlertPercent: c.BudgetAlertPercent,
	}
}

// sourceCost estimates one call of a source for one shop, from the cost
// settings.
func sourceCost(cfg config.Config, provider, market string) float64 {
	switch {
	case provider == "apify":
		return cfg.ApifyRunCostUSD[market]
	case strings.HasPrefix(provider, "serpapi"):
		return cfg.SerpAPISearchCostUSD
	}
	return 0
}

// callCost estimates one call of a source for several shops: a scraper
// runs once per shop, a SerpApi call is one search whatever the shops.
func callCost(cfg config.Config, provider string) func(markets []string) float64 {
	return func(markets []string) float64 {
		if provider == "apify" {
			var sum float64
			for _, m := range markets {
				sum += sourceCost(cfg, provider, m)
			}
			return sum
		}
		if len(markets) == 0 {
			return 0
		}
		return sourceCost(cfg, provider, markets[0])
	}
}

// recorder returns the usage counters as a search.Recorder (nil = off).
func (sh shared) recorder() search.Recorder {
	if sh.stats == nil {
		return nil
	}
	return sh.stats
}

// keyAuthenticator returns the API key check (nil = keys not accepted).
func (sh shared) keyAuthenticator() api.KeyAuthenticator {
	if sh.keys == nil {
		return nil
	}
	return sh.keys
}

// searchRecorder returns the usage counters as a metering.SearchRecorder.
func (sh shared) searchRecorder() metering.SearchRecorder {
	if sh.stats == nil {
		return nil
	}
	return sh.stats
}

// Usage data is kept this long (UTC days).
const (
	usageRetention   = 400 * 24 * time.Hour
	queriesRetention = 90 * 24 * time.Hour
)

// watchUsage checks the monthly budget every minute (alert in the log and
// the audit log) and deletes old usage data every hour, until ctx ends.
func watchUsage(ctx context.Context, db *store.DB, cfg func() config.Config, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	var lastPrune time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		c := cfg()
		if c.BudgetMonthlyUSD > 0 {
			if _, err := stats.CheckBudget(ctx, db, time.Now(), c.BudgetMonthlyUSD, c.BudgetAlertPercent, log); err != nil && ctx.Err() == nil {
				log.Warn("budget check failed", "err", err)
			}
		}
		if time.Since(lastPrune) > time.Hour {
			now := time.Now().UTC()
			if err := db.PruneUsage(ctx, now.Add(-usageRetention), now.Add(-queriesRetention)); err != nil && ctx.Err() == nil {
				log.Warn("deleting old usage data failed", "err", err)
			}
			lastPrune = time.Now()
		}
	}
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
func buildWebProvider(cfg config.Config, rec search.Recorder, log *slog.Logger) (search.Provider, error) {
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
		providers = append(providers, search.Instrument(p, rec, callCost(cfg, p.Name())))
	}
	return search.NewFallback(providers, search.FallbackOptions{
		AttemptTimeout:   cfg.ProviderAttemptTimeout,
		Timeouts:         map[string]time.Duration{"serpapi": cfg.SerpAPITimeout},
		FailureThreshold: cfg.ProviderFailThreshold,
		Cooldown:         cfg.ProviderCooldown,
		Recorder:         rec,
	}, log)
}

// addSerpAPIMarkets routes each shop whose source is serpapi to its
// dedicated SerpApi engine (Amazon / eBay Search API). There is no backup
// source: when it fails, the shop's last known results are shown.
func addSerpAPIMarkets(cfg config.Config, catalog *markets.Catalog, providers map[string]search.Provider,
	_ search.Provider, rec search.Recorder, log *slog.Logger) (*markets.Catalog, error) {
	if len(cfg.SerpAPIMarkets) == 0 {
		return catalog, nil
	}
	overrides := make(map[string]string, len(cfg.SerpAPIMarkets))
	for _, m := range cfg.SerpAPIMarkets {
		sc := serpapi.Config{APIKey: cfg.SerpAPIKey, BaseURL: cfg.SerpAPIBaseURL,
			Combined: cfg.SearchCombined, HTTPClient: &http.Client{Timeout: cfg.Shops[m].Timeout + 5*time.Second}}
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
			err = fmt.Errorf("SHOP_%s_SOURCE: no dedicated SerpApi engine for %q", strings.ToUpper(m), m)
		}
		if err != nil {
			return nil, err
		}
		name := engine.Name()
		providers[name] = search.Instrument(engine, rec, callCost(cfg, name))
		overrides[m] = name
	}
	return catalog.WithProviders(overrides)
}

// addApifyProviders routes each shop whose source is apify to its Apify
// scraper. There is no backup source: when it fails or runs out of time,
// the shop's last known results are shown.
func addApifyProviders(cfg config.Config, catalog *markets.Catalog, providers map[string]search.Provider,
	_ search.Provider, rec search.Recorder, log *slog.Logger) (*markets.Catalog, error) {
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
			Timeout:       cfg.ApifyRunTimeout(m),
			MemoryMB:      cfg.ApifyMemoryMB[m],
			MaxChargeUSD:  cfg.ApifyMaxChargeUSD,
			Currency:      cfg.ApifyCurrencies[m],
			Combined:      cfg.SearchCombined,
			BaseURL:       cfg.ApifyBaseURL,
			EmptyFallback: cfg.ApifyEmptyFallback,
		})
		if err != nil {
			return nil, fmt.Errorf("APIFY_%s: %w", strings.ToUpper(m), err)
		}
		name := "apify-" + m
		providers[name] = search.Instrument(scraper, rec, callCost(cfg, scraper.Name()))
		overrides[m] = name
		memory := "default"
		if mb := cfg.ApifyMemoryMB[m]; mb > 0 {
			memory = strconv.Itoa(mb) + "MB"
		}
		log.Info("apify scraper enabled", "market", m, "actor", cfg.ApifyActors[m], "max_items", cfg.ApifyMarketMaxItems[m],
			"timeout", cfg.Shops[m].Timeout.String(), "apify_run_timeout", cfg.ApifyRunTimeout(m).String(), "memory", memory,
			"group", cfg.Shops[m].Group, "search_timeout", cfg.SearchTimeout.String())
	}
	return catalog.WithProviders(overrides)
}

// turnOffShops marks the shops whose source is off: they are not searched
// and show the status "off".
func turnOffShops(cfg config.Config, catalog *markets.Catalog, log *slog.Logger) (*markets.Catalog, error) {
	overrides := map[string]string{}
	for m, r := range cfg.Shops {
		if r.Source == config.SourceOff {
			overrides[m] = search.OffProvider
		}
	}
	if len(overrides) == 0 {
		return catalog, nil
	}
	return catalog.WithProviders(overrides)
}

// cacheShops describes, per shop, what its cached results depend on: the
// source and its settings. Changing any of them starts new entries for
// that shop only.
func cacheShops(cfg config.Config, webChain string) map[string]cache.Shop {
	out := make(map[string]cache.Shop, len(cfg.Shops))
	for m, r := range cfg.Shops {
		setup := fmt.Sprintf("source=%s;per_market=%d", r.Source, cfg.ResultsPerMarket)
		var byRegion bool
		switch r.Source {
		case config.SourceOff:
			continue
		case config.SourceSerpAPI:
			setup += ";engine=serpapi-" + m
		case config.SourceApify:
			in := cfg.ApifyInputs[m]
			setup += fmt.Sprintf(";actor=%s;input=%s;max=%d;currency=%s",
				cfg.ApifyActors[m], in, cfg.ApifyMarketMaxItems[m], cfg.ApifyCurrencies[m])
			byRegion = strings.Contains(in, "{{country}}")
		case config.SourceWeb:
			setup += fmt.Sprintf(";chain=%s;serpapi=%s;searxng_one_query=%t;combined=%t",
				webChain, cfg.SerpAPIEngine, cfg.SearXNGOneQuery, cfg.SearchCombined)
			byRegion = true
		}
		out[m] = cache.Shop{Setup: setup, ByRegion: byRegion}
	}
	return out
}
