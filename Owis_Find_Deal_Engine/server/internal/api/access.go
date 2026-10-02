package api

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/usage"
)

// TokenVerifier checks a bearer token and returns its user.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (auth.User, error)
}

// QuotaTaker consumes and refunds per-user quota.
type QuotaTaker interface {
	Take(ctx context.Context, userID, plan string) (usage.Decision, error)
	Refund(ctx context.Context, d usage.Decision) error
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
// upstream providers; the daily allowance is enforced by enforceQuota.
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

// quotaRecorder captures the status the wrapped handler writes.
type quotaRecorder struct {
	http.ResponseWriter
	status int
}

func (q *quotaRecorder) WriteHeader(code int) {
	q.status = code
	q.ResponseWriter.WriteHeader(code)
}

// enforceQuota takes one unit of the user's daily allowance per request:
//   - within the allowance: counted, request continues;
//   - free plan used up: 402 Payment Required (upgrade);
//   - paid plan used up: 429 Too Many Requests until the daily reset.
//
// Requests that end in an error (4xx/5xx) are refunded, so users only pay
// for searches that returned results. If the counter store is down the
// request is refused (fail closed) to protect paid upstream quota.
// Must run after requireUser.
func enforceQuota(q QuotaTaker, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := auth.UserFrom(r.Context())
			d, err := q.Take(r.Context(), user.ID, user.Plan)
			if err != nil {
				log.ErrorContext(r.Context(), "quota check failed", "err", err)
				writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "service temporarily unavailable, please retry")
				return
			}

			h := w.Header()
			h.Set("X-Plan", d.Plan.Name)
			h.Set("X-RateLimit-Limit", strconv.FormatInt(d.Plan.DailyLimit, 10))
			h.Set("X-RateLimit-Remaining", strconv.FormatInt(d.Remaining(), 10))
			h.Set("X-RateLimit-Reset", strconv.FormatInt(d.Reset.Unix(), 10))

			if !d.Allowed {
				if d.UpgradeRequired {
					writeError(w, http.StatusPaymentRequired, CodePaymentRequired,
						"free daily searches used up; upgrade your plan to continue")
					return
				}
				h.Set("Retry-After", strconv.Itoa(max(int(time.Until(d.Reset).Seconds()), 1)))
				writeError(w, http.StatusTooManyRequests, CodeQuotaExceeded, "daily search limit reached")
				return
			}

			rec := &quotaRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			if rec.status >= 400 {
				// Detached from the request context, which may be cancelled.
				ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
				defer cancel()
				if err := q.Refund(ctx, d); err != nil {
					log.WarnContext(r.Context(), "quota refund failed", "err", err)
				}
			}
		})
	}
}
