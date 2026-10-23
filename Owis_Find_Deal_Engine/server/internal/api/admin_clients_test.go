package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/store"
)

// fakeClients is an in-memory store.DB for API clients and keys.
type fakeClients struct {
	mu      sync.Mutex
	clients []store.Client
	hashes  map[int64][]byte // key id -> hash
	nextKey int64
	changed int
}

func newFakeClients() *fakeClients { return &fakeClients{hashes: map[int64][]byte{}} }

func (f *fakeClients) newKey(clientID int64, k store.NewKey, by string) store.APIKey {
	f.nextKey++
	f.hashes[f.nextKey] = k.Hash
	return store.APIKey{ID: f.nextKey, ClientID: clientID, Prefix: k.Prefix, CreatedAt: time.Now(), CreatedBy: by}
}

func (f *fakeClients) CreateClient(_ context.Context, c store.Client, k store.NewKey, by string) (store.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.ID = int64(len(f.clients) + 1)
	c.CreatedAt, c.CreatedBy, c.UpdatedAt, c.UpdatedBy = time.Now(), by, time.Now(), by
	c.Keys = []store.APIKey{f.newKey(c.ID, k, by)}
	f.clients = append(f.clients, c)
	return c, nil
}

func (f *fakeClients) Clients(context.Context) ([]store.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Client(nil), f.clients...), nil
}

func (f *fakeClients) Client(_ context.Context, id int64) (store.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id < 1 || int(id) > len(f.clients) {
		return store.Client{}, store.ErrClientNotFound
	}
	c := f.clients[id-1]
	c.Keys = append([]store.APIKey(nil), c.Keys...)
	return c, nil
}

func (f *fakeClients) UpdateClient(_ context.Context, c store.Client, by string) (store.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	old := &f.clients[c.ID-1]
	if old.RevokedAt != nil {
		return store.Client{}, store.ErrClientRevoked
	}
	c.Keys, c.UpdatedBy = old.Keys, by
	*old = c
	return c, nil
}

func (f *fakeClients) RotateKey(_ context.Context, id int64, k store.NewKey, until time.Time, by string) (store.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &f.clients[id-1]
	if c.RevokedAt != nil {
		return store.APIKey{}, store.ErrClientRevoked
	}
	for i := range c.Keys {
		if c.Keys[i].RevokedAt == nil && (c.Keys[i].ExpiresAt == nil || c.Keys[i].ExpiresAt.After(until)) {
			c.Keys[i].ExpiresAt = &until
		}
	}
	nk := f.newKey(id, k, by)
	c.Keys = append([]store.APIKey{nk}, c.Keys...)
	return nk, nil
}

func (f *fakeClients) RevokeClient(_ context.Context, id int64, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &f.clients[id-1]
	if c.RevokedAt != nil {
		return store.ErrClientRevoked
	}
	now := time.Now()
	c.RevokedAt, c.RevokedBy = &now, by
	return nil
}

func (f *fakeClients) RevokeKey(_ context.Context, clientID, keyID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &f.clients[clientID-1]
	for i := range c.Keys {
		if c.Keys[i].ID == keyID {
			now := time.Now()
			c.Keys[i].RevokedAt = &now
			return nil
		}
	}
	return store.ErrKeyNotFound
}

func (f *fakeClients) SetWebhookSalt(_ context.Context, id int64, salt []byte, prefix, by string) (store.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id < 1 || int(id) > len(f.clients) {
		return store.Client{}, store.ErrClientNotFound
	}
	c := &f.clients[id-1]
	if c.RevokedAt != nil {
		return store.Client{}, store.ErrClientRevoked
	}
	now := time.Now()
	c.WebhookSalt, c.WebhookSecretPrefix, c.WebhookSecretAt, c.UpdatedBy = salt, prefix, &now, by
	return *c, nil
}

func (f *fakeClients) ClientByKeyHash(_ context.Context, hash []byte) (store.Client, store.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.clients {
		for _, k := range c.Keys {
			if bytes.Equal(f.hashes[k.ID], hash) {
				return c, k, nil
			}
		}
	}
	return store.Client{}, store.APIKey{}, store.ErrKeyNotFound
}

func (f *fakeClients) TouchKeys(context.Context, map[int64]time.Time) error { return nil }

