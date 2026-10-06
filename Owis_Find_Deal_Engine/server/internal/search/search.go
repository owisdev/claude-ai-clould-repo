// Package search fans a product query out to the marketplaces of a country
// and merges the results.
package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"owis_find_deal_engine/internal/markets"
)

// Errors returned by Service.Search.
var (
	ErrUnknownCountry = errors.New("unsupported country")
	ErrInvalidTitle   = errors.New("invalid title")
	ErrAllFailed      = errors.New("all marketplaces failed")
)

const (
	MinTitleLen = 2
	MaxTitleLen = 200
)

// Product is one search result.
type Product struct {
	Market     string   `json:"market"`
	Title      string   `json:"title"`
	Link       string   `json:"link"`
	Snippet    string   `json:"snippet,omitempty"`
	Thumbnail  string   `json:"thumbnail,omitempty"`
	Price      *float64 `json:"price,omitempty"`
	Currency   string   `json:"currency,omitempty"`
	Position   int      `json:"position"`
	Provider   string   `json:"provider"`
	Extensions []string `json:"extensions,omitempty"`
}

// Query is what a Provider is asked to search.
type Query struct {
	Title    string
	Region   string // search region, e.g. "us", "sa", "jo"
	Language string
	Targets  []markets.Target
}

// Provider searches one or more marketplaces.
type Provider interface {
	Name() string
	// Batch reports whether the provider searches all targets in one call.
	// Otherwise the service calls it once per target, in parallel.
	Batch() bool
	// Search returns products for q.Targets; Product.Market must be set.
	Search(ctx context.Context, q Query) ([]Product, error)
}

// Market statuses reported per marketplace.
const (
	StatusOK        = "ok"
	StatusNoResults = "no_results" // searched fine, nothing matched
	StatusError     = "error"
)

// PartialError is returned by a provider that searched several marketplaces
// in one call and failed for some of them. Products holds the results of
// the others; Failed maps each failed marketplace to its error.
type PartialError struct {
	Products []Product
	Failed   map[string]error
}

func (e *PartialError) Error() string {
	parts := make([]string, 0, len(e.Failed))
	for m, err := range e.Failed {
		parts = append(parts, m+": "+err.Error())
	}
	sort.Strings(parts)
	return "some marketplaces failed: " + strings.Join(parts, "; ")
}

// Request is one search as asked by a client.
type Request struct {
	Title   string
	Country string
	// Refresh asks for live results instead of cached ones (pull to refresh).
	Refresh bool
}

// Result is the merged search response.
type Result struct {
	Query   string            `json:"query"`
	Country string            `json:"country"`
	Results []Product         `json:"results"`
	Markets map[string]string `json:"markets"`
	TookMS  int64             `json:"took_ms"`
	// FetchedAt is when the results were fetched from the providers; for
	// cached answers it tells the user how old the prices are.
	FetchedAt time.Time `json:"fetched_at"`
	Cached    bool      `json:"cached"`
	// Stale is set when a cached answer is past its fresh period; a
	// background refresh has been started.
	Stale bool `json:"stale"`
}

// Partial reports whether at least one marketplace failed.
func (r *Result) Partial() bool {
	for _, status := range r.Markets {
		if status == StatusError {
			return true
		}
	}
	return false
}

// NormalizeTitle trims and collapses whitespace.
func NormalizeTitle(title string) string {
	return strings.Join(strings.Fields(title), " ")
}

// Options tunes the service.
type Options struct {
	Timeout        time.Duration // whole search
	MaxConcurrency int           // provider calls in flight per search
}

// Service runs searches. Safe for concurrent use.
type Service struct {
	catalog   *markets.Catalog
	providers map[string]Provider // keyed by catalog provider name, e.g. "web"
	opts      Options
	log       *slog.Logger
}

// NewService builds a Service. providers maps the catalog's provider names
// (such as "web") to implementations.
func NewService(catalog *markets.Catalog, providers map[string]Provider, opts Options, log *slog.Logger) *Service {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = 4
	}
	return &Service{catalog: catalog, providers: providers, opts: opts, log: log}
}

// job is one provider call covering one or more targets.
type job struct {
	provider Provider
	targets  []markets.Target
}

type outcome struct {
	job      job
	products []Product
	err      error
}

