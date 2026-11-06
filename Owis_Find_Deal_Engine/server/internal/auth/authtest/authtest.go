// Package authtest provides a fake auth provider (JWKS server + token
// signer) for tests.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	Issuer   = "https://securetoken.google.com/test-project"
	Audience = "test-project"
)

// Provider serves a JWKS and signs tokens with its current key.
type Provider struct {
	Server *httptest.Server

	mu    sync.Mutex
	kid   string
	key   *rsa.PrivateKey
	keys  map[string]*rsa.PublicKey
	Fetch int // number of JWKS downloads
}

// New starts a provider with one RSA key.
func New(t *testing.T) *Provider {
	t.Helper()
	p := &Provider{keys: map[string]*rsa.PublicKey{}}
	p.Rotate(t)
	p.Server = httptest.NewServer(http.HandlerFunc(p.serveJWKS))
	t.Cleanup(p.Server.Close)
	return p
}

// URL is the JWKS endpoint.
func (p *Provider) URL() string { return p.Server.URL }

// Rotate adds a new signing key and uses it for new tokens.
func (p *Provider) Rotate(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kid = "kid-" + big.NewInt(int64(len(p.keys)+1)).String()
	p.key = key
	p.keys[p.kid] = &key.PublicKey
}

func (p *Provider) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Fetch++
	type jwk struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	for kid, k := range p.keys {
		set.Keys = append(set.Keys, jwk{
			Kid: kid, Kty: "RSA", Alg: "RS256", Use: "sig",
			N: base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(set)
}

// Token returns a valid RS256 token for sub; extra claims override defaults.
func (p *Provider) Token(t *testing.T, sub string, extra jwt.MapClaims) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": Issuer,
		"aud": Audience,
		"sub": sub,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	p.mu.Lock()
	kid, key := p.kid, p.key
	p.mu.Unlock()

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
