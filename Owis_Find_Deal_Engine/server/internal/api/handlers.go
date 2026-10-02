package api

import (
	"context"
	"errors"
	"net/http"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

// Searcher is the part of search.Service the API needs.
type Searcher interface {
	Search(ctx context.Context, title, country string) (*search.Result, error)
}

type handlers struct {
	catalog  *markets.Catalog
	searcher Searcher
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
}

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}

	res, err := h.searcher.Search(r.Context(), req.Title, req.Country)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, search.ErrInvalidTitle):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, search.ErrUnknownCountry):
		writeError(w, http.StatusBadRequest, CodeUnsupportedCountry, "country must be one of the codes from GET /api/v1/countries")
	case errors.Is(err, search.ErrAllFailed):
		writeError(w, http.StatusBadGateway, CodeUpstream, "search is temporarily unavailable, please retry")
	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
	}
}
