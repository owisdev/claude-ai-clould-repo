package search

import (
	"context"
	"errors"
	"time"
)

// Source call outcomes recorded for the usage dashboard.
const (
	CallOK        = "ok"
	CallNoResults = "no_results"
	CallError     = "error"
)

// Fallback reasons recorded for the usage dashboard.
const (
	FallbackError       = "error"
	FallbackPartial     = "partial"
	FallbackCircuitOpen = "circuit_open"
)

// Recorder receives what the search layer does, for the usage dashboard.
// Implementations must be fast and safe for concurrent use.
type Recorder interface {
	// SourceCall is one call of a source (a leaf provider): the outcome
	// for each shop it was asked for, and its estimated cost in USD.
	SourceCall(provider string, outcomes map[string]string, took time.Duration, costUSD float64)
	// Fallback is a chain moving from one provider to the next.
	Fallback(chain, from, to, reason string)
}

// Instrument wraps a source so every call is reported to rec with its
// estimated cost (cost gets the call's markets). A nil rec returns p.
func Instrument(p Provider, rec Recorder, cost func(markets []string) float64) Provider {
	if rec == nil {
		return p
	}
	return &instrumented{Provider: p, rec: rec, cost: cost}
}

type instrumented struct {
	Provider
	rec  Recorder
	cost func(markets []string) float64
}

func (i *instrumented) Search(ctx context.Context, q Query) ([]Product, error) {
	start := time.Now()
	products, err := i.Provider.Search(ctx, q)
	took := time.Since(start)
	var pe *PartialError
	found := products
	if errors.As(err, &pe) {
		found = pe.Products
	}
	got := make(map[string]bool, len(found))
	for _, p := range found {
		got[p.Market] = true
	}
	outcomes := make(map[string]string, len(q.Targets))
	for _, t := range q.Targets {
		switch {
		case err != nil && pe == nil:
			outcomes[t.Market] = CallError
		case pe != nil && pe.Failed[t.Market] != nil:
			outcomes[t.Market] = CallError
		case got[t.Market]:
			outcomes[t.Market] = CallOK
		default:
			outcomes[t.Market] = CallNoResults
		}
	}
	var cost float64
	if i.cost != nil {
		cost = i.cost(targetIDs(q.Targets))
	}
	i.rec.SourceCall(i.Name(), outcomes, took, cost)
	return products, err
}
