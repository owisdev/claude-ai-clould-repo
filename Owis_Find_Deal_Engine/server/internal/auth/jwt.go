// Package auth verifies end-user JWTs issued by an external auth provider
// (Firebase Auth, Supabase Auth, Clerk, Auth0) after Google, Apple or
// Facebook sign-in. Verification is stateless: signatures are checked with
// the provider's public keys (JWKS), so no call to the provider is made per
// request.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned for any token that must be rejected.
var ErrInvalidToken = errors.New("invalid token")

// User is the authenticated caller.
type User struct {
	ID    string // the token's "sub"
	Email string
	Plan  string // from the plan claim; empty if absent
}

// KeySource returns the public key for a key ID.
type KeySource interface {
	Key(ctx context.Context, kid string) (any, error)
}

// VerifierConfig configures token checks.
type VerifierConfig struct {
	Issuer    string // required "iss"
	Audience  string // required "aud"
	PlanClaim string // custom claim holding the subscription tier, e.g. "plan"
	Leeway    time.Duration
}

// Verifier validates tokens. Safe for concurrent use.
type Verifier struct {
	keys   KeySource
	cfg    VerifierConfig
	parser *jwt.Parser
}

// NewVerifier returns a Verifier that accepts only asymmetric algorithms.
func NewVerifier(keys KeySource, cfg VerifierConfig) (*Verifier, error) {
	if cfg.Issuer == "" || cfg.Audience == "" {
		return nil, errors.New("auth: issuer and audience are required")
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	return &Verifier{
		keys: keys,
		cfg:  cfg,
		parser: jwt.NewParser(
			// Asymmetric only: "none" and HMAC tokens are rejected.
			jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384"}),
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithAudience(cfg.Audience),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithLeeway(cfg.Leeway),
		),
	}, nil
}

// Verify checks the token's signature and claims and returns its user.
func (v *Verifier) Verify(ctx context.Context, token string) (User, error) {
	claims := jwt.MapClaims{}
	_, err := v.parser.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return v.keys.Key(ctx, kid)
	})
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	sub, _ := claims.GetSubject()
	if sub == "" || len(sub) > 128 {
		return User{}, fmt.Errorf("%w: missing or invalid sub", ErrInvalidToken)
	}
	u := User{ID: sub}
	u.Email, _ = claims["email"].(string)
	if v.cfg.PlanClaim != "" {
		u.Plan, _ = claims[v.cfg.PlanClaim].(string)
	}
	return u, nil
}

type ctxKey struct{}

// WithUser stores the authenticated user in ctx.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom returns the authenticated user from ctx.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}