// Search validates the input, searches every marketplace of the country with
// a bounded worker pool and merges the results. A failing marketplace is
// reported in Result.Markets; only when all fail is ErrAllFailed returned.
func (s *Service) Search(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()

	title := NormalizeTitle(req.Title)
	if n := len([]rune(title)); n < MinTitleLen || n > MaxTitleLen {
		return nil, fmt.Errorf("%w: must be %d-%d characters", ErrInvalidTitle, MinTitleLen, MaxTitleLen)
	}
	country, ok := s.catalog.Country(req.Country)
	if !ok {
		return nil, ErrUnknownCountry
	}

	ctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()

	result := &Result{
		Query:   title,
		Country: country.Code,
		Results: []Product{},
		Markets: make(map[string]string, len(country.Targets)),
	}

	jobs := s.planJobs(country.Targets, result)
	q := Query{Title: title, Region: country.Region, Language: country.Language}
	for o := range s.runPool(ctx, q, jobs) {
		var failed map[string]error
		var partial *PartialError
		switch {
		case errors.As(o.err, &partial):
			failed = partial.Failed
			o.products = partial.Products
		case o.err != nil:
			failed = make(map[string]error, len(o.job.targets))
			for _, t := range o.job.targets {
				failed[t.Market] = o.err
			}
		}
		if len(failed) > 0 {
			s.log.WarnContext(ctx, "provider failed",
				"provider", o.job.provider.Name(), "markets", targetIDs(o.job.targets), "err", o.err)
		}
		for _, t := range o.job.targets {
			if _, bad := failed[t.Market]; bad {
				result.Markets[t.Market] = StatusError
			} else {
				result.Markets[t.Market] = StatusNoResults // until a product shows up
			}
		}
		result.Results = append(result.Results, o.products...)
	}
	for _, p := range result.Results {
		if result.Markets[p.Market] == StatusNoResults {
			result.Markets[p.Market] = StatusOK
		}
	}

	sortProducts(result.Results, country.Targets)
	result.TookMS = time.Since(start).Milliseconds()
	result.FetchedAt = time.Now().UTC()

	for _, status := range result.Markets {
		if status != StatusError {
			return result, nil
		}
	}
	return result, ErrAllFailed
}

// planJobs groups the targets into provider calls. Targets whose provider
// is not configured are marked as failed in result.
func (s *Service) planJobs(targets []markets.Target, result *Result) []job {
	var jobs []job
	batched := make(map[string]int) // provider name -> index in jobs
	for _, t := range targets {
		p, ok := s.providers[t.Provider]
		if !ok {
			result.Markets[t.Market] = StatusError
			s.log.Warn("no provider configured", "provider", t.Provider, "market", t.Market)
			continue
		}
		if !p.Batch() {
			jobs = append(jobs, job{provider: p, targets: []markets.Target{t}})
			continue
		}
		if i, ok := batched[t.Provider]; ok {
			jobs[i].targets = append(jobs[i].targets, t)
			continue
		}
		batched[t.Provider] = len(jobs)
		jobs = append(jobs, job{provider: p, targets: []markets.Target{t}})
	}
	return jobs
}

// runPool runs jobs on at most MaxConcurrency goroutines. The returned
// channel yields exactly one outcome per job and is closed when all are done.
// Both channels are buffered to len(jobs), so no goroutine can block or leak
// even if the caller stops early or ctx is cancelled.
func (s *Service) runPool(ctx context.Context, q Query, jobs []job) <-chan outcome {
	jobCh := make(chan job, len(jobs))
	out := make(chan outcome, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	var wg sync.WaitGroup
	for range min(s.opts.MaxConcurrency, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				out <- s.runJob(ctx, q, j)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// runJob calls the provider for one job, turning panics into errors.
func (s *Service) runJob(ctx context.Context, q Query, j job) (o outcome) {
	o.job = j
	defer func() {
		if r := recover(); r != nil {
			o.products, o.err = nil, fmt.Errorf("provider %s panicked: %v", j.provider.Name(), r)
		}
	}()
	if err := ctx.Err(); err != nil {
		o.err = err
		return o
	}
	q.Targets = j.targets
	o.products, o.err = j.provider.Search(ctx, q)
	return o
}

// sortProducts orders results by rank, then by the country's market order,
// so the first results interleave the best match of every marketplace.
func sortProducts(products []Product, targets []markets.Target) {
	order := make(map[string]int, len(targets))
	for i, t := range targets {
		order[t.Market] = i
	}
	sort.SliceStable(products, func(i, j int) bool {
		if products[i].Position != products[j].Position {
			return products[i].Position < products[j].Position
		}
		return order[products[i].Market] < order[products[j].Market]
	})
}

func targetIDs(targets []markets.Target) []string {
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.Market
	}
	return ids
}
