package search

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"owis_find_deal_engine/internal/markets"
)

// fakeProvider returns one product per target, or an error for listed markets.
type fakeProvider struct {
	batch    bool
	failFor  map[string]bool
	panicFor map[string]bool
	delay    time.Duration

	mu    sync.Mutex
	calls [][]string

	inFlight, maxInFlight atomic.Int32
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Batch() bool  { return f.batch }

func (f *fakeProvider) Search(ctx context.Context, q Query) ([]Product, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxInFlight.Load()
		if n <= m || f.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}

	f.mu.Lock()
	f.calls = append(f.calls, targetIDs(q.Targets))
	f.mu.Unlock()

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var out []Product
	for _, t := range q.Targets {
		if f.panicFor[t.Market] {
			panic("boom")
		}
		if f.failFor[t.Market] {
			return nil, errors.New("upstream down")
		}
		out = append(out, Product{Market: t.Market, Title: q.Title, Position: 1, Provider: "fake"})
	}
	return out, nil
}

func newTestService(t *testing.T, p Provider, opts Options) *Service {
	t.Helper()
	cat, err := markets.Load("")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(cat, map[string]Provider{"web": p}, opts, log)
}

func TestSearchBatchedUsesOneCall(t *testing.T) {
	p := &fakeProvider{batch: true}
	svc := newTestService(t, p, Options{})

	res, err := svc.Search(context.Background(), Request{Title: "  samsung   s pen ", Country: "JOR"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(p.calls) != 1 || len(p.calls[0]) != 4 {
		t.Fatalf("calls = %v, want one call with 4 markets", p.calls)
	}
	if res.Query != "samsung s pen" || res.Country != "jor" {
		t.Errorf("query/country = %q/%q", res.Query, res.Country)
	}
	if _, ok := res.Markets["ebay"]; ok {
		t.Error("ebay must not be searched for jor")
	}
	want := []string{"amazon", "aliexpress", "temu", "shein"}
	for i, p := range res.Results {
		if p.Market != want[i] {
			t.Errorf("result %d market = %s, want %s", i, p.Market, want[i])
		}
	}
}

func TestSearchPerTargetIsParallelAndBounded(t *testing.T) {
	p := &fakeProvider{delay: 50 * time.Millisecond}
	svc := newTestService(t, p, Options{MaxConcurrency: 2})

	res, err := svc.Search(context.Background(), Request{Title: "usb hub", Country: "usa"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(p.calls) != 5 {
		t.Fatalf("calls = %d, want 5", len(p.calls))
	}
	if got := p.maxInFlight.Load(); got != 2 {
		t.Errorf("max concurrent calls = %d, want 2", got)
	}
	if len(res.Results) != 5 {
		t.Errorf("results = %d, want 5", len(res.Results))
	}
}

func TestSearchPartialFailure(t *testing.T) {
	p := &fakeProvider{failFor: map[string]bool{"temu": true}, panicFor: map[string]bool{"shein": true}}
	svc := newTestService(t, p, Options{})

	res, err := svc.Search(context.Background(), Request{Title: "usb hub", Country: "jor"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := map[string]string{"amazon": "ok", "aliexpress": "ok", "temu": "error", "shein": "error"}
	for m, s := range want {
		if res.Markets[m] != s {
			t.Errorf("market %s = %q, want %q", m, res.Markets[m], s)
		}
	}
	if len(res.Results) != 2 {
		t.Errorf("results = %d, want 2", len(res.Results))
	}
}

// emptyProvider finds nothing for some markets and fails for others
// through a *PartialError, like a batch provider searching each market.
type emptyProvider struct{ empty, fail map[string]bool }

func (p *emptyProvider) Name() string { return "fake" }
func (p *emptyProvider) Batch() bool  { return true }

func (p *emptyProvider) Search(_ context.Context, q Query) ([]Product, error) {
	var out []Product
	failed := map[string]error{}
	for _, t := range q.Targets {
		switch {
		case p.fail[t.Market]:
			failed[t.Market] = errors.New("down")
		case !p.empty[t.Market]:
			out = append(out, Product{Market: t.Market, Position: 1})
		}
	}
	if len(failed) > 0 {
		return nil, &PartialError{Products: out, Failed: failed}
	}
	return out, nil
}

func TestSearchMarketStatuses(t *testing.T) {
	p := &emptyProvider{empty: map[string]bool{"aliexpress": true}, fail: map[string]bool{"temu": true}}
	svc := newTestService(t, p, Options{})

	res, err := svc.Search(context.Background(), Request{Title: "usb hub", Country: "jor"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := map[string]string{"amazon": "ok", "aliexpress": "no_results", "temu": "error", "shein": "ok"}
	for m, s := range want {
		if res.Markets[m] != s {
			t.Errorf("market %s = %q, want %q", m, res.Markets[m], s)
		}
	}
	if len(res.Results) != 2 || !res.Partial() {
		t.Errorf("results = %d, partial = %v", len(res.Results), res.Partial())
	}
}

func TestSearchNothingFoundIsNotAFailure(t *testing.T) {
	p := &emptyProvider{empty: map[string]bool{"amazon": true, "aliexpress": true, "temu": true, "shein": true}}
	svc := newTestService(t, p, Options{})

	res, err := svc.Search(context.Background(), Request{Title: "zzqx", Country: "jor"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Partial() || res.Markets["amazon"] != "no_results" {
		t.Errorf("markets = %v", res.Markets)
	}
}

func TestSearchAllFailed(t *testing.T) {
	p := &fakeProvider{batch: true, failFor: map[string]bool{"amazon": true}}
	svc := newTestService(t, p, Options{})

	_, err := svc.Search(context.Background(), Request{Title: "usb hub", Country: "jor"})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
}

func TestSearchTimeout(t *testing.T) {
	p := &fakeProvider{delay: time.Second}
	svc := newTestService(t, p, Options{Timeout: 30 * time.Millisecond})

	start := time.Now()
	_, err := svc.Search(context.Background(), Request{Title: "usb hub", Country: "jor"})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("search did not stop at the timeout")
	}
}

func TestSearchValidation(t *testing.T) {
	svc := newTestService(t, &fakeProvider{}, Options{})
	ctx := context.Background()

	if _, err := svc.Search(ctx, Request{Title: "usb hub", Country: "fra"}); !errors.Is(err, ErrUnknownCountry) {
		t.Errorf("unknown country: err = %v", err)
	}
	if _, err := svc.Search(ctx, Request{Title: " a ", Country: "usa"}); !errors.Is(err, ErrInvalidTitle) {
		t.Errorf("short title: err = %v", err)
	}
	long := make([]byte, MaxTitleLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := svc.Search(ctx, Request{Title: string(long), Country: "usa"}); !errors.Is(err, ErrInvalidTitle) {
		t.Errorf("long title: err = %v", err)
	}
}

func TestSearchMissingProvider(t *testing.T) {
	cat, _ := markets.Load("")
	svc := NewService(cat, map[string]Provider{}, Options{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	res, err := svc.Search(context.Background(), Request{Title: "usb hub", Country: "jor"})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
	if res.Markets["amazon"] != StatusError {
		t.Errorf("amazon status = %q", res.Markets["amazon"])
	}
}
