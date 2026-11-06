package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"owis_find_deal_engine/internal/markets"
)

// ProbeRequest is an admin "test search".
type ProbeRequest struct {
	Title   string
	Country string
	// Markets limits the probe to these shops of the country; empty = all.
	Markets []string
	// AllSources asks every source of each shop's chain (the fallbacks
	// too), not only the first one. Paid sources are charged for each.
	AllSources bool
}

// SourceAnswer is what one source answered for one shop.
type SourceAnswer struct {
	Market   string `json:"market"`
	Provider string `json:"provider"`
	// Fallback is set for a source that users only reach when the ones
	// before it fail.
	Fallback bool      `json:"fallback,omitempty"`
	Status   string    `json:"status"` // ok, no_results, error
	Error    string    `json:"error,omitempty"`
	TookMS   int64     `json:"took_ms"`
	CostUSD  float64   `json:"cost_usd"` // estimated, from the cost settings
	Products []Product `json:"products"`
}

// ProbeResult is the answer of every source asked.
type ProbeResult struct {
	Query   string         `json:"query"`
	Country string         `json:"country"`
	Sources []SourceAnswer `json:"sources"`
	TookMS  int64          `json:"took_ms"`
	CostUSD float64        `json:"cost_usd"`
}

// CostFunc estimates what one call of provider for market costs, in USD.
type CostFunc func(provider, market string) float64

// probeConcurrency bounds the source calls of one probe.
const probeConcurrency = 8

// Probe asks the sources of a country's shops directly: no cache, no
// quota, no circuit breaker and no fallback, so an admin sees what each
// source answers, how long it takes and what it costs. Each call is
// bounded by the search timeout.
func (s *Service) Probe(ctx context.Context, req ProbeRequest, cost CostFunc) (*ProbeResult, error) {
	start := time.Now()
	title := NormalizeTitle(req.Title)
	if n := len([]rune(title)); n < MinTitleLen || n > MaxTitleLen {
		return nil, fmt.Errorf("%w: must be %d-%d characters", ErrInvalidTitle, MinTitleLen, MaxTitleLen)
	}
	country, ok := s.catalog.Country(req.Country)
	if !ok {
		return nil, ErrUnknownCountry
	}
	targets := country.Targets
	if len(req.Markets) > 0 {
		targets = nil
		for _, m := range req.Markets {
			i := slices.IndexFunc(country.Targets, func(t markets.Target) bool { return t.Market == m })
			if i < 0 {
				return nil, fmt.Errorf("%w: %s is not a shop of %s", ErrUnknownMarket, m, country.Code)
			}
			targets = append(targets, country.Targets[i])
		}
	}

	type call struct {
		target   markets.Target
		provider Provider
		fallback bool
	}
	var calls []call
	for _, t := range targets {
		chain := sources(s.providers[t.Provider])
		if len(chain) == 0 {
			calls = append(calls, call{target: t})
			continue
		}
		if !req.AllSources {
			chain = chain[:1]
		}
		for i, p := range chain {
			calls = append(calls, call{target: t, provider: p, fallback: i > 0})
		}
	}

	res := &ProbeResult{Query: title, Country: country.Code, Sources: make([]SourceAnswer, len(calls))}
	q := Query{Title: title, Region: country.Region, ShoppingRegion: country.ShoppingRegion, Language: country.Language}
	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	for i, c := range calls {
		ans := &res.Sources[i]
		ans.Market, ans.Fallback, ans.Products = c.target.Market, c.fallback, []Product{}
		if c.provider == nil {
			ans.Provider, ans.Status, ans.Error = c.target.Provider, StatusError, "no provider configured"
			continue
		}
		ans.Provider = c.provider.Name()
		if cost != nil {
			ans.CostUSD = cost(ans.Provider, c.target.Market)
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			cq := q
			cq.Targets = []markets.Target{c.target}
			callCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
			defer cancel()
			t0 := time.Now()
			products, err := probeCall(callCtx, c.provider, cq)
			ans.TookMS = time.Since(t0).Milliseconds()
			var partial *PartialError
			if errors.As(err, &partial) && len(partial.Failed) == 0 {
				products, err = partial.Products, nil // only "not covered"
			}
			switch {
			case err != nil:
				ans.Status, ans.Error = StatusError, err.Error()
			case len(products) == 0:
				ans.Status = StatusNoResults
			default:
				ans.Status, ans.Products = StatusOK, products
			}
		})
	}
	wg.Wait()
	for _, a := range res.Sources {
		res.CostUSD += a.CostUSD
	}
	res.TookMS = time.Since(start).Milliseconds()
	return res, nil
}

// ErrUnknownMarket is returned by Probe for a shop the country does not have.
var ErrUnknownMarket = errors.New("unknown shop")

// probeCall calls p, turning a panic into an error.
func probeCall(ctx context.Context, p Provider, q Query) (products []Product, err error) {
	defer func() {
		if r := recover(); r != nil {
			products, err = nil, fmt.Errorf("provider %s panicked: %v", p.Name(), r)
		}
	}()
	return p.Search(ctx, q)
}

// sources flattens a provider (possibly nested fallback chains) into its
// sources in the order a search tries them.
func sources(p Provider) []Provider {
	f, ok := p.(*Fallback)
	if !ok {
		if p == nil {
			return nil
		}
		return []Provider{p}
	}
	var out []Provider
	for _, sub := range f.providers {
		out = append(out, sources(sub)...)
	}
	return out
}

// ChainHealth is the circuit breaker state of one provider chain.
type ChainHealth struct {
	Name    string         `json:"name"` // catalog provider name, e.g. "web", "apify-temu"
	Chain   string         `json:"chain"`
	Sources []SourceHealth `json:"sources"`
}

// Health returns the breaker state of every provider chain, by name.
func (s *Service) Health() []ChainHealth {
	names := make([]string, 0, len(s.providers))
	for n := range s.providers {
		names = append(names, n)
	}
	slices.Sort(names)
	out := make([]ChainHealth, 0, len(names))
	for _, n := range names {
		p := s.providers[n]
		h := ChainHealth{Name: n, Chain: p.Name(), Sources: []SourceHealth{}}
		if f, ok := p.(*Fallback); ok {
			h.Sources = f.Health()
		}
		out = append(out, h)
	}
	return out
}
