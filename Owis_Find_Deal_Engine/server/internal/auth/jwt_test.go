package auth_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/auth/authtest"
)

func newVerifier(t *testing.T, p *authtest.Provider) (*auth.Verifier, *auth.JWKS) {
	t.Helper()
	jwks := auth.NewJWKS(p.URL(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := jwks.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := auth.NewVerifier(jwks, auth.VerifierConfig{
		Issuer: authtest.Issuer, Audience: authtest.Audience, PlanClaim: "plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	return v, jwks
}

func TestVerifyValid(t *testing.T) {
	p := authtest.New(t)
	v, _ := newVerifier(t, p)

	u, err := v.Verify(context.Background(), p.Token(t, "user-1", jwt.MapClaims{"email": "a@b.c", "plan": "pro"}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if u.ID != "user-1" || u.Email != "a@b.c" || u.Plan != "pro" {
		t.Errorf("user = %+v", u)
	}
}

func TestVerifyRejects(t *testing.T) {
	p := authtest.New(t)
	v, _ := newVerifier(t, p)
	past := time.Now().Add(-2 * time.Hour).Unix()

	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": authtest.Issuer, "aud": authtest.Audience, "sub": "x", "exp": time.Now().Add(time.Hour).Unix(),
	})
	hs.Header["kid"] = "kid-1"
	hsToken, _ := hs.SignedString([]byte("secret"))

	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": authtest.Issuer, "aud": authtest.Audience, "sub": "x", "exp": time.Now().Add(time.Hour).Unix(),
	})
	noneToken, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)

	valid := p.Token(t, "user-1", nil)
	parts := strings.Split(valid, ".")
	tampered := parts[0] + "." + parts[1] + "x." + parts[2]

	tests := map[string]string{
		"expired":        p.Token(t, "u", jwt.MapClaims{"exp": past}),
		"no exp":         p.Token(t, "u", jwt.MapClaims{"exp": nil}),
		"wrong issuer":   p.Token(t, "u", jwt.MapClaims{"iss": "https://evil.example"}),
		"wrong audience": p.Token(t, "u", jwt.MapClaims{"aud": "other-project"}),
		"no sub":         p.Token(t, "", nil),
		"future iat":     p.Token(t, "u", jwt.MapClaims{"iat": time.Now().Add(time.Hour).Unix()}),
		"hmac":           hsToken,
		"alg none":       noneToken,
		"tampered":       tampered,
		"garbage":        "not-a-jwt",
		"empty":          "",
	}
	for name, tok := range tests {
		if _, err := v.Verify(context.Background(), tok); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestNewVerifierRequiresConfig(t *testing.T) {
	if _, err := auth.NewVerifier(nil, auth.VerifierConfig{}); err == nil {
		t.Error("expected error")
	}
}

func TestUserContext(t *testing.T) {
	ctx := auth.WithUser(context.Background(), auth.User{ID: "u"})
	if u, ok := auth.UserFrom(ctx); !ok || u.ID != "u" {
		t.Errorf("UserFrom = %v, %v", u, ok)
	}
	if _, ok := auth.UserFrom(context.Background()); ok {
		t.Error("UserFrom(empty) succeeded")
	}
}
