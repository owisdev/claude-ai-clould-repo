// Package metering decides what a user's search costs them: answers that
// are already cached are free, live searches consume the daily allowance.
// It sits between the API (or any other entry point, such as a future MCP
// tool) and the search cache, so the rule lives in one place.
package metering

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"
)

// ErrQuotaUnavailable means the usage counters could not be read or
// updated. Searches are refused (fail closed) to protect paid providers.
var ErrQuotaUnavailable = errors.New("quota service unavailable")

// QuotaError reports that the user's daily allowance is used up.
type QuotaError struct {
	Decision usage.Decision
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("daily search limit of plan %q reached", e.Decision.Plan.Name)
}

// UpgradeRequired reports whether the user is on the free plan (the API
// answers 402 Payment Required) rather than a paid one (429).
func (e *QuotaError) UpgradeRequired() bool { return e.Decision.UpgradeRequired }

// Searcher answers from cache only, or searches live.
type Searcher interface {
	// Lookup never runs a live search; ok=false means one is needed.
	Lookup(ctx context.Context, req search.Request) (res *search.Result, ok bool)
	Search(ctx context.Context, req search.Request) (*search.Result, error)
}

// Quota consumes, refunds and reports per-user allowance.
type Quota interface {
	Take(ctx context.Context, a usage.Account) (usage.Decision, error)
	Refund(ctx context.Context, d usage.Decision) error
	Status(ctx context.Context, a usage.Account) (usage.Decision, error)
}

// Outcome is a metered search's result plus the user's allowance after it.
type Outcome struct {
	Result *search.Result
	// Quota is the allowance after this search; valid when QuotaKnown.
	Quota      usage.Decision
	QuotaKnown bool
}

// Search outcomes reported to a SearchRecorder.
const (
	OutcomeHit   = "hit"   // fresh cached answer (free)
	OutcomeStale = "stale" // cached answer past its fresh time (free, refreshed)
	OutcomeLive  = "live"  // searched live (counted)
	OutcomeQuota = "quota" // refused: daily allowance used up
	OutcomeError = "error" // failed
)

// SearchRecorder receives every metered search (usage dashboard). Searches
// with invalid input are not reported. client is the API client's user id
// (client:<id>), empty for app users.
type SearchRecorder interface {
	Search(client, country, title, outcome string)
}

// Service meters searches. Safe for concurrent use.
type Service struct {
	searcher Searcher
	quota    Quota
	log      *slog.Logger
	rec      SearchRecorder
}

// WithRecorder reports every search to rec; nil turns reporting off.
func (s *Service) WithRecorder(rec SearchRecorder) *Service {
	s.rec = rec
	return s
}

// Search runs and reports a search; see search.
func (s *Service) Search(ctx context.Context, user auth.User, req search.Request) (Outcome, error) {
	out, err := s.search(ctx, user, req)
	if s.rec != nil && user.ID != "" && !errors.Is(err, search.ErrInvalidTitle) && !errors.Is(err, search.ErrUnknownCountry) {
		var quotaErr *QuotaError
		outcome := OutcomeError
		switch {
		case err == nil && out.Result.Cached && out.Result.Stale:
			outcome = OutcomeStale
		case err == nil && out.Result.Cached:
			outcome = OutcomeHit
		case err == nil:
			outcome = OutcomeLive
		case errors.As(err, &quotaErr):
			outcome = OutcomeQuota
		case errors.Is(err, context.Canceled):
			return out, err // client went away: not a search
		}
		client := ""
		if user.Plan == usage.APIPlan {
			client = user.ID
		}
		s.rec.Search(client, req.Country, req.Title, outcome)
	}
	return out, err
}

