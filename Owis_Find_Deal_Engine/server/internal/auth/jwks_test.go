package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
	"time"

	"owis_find_deal_engine/internal/auth/authtest"
)

func newTestJWKS(t *testing.T, p *authtest.Provider, minRefresh time.Duration) *JWKS {
	t.Helper()
	j := NewJWKS(p.URL(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	j.minRefresh = minRefresh
	if err := j.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestJWKSPicksUpRotatedKey(t *testing.T) {
	p := authtest.New(t)
	j := newTestJWKS(t, p, 0)
	v, _ := NewVerifier(j, VerifierConfig{Issuer: authtest.Issuer, Audience: authtest.Audience})

	time.Sleep(1100 * time.Millisecond) // past Refresh's de-duplication window
	p.Rotate(t)
	if _, err := v.Verify(context.Background(), p.Token(t, "u", nil)); err != nil {
		t.Fatalf("token signed with rotated key rejected: %v", err)
	}
	if p.Fetch != 2 {
		t.Errorf("JWKS fetches = %d, want 2", p.Fetch)
	}
}

func TestJWKSUnknownKidIsRateLimited(t *testing.T) {
	p := authtest.New(t)
	j := newTestJWKS(t, p, time.Minute)

	for i := 0; i < 5; i++ {
		if _, err := j.Key(context.Background(), "attacker-kid"); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if p.Fetch != 1 {
		t.Errorf("JWKS fetches = %d, want 1 (no refetch storm)", p.Fetch)
	}
}

func TestJWKParsesECKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k := jwk{
		Kid: "ec", Kty: "EC", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(key.X.Bytes()),
		Y: base64.RawURLEncoding.EncodeToString(key.Y.Bytes()),
	}
	pub, err := k.publicKey()
	if err != nil {
		t.Fatalf("publicKey: %v", err)
	}
	if !pub.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
		t.Error("parsed key differs")
	}

	k.Crv = "P-521"
	if _, err := k.publicKey(); err == nil {
		t.Error("unsupported curve accepted")
	}
	if _, err := (jwk{Kty: "oct"}).publicKey(); err == nil {
		t.Error("symmetric key accepted")
	}
}
