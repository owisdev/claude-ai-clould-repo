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
	"syscall"
	"time"

	"owis_find_deal_engine/internal/api"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/providers/serpapi"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/search"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
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
	keys, err := auth.ParseKeyStore(cfg.APIKeys)
	if err != nil {
		return err
	}
	web, err := serpapi.New(serpapi.Config{APIKey: cfg.SerpAPIKey, Combined: cfg.SearchCombined})
	if err != nil {
		return err
	}

	searcher := search.NewService(catalog,
		map[string]search.Provider{"web": web},
		search.Options{Timeout: cfg.SearchTimeout, MaxConcurrency: cfg.MaxConcurrency},
		log)

	// ctx is cancelled on SIGINT/SIGTERM and stops background goroutines.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	limiter := ratelimit.New(cfg.RateLimitRPS, cfg.RateLimitBurst, 10*time.Minute)
	go limiter.Cleanup(ctx, time.Minute)

	srv := &http.Server{
		Addr: net.JoinHostPort("", cfg.Port),
		Handler: api.NewRouter(api.Deps{
			Catalog:     catalog,
			Searcher:    searcher,
			Keys:        keys,
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
		log.Info("server started", "addr", srv.Addr, "countries", len(catalog.Countries()))
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
	log.Info("server stopped cleanly")
	return nil
}
