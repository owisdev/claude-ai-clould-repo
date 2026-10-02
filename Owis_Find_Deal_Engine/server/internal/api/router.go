// Package api exposes the service over HTTP.
package api

import (
	"log/slog"
	"net/http"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/ratelimit"
)

// Deps are the router's dependencies.
type Deps struct {
	Catalog     *markets.Catalog
	Searcher    Searcher
	Verifier    TokenVerifier
	Quota       QuotaTaker
	Limiter     *ratelimit.Limiter
	CORSOrigins []string
	Log         *slog.Logger
}

// NewRouter returns the service's HTTP handler.
//
//	GET  /api/v1/health     public
//	GET  /api/v1/countries  public
//	POST /api/v1/search     user JWT -> burst rate limit -> daily quota
func NewRouter(d Deps) http.Handler {
	h := &handlers{catalog: d.Catalog, searcher: d.Searcher}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("GET /api/v1/countries", h.countries)
	mux.Handle("POST /api/v1/search", chain(http.HandlerFunc(h.search),
		requireUser(d.Verifier, d.Log),
		rateLimit(d.Limiter),
		enforceQuota(d.Quota, d.Log),
	))

	return chain(mux,
		requestID,
		logRequests(d.Log),
		recoverPanics(d.Log),
		cors(d.CORSOrigins),
	)
}
