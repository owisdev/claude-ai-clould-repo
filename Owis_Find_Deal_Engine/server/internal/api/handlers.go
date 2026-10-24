package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/jobs"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/metering"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"
)

// MeteredSearcher runs a user's search and accounts for it.
type MeteredSearcher interface {
	Search(ctx context.Context, user auth.User, req search.Request) (metering.Outcome, error)
}

type handlers struct {
	catalog    *markets.Catalog
	searcher   MeteredSearcher
	jobs       *jobs.Runner
	jobTimeout time.Duration
	webhooks   Webhooks
	log        *slog.Logger
}

func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) countries(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"countries": h.catalog.Countries()})
}

type searchRequest struct {
	Title   string `json:"title"`
	Country string `json:"country"`
	Refresh bool   `json:"refresh"`
}

// search serves a product search. What it costs the user is decided by
// the metering service; this handler only maps the outcome to HTTP.
func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	var body searchRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if c, ok := clientFrom(r.Context()); ok && !apikeys.AllowedCountry(c, body.Country) {
		writeError(w, http.StatusForbidden, CodeCountryNotAllowed, "this API key may not search in country "+strconv.Quote(body.Country))
		return
	}

	out, err := h.searcher.Search(r.Context(), user,
		search.Request{Title: body.Title, Country: body.Country, Refresh: body.Refresh})
	if out.QuotaKnown {
		setQuotaHeaders(w, out.Quota)
	}

	if err == nil {
		w.Header().Set("X-Cache", cacheStatus(out.Result))
		writeJSON(w, http.StatusOK, out.Result)
		return
	}
	if errors.Is(err, context.Canceled) {
		return // client went away; nothing to write
	}
	e := searchError(err)
	if e.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.retryAfter))
	}
	writeError(w, e.status, e.code, e.msg)
}

// apiError is an error answer of the public API.
type apiError struct {
	status     int
	code, msg  string
	retryAfter int // seconds; 0 = no Retry-After
}

// searchError maps a failed metered search to its answer (the same for
// POST /api/v1/search and the jobs of POST /api/v1/searches).
func searchError(err error) apiError {
	var quotaErr *metering.QuotaError
	switch {
	case errors.As(err, &quotaErr) && quotaErr.UpgradeRequired():
		return apiError{status: http.StatusPaymentRequired, code: CodePaymentRequired,
			msg: "free daily searches used up; upgrade your plan to continue"}
	case errors.As(err, &quotaErr):
		return apiError{status: http.StatusTooManyRequests, code: CodeQuotaExceeded, msg: "daily search limit reached",
			retryAfter: max(int(time.Until(quotaErr.Decision.Reset).Seconds()), 1)}
	case errors.Is(err, metering.ErrQuotaUnavailable):
		return apiError{status: http.StatusServiceUnavailable, code: CodeUnavailable, msg: "service temporarily unavailable, please retry"}
	case errors.Is(err, search.ErrInvalidTitle):
		return apiError{status: http.StatusBadRequest, code: CodeInvalidRequest, msg: err.Error()}
	case errors.Is(err, search.ErrUnknownCountry):
		return apiError{status: http.StatusBadRequest, code: CodeUnsupportedCountry, msg: "country must be one of the codes from GET /api/v1/countries"}
	case errors.Is(err, search.ErrAllFailed):
		return apiError{status: http.StatusBadGateway, code: CodeUpstream, msg: "search is temporarily unavailable, please retry"}
	default:
		return apiError{status: http.StatusInternalServerError, code: CodeInternal, msg: "internal server error"}
	}
}

func setQuotaHeaders(w http.ResponseWriter, d usage.Decision) {
	h := w.Header()
	h.Set("X-Plan", d.Plan.Name)
	h.Set("X-RateLimit-Limit", strconv.FormatInt(d.Plan.DailyLimit, 10))
	h.Set("X-RateLimit-Remaining", strconv.FormatInt(d.Remaining(), 10))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(d.Reset.Unix(), 10))
}

func cacheStatus(r *search.Result) string {
	switch {
	case !r.Cached:
		return "MISS"
	case r.Stale:
		return "STALE"
	default:
		return "HIT"
	}
}
