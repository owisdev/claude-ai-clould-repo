package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWKS caches the public signing keys published by the auth provider
// (Firebase, Supabase, Clerk, Auth0, ...). Tokens are verified locally with
// these keys; the provider is only contacted to refresh them.
type JWKS struct {
	url    string
	client *http.Client
	log    *slog.Logger

	mu          sync.RWMutex
	keys        map[string]crypto.PublicKey // by "kid"
	lastRefresh time.Time

	refreshMu  sync.Mutex // one refresh at a time
	minRefresh time.Duration
}

// NewJWKS creates a key cache for url. Call Refresh once at startup and
// Run in a goroutine to keep keys current.
func NewJWKS(url string, log *slog.Logger) *JWKS {
	return &JWKS{
		url:        url,
		client:     &http.Client{Timeout: 10 * time.Second},
		log:        log,
		keys:       map[string]crypto.PublicKey{},
		minRefresh: time.Minute,
	}
}

// Key returns the key for kid. An unknown kid triggers a refresh (at most
// once per minute) so key rotations are picked up without a restart.
func (j *JWKS) Key(ctx context.Context, kid string) (any, error) {
	if k, ok := j.lookup(kid); ok {
		return k, nil
	}
	j.mu.RLock()
	recent := time.Since(j.lastRefresh) < j.minRefresh
	j.mu.RUnlock()
	if !recent {
		if err := j.Refresh(ctx); err != nil {
			return nil, err
		}
		if k, ok := j.lookup(kid); ok {
			return k, nil
		}
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (j *JWKS) lookup(kid string) (crypto.PublicKey, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	k, ok := j.keys[kid]
	return k, ok
}

// Refresh downloads the key set. Concurrent callers share one download.
func (j *JWKS) Refresh(ctx context.Context) error {
	j.refreshMu.Lock()
	defer j.refreshMu.Unlock()

	// Another caller may have refreshed while we waited for the lock.
	j.mu.RLock()
	fresh := time.Since(j.lastRefresh) < time.Second
	j.mu.RUnlock()
	if fresh {
		return nil
	}

	keys, err := j.fetch(ctx)
	if err != nil {
		return err
	}
	j.mu.Lock()
	j.keys = keys
	j.lastRefresh = time.Now()
	j.mu.Unlock()
	return nil
}

// Run refreshes the keys every interval until ctx is done.
func (j *JWKS) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := j.Refresh(ctx); err != nil {
				j.log.Warn("jwks refresh failed", "err", err)
			}
		}
	}
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (j *JWKS) fetch(ctx context.Context) (map[string]crypto.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: fetch: status %d", resp.StatusCode)
	}

	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("jwks: decode: %w", err)
	}
	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pub, err := k.publicKey()
		if err != nil {
			j.log.Warn("jwks: skipping key", "kid", k.Kid, "err", err)
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks: no usable signing keys")
	}
	return keys, nil
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64int(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64int(k.E)
		if err != nil {
			return nil, err
		}
		if !e.IsInt64() || e.Int64() < 3 || n.BitLen() < 2048 {
			return nil, errors.New("weak or invalid RSA key")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		default:
			return nil, fmt.Errorf("unsupported curve %q", k.Crv)
		}
		x, err := b64int(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64int(k.Y)
		if err != nil {
			return nil, err
		}
		pub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("EC point not on curve")
		}
		return pub, nil
	default:
		return nil, fmt.Errorf("unsupported key type %q", k.Kty)
	}
}

func b64int(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil, errors.New("invalid base64url integer")
	}
	return new(big.Int).SetBytes(b), nil
}
