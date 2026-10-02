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
	Take(ctx context.Context, userID, plan string) (usage.Decision, error)
	Refund(ctx context.Context, d usage.Decision) error
	Status(ctx context.Context, userID, plan string) (usage.Decision, error)
}

// Outcome is a metered search's result plus the user's allowance after it.
type Outcome struct {
	Result *search.Result
	// Quota is the allowance after this search; valid when QuotaKnown.
	Quota      usage.Decision
	QuotaKnown bool
}

// Service meters searches. Safe for concurrent use.
type Service struct {
	searcher Searcher
	quota    Quota
	log      *slog.Logger
}

// New returns a metering Service.
func New(searcher Searcher, quota Quota, log *slog.Logger) *Service {
	return &Service{searcher: searcher, quota: quota, log: log}
}

// Search runs a search for user:
//  1. answer cached → returned free (not counted), even when the
//     allowance is used up;
//  2. otherwise one unit of the daily allowance is taken (*QuotaError when
//     used up, ErrQuotaUnavailable when the counters are down);
//  3. live search; if it fails, or turns out to be answered from cache
//     after all (another request filled it meanwhile), the unit is
//     given back.
//
// Search errors (search.ErrInvalidTitle, ...) are returned unchanged.
func (s *Service) Search(ctx context.Context, user auth.User, req search.Request) (Outcome, error) {
	if user.ID == "" {
		return Outcome{}, errors.New("metering: unauthenticated search")
	}

	if res, ok := s.searcher.Lookup(ctx, req); ok {
		out := Outcome{Result: res}
		// Allowance is informational here; a counter error must not block
		// a free answer.
		if d, err := s.quota.Status(ctx, user.ID, user.Plan); err == nil {
			out.Quota, out.QuotaKnown = d, true
		}
		return out, nil
	}

	d, err := s.quota.Take(ctx, user.ID, user.Plan)
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