func TestAdminClients(t *testing.T) {
	e := newAdminEnv(t)
	tok := e.token(t, "uid-1", "owner@example.com", nil)

	// Defaults; the key is in the answer once, never in the audit log.
	rec, out := e.do(t, "POST", "/admin/v1/clients", tok, `{"name":"my agent","owner":"ops"}`)
	if rec.Code != 201 {
		t.Fatalf("create = %d %v", rec.Code, out)
	}
	key, _ := out["key"].(string)
	c := out["client"].(map[string]any)
	if !strings.HasPrefix(key, apikeys.Prefix) || c["status"] != "active" || c["daily_quota"] != float64(apikeys.DefaultDailyQuota) ||
		len(c["scopes"].([]any)) != 2 || len(c["countries"].([]any)) != 0 || c["id"] != float64(1) {
		t.Fatalf("created = %v", out)
	}
	k := c["keys"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(key, k["prefix"].(string)) || k["status"] != "active" {
		t.Errorf("key view = %v", k)
	}
	if a := e.store.lastAudit(); a.Action != "clients.create" || a.Target != "client:1 my agent" || strings.Contains(string(a.Detail), key) {
		t.Errorf("audit = %+v %s", a, a.Detail)
	}
	if e.clients.changed != 1 {
		t.Error("key cache not dropped after create")
	}

	// The list never holds keys.
	rec, out = e.do(t, "GET", "/admin/v1/clients", tok, "")
	if rec.Code != 200 || len(out["clients"].([]any)) != 1 || strings.Contains(rec.Body.String(), key) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}

	for body, want := range map[string]string{
		`{"owner":"x"}`:                                    "name is required",
		`{"name":"a","scopes":["admin"]}`:                  "scope",
		`{"name":"a","countries":["deu"]}`:                 "country",
		`{"name":"a","daily_quota":0}`:                     "daily_quota",
		`{"name":"a","ip_allow":["nope"]}`:                 "ip_allow",
		`{"name":"a","expires_at":"2001-01-01T00:00:00Z"}`: "future",
		`{"name":"a","expires_at":"tomorrow"}`:             "expires_at",
		`{"name":"a","key":"ofd_live_mine"}`:               "unknown field",
	} {
		if rec, out := e.do(t, "POST", "/admin/v1/clients", tok, body); rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("create %s = %d %v, want 400 %q", body, rec.Code, out, want)
		}
	}

	// Change: only the fields given; normalized.
	rec, out = e.do(t, "PATCH", "/admin/v1/clients/1", tok,
		`{"daily_quota":5,"countries":["JOR"],"ip_allow":["203.0.113.9"],"expires_at":"2099-01-01T00:00:00Z"}`)
	if rec.Code != 200 || out["daily_quota"] != float64(5) || out["countries"].([]any)[0] != "jor" ||
		out["ip_allow"].([]any)[0] != "203.0.113.9/32" || out["name"] != "my agent" || out["expires_at"] == nil {
		t.Fatalf("change = %d %v", rec.Code, out)
	}
	if a := e.store.lastAudit(); a.Action != "clients.change" || !strings.Contains(string(a.Detail), `"fields":["daily_quota","countries","ip_allow","expires_at"]`) {
		t.Errorf("audit = %+v %s", a, a.Detail)
	}
	if rec, out := e.do(t, "PATCH", "/admin/v1/clients/1", tok, `{"expires_at":null}`); rec.Code != 200 || out["expires_at"] != nil {
		t.Errorf("remove expiry = %d %v", rec.Code, out)
	}
	if rec, _ := e.do(t, "PATCH", "/admin/v1/clients/1", tok, `{}`); rec.Code != 400 {
		t.Errorf("empty change = %d", rec.Code)
	}
	if rec, _ := e.do(t, "PATCH", "/admin/v1/clients/9", tok, `{"name":"x"}`); rec.Code != 404 {
		t.Errorf("unknown client = %d", rec.Code)
	}
	if rec, _ := e.do(t, "GET", "/admin/v1/clients/abc", tok, ""); rec.Code != 404 {
		t.Errorf("bad id = %d", rec.Code)
	}

	// Rotate: new key, old one in its grace period.
	if rec, _ := e.do(t, "POST", "/admin/v1/clients/1/rotate", tok, `{"grace":"999h"}`); rec.Code != 400 {
		t.Errorf("grace too long = %d", rec.Code)
	}
	rec, out = e.do(t, "POST", "/admin/v1/clients/1/rotate", tok, `{"grace":"1h"}`)
	newKey, _ := out["key"].(string)
	if rec.Code != 201 || newKey == key || !strings.HasPrefix(newKey, apikeys.Prefix) {
		t.Fatalf("rotate = %d %v", rec.Code, out)
	}
	keys := out["client"].(map[string]any)["keys"].([]any)
	if len(keys) != 2 || keys[0].(map[string]any)["status"] != "active" || keys[1].(map[string]any)["status"] != "grace" {
		t.Errorf("keys after rotation = %v", keys)
	}
	if a := e.store.lastAudit(); a.Action != "clients.rotate" || strings.Contains(string(a.Detail), newKey) || !strings.Contains(string(a.Detail), `"grace":"1h0m0s"`) {
		t.Errorf("audit = %+v %s", a, a.Detail)
	}
	// Rotate without a body: default grace.
	if rec, _ := e.do(t, "POST", "/admin/v1/clients/1/rotate", tok, ""); rec.Code != 201 {
		t.Errorf("rotate without body = %d", rec.Code)
	}

	// Revoke one key, then the client.
	oldID := int64(keys[1].(map[string]any)["id"].(float64))
	if rec, _ := e.do(t, "POST", "/admin/v1/clients/1/keys/99/revoke", tok, ""); rec.Code != 404 {
		t.Errorf("unknown key = %d", rec.Code)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/clients/1/keys/"+strconv.FormatInt(oldID, 10)+"/revoke", tok, ""); rec.Code != 200 {
		t.Errorf("revoke key = %d", rec.Code)
	}
	if a := e.store.lastAudit(); a.Action != "clients.key_revoke" {
		t.Errorf("audit = %+v", a)
	}
	if rec, _ := e.do(t, "POST", "/admin/v1/clients/1/revoke", tok, ""); rec.Code != 200 {
		t.Errorf("revoke = %d", rec.Code)
	}
	if rec, out := e.do(t, "GET", "/admin/v1/clients/1", tok, ""); rec.Code != 200 || out["status"] != "revoked" || out["revoked_by"] != "admin:owner@example.com" {
		t.Errorf("revoked client = %d %v", rec.Code, out)
	}
	for _, path := range []string{"/admin/v1/clients/1/revoke", "/admin/v1/clients/1/rotate"} {
		if rec, out := e.do(t, "POST", path, tok, ""); rec.Code != 409 || errCode(out) != CodeConflict {
			t.Errorf("%s on a revoked client = %d %v", path, rec.Code, out)
		}
	}
	if rec, _ := e.do(t, "PATCH", "/admin/v1/clients/1", tok, `{"name":"again"}`); rec.Code != 409 {
		t.Errorf("change a revoked client = %d", rec.Code)
	}
	if e.clients.changed != 7 {
		t.Errorf("key cache dropped %d times, want 7 (once per change)", e.clients.changed)
	}
}

