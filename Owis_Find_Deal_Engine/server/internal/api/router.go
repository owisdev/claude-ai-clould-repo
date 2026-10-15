// Package api exposes the service over HTTP.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/ratelimit"
)

// Deps are the router's dependencies.
type Deps struct {
	Catalog  *markets.Catalog
	Searcher MeteredSearcher
	Verifier TokenVerifier
	// Keys checks API keys; nil = only app users' login tokens.
	Keys        KeyAuthenticator
	Limiter     *ratelimit.Limiter
	CORSOrigins []string
	// WriteTimeout bounds writing each response (0 = none). It is set per
	// request because it follows the search timeout, a runtime setting.
	WriteTimeout time.Duration
	Log          *slog.Logger
}

// NewRouter returns the service's HTTP handler.
//
//	GET  /api/v1/health     public
//	GET  /api/v1/countries  public
//	POST /api/v1/search     user JWT or API key (scope "search") -> burst
//	                        rate limit -> metered search (cached answers
//	                        free, live searches counted per user / client)
func NewRouter(d Deps) http.Handler {
	h := &handlers{catalog: d.Catalog, searcher: d.Searcher}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("GET /api/v1/countries", h.countries)
	mux.Handle("POST /api/v1/search", chain(http.HandlerFunc(h.search),
		requireCaller(d.Verifier, d.Keys, apikeys.ScopeSearch, d.Log),
		rateLimit(d.Limiter),
	))

	return chain(mux,
		writeDeadline(d.WriteTimeout),
		requestID,
		logRequests(d.Log),
		recoverPanics(d.Log),
		cors(d.CORSOrigins),
	)
}
