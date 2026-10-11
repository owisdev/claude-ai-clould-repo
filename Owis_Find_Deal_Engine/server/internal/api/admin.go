package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/ratelimit"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/store"
)

// Error codes of the admin API (besides the public ones).
const (
	CodeForbidden            = "forbidden"
	CodeSecondFactorRequired = "second_factor_required"
	CodeSessionExpired       = "session_expired"
	CodeNotFound             = "not_found"
	CodeConflict             = "conflict"
	CodeCacheDisabled        = "cache_disabled"
)

// AdminStore is the admin list, audit log and settings history (store.DB).
type AdminStore interface {
	AdminForLogin(ctx context.Context, uid, email string, emailVerified bool) (store.Admin, error)
	Admins(ctx context.Context) ([]store.Admin, error)
	AddAdmin(ctx context.Context, email, by string) (store.Admin, error)
	SetAdminDisabled(ctx context.Context, email string, disabled bool) error
	UnlinkAdmin(ctx context.Context, email string) error
	AddAudit(ctx context.Context, e store.AuditEntry) error
	AuditLog(ctx context.Context, f store.AuditFilter) ([]store.AuditEntry, error)
	Settings(ctx context.Context) ([]store.Setting, int64, error)
	SettingsVersion(ctx context.Context) (int64, error)
	SettingsHistory(ctx context.Context, key string, limit int) ([]store.Change, error)
}

// SettingsChanger validates and stores settings changes (settings.Manager).
type SettingsChanger interface {
	Set(ctx context.Context, by string, changes map[string]*string) (int64, []string, error)
}

// ErrCacheDisabled is returned by AdminRuntime.ForgetCached when the
// search cache is off.
var ErrCacheDisabled = errors.New("the search cache is disabled (CACHE_ENABLED=false)")

// AdminRuntime is the running search setup, which changes with the
// settings: ask for it per request.
type AdminRuntime interface {
	Probe(ctx context.Context, req search.ProbeRequest) (*search.ProbeResult, error)
	ForgetCached(ctx context.Context, req search.Request) (bool, error)
	Health() []search.ChainHealth
	Status() RuntimeStatus
}

// RuntimeStatus describes the settings the server runs with.
type RuntimeStatus struct {
	Version         string `json:"version"`          // build
	SettingsVersion int64  `json:"settings_version"` // applied; the stored one may be newer for up to the poll interval
	PollInterval    string `json:"settings_poll_interval"`
	MarketsSource   string `json:"markets_source"` // setting, file, built-in
	MarketsJSON     []byte `json:"-"`
	CacheEnabled    bool   `json:"cache_enabled"`
	CacheGeneration int    `json:"cache_generation"`
}

// AdminDeps are the admin router's dependencies.
type AdminDeps struct {
	Store    AdminStore
	Settings SettingsChanger
	Runtime  func() AdminRuntime
	Verifier TokenVerifier
	// Require2FA refuses admins who signed in without a second factor.
	Require2FA bool
	// SessionMaxAge: an admin must sign in again after this long, even
	// with fresh tokens (0 = no limit).
	SessionMaxAge time.Duration
	Log           *slog.Logger

	// Rate limits; nil = defaults.
	IPLimiter, AdminLimiter, ToolLimiter *ratelimit.Limiter
}

// adminWriteTimeout bounds an admin response; a test search with every
// source can take as long as the slowest source (Apify: up to 300 s).
const adminWriteTimeout = 330 * time.Second

// NewAdminRouter returns the admin API, separate from the public API.
// Every call needs an admin: a valid login token, two-factor sign-in, a
// recent sign-in and an active entry in the admin list, checked on each
// request. Calls are rate-limited, and changes are written to the audit
// log.
//
//	GET    /admin/v1/me
//	GET    /admin/v1/status
//	GET    /admin/v1/settings
//	PATCH  /admin/v1/settings                {"changes": {"KEY": "value" | null}}
//	GET    /admin/v1/settings/history        ?key=&limit=
//	GET    /admin/v1/markets                 countries and shops (JSON)
//	PUT    /admin/v1/markets                 replace them
//	DELETE /admin/v1/markets                 back to the file / built-in ones
//	GET    /admin/v1/admins
//	POST   /admin/v1/admins                  {"email": "..."}
//	POST   /admin/v1/admins/{email}/disable | enable | unlink
//	GET    /admin/v1/audit                   ?actor=&action=&before=&limit=
//	POST   /admin/v1/tools/test-search       {"title", "country", "markets", "all_sources"}
//	POST   /admin/v1/tools/cache/forget      {"title", "country"}
//	POST   /admin/v1/tools/cache/bump        every cached answer becomes a miss
//	GET    /admin/v1/tools/providers         circuit breaker state per source
func NewAdminRouter(d AdminDeps) http.Handler {
	if d.IPLimiter == nil {
		d.IPLimiter = ratelimit.New(10, 40, 10*time.Minute)
	}
	if d.AdminLimiter == nil {
		d.AdminLimiter = ratelimit.New(5, 30, 10*time.Minute)
	}
	if d.ToolLimiter == nil {
		// Test searches cost money: about one per 6 s, 5 in a row.
		d.ToolLimiter = ratelimit.New(1.0/6, 5, 10*time.Minute)
	}
	// Anyone can create a login: audit refusals of one login at most once
	// a minute so the audit log cannot be flooded.
	h := &adminHandlers{d: d, deniedAudit: ratelimit.New(1.0/60, 1, 10*time.Minute)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/me", h.me)
	mux.HandleFunc("GET /admin/v1/status", h.status)
	mux.HandleFunc("GET /admin/v1/settings", h.listSettings)
	mux.HandleFunc("PATCH /admin/v1/settings", h.changeSettings)
	mux.HandleFunc("GET /admin/v1/settings/history", h.settingsHistory)
	mux.HandleFunc("GET /admin/v1/markets", h.getMarkets)
	mux.HandleFunc("PUT /admin/v1/markets", h.putMarkets)
	mux.HandleFunc("DELETE /admin/v1/markets", h.resetMarkets)
	mux.HandleFunc("GET /admin/v1/admins", h.listAdmins)
	mux.HandleFunc("POST /admin/v1/admins", h.addAdmin)
	mux.HandleFunc("POST /admin/v1/admins/{email}/{action}", h.changeAdmin)
	mux.HandleFunc("GET /admin/v1/audit", h.auditLog)
	mux.Handle("POST /admin/v1/tools/test-search", h.limitTool(http.HandlerFunc(h.testSearch)))
	mux.HandleFunc("POST /admin/v1/tools/cache/forget", h.forgetCached)
	mux.HandleFunc("POST /admin/v1/tools/cache/bump", h.bumpCache)
	mux.HandleFunc("GET /admin/v1/tools/providers", h.providers)
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such admin endpoint")
	})

	return chain(mux,
		writeDeadline(adminWriteTimeout),
		requestID,
		logRequests(d.Log),
		recoverPanics(d.Log),
		noStore,
		limitBy(d.IPLimiter, func(r *http.Request) string { return "ip:" + remoteIP(r) }),
		h.requireAdmin,
		limitBy(d.AdminLimiter, func(r *http.Request) string { return "admin:" + adminFrom(r.Context()).Email }),
	)
}