// TestAPIKeysOnPublicAPI: a key made in the admin API works on the public
// search, with its scopes, countries, address rules and quota.
func TestAPIKeysOnPublicAPI(t *testing.T) {
	admin := newAdminEnv(t)
	tok := admin.token(t, "uid-1", "owner@example.com", nil)
	authn := apikeys.NewAuthenticator(admin.clients, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pub := newEnv(t, envOpts{keys: authn})

	create := func(body string) string {
		rec, out := admin.do(t, "POST", "/admin/v1/clients", tok, body)
		if rec.Code != 201 {
			t.Fatalf("create %s = %d %v", body, rec.Code, out)
		}
		return out["key"].(string)
	}
	key := create(`{"name":"agent","daily_quota":2,"countries":["jor"]}`)

	rec := pub.do("POST", "/api/v1/search", key, searchBody)
	if rec.Code != 200 || rec.Header().Get("X-Plan") != "api" || rec.Header().Get("X-RateLimit-Limit") != "2" ||
		rec.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("search with key = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	_ = pub.do("POST", "/api/v1/search", key, searchBody)
	if rec := pub.do("POST", "/api/v1/search", key, searchBody); rec.Code != 429 || errorCode(t, rec) != CodeQuotaExceeded {
		t.Errorf("over the client quota = %d %s, want 429 (never 402 for clients)", rec.Code, rec.Body)
	}
	// Its own counter: another client is not affected.
	other := create(`{"name":"other"}`)
	if rec := pub.do("POST", "/api/v1/search", other, searchBody); rec.Code != 200 {
		t.Errorf("other client = %d", rec.Code)
	}

	if rec := pub.do("POST", "/api/v1/search", key, `{"title":"s pen","country":"usa"}`); rec.Code != 403 || errorCode(t, rec) != CodeCountryNotAllowed {
		t.Errorf("country not allowed = %d %s", rec.Code, rec.Body)
	}
	noSearch := create(`{"name":"lister","scopes":["countries"]}`)
	if rec := pub.do("POST", "/api/v1/search", noSearch, searchBody); rec.Code != 403 || !strings.Contains(rec.Body.String(), "scope") {
		t.Errorf("without scope = %d %s", rec.Code, rec.Body)
	}
	walled := create(`{"name":"walled","ip_allow":["203.0.113.0/24"]}`)
	if rec := pub.do("POST", "/api/v1/search", walled, searchBody); rec.Code != 403 {
		t.Errorf("address not allowed = %d %s", rec.Code, rec.Body) // httptest: 192.0.2.1
	}

	bad, _ := apikeys.Generate()
	if rec := pub.do("POST", "/api/v1/search", bad, searchBody); rec.Code != 401 || !strings.Contains(rec.Body.String(), "invalid API key") {
		t.Errorf("unknown key = %d %s", rec.Code, rec.Body)
	}
	if rec := pub.do("POST", "/api/v1/search", "ofd_live_x", searchBody); rec.Code != 401 {
		t.Errorf("malformed key = %d", rec.Code)
	}

	// Rotation: both keys work during the grace period; revoking the
	// client stops all at once (the admin API drops the cache).
	rec2, out := admin.do(t, "POST", "/admin/v1/clients/2/rotate", tok, `{"grace":"1h"}`)
	if rec2.Code != 201 {
		t.Fatal(rec2.Body)
	}
	authn.Forget()
	rotated := out["key"].(string)
	for _, k := range []string{other, rotated} {
		if rec := pub.do("POST", "/api/v1/search", k, searchBody); rec.Code != 200 {
			t.Errorf("key in grace / new key = %d %s", rec.Code, rec.Body)
		}
	}
	admin.do(t, "POST", "/admin/v1/clients/2/revoke", tok, "")
	authn.Forget()
	if rec := pub.do("POST", "/api/v1/search", rotated, searchBody); rec.Code != 401 || !strings.Contains(rec.Body.String(), "revoked") {
		t.Errorf("revoked = %d %s", rec.Code, rec.Body)
	}

	// Without a key store, keys are refused; JWTs keep working.
	plain := newEnv(t, envOpts{})
	if rec := plain.do("POST", "/api/v1/search", key, searchBody); rec.Code != 401 {
		t.Errorf("keys without a database = %d", rec.Code)
	}
	if rec := plain.do("POST", "/api/v1/search", plain.provider.Token(t, "u1", nil), searchBody); rec.Code != 200 {
		t.Errorf("user = %d", rec.Code)
	}
	// A login token can never act as a client.
	if rec := plain.do("POST", "/api/v1/search", plain.provider.Token(t, "client:1", nil), searchBody); rec.Code != 401 {
		t.Errorf("token with a client subject = %d", rec.Code)
	}
}

// TestClientRateLimit: each client has its own burst limit.
func TestClientRateLimit(t *testing.T) {
	admin := newAdminEnv(t)
	tok := admin.token(t, "uid-1", "owner@example.com", nil)
	authn := apikeys.NewAuthenticator(admin.clients, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pub := newEnv(t, envOpts{keys: authn})
	_, out := admin.do(t, "POST", "/admin/v1/clients", tok, `{"name":"slow","rate_limit_rps":0.01,"rate_limit_burst":1}`)
	key := out["key"].(string)
	if rec := pub.do("POST", "/api/v1/search", key, searchBody); rec.Code != 200 {
		t.Fatalf("first = %d", rec.Code)
	}
	if rec := pub.do("POST", "/api/v1/search", key, searchBody); rec.Code != 429 || errorCode(t, rec) != CodeRateLimited || rec.Header().Get("Retry-After") == "" {
		t.Errorf("second = %d %s", rec.Code, rec.Body)
	}
}
