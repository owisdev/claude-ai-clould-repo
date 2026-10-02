package api

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/ratelimit"
)

// TokenVerifier checks a bearer token and returns its user.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (auth.User, error)
}

// requireUser accepts only requests with a valid "Authorization: Bearer <JWT>".
func requireUser(v TokenVerifier, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
				unauthorized(w, "missing bearer token")
				return
			}
			user, err := v.Verify(r.Context(), strings.TrimSpace(token))
			if err != nil {
				if !errors.Is(err, auth.ErrInvalidToken) {
					log.WarnContext(r.Context(), "token verification failed", "err", err)
				}
				unauthorized(w, "invalid or expired token")
				return
			}
			if rec, ok := w.(*statusRecorder); ok {
				rec.user = user.ID
			}
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
		})
	}
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="owis_find_deal_engine"`)
	writeError(w, http.StatusUnauthorized, CodeUnauthorized, msg)
}

// rateLimit smooths bursts per user (e.g. 1 req/s, burst 5). It protects the
// service; the daily allowance is enforced in the search handler.
// Must run after requireUser.
func rateLimit(l *ratelimit.Limiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := auth.UserFrom(r.Context())
			if ok, wait := l.Allow("user:" + user.ID); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(wait.Seconds())), 1)))
				writeError(w, http.StatusTooManyRequests, CodeRateLimited, "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