// Check tells, without a live search and without using the allowance,
// whether Search would be refused for the allowance right now: nil when
// the answer is cached (free) or allowance is left, *QuotaError when it is
// used up, ErrQuotaUnavailable when the counters are down. cached reports
// a cached answer. The asynchronous search uses it to refuse at once
// rather than in the job; Search still decides (another search may use
// the last unit meanwhile).
func (s *Service) Check(ctx context.Context, user auth.User, req search.Request) (out Outcome, cached bool, err error) {
	if user.ID == "" {
		return Outcome{}, false, errors.New("metering: unauthenticated search")
	}
	_, cached = s.searcher.Lookup(ctx, req)
	d, err := s.quota.Status(ctx, account(user))
	if err != nil {
		if cached {
			return Outcome{}, true, nil // free answer: the counters do not matter
		}
		s.log.ErrorContext(ctx, "quota check failed", "err", err)
		return Outcome{}, false, fmt.Errorf("%w: %v", ErrQuotaUnavailable, err)
	}
	out = Outcome{Quota: d, QuotaKnown: true}
	if !cached && !d.Allowed {
		return out, false, &QuotaError{Decision: d}
	}
	return out, cached, nil
}

// New returns a metering Service.
func New(searcher Searcher, quota Quota, log *slog.Logger) *Service {
	return &Service{searcher: searcher, quota: quota, log: log}
}

// search runs a search for user:
//  1. answer cached → returned free (not counted), even when the
//     allowance is used up;
//  2. otherwise one unit of the daily allowance is taken (*QuotaError when
//     used up, ErrQuotaUnavailable when the counters are down);
//  3. live search; if it fails, or turns out to be answered from cache
//     after all (another request filled it meanwhile), the unit is
//     given back.
//
// Search errors (search.ErrInvalidTitle, ...) are returned unchanged.
func (s *Service) search(ctx context.Context, user auth.User, req search.Request) (Outcome, error) {
	if user.ID == "" {
		return Outcome{}, errors.New("metering: unauthenticated search")
	}

	if res, ok := s.searcher.Lookup(ctx, req); ok {
		out := Outcome{Result: res}
		// Allowance is informational here; a counter error must not block
		// a free answer.
		if d, err := s.quota.Status(ctx, account(user)); err == nil {
			out.Quota, out.QuotaKnown = d, true
		}
		return out, nil
	}

	d, err := s.quota.Take(ctx, account(user))
	if err != nil {
		s.log.ErrorContext(ctx, "quota check failed", "err", err)
		return Outcome{}, fmt.Errorf("%w: %v", ErrQuotaUnavailable, err)
	}
	out := Outcome{Quota: d, QuotaKnown: true}
	if !d.Allowed {
		return out, &QuotaError{Decision: d}
	}

	res, err := s.searcher.Search(ctx, req)
	if err != nil || res.Cached {
		s.refund(ctx, d)
		out.Quota.Used = max(out.Quota.Used-1, 0)
	}
	if err != nil {
		return out, err
	}
	out.Result = res
	return out, nil
}

// account is whose allowance user's searches use: app users by plan, API
// clients by their own daily limit (each client separately).
func account(user auth.User) usage.Account {
	return usage.Account{ID: user.ID, Plan: user.Plan, DailyLimit: user.DailyLimit}
}

// refund gives a unit back, detached from ctx, which may be cancelled.
func (s *Service) refund(ctx context.Context, d usage.Decision) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.quota.Refund(rctx, d); err != nil {
		s.log.WarnContext(ctx, "quota refund failed", "err", err)
	}
}

// WithoutCache adapts a plain search service when the cache is disabled:
// nothing is ever answered for free.
func WithoutCache(s interface {
	Search(ctx context.Context, req search.Request) (*search.Result, error)
}) Searcher {
	return noCache{s}
}

type noCache struct {
	live interface {
		Search(ctx context.Context, req search.Request) (*search.Result, error)
	}
}

func (noCache) Lookup(context.Context, search.Request) (*search.Result, bool) { return nil, false }
func (n noCache) Search(ctx context.Context, req search.Request) (*search.Result, error) {
	return n.live.Search(ctx, req)
}
