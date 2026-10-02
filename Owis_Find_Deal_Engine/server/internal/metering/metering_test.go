package metering

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"
)

// fakeSearcher answers Lookup from cached and Search from live/err.
type fakeSearcher struct {
	cached     *search.Result
	live       *search.Result
	err        error
	liveCalls  atomic.Int32
	lookupHits atomic.Int32
}

func (f *fakeSearcher) Lookup(context.Context, search.Request) (*search.Result, bool) {
	if f.cached == nil {
		return nil, false
	}
	f.lookupHits.Add(1)
	r := *f.cached
	return &r, true
}

func (f *fakeSearcher) Search(context.Context, search.Request) (*search.Result, error) {
	f.liveCalls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	r := *f.live
	return &r, nil
}

var (
	alice  = auth.User{ID: "alice"}
	req    = search.Request{Title: "s pen", Country: "jor"}
	live   = &search.Result{Query: "s pen"}
	cached = &search.Result{Query: "s pen", Cached: true}
)

func newService(t *testing.T, s Searcher, plans string) (*Service, *usage.Quota) {
	t.Helper()
	p, err := usage.ParsePlans(plans, "free")
	if err != nil {
		t.Fatal(err)
	}
	q := usage.NewQuota(usage.NewMemoryStore(), p)
	return New(s, q, slog.New(slog.NewTextHandler(io.Discard, nil))), q
}

func remaining(t *testing.T, q *usage.Quota, user auth.User) int64 {
	t.Helper()
	d, err := q.Status(context.Background(), user.ID, user.Plan)
	if err != nil {
		t.Fatal(err)
	}
	return d.Remaining()
}

func TestLiveSearchIsCounted(t *testing.T) {
	f := &fakeSearcher{live: live}
	m, q := newService(t, f, "free:3")

	out, err := m.Search(context.Background(), alice, req)
	if err != nil || out.Result == nil || !out.QuotaKnown || out.Quota.Remaining() != 2 {
		t.Fatalf("out = %+v, %v", out, err)
	}
	if remaining(t, q, alice) != 2 {
		t.Error("live search not counted")
	}
}

func TestCachedAnswerIsFree(t *testing.T) {
	f := &fakeSearcher{cached: cached}
	m, q := newService(t, f, "free:3")

	for i := 0; i < 5; i++ {
		out, err := m.Search(context.Background(), alice, req)
		if err != nil || !out.Result.Cached || out.Quota.Remaining() != 3 {
			t.Fatalf("search %d: out = %+v, %v", i, out, err)
		}
	}
	if f.liveCalls.Load() != 0 || remaining(t, q, alice) != 3 {
		t.Errorf("cached answers consumed quota or searched live")
	}
}

func TestCachedAnswerServedWhenAllowanceUsedUp(t *testing.T) {
	f := &fakeSearcher{live: live}
	m, _ := newService(t, f, "free:1")
	ctx := context.Background()

	if _, err := m.Search(ctx, alice, req); err != nil {
		t.Fatal(err)
	}
	_, err := m.Search(ctx, alice, req)
	var qe *QuotaError
	if !errors.As(err, &qe) || !qe.UpgradeRequired() {
		t.Fatalf("err = %v, want free-plan QuotaError", err)
	}

	f.cached = cached
	out, err := m.Search(ctx, alice, req)
	if err != nil || !out.Result.Cached || out.Quota.Remaining() != 0 {
		t.Errorf("cached answer over quota: out = %+v, %v", out, err)
	}
}

func TestPaidPlanQuotaError(t *testing.T) {
	m, _ := newService(t, &fakeSearcher{live: live}, "free:5,pro:1")
	pro := auth.User{ID: "bob", Plan: "pro"}
	_, _ = m.Search(context.Background(), pro, req)
	_, err := m.Search(context.Background(), pro, req)
	var qe *QuotaError
	if !errors.As(err, &qe) || qe.UpgradeRequired() {
		t.Errorf("err = %v, want paid-plan QuotaError", err)
	}
}

func TestFailedOrCachedLiveSearchIsRefunded(t *testing.T) {
	tests := map[string]*fakeSearcher{
		"upstream failed":         {err: search.ErrAllFailed},
		"invalid title":           {err: search.ErrInvalidTitle},
		"filled by other request": {live: cached},
	}
	for name, f := range tests {
		t.Run(name, func(t *testing.T) {
			m, q := newService(t, f, "free:2")
			out, err := m.Search(context.Background(), alice, req)
			if f.err != nil && !errors.Is(err, f.err) {
				t.Errorf("err = %v, want %v", err, f.err)
			}
			if remaining(t, q, alice) != 2 {
				t.Error("not refunded")
			}
			if out.QuotaKnown && out.Quota.Remaining() != 2 {
				t.Errorf("reported remaining = %d, want 2", out.Quota.Remaining())
			}
		})
	}
}

type brokenQuota struct{}

func (brokenQuota) Take(context.Context, string, string) (usage.Decision, error) {
	return usage.Decision{}, errors.New("redis down")
}
func (brokenQuota) Refund(context.Context, usage.Decision) error { return nil }
func (brokenQuota) Status(context.Context, string, string) (usage.Decision, error) {
	return usage.Decision{}, errors.New("redis down")
}

func TestQuotaDown(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Live search: fail closed.
	m := New(&fakeSearcher{live: live}, brokenQuota{}, log)
	if _, err := m.Search(context.Background(), alice, req); !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("live: err = %v, want ErrQuotaUnavailable", err)
	}

	// Cached answer: still served, allowance just unknown.
	m = New(&fakeSearcher{cached: cached}, brokenQuota{}, log)
	out, err := m.Search(context.Background(), alice, req)
	if err != nil || out.Result == nil || out.QuotaKnown {
		t.Errorf("cached: out = %+v, %v", out, err)
	}
}

func TestRequiresUser(t *testing.T) {
	m, _ := newService(t, &fakeSearcher{live: live}, "free:1")
	if _, err := m.Search(context.Background(), auth.User{}, req); err == nil {
		t.Error("anonymous search accepted")
	}
}

func TestWithoutCacheNeverFree(t *testing.T) {
	f := &fakeSearcher{live: live, cached: cached}
	s := WithoutCache(f)
	if _, ok := s.Lookup(context.Background(), req); ok {
		t.Error("WithoutCache answered from cache")
	}
	if _, err := s.Search(context.Background(), req); err != nil || f.liveCalls.Load() != 1 {
		t.Errorf("Search not delegated: %v", err)
	}
}
