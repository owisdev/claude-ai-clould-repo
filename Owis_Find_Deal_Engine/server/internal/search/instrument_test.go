package search

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"owis_find_deal_engine/internal/markets"
)

type recCall struct {
	provider string
	outcomes map[string]string
	cost     float64
}

type recFallback struct{ chain, from, to, reason string }

type fakeRecorder struct {
	mu        sync.Mutex
	calls     []recCall
	fallbacks []recFallback
}

func (r *fakeRecorder) SourceCall(provider string, outcomes map[string]string, _ time.Duration, cost float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recCall{provider: provider, outcomes: outcomes, cost: cost})
}

func (r *fakeRecorder) Fallback(chain, from, to, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallbacks = append(r.fallbacks, recFallback{chain, from, to, reason})
}

func TestInstrumentRecordsCalls(t *testing.T) {
	rec := &fakeRecorder{}
	if p := (&stubProvider{name: "x"}); Instrument(p, nil, nil) != Provider(p) {
		t.Error("nil recorder must return the provider itself")
	}
	ok := Instrument(&stubProvider{name: "apify"}, rec, func(m []string) float64 { return 0.1 * float64(len(m)) })
	bad := Instrument(&stubProvider{name: "serpapi", err: errors.New("down")}, rec, nil)
	if ok.Name() != "apify" {
		t.Errorf("name = %s", ok.Name())
	}
	two := Query{Title: "hub", Targets: []markets.Target{{Market: "temu"}, {Market: "shein"}}}
	_, _ = ok.Search(context.Background(), two)
	_, _ = bad.Search(context.Background(), oneTarget)
	if len(rec.calls) != 2 {
		t.Fatalf("calls = %+v", rec.calls)
	}
	// The stub answers only the first shop of a call.
	if c := rec.calls[0]; c.provider != "apify" || c.outcomes["temu"] != CallOK || c.outcomes["shein"] != CallNoResults || c.cost != 0.2 {
		t.Errorf("ok call = %+v", c)
	}
	if c := rec.calls[1]; c.outcomes["amazon"] != CallError || c.cost != 0 {
		t.Errorf("failed call = %+v", c)
	}

	// Partial answer: failed shops are errors, uncovered ones no results.
	partial := Instrument(splitProvider{}, rec, nil)
	three := Query{Title: "hub", Targets: []markets.Target{{Market: "a"}, {Market: "b"}, {Market: "c"}}}
	_, _ = partial.Search(context.Background(), three)
	if o := rec.calls[2].outcomes; o["a"] != CallOK || o["b"] != CallError || o["c"] != CallNoResults {
		t.Errorf("partial call = %+v", o)
	}
}

type splitProvider struct{}

func (splitProvider) Name() string { return "searxng" }
func (splitProvider) Batch() bool  { return true }
func (splitProvider) Search(context.Context, Query) ([]Product, error) {
	return nil, &PartialError{Products: []Product{{Market: "a"}},
		Failed: map[string]error{"b": errors.New("blocked")}, Uncovered: map[string]bool{"c": true}}
}

func TestFallbackRecordsMoves(t *testing.T) {
	rec := &fakeRecorder{}
	free := &stubProvider{name: "searxng", err: errors.New("blocked")}
	paid := &stubProvider{name: "serpapi"}
	f, _ := NewFallback([]Provider{free, paid}, FallbackOptions{FailureThreshold: 1, Cooldown: time.Minute, Recorder: rec}, discard)
	for range 2 {
		if _, err := f.Search(context.Background(), oneTarget); err != nil {
			t.Fatal(err)
		}
	}
	want := []recFallback{
		{"searxng>serpapi", "searxng", "serpapi", FallbackError},
		{"searxng>serpapi", "searxng", "serpapi", FallbackCircuitOpen},
	}
	if len(rec.fallbacks) != 2 || rec.fallbacks[0] != want[0] || rec.fallbacks[1] != want[1] {
		t.Errorf("fallbacks = %+v", rec.fallbacks)
	}
}
