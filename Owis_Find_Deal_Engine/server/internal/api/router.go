// Package api exposes the service over HTTP.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/devdocs"
	"owis_find_deal_engine/internal/jobs"
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

	// Jobs runs asynchronous searches (POST /api/v1/searches); nil = off.
	Jobs *jobs.Runner
	// JobTimeout bounds one asynchronous search.
	JobTimeout time.Duration
	// Webhooks sign and send the jobs' webhooks.
	Webhooks Webhooks
	// PollLimiter limits reading jobs per caller (nil = 5/s, burst 20); it
	// also limits the MCP endpoint's messages per API client.
	PollLimiter *ratelimit.Limiter

	// Version is the server's version (shown to MCP clients).
	Version string
}

// NewRouter returns the service's HTTP handler.
//
//	GET  /api/v1/health     public
//	GET  /api/v1/countries  public
//	POST /api/v1/search     user JWT or API key (scope "search") -> burst
//	                        rate limit -> metered search (cached answers
//	                        free, live searches counted per user / client)
//	POST /api/v1/searches   the same search, asynchronous: 202 + job id,
//	                        optional signed webhook (API keys only)
//	GET  /api/v1/searches/{id}  the job: running, done (result) or failed
//	POST /mcp               MCP (Streamable HTTP) for AI agents, API keys
//	                        only: tools search_products, list_countries
//	GET  /api/v1/openapi.json  OpenAPI 3.1 description (public)
//	GET  /docs/             developer documentation (public)
func NewRouter(d Deps) http.Handler {
	h := &handlers{catalog: d.Catalog, searcher: d.Searcher, jobs: d.Jobs, jobTimeout: d.JobTimeout, webhooks: d.Webhooks,
		limiter: d.Limiter, log: d.Log,
		mcpServer: &mcpServer{version: d.Version, origins: d.CORSOrigins, tools: mcpTools(d.Catalog)}}
	if d.PollLimiter == nil {
		d.PollLimiter = ratelimit.New(5, 20, 10*time.Minute)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("GET /api/v1/countries", h.countries)
	mux.Handle("POST /api/v1/search", chain(http.HandlerFunc(h.search),
		requireCaller(d.Verifier, d.Keys, apikeys.ScopeSearch, d.Log),
		rateLimit(d.Limiter),
	))
	if d.Jobs != nil {
		mux.Handle("POST /api/v1/searches", chain(http.HandlerFunc(h.startSearch),
			requireCaller(d.Verifier, d.Keys, apikeys.ScopeSearch, d.Log),
			rateLimit(d.Limiter),
		))
		mux.Handle("GET /api/v1/searches/{id}", chain(http.HandlerFunc(h.getSearch),
			requireCaller(d.Verifier, d.Keys, apikeys.ScopeSearch, d.Log),
			limitBy(d.PollLimiter, func(r *http.Request) string { u, _ := auth.UserFrom(r.Context()); return "poll:" + u.ID }),
		))
	}

	// MCP: API keys only; each tool checks its scope. Every message is
	// limited per client; search_products also takes from the client's
	// search burst limit, like POST /api/v1/search.
	mux.Handle("POST /mcp", chain(http.HandlerFunc(h.mcp),
		requireCaller(nil, d.Keys, "", d.Log),
		limitBy(d.PollLimiter, func(r *http.Request) string { u, _ := auth.UserFrom(r.Context()); return "mcp:" + u.ID }),
	))

	mux.Handle("GET /api/v1/openapi.json", devdocs.OpenAPI())
	mux.Handle("GET /docs/", devdocs.Page())
	mux.Handle("GET /docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))

	return chain(mux,
		writeDeadline(d.WriteTimeout),
		requestID,
		logRequests(d.Log),
		recoverPanics(d.Log),
		cors(d.CORSOrigins),
	)
}
