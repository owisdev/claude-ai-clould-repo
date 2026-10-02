package cache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// upstream is a fake search.Service.
type upstream struct {
	clock   *clock
	calls   atomic.Int32
	delay   time.Duration
	err     atomic.Pointer[error]
	partial bool
	empty   bool
	price   atomic.Int64 // changes between calls to simulate price updates
}

func (u *upstream) setErr(err error) { u.err.Store(&err) }

func (u *upstream) Search(ctx context.Context, req search.Request) (*search.Result, error) {
	u.calls.Add(1)
	if u.delay > 0 {
		select {
		case <-time.After(u.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p := u.err.Load(); p != nil && *p != nil {
		return nil, *p
	}
	price := float64(u.price.Load())
	res := &search.Result{
		Query:     search.NormalizeTitle(req.Title),
		Country:   req.Country,
		Markets:   map[string]string{"amazon": "ok"},
		FetchedAt: u.clock.Now(),
	}
	if !u.empty {
		res.Results = []search.Product{{Market: "amazon", Title: "S Pen", Price: &price, Position: 1}}
	}
	if u.partial {
		res.Markets["temu"] = search.StatusError
	}
	return res, nil
}

type env struct {
	cache *Cache
	up    *upstream
	clock *clock
	store *MemoryStore
}

func newEnv(t *testing.T, opts Options) *env {
	t.Helper()
	cat, err := markets.Load("")
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	up := &upstream{clock: clk}
	store := NewMemoryStore(100)
	store.now = clk.Now
	c := New(up, store, cat, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.now = clk.Now
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return &env{cache: c, up: up, clock: clk, store: store}
}

func (e *env) search(t *testing.T, title string, refresh bool) *search.Result {
	t.Helper()
	res, err := e.cache.Search(context.Background(), search.Request{Title: title, Country: "jor", Refresh: refresh})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return res
}

// waitBackground waits for background refreshes to finish.
func (e *env) waitBackground(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.cache.mu.Lock()
	e.cache.mu.Unlock()
	done := make(chan struct{})
	go func() { e.cache.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("background refresh did not finish")
	}
}

func TestMissThenHit(t *testing.T) {
	e := newEnv(t, Options{})
	first := e.search(t, "Samsung S Pen", false)
	if first.Cached || first.Stale {
		t.Errorf("first = cached %v stale %v, want live", first.Cached, first.Stale)
	}
	// Same query with different case and spacing hits the same entry.
	second := e.search(t, "  samsung   s PEN ", false)
	if !second.Cached || second.Stale {
		t.Errorf("second = cached %v stale %v, want fresh hit", second.Cached, second.Stale)
	}
	if !second.FetchedAt.Equal(first.FetchedAt) {
		t.Error("cached result lost fetched_at")
	}
	if e.up.calls.Load() != 1 {
		t.Errorf("upstream calls = %d, want 1", e.up.calls.Load())
	}

	// Another country is another entry.
	if _, err := e.cache.Search(context.Background(), search.Request{Title: "samsung s pen", Country: "usa"}); err != nil {
		t.Fatal(err)
	}
	if e.up.calls.Load() != 2 {
		t.Errorf("upstream calls = %d, want 2", e.up.calls.Load())
	}
}

func TestStaleWhileRevalidate(t *testing.T) {
	e := newEnv(t, Options{FreshTTL: time.Hour, StaleTTL: 24 * time.Hour})
	e.up.price.Store(100)
	e.search(t, "s pen", false)

	// Price changes at the shop; two hours later the entry is stale.
	e.up.price.Store(80)
	e.clock.Add(2 * time.Hour)

	stale := e.search(t, "s pen", false)
	if !stale.Cached || !stale.Stale || *stale.Results[0].Price != 100 {
		t.Fatalf("stale answer = %+v, want old price served immediately", stale)
	}
	e.waitBackground(t)
	if e.up.calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (one background refresh)", e.up.calls.Load())
	}

	fresh := e.search(t, "s pen", false)
	if !fresh.Cached || fresh.Stale || *fresh.Results[0].Price != 80 {
		t.Errorf("after refresh = %+v, want new price as fresh hit", fresh)
	}
}

func TestLookupNeverFetches(t *testing.T) {
	e := newEnv(t, Options{MinRefresh: time.Minute})
	ctx := context.Background()
	req := search.Request{Title: "s pen", Country: "jor"}

	if _, ok := e.cache.Lookup(ctx, req); ok {
		t.Fatal("lookup hit on empty cache")
	}
	if e.up.calls.Load() != 0 {
		t.Fatal("lookup called upstream")
	}
	e.search(t, "s pen", false)
	if res, ok := e.cache.Lookup(ctx, req); !ok || !res.Cached {
		t.Fatalf("lookup after search = %+v, %v", res, ok)
	}
	// An allowed refresh needs a live search: lookup must say no.
	e.clock.Add(2 * time.Minute)
	if _, ok := e.cache.Lookup(ctx, search.Request{Title: "s pen", Country: "jor", Refresh: true}); ok {
		t.Error("lookup answered an allowed refresh from cache")
	}
	if _, ok := e.cache.Lookup(ctx, search.Request{Title: "x", Country: "jor"}); ok {
		t.Error("lookup answered invalid input")
	}
}

func TestExpiredEntryFetchedLive(t *testing.T) {
	e := newEnv(t, Options{FreshTTL: time.Hour, StaleTTL: 3 * time.Hour})
	e.search(t, "s pen", false)
	e.clock.Add(4 * time.Hour)
	if res := e.search(t, "s pen", false); res.Cached {
		t.Error("expired entry served")
	}
	if e.up.calls.Load() != 2 {
		t.Errorf("upstream calls = %d", e.up.calls.Load())
	}
}

func TestShortTTLForPartialAndEmpty(t *testing.T) {
	for name, set := range map[string]func(*upstream){
		"partial": func(u *upstream) { u.partial = true },
		"empty":   func(u *upstream) { u.empty = true },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, Options{FreshTTL: 2 * time.Hour, PartialTTL: 10 * time.Minute, EmptyTTL: 10 * time.Minute})
			set(e.up)
			e.search(t, "s pen", false)
			e.clock.Add(15 * time.Minute)
			if res := e.search(t, "s pen", false); !res.Stale {
				t.Error("partial/empty result still fresh after its short TTL")
			}
		})
	}
}

