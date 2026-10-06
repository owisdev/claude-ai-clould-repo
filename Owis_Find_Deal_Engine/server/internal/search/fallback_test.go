package search

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"owis_find_deal_engine/internal/markets"
)

// stubProvider returns a fixed answer and counts calls.
type stubProvider struct {
	name  string
	batch bool
	err   error
	delay time.Duration
	calls atomic.Int32
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Batch() bool  { return s.batch }

func (s *stubProvider) Search(ctx context.Context, q Query) ([]Product, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return []Product{{Market: q.Targets[0].Market, Title: q.Title, Provider: s.name, Position: 1}}, nil
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

var oneTarget = Query{Title: "hub", Targets: []markets.Target{{Market: "amazon", Domain: "amazon.com"}}}

func TestFallbackUsesFirstSuccess(t *testing.T) {
	free := &stubProvider{name: "searxng"}
	paid := &stubProvider{name: "serpapi"}
	f, err := NewFallback([]Provider{free, paid}, FallbackOptions{}, discard)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Search(context.Background(), oneTarget)
	if err != nil || got[0].Provider != "searxng" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if paid.calls.Load() != 0 {
		t.Error("paid provider called although the free one succeeded")
	}
	if f.Name() != "searxng>serpapi" {
		t.Errorf("name = %q", f.Name())
	}
}

func TestFallbackFallsBackOnError(t *testing.T) {
	free := &stubProvider{name: "searxng", err: errors.New("captcha")}
	paid := &stubProvider{name: "serpapi"}
	f, _ := NewFallback([]Provider{free, paid}, FallbackOptions{}, discard)

	got, err := f.Search(context.Background(), oneTarget)
	if err != nil || got[0].Provider != "serpapi" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestFallbackSlowPrimaryLeavesTimeForNext(t *testing.T) {
	free := &stubProvider{name: "searxng", delay: time.Second}
	paid := &stubProvider{name: "serpapi"}
	f, _ := NewFallback([]Provider{free, paid}, FallbackOptions{AttemptTimeout: 30 * time.Millisecond}, discard)

	start := time.Now()
	got, err := f.Search(context.Background(), oneTarget)
	if err != nil || got[0].Provider != "serpapi" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("attempt timeout not applied")
	}
}

func TestFallbackAllFail(t *testing.T) {
	f, _ := NewFallback([]Provider{
		&stubProvider{name: "a", err: errors.New("a down")},
		&stubProvider{name: "b", err: errors.New("b down")},
	}, FallbackOptions{}, discard)

	_, err := f.Search(context.Background(), oneTarget)
	if err == nil || err.Error() != "a down\nb down" {
		t.Fatalf("err = %v", err)
	}
}

func TestFallbackStopsWhenSearchTimesOut(t *testing.T) {
	free := &stubProvider{name: "searxng", delay: time.Second}
	paid := &stubProvider{name: "serpapi"}
	f, _ := NewFallback([]Provider{free, paid}, FallbackOptions{AttemptTimeout: time.Second}, discard)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := f.Search(ctx, oneTarget); err == nil {
		t.Fatal("expected error")
	}
	if paid.calls.Load() != 0 {
		t.Error("fallback ran after the overall deadline")
	}
}

func TestFallbackCircuitBreaker(t *testing.T) {
	free := &stubProvider{name: "searxng", err: errors.New("down")}
	paid := &stubProvider{name: "serpapi"}
	f, _ := NewFallback([]Provider{free, paid}, FallbackOptions{FailureThreshold: 2, Cooldown: time.Minute}, discard)
	now := time.Now()
	f.breakers[0].now = func() time.Time { return now }

	for i := 0; i < 5; i++ {
		if _, err := f.Search(context.Background(), oneTarget); err != nil {
			t.Fatal(err)
		}
	}
	if got := free.calls.Load(); got != 2 {
		t.Errorf("broken provider called %d times, want 2 (then skipped)", got)
	}

	// After the cooldown one trial goes through; success closes the circuit.
	now = now.Add(2 * time.Minute)
	free.err = nil
	got, _ := f.Search(context.Background(), oneTarget)
	if got[0].Provider != "searxng" {
		t.Errorf("half-open trial not used: %+v", got)
	}
	if _, _ = f.Search(context.Background(), oneTarget); free.calls.Load() != 4 {
		t.Errorf("circuit not closed after success: calls = %d", free.calls.Load())
	}
}

func TestFallbackLastProviderAlwaysTried(t *testing.T) {
	only := &stubProvider{name: "serpapi", err: errors.New("down")}
	f, _ := NewFallback([]Provider{only}, FallbackOptions{FailureThreshold: 1}, discard)
	for i := 0; i < 3; i++ {
		_, _ = f.Search(context.Background(), oneTarget)
	}
	if only.calls.Load() != 3 {
		t.Errorf("last resort skipped: calls = %d", only.calls.Load())
	}
}

func TestNewFallbackValidation(t *testing.T) {
	if _, err := NewFallback(nil, FallbackOptions{}, discard); err == nil {
		t.Error("empty chain accepted")
	}
	mixed := []Provider{&stubProvider{name: "a", batch: true}, &stubProvider{name: "b"}}
	if _, err := NewFallback(mixed, FallbackOptions{}, discard); err == nil {
		t.Error("mixed batch modes accepted")
	}
}

// partialProvider fails for the markets in failFor and answers the others.
type partialProvider struct {
	name    string
	failFor map[string]bool
	calls   [][]string
}

func (p *partialProvider) Name() string { return p.name }
func (p *partialProvider) Batch() bool  { return true }

func (p *partialProvider) Search(_ context.Context, q Query) ([]Product, error) {
	p.calls = append(p.calls, targetIDs(q.Targets))
	var out []Product
	failed := map[string]error{}
	for _, t := range q.Targets {
		if p.failFor[t.Market] {
			failed[t.Market] = errors.New(t.Market + " down")
			continue
		}
		out = append(out, Product{Market: t.Market, Provider: p.name, Position: 1})
	}
	if len(failed) == 0 {
		return out, nil
	}
	return nil, &PartialError{Products: out, Failed: failed}
}

var threeTargets = Query{Title: "hub", Targets: []markets.Target{
	{Market: "amazon", Domain: "amazon.com"},
	{Market: "temu", Domain: "temu.com"},
	{Market: "shein", Domain: "ar.shein.com"},
}}

func TestFallbackRetriesOnlyFailedMarkets(t *testing.T) {
	free := &partialProvider{name: "searxng", failFor: map[string]bool{"temu": true}}
	paid := &partialProvider{name: "serpapi"}
	f, _ := NewFallback([]Provider{free, paid}, FallbackOptions{FailureThreshold: 1}, discard)

	got, err := f.Search(context.Background(), threeTargets)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(paid.calls) != 1 || len(paid.calls[0]) != 1 || paid.calls[0][0] != "temu" {
		t.Errorf("fallback calls = %v, want only temu", paid.calls)
	}
	byMarket := map[string]string{}
	for _, p := range got {
		byMarket[p.Market] = p.Provider
	}
	want := map[string]string{"amazon": "searxng", "temu": "serpapi", "shein": "searxng"}
	if len(got) != 3 || byMarket["amazon"] != want["amazon"] || byMarket["temu"] != want["temu"] || byMarket["shein"] != want["shein"] {
		t.Errorf("got %+v", got)
	}
	// A partial answer is not a provider failure: the circuit stays closed.
	if !f.breakers[0].allow() {
		t.Error("partial failure opened the circuit")
	}
}

func TestFallbackPartialWhenLastAlsoFails(t *testing.T) {
	free := &partialProvider{name: "searxng", failFor: map[string]bool{"temu": true, "shein": true}}
	paid := &stubProvider{name: "serpapi", batch: true, err: errors.New("no credits")}
	f, _ := NewFallback([]Provider{free, paid}, FallbackOptions{}, discard)

	_, err := f.Search(context.Background(), threeTargets)
	var pe *PartialError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *PartialError", err)
	}
	if len(pe.Products) != 1 || pe.Products[0].Market != "amazon" {
		t.Errorf("products = %+v", pe.Products)
	}
	if len(pe.Failed) != 2 || pe.Failed["temu"] == nil || pe.Failed["shein"] == nil {
		t.Errorf("failed = %v", pe.Failed)
	}
}

// shoppingLike answers amazon and leaves the other markets uncovered.
type shoppingLike struct{}

func (shoppingLike) Name() string { return "serpapi" }
func (shoppingLike) Batch() bool  { return true }
func (shoppingLike) Search(_ context.Context, q Query) ([]Product, error) {
	uncovered := map[string]bool{}
	var out []Product
	for _, t := range q.Targets {
		if t.Market == "amazon" {
			out = append(out, Product{Market: "amazon", Provider: "serpapi"})
		} else {
			uncovered[t.Market] = true
		}
	}
	return nil, &PartialError{Products: out, Uncovered: uncovered}
}

func TestFallbackUncoveredGoToNextProvider(t *testing.T) {
	free := &partialProvider{name: "searxng"}
	f, _ := NewFallback([]Provider{shoppingLike{}, free}, FallbackOptions{}, discard)

	got, err := f.Search(context.Background(), threeTargets)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(free.calls) != 1 || len(free.calls[0]) != 2 {
		t.Errorf("next provider calls = %v, want temu and shein", free.calls)
	}
	if len(got) != 3 {
		t.Errorf("got %+v", got)
	}
}

func TestFallbackUncoveredOnLastIsNoError(t *testing.T) {
	f, _ := NewFallback([]Provider{shoppingLike{}}, FallbackOptions{}, discard)
	got, err := f.Search(context.Background(), threeTargets)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v; want amazon only and no error", got, err)
	}
}
