// Package usage enforces per-user daily search quotas by subscription plan.
package usage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Plan is a subscription tier.
type Plan struct {
	Name       string
	DailyLimit int64
}

// Plans holds the known tiers and the default (free) one.
type Plans struct {
	byName      map[string]Plan
	defaultPlan Plan
}

// ParsePlans parses "free:20,pro:500"; defaultName must be one of them.
func ParsePlans(spec, defaultName string) (*Plans, error) {
	p := &Plans{byName: map[string]Plan{}}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, limit, ok := strings.Cut(entry, ":")
		n, err := strconv.ParseInt(limit, 10, 64)
		if !ok || name == "" || err != nil || n <= 0 {
			return nil, fmt.Errorf("usage: plan %q must be name:positive-daily-limit", entry)
		}
		p.byName[name] = Plan{Name: name, DailyLimit: n}
	}
	def, ok := p.byName[defaultName]
	if !ok {
		return nil, fmt.Errorf("usage: default plan %q is not defined", defaultName)
	}
	p.defaultPlan = def
	return p, nil
}

// Resolve returns the plan by name, falling back to the default plan for
// empty or unknown names.
func (p *Plans) Resolve(name string) Plan {
	if plan, ok := p.byName[name]; ok {
		return plan
	}
	return p.defaultPlan
}

// IsDefault reports whether plan is the default (free) tier.
func (p *Plans) IsDefault(plan Plan) bool { return plan.Name == p.defaultPlan.Name }

// Store is an atomic counter store (Redis in production, memory for a
// single instance or tests).
type Store interface {
	// Incr atomically increments key, setting ttl when it is created,
	// and returns the new value.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
	// Decr atomically decrements key.
	Decr(ctx context.Context, key string) error
}

// Decision is the result of a quota check.
type Decision struct {
	Allowed bool
	Plan    Plan
	Used    int64
	Reset   time.Time // when the daily window resets (UTC midnight)
	// UpgradeRequired is set when a default-plan user ran out of quota:
	// the API answers 402 Payment Required instead of 429.
	UpgradeRequired bool
	key             string
}

// Remaining is how many searches are left today.
func (d Decision) Remaining() int64 { return max(d.Plan.DailyLimit-d.Used, 0) }

// Quota counts searches per user per UTC day.
type Quota struct {
	store Store
	plans *Plans
	now   func() time.Time
}

// NewQuota returns a Quota backed by store.
func NewQuota(store Store, plans *Plans) *Quota {
	return &Quota{store: store, plans: plans, now: time.Now}
}

// Take consumes one search for userID if the plan allows it. Increment
// first, then compare: atomic, so concurrent requests cannot overshoot.
func (q *Quota) Take(ctx context.Context, userID, planName string) (Decision, error) {
	if userID == "" {
		return Decision{}, errors.New("usage: empty user id")
	}
	now := q.now().UTC()
	day := now.Format("20060102")
	reset := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	plan := q.plans.Resolve(planName)

	d := Decision{Plan: plan, Reset: reset, key: "usage:" + userID + ":" + day}
	n, err := q.store.Incr(ctx, d.key, 48*time.Hour)
	if err != nil {
		return Decision{}, fmt.Errorf("usage: %w", err)
	}
	if n > plan.DailyLimit {
		// Undo, so rejected attempts do not count against tomorrow's
		// headroom checks or skew usage stats.
		_ = q.store.Decr(ctx, d.key)
		d.Used = plan.DailyLimit
		d.UpgradeRequired = q.plans.IsDefault(plan)
		return d, nil
	}
	d.Allowed = true
	d.Used = n
	return d, nil
}

// Refund gives back a search that did not produce a result (e.g. invalid
// input or an upstream failure).
func (q *Quota) Refund(ctx context.Context, d Decision) error {
	if !d.Allowed || d.key == "" {
		return nil
	}
	return q.store.Decr(ctx, d.key)
}
