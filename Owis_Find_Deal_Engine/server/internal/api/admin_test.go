package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/auth/authtest"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/settings"
	"owis_find_deal_engine/internal/store"
	"owis_find_deal_engine/internal/webhook"
)

// fakeAdminStore is an in-memory store.DB for the admin API.
type fakeAdminStore struct {
	mu       sync.Mutex
	admins   []store.Admin
	audit    []store.AuditEntry
	settings []store.Setting
	history  []store.Change
}

func (f *fakeAdminStore) AdminForLogin(_ context.Context, uid, email string, verified bool) (store.Admin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, a := range f.admins {
		if a.FirebaseUID != nil && *a.FirebaseUID == uid {
			return a, nil
		}
		if a.Email == email && verified {
			if a.FirebaseUID != nil {
				return store.Admin{}, store.ErrAdminLinked
			}
			f.admins[i].FirebaseUID = &uid
			return f.admins[i], nil
		}
	}
	return store.Admin{}, store.ErrNotAdmin
}

func (f *fakeAdminStore) Admins(context.Context) ([]store.Admin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Admin(nil), f.admins...), nil
}

func (f *fakeAdminStore) AddAdmin(_ context.Context, email, by string) (store.Admin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.admins {
		if a.Email == email {
			return store.Admin{}, store.ErrAdminExists
		}
	}
	a := store.Admin{ID: int64(len(f.admins) + 1), Email: email, CreatedBy: by, CreatedAt: time.Now()}
	f.admins = append(f.admins, a)
	return a, nil
}

func (f *fakeAdminStore) SetAdminDisabled(_ context.Context, email string, disabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, a := range f.admins {
		if a.Email == email {
			var at *time.Time
			if disabled {
				now := time.Now()
				at = &now
			}
			f.admins[i].DisabledAt = at
			return nil
		}
	}
	return store.ErrAdminNotFound
}

func (f *fakeAdminStore) UnlinkAdmin(_ context.Context, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, a := range f.admins {
		if a.Email == email {
			f.admins[i].FirebaseUID = nil
			return nil
		}
	}
	return store.ErrAdminNotFound
}

func (f *fakeAdminStore) AddAudit(_ context.Context, e store.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.ID = int64(len(f.audit) + 1)
	f.audit = append(f.audit, e)
	return nil
}

func (f *fakeAdminStore) AuditLog(_ context.Context, flt store.AuditFilter) ([]store.AuditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.AuditEntry
	for i := len(f.audit) - 1; i >= 0; i-- {
		if strings.HasPrefix(f.audit[i].Action, flt.Action) {
			out = append(out, f.audit[i])
		}
	}
	return out, nil
}

func (f *fakeAdminStore) Settings(context.Context) ([]store.Setting, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Setting(nil), f.settings...), int64(len(f.history)), nil
}

func (f *fakeAdminStore) SettingsVersion(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.history)), nil
}

func (f *fakeAdminStore) SettingsHistory(context.Context, string, int) ([]store.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Change(nil), f.history...), nil
}

func (f *fakeAdminStore) UsageRows(_ context.Context, from, to time.Time) ([]store.UsageRow, error) {
	return []store.UsageRow{
		{Day: to, Kind: "search", K1: "jor", K2: "hit", Count: 3},
		{Day: to, Kind: "search", K1: "jor", K2: "live", Count: 1},
		{Day: to, Kind: "source", K1: "apify", K2: "temu", K3: "ok", Count: 1, CostUSD: 0.1, TotalMS: 30000},
		{Day: to, Kind: "client", K1: "client:1", K2: "jor", K3: "live", Count: 1},
		{Day: to, Kind: "client", K1: "client:1", K2: "jor", K3: "hit", Count: 2},
	}, nil
}

func (f *fakeAdminStore) TopQueries(context.Context, time.Time, time.Time, int) ([]store.QueryRow, error) {
	return []store.QueryRow{{Country: "jor", Query: "usb hub", Searches: 4, Live: 1}}, nil
}

func (f *fakeAdminStore) UsageCost(context.Context, time.Time, time.Time) (float64, error) {
	return 4.5, nil
}

