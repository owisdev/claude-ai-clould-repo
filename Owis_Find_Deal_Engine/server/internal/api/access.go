package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/store"
	"owis_find_deal_engine/internal/usage"
)

// TokenVerifier checks a bearer token and returns its user.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (auth.User, error)
}

// KeyAuthenticator checks API keys (apikeys.Authenticator).
type KeyAuthenticator interface {
	Authenticate(ctx context.Context, key string) (apikeys.Caller, error)
}

type clientKey struct{}

// clientFrom returns the API client making the request, if it is one.
func clientFrom(ctx context.Context) (store.Client, bool) {
	c, ok := ctx.Value(clientKey{}).(store.Client)
	return c, ok
}

// requireCaller accepts "Authorization: Bearer <token>" with either an app
// user's login token (JWT) or an API client's key (ofd_live_...) that has
// scope. keys nil = API keys are not accepted (no database).
func requireCaller(v TokenVerifier, keys KeyAuthenticator, scope string, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			token = strings.TrimSpace(token)
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
				unauthorized(w, "missing bearer token")
				return
			}
			ctx := r.Context()
			var user auth.User
			if apikeys.LooksLikeKey(token) {
				if keys == nil {
					unauthorized(w, "API keys are not enabled on this server")
					return
				}
				caller, err := keys.Authenticate(ctx, token)
				switch {
				case errors.Is(err, apikeys.ErrInvalid), errors.Is(err, apikeys.ErrRevoked), errors.Is(err, apikeys.ErrExpired):
					unauthorized(w, err.Error())
					return
				case err != nil:
					log.ErrorContext(ctx, "API key check failed", "err", err)
					writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "service temporarily unavailable, please retry")
					return
				}
				c := caller.Client
				if rec, ok := w.(*statusRecorder); ok {
					rec.user = caller.UserID()
				}
				if !apikeys.AllowedIP(c, remoteIP(r)) {
					writeError(w, http.StatusForbidden, CodeForbidden, "this API key may not be used from this address")
					return
				}
				if !apikeys.HasScope(c, scope) {
					writeError(w, http.StatusForbidden, CodeForbidden, fmt.Sprintf("this API key lacks the scope %q", scope))
					return
				}
				user = auth.User{ID: caller.UserID(), Plan: usage.APIPlan, DailyLimit: c.DailyQuota}
				ctx = context.WithValue(ctx, clientKey{}, c)
			} else {
				var err error
				user, err = v.Verify(ctx, token)
				if err == nil && apikeys.IsClientID(user.ID) {
					// Quotas and limits of app users and API clients
					// must never share a counter.
					err = fmt.Errorf("%w: reserved subject", auth.ErrInvalidToken)
				}
				if err != nil {
					if !errors.Is(err, auth.ErrInvalidToken) {
						log.WarnContext(ctx, "token verification failed", "err", err)
					}
					unauthorized(w, "invalid or expired token")
					return
				}
				if rec, ok := w.(*statusRecorder); ok {
					rec.user = user.ID
				}
			}
			next.ServeHTTP(w, r.WithContext(auth.WithUser(ctx, user)))
		})
	}
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="owis_find_deal_engine"`)
	writeError(w, http.StatusUnauthorized, CodeUnauthorized, msg)
}

// rateLimit smooths bursts per user (e.g. 1 req/s, burst 5) and per API
// client (its own limit). It protects the service; the daily allowance is
// enforced in the search handler. Must run after requireCaller.
func rateLimit(l *ratelimit.Limiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := auth.UserFrom(r.Context())
			ok, wait := false, time.Duration(0)
			if c, isClient := clientFrom(r.Context()); isClient {
				ok, wait = l.AllowRate(user.ID, c.RateRPS, c.RateBurst)
			} else {
				ok, wait = l.Allow("user:" + user.ID)
			}
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(wait.Seconds())), 1)))
				writeError(w, http.StatusTooManyRequests, CodeRateLimited, "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
