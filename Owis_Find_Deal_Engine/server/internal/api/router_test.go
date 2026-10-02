package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/search"
)

type fakeSearcher struct {
	res *search.Result
	err error
}

func (f fakeSearcher) Search(_ context.Context, title, country string) (*search.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	r := *f.res
	r.Query, r.Country = title, country
	return &r, nil
}

func newTestRouter(t *testing.T, s Searcher, burst int) (http.Handler, string) {
	t.Helper()
	key, hash, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.ParseKeyStore("test:" + hash)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := markets.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(Deps{
		Catalog:     cat,
		Searcher:    s,
		Keys:        keys,
		Limiter:     ratelimit.New(0.001, burst, time.Minute),
		CORSOrigins: []string{"https://app.example"},
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}), key
}

func do(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

var okSearcher = fakeSearcher{res: &search.Result{
	Results: []search.Product{{Market: "amazon", Title: "S Pen", Link: "https://amazon.com/x", Position: 1}},
	Markets: map[string]string{"amazon": "ok"},
}}

func TestHealthIsPublic(t *testing.T) {
	h, _ := newTestRouter(t, okSearcher, 5)
	rec := do(h, http.MethodGet, "/api/v1/health", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID")
	}
}

func TestAPIKeyRequired(t *testing.T) {
	h, _ := newTestRouter(t, okSearcher, 5)
	for _, key := range []string{"", "ofde_wrong"} {
		rec := do(h, http.MethodPost, "/api/v1/search", key, `{"title":"s pen","country":"jor"}`)
		if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != CodeUnauthorized {
			t.Errorf("key %q: status = %d body = %s", key, rec.Code, rec.Body)
		}
	}
}

func TestCountries(t *testing.T) {
	h, key := newTestRouter(t, okSearcher, 5)
	rec := do(h, http.MethodGet, "/api/v1/countries", key, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Countries []struct {
			Code    string `json:"code"`
			Markets []struct {
				ID string `json:"id"`
			} `json:"markets"`
		} `json:"countries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Countries) != 3 || body.Countries[0].Code != "jor" || len(body.Countries[0].Markets) != 4 {
		t.Errorf("unexpected countries: %s", rec.Body)
	}
}

func TestSearchOK(t *testing.T) {
	h, key := newTestRouter(t, okSearcher, 5)
	rec := do(h, http.MethodPost, "/api/v1/search", key, `{"title":"s pen","country":"jor"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	var res search.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Country != "jor" || len(res.Results) != 1 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name     string
		searcher Searcher
		body     string
		status   int
		code     string
	}{
		{"bad json", okSearcher, `{`, 400, CodeInvalidRequest},
		{"unknown field", okSearcher, `{"title":"x","foo":1}`, 400, CodeInvalidRequest},
		{"empty body", okSearcher, ``, 400, CodeInvalidRequest},
		{"bad title", fakeSearcher{err: search.ErrInvalidTitle}, `{"title":"x","country":"jor"}`, 400, CodeInvalidRequest},
		{"bad country", fakeSearcher{err: search.ErrUnknownCountry}, `{"title":"xx","country":"fra"}`, 400, CodeUnsupportedCountry},
		{"upstream", fakeSearcher{err: search.ErrAllFailed}, `{"title":"xx","country":"jor"}`, 502, CodeUpstream},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, key := newTestRouter(t, tt.searcher, 5)
			rec := do(h, http.MethodPost, "/api/v1/search", key, tt.body)
			if rec.Code != tt.status || errorCode(t, rec) != tt.code {
				t.Errorf("status = %d body = %s, want %d %s", rec.Code, rec.Body, tt.status, tt.code)
			}
		})
	}
}

func TestRateLimit(t *testing.T) {
	h, key := newTestRouter(t, okSearcher, 2)
	for i := 0; i < 2; i++ {
		if rec := do(h, http.MethodGet, "/api/v1/countries", key, ""); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d", i, rec.Code)
		}
	}
	rec := do(h, http.MethodGet, "/api/v1/countries", key, "")
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != CodeRateLimited {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
}

func TestPanicRecovered(t *testing.T) {
	h, key := newTestRouter(t, panicSearcher{}, 5)
	rec := do(h, http.MethodPost, "/api/v1/search", key, `{"title":"xx","country":"jor"}`)
	if rec.Code != http.StatusInternalServerError || errorCode(t, rec) != CodeInternal {
		t.Errorf("status = %d body = %s", rec.Code, rec.Body)
	}
}

type panicSearcher struct{}

func (panicSearcher) Search(context.Context, string, string) (*search.Result, error) { panic("boom") }

func TestCORSPreflight(t *testing.T) {
	h, _ := newTestRouter(t, okSearcher, 5)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/search", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Errorf("allowed origin: status = %d headers = %v", rec.Code, rec.Header())
	}

	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS header set for unknown origin")
	}
}
