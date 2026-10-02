package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/auth/authtest"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"
)

type fakeSearcher struct {
	res    *search.Result
	err    error
	cached *search.Result // answer for Lookup; nil = not cached
}

func (f fakeSearcher) Lookup(context.Context, search.Request) (*search.Result, bool) {
	if f.cached == nil {
		return nil, false
	}
	r := *f.cached
	return &r, true
}

func (f fakeSearcher) Search(_ context.Context, req search.Request) (*search.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	r := *f.res
	r.Query, r.Country = req.Title, req.Country
	return &r, nil
}

var okSearcher = fakeSearcher{res: &search.Result{
	Results: []search.Product{{Market: "amazon", Title: "S Pen", Link: "https://amazon.com/x", Position: 1}},
	Markets: map[string]string{"amazon": "ok"},
}}

type testEnv struct {
	handler  http.Handler
	provider *authtest.Provider
	store    *usage.MemoryStore
}

type envOpts struct {
	searcher Searcher
	burst    int
	plans    string
	quota    QuotaTaker
}

func newEnv(t *testing.T, o envOpts) *testEnv {
	t.Helper()
	if o.searcher == nil {
		o.searcher = okSearcher
	}
	if o.burst == 0 {
		o.burst = 100
	}
	if o.plans == "" {
		o.plans = "free:100,pro:1000"
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	p := authtest.New(t)
	jwks := auth.NewJWKS(p.URL(), log)
	if err := jwks.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewVerifier(jwks, auth.VerifierConfig{
		Issuer: authtest.Issuer, Audience: authtest.Audience, PlanClaim: "plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := usage.ParsePlans(o.plans, "free")
	if err != nil {
		t.Fatal(err)
	}
	store := usage.NewMemoryStore()
	if o.quota == nil {
		o.quota = usage.NewQuota(store, plans)
	}
	cat, err := markets.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{
		handler: NewRouter(Deps{
			Catalog:     cat,
			Searcher:    o.searcher,
			Verifier:    verifier,
			Quota:       o.quota,
			Limiter:     ratelimit.New(0.001, o.burst, time.Minute),
			CORSOrigins: []string{"https://app.example"},
			Log:         log,
		}),
		provider: p,
		store:    store,
	}
}

func (e *testEnv) do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

const searchBody = `{"title":"s pen","country":"jor"}`

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestPublicEndpoints(t *testing.T) {
	e := newEnv(t, envOpts{})

	rec := e.do(http.MethodGet, "/api/v1/health", "", "")
	if rec.Code != http.StatusOK || rec.Header().Get("X-Request-ID") == "" {
		t.Errorf("health: status = %d", rec.Code)
	}

	rec = e.do(http.MethodGet, "/api/v1/countries", "", "")
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
		t.Errorf("countries: %s", rec.Body)
	}
}

func TestSearchRequiresValidToken(t *testing.T) {
	e := newEnv(t, envOpts{})
	expired := e.provider.Token(t, "u1", jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()})

	for name, tok := range map[string]string{"none": "", "garbage": "abc", "expired": expired} {
		rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody)
		if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != CodeUnauthorized {
			t.Errorf("%s: status = %d body = %s", name, rec.Code, rec.Body)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: missing WWW-Authenticate", name)
		}
	}
}

func TestSearchOK(t *testing.T) {
	e := newEnv(t, envOpts{})
	rec := e.do(http.MethodPost, "/api/v1/search", e.provider.Token(t, "u1", nil), searchBody)
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
	h := rec.Header()
	if h.Get("X-Plan") != "free" || h.Get("X-RateLimit-Limit") != "100" || h.Get("X-RateLimit-Remaining") != "99" {
		t.Errorf("quota headers = %v", h)
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
		{"bad title", fakeSearcher{err: search.ErrInvalidTitle}, searchBody, 400, CodeInvalidRequest},
		{"bad country", fakeSearcher{err: search.ErrUnknownCountry}, searchBody, 400, CodeUnsupportedCountry},
		{"upstream", fakeSearcher{err: search.ErrAllFailed}, searchBody, 502, CodeUpstream},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, envOpts{searcher: tt.searcher})
			tok := e.provider.Token(t, "u1", nil)
			rec := e.do(http.MethodPost, "/api/v1/search", tok, tt.body)
			if rec.Code != tt.status || errorCode(t, rec) != tt.code {
				t.Errorf("status = %d body = %s, want %d %s", rec.Code, rec.Body, tt.status, tt.code)
			}
			// Failed searches are not counted: a following valid search
			// sees the full allowance minus itself.
			rec = e.do(http.MethodPost, "/api/v1/search", tok, searchBody)
			if got := rec.Header().Get("X-RateLimit-Remaining"); got != "99" {
				t.Errorf("remaining after refund = %s, want 99", got)
			}
		})
	}
}

func TestFreePlanQuotaReturns402(t *testing.T) {
	e := newEnv(t, envOpts{plans: "free:2,pro:3"})
	tok := e.provider.Token(t, "u1", nil)
	for i := 0; i < 2; i++ {
		if rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody); rec.Code != http.StatusOK {
			t.Fatalf("search %d: status = %d", i, rec.Code)
		}
	}
	rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody)
	if rec.Code != http.StatusPaymentRequired || errorCode(t, rec) != CodePaymentRequired {
		t.Errorf("status = %d body = %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("remaining = %s", rec.Header().Get("X-RateLimit-Remaining"))
	}

	// Another user has their own allowance.
	if rec := e.do(http.MethodPost, "/api/v1/search", e.provider.Token(t, "u2", nil), searchBody); rec.Code != http.StatusOK {
		t.Errorf("other user: status = %d", rec.Code)
	}
}