func (f *fakeAdminStore) lastAudit() store.AuditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.audit) == 0 {
		return store.AuditEntry{}
	}
	return f.audit[len(f.audit)-1]
}

// fakeSettings records changes; keys starting with BAD are refused.
type fakeSettings struct {
	st *fakeAdminStore
}

func (f fakeSettings) Set(_ context.Context, by string, changes map[string]*string) (int64, []string, error) {
	var changed []string
	for k, v := range changes {
		if strings.HasPrefix(k, "BAD") {
			return 0, nil, fmt.Errorf("%w: %s is not a runtime setting", settings.ErrInvalid, k)
		}
		changed = append(changed, k)
		f.st.mu.Lock()
		f.st.history = append(f.st.history, store.Change{Key: k, New: v, ChangedBy: by})
		f.st.settings = append(f.st.settings, store.Setting{Key: k, Value: *v, UpdatedBy: by})
		f.st.mu.Unlock()
	}
	return int64(len(f.st.history)), changed, nil
}

type fakeRuntime struct {
	probes  int
	cacheOn bool
}

func (f *fakeRuntime) Probe(_ context.Context, req search.ProbeRequest) (*search.ProbeResult, error) {
	f.probes++
	if req.Country != "jor" {
		return nil, search.ErrUnknownCountry
	}
	return &search.ProbeResult{Query: req.Title, Country: "jor", CostUSD: 0.1,
		Sources: []search.SourceAnswer{{Market: "temu", Provider: "apify", Status: "ok", CostUSD: 0.1}}}, nil
}

func (f *fakeRuntime) ForgetCached(context.Context, search.Request) (bool, error) {
	if !f.cacheOn {
		return false, ErrCacheDisabled
	}
	return true, nil
}

func (f *fakeRuntime) Health() []search.ChainHealth {
	return []search.ChainHealth{{Name: "web", Chain: "searxng", Sources: []search.SourceHealth{{Provider: "searxng", State: "closed"}}}}
}

func (f *fakeRuntime) Countries() []string { return []string{"jor", "ksa", "usa"} }

func (f *fakeRuntime) Status() RuntimeStatus {
	return RuntimeStatus{SettingsVersion: 3, MarketsSource: "built-in", MarketsJSON: markets.Default(), CacheEnabled: f.cacheOn, CacheGeneration: 4,
		BudgetMonthlyUSD: 5, BudgetAlertPercent: 80}
}