type adminHandlers struct {
	d           AdminDeps
	deniedAudit *ratelimit.Limiter
}

type adminKey struct{}

func adminFrom(ctx context.Context) store.Admin {
	a, _ := ctx.Value(adminKey{}).(store.Admin)
	return a
}

// actor is the audit / history name of the admin making the request.
func actor(r *http.Request) string { return "admin:" + adminFrom(r.Context()).Email }

// requireAdmin lets only active admins through, signed in with a second
// factor and not too long ago. Denials of valid logins are audited.
func (h *adminHandlers) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			unauthorized(w, "missing bearer token")
			return
		}
		user, err := h.d.Verifier.Verify(r.Context(), strings.TrimSpace(token))
		if err != nil {
			if !errors.Is(err, auth.ErrInvalidToken) {
				h.d.Log.WarnContext(r.Context(), "token verification failed", "err", err)
			}
			unauthorized(w, "invalid or expired token")
			return
		}
		if rec, ok := w.(*statusRecorder); ok {
			rec.user = user.ID
		}
		deny := func(status int, code, msg, reason string) {
			if ok, _ := h.deniedAudit.Allow(user.ID); !ok {
				writeError(w, status, code, msg)
				return
			}
			h.audit(r, store.AuditEntry{Actor: "user:" + user.ID, Action: "admin.login", Target: user.Email,
				Outcome: store.OutcomeDenied, Detail: jsonDetail(map[string]string{"reason": reason})})
			writeError(w, status, code, msg)
		}
		if h.d.Require2FA && user.SecondFactor == "" {
			deny(http.StatusForbidden, CodeSecondFactorRequired,
				"admins must sign in with a second factor (two-step verification)", "no second factor")
			return
		}
		if h.d.SessionMaxAge > 0 && (user.AuthTime.IsZero() || time.Since(user.AuthTime) > h.d.SessionMaxAge) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="owis_find_deal_engine", error="invalid_token"`)
			writeError(w, http.StatusUnauthorized, CodeSessionExpired, "sign in again: the admin session is older than "+h.d.SessionMaxAge.String())
			return
		}
		admin, err := h.d.Store.AdminForLogin(r.Context(), user.ID, user.Email, user.EmailVerified)
		switch {
		case errors.Is(err, store.ErrNotAdmin):
			deny(http.StatusForbidden, CodeForbidden, "not an admin", "not on the admin list")
			return
		case errors.Is(err, store.ErrAdminLinked):
			deny(http.StatusForbidden, CodeForbidden, "not an admin", "email linked to another login")
			return
		case err != nil:
			h.d.Log.ErrorContext(r.Context(), "admin lookup failed", "err", err)
			writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "admin list unavailable, please retry")
			return
		case admin.DisabledAt != nil:
			deny(http.StatusForbidden, CodeForbidden, "not an admin", "admin disabled")
			return
		}
		ctx := auth.WithUser(context.WithValue(r.Context(), adminKey{}, admin), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// limitTool rate-limits the costly tools per admin.
func (h *adminHandlers) limitTool(next http.Handler) http.Handler {
	return limitBy(h.d.ToolLimiter, func(r *http.Request) string { return "tool:" + adminFrom(r.Context()).Email })(next)
}

// limitBy rate-limits requests by key.
func limitBy(l *ratelimit.Limiter, key func(*http.Request) string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ok, wait := l.Allow(key(r)); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(wait.Seconds())), 1)))
				writeError(w, http.StatusTooManyRequests, CodeRateLimited, "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// noStore keeps admin answers out of browser and proxy caches.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// remoteIP is the connection's address. Behind a reverse proxy (step 14)
// this is the proxy; the proxy's client header is not trusted yet.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// audit writes an audit entry; a failure is logged, never shown.
func (h *adminHandlers) audit(r *http.Request, e store.AuditEntry) {
	e.RequestID = requestIDFrom(r.Context())
	e.RemoteIP = remoteIP(r)
	if e.Actor == "" {
		e.Actor = actor(r)
	}
	if err := h.d.Store.AddAudit(context.WithoutCancel(r.Context()), e); err != nil {
		h.d.Log.ErrorContext(r.Context(), "audit log write failed", "action", e.Action, "actor", e.Actor, "err", err)
	}
}

func jsonDetail(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