func TestPaidPlanQuotaReturns429(t *testing.T) {
	e := newEnv(t, envOpts{plans: "free:1,pro:2"})
	tok := e.provider.Token(t, "u1", jwt.MapClaims{"plan": "pro"})
	for i := 0; i < 2; i++ {
		if rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody); rec.Code != http.StatusOK {
			t.Fatalf("search %d: status = %d", i, rec.Code)
		}
	}
	rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody)
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != CodeQuotaExceeded {
		t.Errorf("status = %d body = %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
}

func TestBurstRateLimit(t *testing.T) {
	e := newEnv(t, envOpts{burst: 2})
	tok := e.provider.Token(t, "u1", nil)
	for i := 0; i < 2; i++ {
		if rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d", i, rec.Code)
		}
	}
	rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody)
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != CodeRateLimited {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
}

type brokenQuota struct{}

func (brokenQuota) Take(context.Context, string, string) (usage.Decision, error) {
	return usage.Decision{}, errors.New("redis down")
}
func (brokenQuota) Refund(context.Context, usage.Decision) error { return nil }
func (brokenQuota) Status(context.Context, string, string) (usage.Decision, error) {
	return usage.Decision{}, errors.New("redis down")
}

func TestQuotaStoreDownFailsClosed(t *testing.T) {
	e := newEnv(t, envOpts{quota: brokenQuota{}})
	rec := e.do(http.MethodPost, "/api/v1/search", e.provider.Token(t, "u1", nil), searchBody)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != CodeUnavailable {
		t.Errorf("status = %d body = %s", rec.Code, rec.Body)
	}
}

type panicSearcher struct{}

func (panicSearcher) Lookup(context.Context, search.Request) (*search.Result, bool) {
	return nil, false
}

func (panicSearcher) Search(context.Context, search.Request) (*search.Result, error) { panic("boom") }

func TestPanicRecovered(t *testing.T) {
	e := newEnv(t, envOpts{searcher: panicSearcher{}})
	rec := e.do(http.MethodPost, "/api/v1/search", e.provider.Token(t, "u1", nil), searchBody)
	if rec.Code != http.StatusInternalServerError || errorCode(t, rec) != CodeInternal {
		t.Errorf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestCORSPreflight(t *testing.T) {
	e := newEnv(t, envOpts{})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/search", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Errorf("allowed origin: status = %d headers = %v", rec.Code, rec.Header())
	}

	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS header set for unknown origin")
	}
}

var cachedResult = &search.Result{
	Results: []search.Product{{Market: "amazon", Title: "S Pen", Link: "https://amazon.com/x", Position: 1}},
	Markets: map[string]string{"amazon": "ok"},
	Cached:  true,
}

func TestCachedAnswersAreFree(t *testing.T) {
	e := newEnv(t, envOpts{plans: "free:2", searcher: fakeSearcher{cached: cachedResult}})
	tok := e.provider.Token(t, "u1", nil)
	for i := 0; i < 5; i++ {
		rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody)
		if rec.Code != http.StatusOK || rec.Header().Get("X-Cache") != "HIT" {
			t.Fatalf("search %d: status = %d cache = %s", i, rec.Code, rec.Header().Get("X-Cache"))
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != "2" {
			t.Errorf("search %d: remaining = %s, want 2 (cache hits are free)", i, got)
		}
	}
}

func TestCachedAnswersServedWhenQuotaUsedUp(t *testing.T) {
	plans, _ := usage.ParsePlans("free:1", "free")
	quota := usage.NewQuota(usage.NewMemoryStore(), plans)
	live := newEnv(t, envOpts{quota: quota})
	tok := live.provider.Token(t, "u1", nil)

	// Use up the allowance with a live search.
	if rec := live.do(http.MethodPost, "/api/v1/search", tok, searchBody); rec.Code != http.StatusOK {
		t.Fatalf("live search: %d", rec.Code)
	}
	if rec := live.do(http.MethodPost, "/api/v1/search", tok, searchBody); rec.Code != http.StatusPaymentRequired {
		t.Fatalf("second live search: %d, want 402", rec.Code)
	}

	// Same user, same counters, but the answer is cached: still served.
	cached := newEnv(t, envOpts{quota: quota, searcher: fakeSearcher{cached: cachedResult}})
	rec := cached.do(http.MethodPost, "/api/v1/search", cached.provider.Token(t, "u1", nil), searchBody)
	if rec.Code != http.StatusOK || rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("cached answer over quota: status = %d remaining = %s", rec.Code, rec.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestLiveSearchAnsweredFromCacheIsRefunded(t *testing.T) {
	// Lookup missed, but by the time Search ran another request had filled
	// the cache: the answer is cached, so it must not be counted.
	e := newEnv(t, envOpts{plans: "free:2", searcher: fakeSearcher{res: cachedResult}})
	tok := e.provider.Token(t, "u1", nil)
	for i := 0; i < 3; i++ {
		if rec := e.do(http.MethodPost, "/api/v1/search", tok, searchBody); rec.Code != http.StatusOK {
			t.Fatalf("search %d: status = %d (cached answers must not use quota)", i, rec.Code)
		}
	}
}
