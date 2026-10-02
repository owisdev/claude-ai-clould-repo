package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// FallbackOptions tunes a Fallback chain.
type FallbackOptions struct {
	// AttemptTimeout bounds each provider attempt so a slow provider leaves
	// time for the next one. The search's overall timeout still applies.
	AttemptTimeout time.Duration
	// After FailureThreshold consecutive failures a provider is skipped for
	// Cooldown (circuit breaker), so a broken instance adds no latency.
	FailureThreshold int
	Cooldown         time.Duration
}

// Fallback tries providers in order (e.g. free SearXNG, then paid SerpApi)
// and returns the first successful answer. Safe for concurrent use.
type Fallback struct {
	providers []Provider
	breakers  []*breaker
	opts      FallbackOptions
	log       *slog.Logger
	name      string
}

var _ Provider = (*Fallback)(nil)

// NewFallback chains providers. All must agree on Batch().
func NewFallback(providers []Provider, opts FallbackOptions, log *slog.Logger) (*Fallback, error) {
	if len(providers) == 0 {
		return nil, errors.New("search: fallback needs at least one provider")
	}
	if opts.AttemptTimeout <= 0 {
		opts.AttemptTimeout = 8 * time.Second
	}
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = 3
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = time.Minute
	}
	names := make([]string, len(providers))
	breakers := make([]*breaker, len(providers))
	for i, p := range providers {
		if p.Batch() != providers[0].Batch() {
			return nil, fmt.Errorf("search: provider %s disagrees on batch mode", p.Name())
		}
		names[i] = p.Name()
		breakers[i] = &breaker{threshold: opts.FailureThreshold, cooldown: opts.Cooldown}
	}
	return &Fallback{
		providers: providers,
		breakers:  breakers,
		opts:      opts,
		log:       log,
		name:      strings.Join(names, ">"),
	}, nil
}

func (f *Fallback) Name() string { return f.name }
func (f *Fallback) Batch() bool  { return f.providers[0].Batch() }

// Search returns the first provider's successful result. The last provider
// is always tried, even if its breaker is open: it is the last resort.
func (f *Fallback) Search(ctx context.Context, q Query) ([]Product, error) {
	var errs []error
	for i, p := range f.providers {
		last := i == len(f.providers)-1
		b := f.breakers[i]
		if !last && !b.allow() {
			errs = append(errs, fmt.Errorf("%s: skipped, circuit open", p.Name()))
			continue
		}

		attemptCtx, cancel := context.WithTimeout(ctx, f.opts.AttemptTimeout)
		products, err := p.Search(attemptCtx, q)
		cancel()
		if err == nil {
			b.success()
			return products, nil
		}
		if ctx.Err() != nil {
			// The whole search ran out of time; no point trying further.
			return nil, errors.Join(append(errs, err)...)
		}
		errs = append(errs, err)
		if last {
			break // the last resort has no breaker: it is never skipped
		}
		if b.failure() {
			f.log.WarnContext(ctx, "provider circuit opened",
				"provider", p.Name(), "cooldown", f.opts.Cooldown.String())
		}
		f.log.InfoContext(ctx, "provider failed, falling back",
			"provider", p.Name(), "next", f.providers[i+1].Name(), "err", err)
	}
	return nil, errors.Join(errs...)
}

// breaker is a minimal consecutive-failure circuit breaker. After cooldown
// one trial request is let through (half-open); success closes it again.
type breaker struct {
	threshold int
	cooldown  time.Duration

	mu        sync.Mutex
	failures  int
	openUntil time.Time
	now       func() time.Time
}

func (b *breaker) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if b.clock().Before(b.openUntil) {
		return false
	}
	// Half-open: let this request through; extend the window so concurrent
	// requests keep skipping until it reports back.
	b.openUntil = b.clock().Add(b.cooldown)
	return true
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
}

// failure records a failure and reports whether the circuit just opened.
func (b *breaker) failure() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		wasClosed := b.openUntil.IsZero()
		b.openUntil = b.clock().Add(b.cooldown)
		return wasClosed
	}
	return false
}
