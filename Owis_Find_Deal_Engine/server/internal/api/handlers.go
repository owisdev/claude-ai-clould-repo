package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"
)

// Searcher runs searches (the cache in front of search.Service).
type Searcher interface {
	// Lookup answers from cache only, never live; ok=false means a live
	// search is needed.
	Lookup(ctx context.Context, req search.Request) (res *search.Result, ok bool)
	Search(ctx context.Context, req search.Request) (*search.Result, error)
}

// LiveSearcher is a searcher without a cache (search.Service).
type LiveSearcher interface {
	Search(ctx context.Context, req search.Request) (*search.Result, error)
}

// WithoutCache adapts a LiveSearcher when the cache is disabled.
func WithoutCache(s LiveSearcher) Searcher { return noCache{s} }

type noCache struct{ LiveSearcher }

func (noCache) Lookup(context.Context, search.Request) (*search.Result, bool) { return nil, false }

type handlers struct {
	catalog  *markets.Catalog
	searcher Searcher
	quota    QuotaTaker
	log      *slog.Logger
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

// search serves a product search. Answers from the cache are free: they
// do not count against the daily allowance and are served even when it is
// used up. Only live searches consume quota:
//   - within the allowance: counted, searched live;
//   - free plan used up: 402 Payment Required (upgrade);
//   - paid plan used up: 429 Too Many Requests until the daily reset;
//   - counter store down: 503 (fail closed, protects paid upstream quota).
//
// A live search that fails, or that turns out to be served from cache
// after all (another request filled it meanwhile), is refunded.
func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	var body searchRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	req := search.Request{Title: body.Title, Country: body.Country, Refresh: body.Refresh}
	user, _ := auth.UserFrom(r.Context())

	if res, ok := h.searcher.Lookup(r.Context(), req); ok {
		if d, err := h.quota.Status(r.Context(), user.ID, user.Plan); err == nil {
			setQuotaHeaders(w, d)
		}
		writeResult(w, res)
		return
	}

	d, err := h.quota.Take(r.Context(), user.ID, user.Plan)
	if err != nil {
		h.log.ErrorContext(r.Context(), "quota check failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "service temporarily unavailable, please retry")
		return
	}
	setQuotaHeaders(w, d)
	if !d.Allowed {
		if d.UpgradeRequired {
			writeError(w, http.StatusPaymentRequired, CodePaymentRequired,
				"free daily searches used up; upgrade your plan to continue")
			return
		}
		w.Header().Set("Retry-After", strconv.Itoa(max(int(time.Until(d.Reset).Seconds()), 1)))
		writeError(w, http.StatusTooManyRequests, CodeQuotaExceeded, "daily search limit reached")
		return
	}

	res, err := h.searcher.Search(r.Context(), req)
	if err != nil || res.Cached {
		h.refund(r, d)
	}
	switch {
	case err == nil:
		writeResult(w, res)
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

// refund gives the search back and corrects the Remaining header.
func (h *handlers) refund(r *http.Request, d usage.Decision) {
	// Detached from the request context, which may be cancelled.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if err := h.quota.Refund(ctx, d); err != nil {
		h.log.WarnContext(r.Context(), "quota refund failed", "err", err)
	}
}

func setQuotaHeaders(w http.ResponseWriter, d usage.Decision) {
	h := w.Header()
	h.Set("X-Plan", d.Plan.Name)
	h.Set("X-RateLimit-Limit", strconv.FormatInt(d.Plan.DailyLimit, 10))
	h.Set("X-RateLimit-Remaining", strconv.FormatInt(d.Remaining(), 10))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(d.Reset.Unix(), 10))
}

func writeResult(w http.ResponseWriter, res *search.Result) {
	w.Header().Set("X-Cache", cacheStatus(res))
	writeJSON(w, http.StatusOK, res)
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
