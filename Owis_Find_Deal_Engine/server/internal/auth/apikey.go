// Package auth identifies client applications by API key.
//
// Keys are never stored: the server only knows their SHA-256 hashes, so a
// leaked config does not leak usable keys.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeyPrefix marks keys issued by this service.
const KeyPrefix = "ofde_"

// Client is an application allowed to call the API.
type Client struct {
	Name string
}

// KeyStore maps key hashes to clients. Read-only after creation.
type KeyStore struct {
	byHash map[[sha256.Size]byte]Client
}

// ParseKeyStore parses "name:sha256hex,name2:sha256hex".
func ParseKeyStore(spec string) (*KeyStore, error) {
	ks := &KeyStore{byHash: make(map[[sha256.Size]byte]Client)}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, hexHash, ok := strings.Cut(entry, ":")
		if !ok || name == "" {
			return nil, fmt.Errorf("auth: entry %q must be name:sha256hex", entry)
		}
		raw, err := hex.DecodeString(hexHash)
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("auth: entry %q has an invalid SHA-256 hash", name)
		}
		var h [sha256.Size]byte
		copy(h[:], raw)
		ks.byHash[h] = Client{Name: name}
	}
	if len(ks.byHash) == 0 {
		return nil, errors.New("auth: no API keys configured")
	}
	return ks, nil
}

// Lookup returns the client owning key.
func (ks *KeyStore) Lookup(key string) (Client, bool) {
	if key == "" {
		return Client{}, false
	}
	c, ok := ks.byHash[sha256.Sum256([]byte(key))]
	return c, ok
}

// GenerateKey returns a new random key and its hex SHA-256 hash.
func GenerateKey() (key, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	key = KeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return key, HashKey(key), nil
}

// HashKey returns the hex SHA-256 of key.
func HashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

type ctxKey struct{}

// WithClient stores the authenticated client in ctx.
func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// ClientFrom returns the authenticated client from ctx.
func ClientFrom(ctx context.Context) (Client, bool) {
	c, ok := ctx.Value(ctxKey{}).(Client)
	return c, ok
}
