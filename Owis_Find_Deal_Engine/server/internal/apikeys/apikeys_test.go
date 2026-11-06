package apikeys

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"owis_find_deal_engine/internal/store"
)

type fakeStore struct {
	mu      sync.Mutex
	clients map[string]store.Client // by hash
	keys    map[string]store.APIKey
	lookups int
	touched map[int64]time.Time
	fail    error
}

func (f *fakeStore) ClientByKeyHash(_ context.Context, hash []byte) (store.Client, store.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.fail != nil {
		return store.Client{}, store.APIKey{}, f.fail
	}
	c, ok := f.clients[string(hash)]
	if !ok {
		return store.Client{}, store.APIKey{}, store.ErrKeyNotFound
	}
	return c, f.keys[string(hash)], nil
}

func (f *fakeStore) TouchKeys(_ context.Context, used map[int64]time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	for id, at := range used {
		f.touched[id] = at
	}
	return nil
}

func (f *fakeStore) add(c store.Client, k store.APIKey) string {
	key, nk := Generate()
	f.clients[string(nk.Hash)] = c
	f.keys[string(nk.Hash)] = k
	return key
}

func newFake() *fakeStore {
	return &fakeStore{clients: map[string]store.Client{}, keys: map[string]store.APIKey{}, touched: map[int64]time.Time{}}
}