type adminEnv struct {
	handler  http.Handler
	provider *authtest.Provider
	store    *fakeAdminStore
	runtime  *fakeRuntime
	clients  *fakeClients
	secrets  *webhook.Secrets
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	p := authtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jwks := auth.NewJWKS(p.URL(), log)
	if err := jwks.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := auth.NewVerifier(jwks, auth.VerifierConfig{Issuer: authtest.Issuer, Audience: authtest.Audience})
	if err != nil {
		t.Fatal(err)
	}
	st := &fakeAdminStore{admins: []store.Admin{
		{ID: 1, Email: "owner@example.com", CreatedBy: "bootstrap:ADMIN_EMAIL"},
	}}
	rt := &fakeRuntime{cacheOn: true}
	cs := newFakeClients()
	secrets, err := webhook.NewSecrets(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h := NewAdminRouter(AdminDeps{
		WebhookSecrets: secrets,
		Store:          st, Settings: fakeSettings{st: st}, Runtime: func() AdminRuntime { return rt },
		Clients: cs, ClientsChanged: func() { cs.changed++ },
		Verifier: v, Require2FA: true, RequireVerifiedEmail: true, SessionMaxAge: time.Hour, Log: log,
		ToolLimiter: ratelimit.New(0.001, 2, time.Minute),
	})
	return &adminEnv{handler: h, provider: p, store: st, runtime: rt, clients: cs, secrets: secrets}
}

// token signs in sub with email (verified), a second factor and a recent sign-in.
func (e *adminEnv) token(t *testing.T, sub, email string, extra jwt.MapClaims) string {
	claims := jwt.MapClaims{
		"email": email, "email_verified": true, "auth_time": time.Now().Add(-time.Minute).Unix(),
		"firebase": map[string]any{"sign_in_second_factor": "totp"},
	}
	for k, v := range extra {
		claims[k] = v
	}
	return e.provider.Token(t, sub, claims)
}

func (e *adminEnv) do(t *testing.T, method, path, token, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func errCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestAdminAccess(t *testing.T) {
	e := newAdminEnv(t)
	tests := []struct {
		name   string
		token  string
		status int
		code   string
	}{
		{"no token", "", 401, CodeUnauthorized},
		{"garbage", "x.y.z", 401, CodeUnauthorized},
		{"no second factor", e.token(t, "uid-1", "owner@example.com", jwt.MapClaims{"firebase": map[string]any{}}), 403, CodeSecondFactorRequired},
		{"old sign-in", e.token(t, "uid-1", "owner@example.com", jwt.MapClaims{"auth_time": time.Now().Add(-2 * time.Hour).Unix()}), 401, CodeSessionExpired},
		{"no auth_time", e.token(t, "uid-1", "owner@example.com", jwt.MapClaims{"auth_time": nil}), 401, CodeSessionExpired},
		{"not an admin", e.token(t, "uid-9", "user@example.com", nil), 403, CodeForbidden},
		{"admin email not verified", e.token(t, "uid-1", "owner@example.com", jwt.MapClaims{"email_verified": false}), 403, CodeForbidden},
	}
	for _, tc := range tests {
		rec, out := e.do(t, "GET", "/admin/v1/me", tc.token, "")
		if rec.Code != tc.status || errCode(out) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, rec.Code, errCode(out), tc.status, tc.code)
		}
	}
	// Refusals are audited, at most once a minute per login.
	denied := 0
	for _, a := range e.store.audit {
		if a.Action == "admin.login" && a.Outcome == store.OutcomeDenied {
			denied++
		}
	}
	if denied != 2 {
		t.Errorf("denied logins audited %d times, want 2 (uid-1, uid-9)", denied)
	}

	// The first good login links the uid to the admin entry.
	tok := e.token(t, "uid-1", "Owner@example.com", nil)
	rec, out := e.do(t, "GET", "/admin/v1/me", e.token(t, "uid-1", "owner@example.com", nil), "")
	if rec.Code != 200 || out["email"] != "owner@example.com" || out["second_factor"] != "totp" {
		t.Fatalf("me = %d %v", rec.Code, out)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("admin answers must not be cached")
	}
	// Same email, another login: refused.
	if rec, _ := e.do(t, "GET", "/admin/v1/me", e.token(t, "uid-2", "owner@example.com", nil), ""); rec.Code != 403 {
		t.Errorf("other login with the admin's email: %d", rec.Code)
	}
	// Disabled admins are refused at once.
	_ = e.store.SetAdminDisabled(context.Background(), "owner@example.com", true)
	if rec, _ := e.do(t, "GET", "/admin/v1/me", tok, ""); rec.Code != 403 {
		t.Errorf("disabled admin: %d", rec.Code)
	}
	if rec, out := e.do(t, "GET", "/admin/v1/nope", "", ""); rec.Code != 401 {
		t.Errorf("unknown path without login: %d %v", rec.Code, out)
	}
}