func TestShortTTLsClampedToFresh(t *testing.T) {
	o := Options{FreshTTL: time.Minute, PartialTTL: time.Hour, EmptyTTL: time.Hour}
	o.defaults()
	if o.PartialTTL != time.Minute || o.EmptyTTL != time.Minute {
		t.Errorf("partial=%v empty=%v, want clamped to 1m", o.PartialTTL, o.EmptyTTL)
	}
}

func TestForcedRefresh(t *testing.T) {
	e := newEnv(t, Options{MinRefresh: 10 * time.Minute})
	e.search(t, "s pen", false)

	// Too soon: refresh ignored, cached answer returned.
	e.clock.Add(5 * time.Minute)
	if res := e.search(t, "s pen", true); !res.Cached {
		t.Error("refresh within MinRefresh hit upstream")
	}
	// Old enough: live fetch.
	e.clock.Add(10 * time.Minute)
	if res := e.search(t, "s pen", true); res.Cached {
		t.Error("refresh after MinRefresh served cache")
	}
	if e.up.calls.Load() != 2 {
		t.Errorf("upstream calls = %d, want 2", e.up.calls.Load())
	}
}

func TestStaleIfError(t *testing.T) {
	e := newEnv(t, Options{MinRefresh: time.Minute})
	e.search(t, "s pen", false)
	e.up.setErr(search.ErrAllFailed)
	e.clock.Add(time.Hour)

	res := e.search(t, "s pen", true)
	if !res.Cached || !res.Stale {
		t.Errorf("want stale cached answer when live refresh fails, got %+v", res)
	}
}

func TestErrorsNotCached(t *testing.T) {
	e := newEnv(t, Options{})
	e.up.setErr(search.ErrAllFailed)
	if _, err := e.cache.Search(context.Background(), search.Request{Title: "s pen", Country: "jor"}); !errors.Is(err, search.ErrAllFailed) {
		t.Fatalf("err = %v", err)
	}
	e.up.setErr(nil)
	if res := e.search(t, "s pen", false); res.Cached {
		t.Error("failure was cached")
	}
}

func TestConcurrentMissesShareOneFetch(t *testing.T) {
	e := newEnv(t, Options{})
	e.up.delay = 50 * time.Millisecond

	var wg sync.WaitGroup
	results := make([]*search.Result, 20)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.cache.Search(context.Background(), search.Request{Title: "s pen", Country: "jor"})
			if err != nil {
				t.Error(err)
				return
			}
			res.Stale = true // callers must get their own copies
			results[i] = res
		}()
	}
	wg.Wait()
	if got := e.up.calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}
	if res := e.search(t, "s pen", false); res.Stale {
		t.Error("a caller's change leaked into the cache")
	}
}