func TestGenerate(t *testing.T) {
	k1, s1 := Generate()
	k2, _ := Generate()
	if k1 == k2 || !strings.HasPrefix(k1, Prefix) || len(k1) != len(Prefix)+randLen || !wellFormed(k1) {
		t.Fatalf("keys %q %q", k1, k2)
	}
	if !strings.HasPrefix(k1, s1.Prefix) || len(s1.Prefix) != len(Prefix)+shownChars || !bytes.Equal(s1.Hash, Hash(k1)) || len(s1.Hash) != 32 {
		t.Errorf("stored = %+v", s1)
	}
	if strings.Contains(s1.Prefix, k1[len(s1.Prefix):]) {
		t.Error("prefix holds the secret part")
	}
	if !LooksLikeKey(k1) || LooksLikeKey("eyJhbGciOi.x.y") {
		t.Error("LooksLikeKey")
	}
	for _, bad := range []string{"", Prefix, k1 + "a", strings.ToUpper(k1), Prefix + strings.Repeat("1", randLen), "ofd_test_" + k1[len(Prefix):]} {
		if wellFormed(bad) {
			t.Errorf("wellFormed(%q)", bad)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	f := newFake()
	now := time.Now()
	a := NewAuthenticator(f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.now = func() time.Time { return now }
	past, soon := now.Add(-time.Minute), now.Add(CacheTTL/2)

	good := f.add(store.Client{ID: 1}, store.APIKey{ID: 10})
	revokedClient := f.add(store.Client{ID: 2, RevokedAt: &past}, store.APIKey{ID: 20})
	revokedKey := f.add(store.Client{ID: 3}, store.APIKey{ID: 30, RevokedAt: &past})
	expiredClient := f.add(store.Client{ID: 4, ExpiresAt: &past}, store.APIKey{ID: 40})
	graceKey := f.add(store.Client{ID: 5}, store.APIKey{ID: 50, ExpiresAt: &soon})
	unknown, _ := Generate()

	ctx := context.Background()
	for key, want := range map[string]error{
		good: nil, revokedClient: ErrRevoked, revokedKey: ErrRevoked, expiredClient: ErrExpired,
		graceKey: nil, unknown: ErrInvalid, "ofd_live_short": ErrInvalid,
	} {
		if _, err := a.Authenticate(ctx, key); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", key[:min(len(key), 17)], err, want)
		}
	}
	if f.lookups != 6 {
		t.Errorf("%d lookups, want 6 (malformed keys never reach the store)", f.lookups)
	}

	// Cached: no new lookups within the TTL, unknown keys included.
	c, err := a.Authenticate(ctx, good)
	if err != nil || c.Client.ID != 1 || c.KeyID != 10 || c.UserID() != "client:1" {
		t.Fatalf("good = %+v, %v", c, err)
	}
	_, _ = a.Authenticate(ctx, unknown)
	if f.lookups != 6 {
		t.Errorf("cache missed: %d lookups", f.lookups)
	}

	// The grace period ends while the key is cached.
	now = soon
	if _, err := a.Authenticate(ctx, graceKey); !errors.Is(err, ErrExpired) {
		t.Errorf("after the grace period: %v", err)
	}

	// After the TTL, or Forget, the store is asked again.
	now = now.Add(CacheTTL + time.Second)
	_, _ = a.Authenticate(ctx, good)
	a.Forget()
	_, _ = a.Authenticate(ctx, good)
	if f.lookups != 8 {
		t.Errorf("%d lookups, want 8", f.lookups)
	}

	// Store down: an error that is not a verdict on the key.
	a.Forget()
	f.fail = errors.New("db down")
	if _, err := a.Authenticate(ctx, good); err == nil || errors.Is(err, ErrInvalid) {
		t.Errorf("store down: %v", err)
	}

	// Use is saved; a failed save is kept for the next one.
	a.Flush(ctx)
	if len(f.touched) != 0 {
		t.Fatal("saved although the store is down")
	}
	f.fail = nil
	a.Flush(ctx)
	if !f.touched[10].Equal(now) || f.touched[50].IsZero() || len(f.touched) != 2 {
		t.Errorf("touched = %v", f.touched)
	}
}

func TestNormalize(t *testing.T) {
	now := time.Now()
	countries := []string{"jor", "ksa", "usa"}
	ok := func() store.Client {
		return store.Client{Name: " agent ", Scopes: []string{"search", "Search", "countries"}, DailyQuota: 10, RateRPS: 1, RateBurst: 2,
			Countries: []string{"USA", "jor", "usa"}, IPAllow: []string{"203.0.113.7", "10.1.2.3/8", "2001:db8::1/32", "::ffff:198.51.100.1"}}
	}
	c := ok()
	if err := Normalize(&c, countries, now, true); err != nil {
		t.Fatal(err)
	}
	if c.Name != "agent" || strings.Join(c.Scopes, ",") != "countries,search" || strings.Join(c.Countries, ",") != "jor,usa" ||
		strings.Join(c.IPAllow, ",") != "203.0.113.7/32,10.0.0.0/8,2001:db8::/32,198.51.100.1/32" {
		t.Errorf("normalized = %+v", c)
	}
	past := now.Add(-time.Hour)
	for name, change := range map[string]func(*store.Client){
		"no name":       func(c *store.Client) { c.Name = " " },
		"no scope":      func(c *store.Client) { c.Scopes = nil },
		"bad scope":     func(c *store.Client) { c.Scopes = []string{"admin"} },
		"quota 0":       func(c *store.Client) { c.DailyQuota = 0 },
		"quota huge":    func(c *store.Client) { c.DailyQuota = MaxDailyQuota + 1 },
		"rps 0":         func(c *store.Client) { c.RateRPS = 0 },
		"burst 0":       func(c *store.Client) { c.RateBurst = 0 },
		"country":       func(c *store.Client) { c.Countries = []string{"deu"} },
		"ip":            func(c *store.Client) { c.IPAllow = []string{"10.0.0.0/33"} },
		"host name":     func(c *store.Client) { c.IPAllow = []string{"example.com"} },
		"past expiry":   func(c *store.Client) { c.ExpiresAt = &past },
		"long name":     func(c *store.Client) { c.Name = strings.Repeat("x", maxNameLen+1) },
		"many ip rules": func(c *store.Client) { c.IPAllow = make([]string, maxIPRules+1) },
	} {
		c := ok()
		change(&c)
		if err := Normalize(&c, countries, now, true); !errors.Is(err, ErrInvalidClient) {
			t.Errorf("%s: %v", name, err)
		}
	}
	c = ok()
	c.ExpiresAt = &past
	if err := Normalize(&c, countries, now, false); err != nil {
		t.Errorf("unchecked past expiry: %v", err)
	}
}

func TestAllowed(t *testing.T) {
	c := store.Client{IPAllow: []string{"10.0.0.0/8", "2001:db8::/32"}, Countries: []string{"jor"}, Scopes: []string{"search"}}
	for ip, want := range map[string]bool{"10.2.3.4": true, "::ffff:10.2.3.4": true, "11.0.0.1": false, "2001:db8::5": true, "bad": false} {
		if AllowedIP(c, ip) != want {
			t.Errorf("AllowedIP(%s) != %v", ip, want)
		}
	}
	if !AllowedIP(store.Client{}, "1.2.3.4") {
		t.Error("empty list must allow any address")
	}
	if !AllowedCountry(c, "JOR") || AllowedCountry(c, "usa") || !AllowedCountry(store.Client{}, "usa") {
		t.Error("AllowedCountry")
	}
	if !HasScope(c, ScopeSearch) || HasScope(c, ScopeCountries) {
		t.Error("HasScope")
	}
}
