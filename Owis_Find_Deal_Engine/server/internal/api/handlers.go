package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"owis_find_deal_engine/internal/auth"
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
	catalog  *markets.Catalog
	searcher MeteredSearcher
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

	out, err := h.searcher.Search(r.Context(), user,
		search.Request{Title: body.Title, Country: body.Country, Refresh: body.Refresh})
	if out.QuotaKnown {
		setQuotaHeaders(w, out.Quota)
	}

	var quotaErr *metering.QuotaError
	switch {
	case err == nil:
		w.Header().Set("X-Cache", cacheStatus(out.Result))
		writeJSON(w, http.StatusOK, out.Result)
	case errors.As(err, &quotaErr) && quotaErr.UpgradeRequired():
		writeError(w, http.StatusPaymentRequired, CodePaymentRequired,
			"free daily searches used up; upgrade your plan to continue")
	case errors.As(err, &quotaErr):
		w.Header().Set("Retry-After", strconv.Itoa(max(int(time.Until(quotaErr.Decision.Reset).Seconds()), 1)))
		writeError(w, http.StatusTooManyRequests, CodeQuotaExceeded, "daily search limit reached")
	case errors.Is(err, metering.ErrQuotaUnavailable):
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "service temporarily unavailable, please retry")
	case errors.Is(err, search.ErrInvalidTitle):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, search.ErrUnknownCountry):
		writeError(w, http.StatusBadRequest, CodeUnsupportedCountry, "country must be one of the codes from GET /api/v1/countries")
	case errors.Is(err, search.ErrAllFailed):
		writeError(w, http.StatusBadGateway, CodeUpstream, "search is temporarily unavailable, please retry")
	case errors.Is(err, context.Canceled):
		// Client went away; nothing to write.
	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
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