func TestCallerCancelDoesNotCancelSharedFetch(t *testing.T) {
	e := newEnv(t, Options{})
	e.up.delay = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := e.cache.Search(ctx, search.Request{Title: "s pen", Country: "jor"})
		errCh <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller err = %v", err)
	}
	// A second caller joins the same flight and still gets the result.
	if res := e.search(t, "s pen", false); len(res.Results) != 1 {
		t.Errorf("second caller = %+v", res)
	}
	if got := e.up.calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}
}

type brokenStore struct{}

func (brokenStore) Get(context.Context, string) (*Entry, error) { return nil, errors.New("down") }
func (brokenStore) Set(context.Context, string, *Entry, time.Duration) error {
	return errors.New("down")
}

func TestBrokenStoreStillSearches(t *testing.T) {
	cat, _ := markets.Load("")
	up := &upstream{clock: &clock{t: time.Now()}}
	c := New(up, brokenStore{}, cat, Options{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := c.Search(context.Background(), search.Request{Title: "s pen", Country: "jor"}); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestInvalidInputPassesThrough(t *testing.T) {
	e := newEnv(t, Options{})
	for _, req := range []search.Request{{Title: "x", Country: "jor"}, {Title: "s pen", Country: "fra"}} {
		_, _ = e.cache.Search(context.Background(), req)
	}
	if e.store.Len() != 0 {
		t.Error("invalid requests were cached")
	}
}

func TestKeyDependsOnMarkets(t *testing.T) {
	e := newEnv(t, Options{})
	jor, _ := e.cache.key(search.Request{Title: "S Pen", Country: "jor"})
	jor2, _ := e.cache.key(search.Request{Title: "s  pen", Country: "JOR"})
	ksa, _ := e.cache.key(search.Request{Title: "s pen", Country: "ksa"})
	if jor != jor2 || jor == ksa {
		t.Errorf("keys: jor=%s jor2=%s ksa=%s", jor, jor2, ksa)
	}
}

func TestCloseStopsBackgroundRefresh(t *testing.T) {
	e := newEnv(t, Options{FreshTTL: time.Minute})
	e.search(t, "s pen", false)
	if err := e.cache.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Hour)
	e.search(t, "s pen", false) // stale, but no refresh after Close
	if e.up.calls.Load() != 1 {
		t.Errorf("refresh started after Close: calls = %d", e.up.calls.Load())
	}
}

func TestMemoryStoreLRU(t *testing.T) {
	m := NewMemoryStore(2)
	ctx := context.Background()
	for _, k := range []string{"a", "b"} {
		_ = m.Set(ctx, k, &Entry{}, time.Hour)
	}
	_, _ = m.Get(ctx, "a") // a is now most recent
	_ = m.Set(ctx, "c", &Entry{}, time.Hour)
	if e, _ := m.Get(ctx, "b"); e != nil {
		t.Error("least recently used entry not evicted")
	}
	if e, _ := m.Get(ctx, "a"); e == nil {
		t.Error("recently used entry evicted")
	}
}

func TestRedisStore(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := NewRedisStore(rdb)
	ctx := context.Background()

	if e, err := s.Get(ctx, "missing"); e != nil || err != nil {
		t.Fatalf("miss = %v, %v", e, err)
	}
	price := 9.5
	in := &Entry{
		Result:     search.Result{Query: "s pen", Results: []search.Product{{Market: "temu", Price: &price}}},
		FreshUntil: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.Set(ctx, "k", in, time.Hour); err != nil {
		t.Fatal(err)
	}
	out, err := s.Get(ctx, "k")
	if err != nil || out == nil || *out.Result.Results[0].Price != 9.5 || !out.FreshUntil.Equal(in.FreshUntil) {
		t.Fatalf("round trip = %+v, %v", out, err)
	}
	if ttl := mr.TTL("k"); ttl != time.Hour {
		t.Errorf("ttl = %v", ttl)
	}
	mr.Set("bad", "not json")
	if e, err := s.Get(ctx, "bad"); e != nil || err != nil {
		t.Errorf("corrupt entry = %v, %v; want miss", e, err)
	}
}
