// Package apikeys issues and checks the API keys of API clients (own apps
// and AI agents calling the public API). A key is shown once when it is
// made; only its SHA-256 hash and a short prefix are stored.
package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"owis_find_deal_engine/internal/store"
)

// Key format: Prefix + 40 lowercase base32 characters (200 random bits).
const (
	Prefix     = "ofd_live_"
	randLen    = 40
	shownChars = 8 // random characters kept in the stored prefix
)

var encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Errors of Authenticate.
var (
	ErrInvalid = errors.New("invalid API key")
	ErrRevoked = errors.New("API key revoked")
	ErrExpired = errors.New("API key expired")
)

// Generate makes a new key. key is shown to the admin once; stored is
// what the database keeps.
func Generate() (key string, stored store.NewKey) {
	b := make([]byte, randLen*5/8)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	key = Prefix + encoding.EncodeToString(b)
	return key, store.NewKey{Prefix: key[:len(Prefix)+shownChars], Hash: Hash(key)}
}

// Hash is the SHA-256 of a key.
func Hash(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// LooksLikeKey reports whether a bearer token is meant as an API key
// (rather than a user's login token).
func LooksLikeKey(token string) bool { return strings.HasPrefix(token, "ofd_") }

// wellFormed checks a key's shape, so garbage never reaches the database.
func wellFormed(key string) bool {
	r, ok := strings.CutPrefix(key, Prefix)
	if !ok || len(r) != randLen {
		return false
	}
	for _, c := range r {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// Store looks keys up and records their use (store.DB).
type Store interface {
	ClientByKeyHash(ctx context.Context, hash []byte) (store.Client, store.APIKey, error)
	TouchKeys(ctx context.Context, used map[int64]time.Time) error
}

// Caller is an authenticated API client and the key it used.
type Caller struct {
	Client store.Client
	KeyID  int64
}

// UserID is the client's id for quotas, rate limits and logs. App user
// ids never look like this (see IsClientID).
func (c Caller) UserID() string { return ClientUserID(c.Client.ID) }

// ClientUserID is the user id of client id.
func ClientUserID(id int64) string { return fmt.Sprintf("client:%d", id) }

// IsClientID reports whether a user id is an API client's.
func IsClientID(id string) bool { return strings.HasPrefix(id, "client:") }

// CacheTTL is how long a looked-up key is trusted before the database is
// asked again: a revocation by another server instance takes effect within
// this time (at once on the instance that made it, see Forget).
const CacheTTL = 10 * time.Second

const maxCached = 10_000

// Authenticator checks keys, with a short cache in front of the database,
// and records when each key was last used. Safe for concurrent use.
type Authenticator struct {
	store Store
	log   *slog.Logger
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]cached // by hash
	used  map[int64]time.Time
}

type cached struct {
	caller Caller
	err    error      // ErrInvalid for unknown keys, ErrRevoked
	until  *time.Time // the key works until then: client expiry or end of a rotation's grace period
	at     time.Time
}

// NewAuthenticator returns an Authenticator over s.
func NewAuthenticator(s Store, log *slog.Logger) *Authenticator {
	return &Authenticator{store: s, log: log, now: time.Now, cache: map[string]cached{}, used: map[int64]time.Time{}}
}

// Authenticate returns the client of key: ErrInvalid for an unknown or
// malformed key, ErrRevoked / ErrExpired for a key (or client) that no
// longer works; any other error means the check could not be made.
func (a *Authenticator) Authenticate(ctx context.Context, key string) (Caller, error) {
	if !wellFormed(key) {
		return Caller{}, ErrInvalid
	}
	h := string(Hash(key))
	now := a.now()

	a.mu.Lock()
	e, ok := a.cache[h]
	a.mu.Unlock()
	if !ok || now.Sub(e.at) > CacheTTL {
		c, k, err := a.store.ClientByKeyHash(ctx, []byte(h))
		switch {
		case errors.Is(err, store.ErrKeyNotFound):
			e = cached{err: ErrInvalid, at: now}
		case err != nil:
			return Caller{}, err
		default:
			e = cached{caller: Caller{Client: c, KeyID: k.ID}, until: earliest(c.ExpiresAt, k.ExpiresAt), at: now}
			if c.RevokedAt != nil || k.RevokedAt != nil {
				e.err = ErrRevoked
			}
		}
		a.mu.Lock()
		if len(a.cache) >= maxCached {
			clear(a.cache)
		}
		a.cache[h] = e
		a.mu.Unlock()
	}
	if e.err != nil {
		return Caller{}, e.err
	}
	// Checked on every use: expiry can pass while the entry is cached.
	if e.until != nil && !now.Before(*e.until) {
		return Caller{}, ErrExpired
	}
	a.mu.Lock()
	a.used[e.caller.KeyID] = now
	a.mu.Unlock()
	return e.caller, nil
}

func earliest(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.Before(*a)) {
		return b
	}
	return a
}

// Forget drops every cached key, so the next request of each client sees
// the stored state (called after an admin changes a client).
func (a *Authenticator) Forget() {
	a.mu.Lock()
	clear(a.cache)
	a.mu.Unlock()
}

// Run saves when keys were last used every interval, and once more when
// ctx ends.
func (a *Authenticator) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			a.Flush(fctx)
			cancel()
			return
		case <-t.C:
			a.Flush(ctx)
		}
	}
}

// Flush saves the recorded key use; on failure it is kept for next time.
func (a *Authenticator) Flush(ctx context.Context) {
	a.mu.Lock()
	used := a.used
	a.used = map[int64]time.Time{}
	a.mu.Unlock()
	if len(used) == 0 {
		return
	}
	if err := a.store.TouchKeys(ctx, used); err != nil {
		a.log.Warn("saving API key use failed", "err", err)
		a.mu.Lock()
		for id, at := range used {
			if at.After(a.used[id]) {
				a.used[id] = at
			}
		}
		a.mu.Unlock()
	}
}