func TestAdminUnverifiedEmailWhenNotRequired(t *testing.T) {
	e := newAdminEnv(t)
	e.handler = NewAdminRouter(AdminDeps{
		Store: e.store, Settings: fakeSettings{st: e.store}, Runtime: func() AdminRuntime { return e.runtime },
		Verifier: verifierFor(t, e.provider), RequireVerifiedEmail: false, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	tok := e.token(t, "uid-1", "owner@example.com", jwt.MapClaims{"email_verified": false})
	if rec, out := e.do(t, "GET", "/admin/v1/me", tok, ""); rec.Code != 200 || out["email"] != "owner@example.com" {
		t.Errorf("unverified admin email, verification off: %d %v", rec.Code, out)
	}
	// Still only emails on the admin list.
	other := e.token(t, "uid-7", "someone@example.com", jwt.MapClaims{"email_verified": false})
	if rec, _ := e.do(t, "GET", "/admin/v1/me", other, ""); rec.Code != 403 {
		t.Errorf("email not on the list: %d", rec.Code)
	}
}

func TestAdminSettings(t *testing.T) {
	e := newAdminEnv(t)
	tok := e.token(t, "uid-1", "owner@example.com", nil)
	e.store.settings = []store.Setting{
		{Key: "SERPAPI_KEY", Value: "v1:sealed", Secret: true, UpdatedBy: "import:env"},
		{Key: "PLANS", Value: "free:20", UpdatedBy: "import:env"},
	}

	rec, out := e.do(t, "GET", "/admin/v1/settings", tok, "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "sealed") {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	found := 0
	for _, s := range out["settings"].([]any) {
		m := s.(map[string]any)
		switch m["key"] {
		case "SERPAPI_KEY":
			found++
			if m["set"] != true || m["secret"] != true || m["value"] != nil {
				t.Errorf("secret shown: %v", m)
			}
		case "PLANS":
			found++
			if m["value"] != "free:20" {
				t.Errorf("PLANS = %v", m)
			}
		case "CACHE_FRESH_TTL":
			found++
			if m["set"] != false {
				t.Errorf("unset key = %v", m)
			}
		}
	}
	if found != 3 {
		t.Errorf("list misses keys: %v", out)
	}

	rec, out = e.do(t, "PATCH", "/admin/v1/settings", tok, `{"changes":{"APIFY_TOKEN":"apify_api_secret","PLANS":"free:30"}}`)
	if rec.Code != 200 || len(out["changed"].([]any)) != 2 {
		t.Fatalf("change = %d %v", rec.Code, out)
	}
	a := e.store.lastAudit()
	if a.Action != "settings.change" || a.Actor != "admin:owner@example.com" || strings.Contains(string(a.Detail), "apify_api_secret") ||
		!strings.Contains(string(a.Detail), `"secrets":["APIFY_TOKEN"]`) {
		t.Errorf("audit = %+v %s", a, a.Detail)
	}
	if e.store.history[len(e.store.history)-1].ChangedBy != "admin:owner@example.com" {
		t.Error("history author is not the admin")
	}

	if rec, out := e.do(t, "PATCH", "/admin/v1/settings", tok, `{"changes":{"BAD_KEY":"1"}}`); rec.Code != 400 || errCode(out) != CodeInvalidRequest {
		t.Errorf("invalid change = %d %v", rec.Code, out)
	}
	if a := e.store.lastAudit(); a.Outcome != store.OutcomeFailed {
		t.Errorf("refused change not audited: %+v", a)
	}
	if rec, _ := e.do(t, "PATCH", "/admin/v1/settings", tok, `{"changes":{}}`); rec.Code != 400 {
		t.Errorf("empty change = %d", rec.Code)
	}
	if rec, _ := e.do(t, "GET", "/admin/v1/settings/history?limit=0", tok, ""); rec.Code != 400 {
		t.Errorf("bad limit = %d", rec.Code)
	}
	if rec, out := e.do(t, "GET", "/admin/v1/settings/history", tok, ""); rec.Code != 200 || len(out["changes"].([]any)) != 2 {
		t.Errorf("history = %d %v", rec.Code, out)
	}
}

func TestAdminMarkets(t *testing.T) {
	e := newAdminEnv(t)
	tok := e.token(t, "uid-1", "owner@example.com", nil)

	rec, out := e.do(t, "GET", "/admin/v1/markets", tok, "")
	if rec.Code != 200 || out["source"] != "built-in" || out["markets"].(map[string]any)["countries"] == nil {
		t.Fatalf("get = %d %v", rec.Code, out)
	}
	if rec, out := e.do(t, "PUT", "/admin/v1/markets", tok, `{"countries":{}}`); rec.Code != 400 || !strings.Contains(out["error"].(map[string]any)["message"].(string), "no countries") {
		t.Errorf("invalid catalog = %d %v", rec.Code, out)
	}
	rec, _ = e.do(t, "PUT", "/admin/v1/markets", tok, string(markets.Default()))
	if rec.Code != 200 || e.store.lastAudit().Action != "markets.change" {
		t.Fatalf("put = %d %s", rec.Code, rec.Body)
	}
	if _, out := e.do(t, "GET", "/admin/v1/markets", tok, ""); out["source"] != "setting" {
		t.Errorf("after put: %v", out["source"])
	}
}

func TestAdminList(t *testing.T) {
	e := newAdminEnv(t)
	tok := e.token(t, "uid-1", "owner@example.com", nil)

	if rec, _ := e.do(t, "POST", "/admin/v1/admins", tok, `{"email":"not-an-email"}`); rec.Code != 400 {
		t.Errorf("bad email = %d", rec.Code)
	}
	rec, out := e.do(t, "POST", "/admin/v1/admins", tok, `{"email":" Second@Example.com "}`)
	if rec.Code != 201 || out["email"] != "second@example.com" || out["created_by"] != "admin:owner@example.com" {
		t.Fatalf("add = %d %v", rec.Code, out)
	}
	if a := e.store.lastAudit(); a.Action != "admins.add" || a.Target != "second@example.com" {
		t.Errorf("audit = %+v", a)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/admins", tok, `{"email":"second@example.com"}`); rec.Code != 409 {
		t.Errorf("duplicate = %d", rec.Code)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/admins/owner@example.com/disable", tok, ""); rec.Code != 409 {
		t.Errorf("disable yourself = %d", rec.Code)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/admins/second@example.com/disable", tok, ""); rec.Code != 200 {
		t.Errorf("disable = %d", rec.Code)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/admins/ghost@example.com/enable", tok, ""); rec.Code != 404 {
		t.Errorf("enable unknown = %d", rec.Code)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/admins/second@example.com/promote", tok, ""); rec.Code != 404 {
		t.Errorf("unknown action = %d", rec.Code)
	}
	rec, out = e.do(t, "GET", "/admin/v1/admins", tok, "")
	if rec.Code != 200 || len(out["admins"].([]any)) != 2 {
		t.Errorf("list = %d %v", rec.Code, out)
	}
	rec, out = e.do(t, "GET", "/admin/v1/audit?action=admins.", tok, "")
	if rec.Code != 200 || len(out["entries"].([]any)) != 2 {
		t.Errorf("audit = %d %v", rec.Code, out)
	}
}

func TestAdminTools(t *testing.T) {
	e := newAdminEnv(t)
	tok := e.token(t, "uid-1", "owner@example.com", nil)

	rec, out := e.do(t, "POST", "/admin/v1/tools/test-search", tok, `{"title":"usb hub","country":"jor","all_sources":true}`)
	if rec.Code != 200 || out["cost_usd"] != 0.1 || len(out["sources"].([]any)) != 1 {
		t.Fatalf("test search = %d %v", rec.Code, out)
	}
	if a := e.store.lastAudit(); a.Action != "tools.test_search" || !strings.Contains(string(a.Detail), `"cost_usd":0.1`) {
		t.Errorf("audit = %+v %s", a, a.Detail)
	}
	if rec, out := e.do(t, "POST", "/admin/v1/tools/test-search", tok, `{"title":"usb hub","country":"xx"}`); rec.Code != 400 || errCode(out) != CodeUnsupportedCountry {
		t.Errorf("bad country = %d %v", rec.Code, out)
	}
	// Burst of 2 used up: the costly tool is rate-limited.
	if rec, _ := e.do(t, "POST", "/admin/v1/tools/test-search", tok, `{"title":"usb hub","country":"jor"}`); rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("third test search = %d", rec.Code)
	}

	rec, out = e.do(t, "POST", "/admin/v1/tools/cache/forget", tok, `{"title":"usb hub","country":"jor"}`)
	if rec.Code != 200 || out["found"] != true || e.store.lastAudit().Action != "tools.cache_forget" {
		t.Errorf("forget = %d %v", rec.Code, out)
	}
	e.runtime.cacheOn = false
	if rec, out := e.do(t, "POST", "/admin/v1/tools/cache/forget", tok, `{"title":"usb hub","country":"jor"}`); rec.Code != 409 || errCode(out) != CodeCacheDisabled {
		t.Errorf("forget without cache = %d %v", rec.Code, out)
	}

	rec, _ = e.do(t, "POST", "/admin/v1/tools/cache/bump", tok, "")
	if rec.Code != 200 {
		t.Fatalf("bump = %d %s", rec.Code, rec.Body)
	}
	if h := e.store.history[len(e.store.history)-1]; h.Key != "CACHE_GENERATION" || *h.New != "5" {
		t.Errorf("bump stored %+v", h)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/tools/cache/bump", tok, ""); rec.Code != 200 || *e.store.history[len(e.store.history)-1].New != "6" {
		t.Error("second bump must build on the stored generation")
	}

	rec, out = e.do(t, "GET", "/admin/v1/tools/providers", tok, "")
	if rec.Code != 200 || len(out["chains"].([]any)) != 1 {
		t.Errorf("providers = %d %v", rec.Code, out)
	}
	rec, out = e.do(t, "GET", "/admin/v1/status", tok, "")
	if rec.Code != 200 || out["runtime"].(map[string]any)["cache_generation"] != 4.0 {
		t.Errorf("status = %d %v", rec.Code, out)
	}
	if rec, out := e.do(t, "GET", "/admin/v1/nope", tok, ""); rec.Code != 404 || errCode(out) != CodeNotFound {
		t.Errorf("unknown endpoint = %d %v", rec.Code, out)
	}
}

func TestAdminStoreDown(t *testing.T) {
	e := newAdminEnv(t)
	e.handler = NewAdminRouter(AdminDeps{
		Store: downStore{e.store}, Settings: fakeSettings{st: e.store}, Runtime: func() AdminRuntime { return e.runtime },
		Verifier: verifierFor(t, e.provider), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	rec, out := e.do(t, "GET", "/admin/v1/me", e.token(t, "uid-1", "owner@example.com", nil), "")
	if rec.Code != 503 || errCode(out) != CodeUnavailable {
		t.Errorf("store down = %d %v", rec.Code, out)
	}
}

type downStore struct{ *fakeAdminStore }

func (downStore) AdminForLogin(context.Context, string, string, bool) (store.Admin, error) {
	return store.Admin{}, errors.New("connection refused")
}

func verifierFor(t *testing.T, p *authtest.Provider) *auth.Verifier {
	t.Helper()
	jwks := auth.NewJWKS(p.URL(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := jwks.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := auth.NewVerifier(jwks, auth.VerifierConfig{Issuer: authtest.Issuer, Audience: authtest.Audience})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAdminUsage(t *testing.T) {
	e := newAdminEnv(t)
	tok := e.token(t, "uid-1", "owner@example.com", nil)
	e.do(t, "POST", "/admin/v1/clients", tok, `{"name":"price bot"}`)
	rec, out := e.do(t, "GET", "/admin/v1/usage?days=7", tok, "")
	if rec.Code != 200 {
		t.Fatalf("usage = %d %s", rec.Code, rec.Body)
	}
	rep := out["report"].(map[string]any)
	totals := rep["totals"].(map[string]any)
	if totals["searches"] != 4.0 || totals["cache_hit_rate"] != 0.75 || totals["cost_usd"] != 0.1 || len(rep["days"].([]any)) != 7 {
		t.Errorf("report = %v", rep)
	}
	budget := rep["budget"].(map[string]any)
	if budget["status"] != "warning" || budget["percent"] != 90.0 || budget["limit_usd"] != 5.0 {
		t.Errorf("budget = %v", budget)
	}
	clients := rep["clients"].([]any)
	if len(clients) != 1 {
		t.Fatalf("clients = %v", rep["clients"])
	}
	c := clients[0].(map[string]any)
	if c["name"] != "price bot" || c["client_id"] != 1.0 || c["searches"] != 3.0 || c["live"] != 1.0 || c["cost_usd_estimate"] != 0.1 {
		t.Errorf("client row = %v", c)
	}
	if len(rep["top_queries"].([]any)) != 1 || out["note"] == "" {
		t.Errorf("top queries / note = %v", out)
	}
	if rec, _ := e.do(t, "GET", "/admin/v1/usage?days=0", tok, ""); rec.Code != 400 {
		t.Errorf("days=0 = %d", rec.Code)
	}
}
