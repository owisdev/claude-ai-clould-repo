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
