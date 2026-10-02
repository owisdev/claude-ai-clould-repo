// Package api exposes the service over HTTP.
package api

import (
	"log/slog"
	"net/http"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/ratelimit"
)

// Deps are the router's dependencies.
type Deps struct {
	Catalog     *markets.Catalog
	Searcher    Searcher
	Keys        *auth.KeyStore
	Limiter     *ratelimit.Limiter
	CORSOrigins []string
	Log         *slog.Logger
}

// NewRouter returns the service's HTTP handler.
//
//	GET  /api/v1/health     public
//	GET  /api/v1/countries  API key
//	POST /api/v1/search     API key + rate limit
func NewRouter(d Deps) http.Handler {
	h := &handlers{catalog: d.Catalog, searcher: d.Searcher}
	protected := func(fn http.HandlerFunc) http.Handler {
		return chain(fn, requireAPIKey(d.Keys), rateLimit(d.Limiter))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.Handle("GET /api/v1/countries", protected(h.countries))
	mux.Handle("POST /api/v1/search", protected(h.search))

	return chain(mux,
		requestID,
		logRequests(d.Log),
		recoverPanics(d.Log),
		cors(d.CORSOrigins),
	)
}
