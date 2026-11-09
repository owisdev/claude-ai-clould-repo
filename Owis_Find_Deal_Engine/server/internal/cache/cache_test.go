package cache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
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

// upstream is a fake search.Service: one product per shop, unless the shop
// is set to fail or to find nothing.
type upstream struct {
	clock *clock
	cat   *markets.Catalog
	calls atomic.Int32
	delay time.Duration
	err   atomic.Pointer[error] // fails the whole call
	price atomic.Int64          // changes between calls to simulate price updates

	mu     sync.Mutex
	asked  [][]string
	delays map[string]time.Duration // per shop (one-shop calls)
	fail   map[string]bool
	empty  map[string]bool
}

func (u *upstream) setErr(err error) { u.err.Store(&err) }

func (u *upstream) set(fail, empty map[string]bool) {
	u.mu.Lock()
	u.fail, u.empty = fail, empty
	u.mu.Unlock()
}

// allAsked returns every shop searched live since the last clearAsked.
func (u *upstream) allAsked() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []string
	for _, a := range u.asked {
		for _, m := range a {
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (u *upstream) setDelays(d map[string]time.Duration) {
	u.mu.Lock()
	u.delays = d
	u.mu.Unlock()
}

func (u *upstream) clearAsked() {
	u.mu.Lock()
	u.asked = nil
	u.mu.Unlock()
}

func (u *upstream) lastAsked() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.asked) == 0 {
		return nil
	}
	return u.asked[len(u.asked)-1]
}

func (u *upstream) Search(ctx context.Context, req search.Request) (*search.Result, error) {
	u.calls.Add(1)
	if u.delay > 0 {
		select {
		case <-time.After(u.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	u.mu.Lock()
	var d time.Duration
	if len(req.Markets) == 1 {
		d = u.delays[req.Markets[0]]
	}
	u.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p := u.err.Load(); p != nil && *p != nil {
		return nil, *p
	}
	country, _ := u.cat.Country(req.Country)
	ms := req.Markets
	if len(ms) == 0 {
		for _, t := range country.Targets {
			ms = append(ms, t.Market)
		}
	}
	u.mu.Lock()
	u.asked = append(u.asked, slices.Sorted(slices.Values(ms)))
	fail, empty := u.fail, u.empty
	u.mu.Unlock()

	price := float64(u.price.Load())
	now := u.clock.Now()
	res := &search.Result{Query: search.NormalizeTitle(req.Title), Country: country.Code,
		Markets: map[string]string{}, Shops: map[string]search.ShopInfo{}, FetchedAt: now}
	for _, m := range ms {
		switch {
		case fail[m]:
			res.Markets[m] = search.StatusError
			res.Shops[m] = search.ShopInfo{Status: search.StatusError, Source: "fake", TookMS: 7}
		case empty[m]:
			res.Markets[m] = search.StatusNoResults
			res.Shops[m] = search.ShopInfo{Status: search.StatusNoResults, Source: "fake", FetchedAt: &now}
		default:
			res.Markets[m] = search.StatusOK
			res.Shops[m] = search.ShopInfo{Status: search.StatusOK, Source: "fake", FetchedAt: &now}
			res.Results = append(res.Results, search.Product{Market: m, Title: "S Pen", Price: &price, Position: 1})
		}
	}
	if search.AllFailed(res.Markets) {
		return res, search.ErrAllFailed
	}
	return res, nil
}

type env struct {
	cache *Cache
	up    *upstream
	clock *clock
	store *MemoryStore
}

var allShops = map[string]Shop{
	"amazon": {Setup: "a"}, "ebay": {Setup: "e"}, "aliexpress": {Setup: "x"}, "temu": {Setup: "t"}, "shein": {Setup: "s"},
}

func testOpts() Options {
	return Options{FreshTTL: time.Hour, EmptyTTL: 10 * time.Minute, StaleTTL: 3 * time.Hour,
		LastKnownTTL: 7 * 24 * time.Hour, MinRefresh: 10 * time.Minute, Shops: allShops}
}

func newEnvCatalog(t *testing.T, cat *markets.Catalog, opts Options) *env {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	up := &upstream{clock: clk, cat: cat}
	store := NewMemoryStore(100)
	store.now = clk.Now
	c := New(up, store, cat, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.now = clk.Now
	c.queue.SetClock(clk.Now)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return &env{cache: c, up: up, clock: clk, store: store}
}

func newEnv(t *testing.T, opts Options) *env {
	t.Helper()
	cat, err := markets.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return newEnvCatalog(t, cat, opts)
}

func (e *env) searchIn(t *testing.T, country, title string, refresh bool) *search.Result {
	t.Helper()
	res, err := e.cache.Search(context.Background(), search.Request{Title: title, Country: country, Refresh: refresh})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return res
}

func (e *env) search(t *testing.T, title string, refresh bool) *search.Result {
	t.Helper()
	return e.searchIn(t, "jor", title, refresh)
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

var jorShops = []string{"aliexpress", "amazon", "shein", "temu"}

func TestMissThenHit(t *testing.T) {
	e := newEnv(t, testOpts())
	res := e.search(t, "S Pen", false)
	if res.Cached || e.up.calls.Load() != 4 || !slices.Equal(e.up.allAsked(), jorShops) {
		t.Fatalf("first search cached=%v calls=%d asked=%v", res.Cached, e.up.calls.Load(), e.up.allAsked())
	}
	if len(res.Results) != 4 || res.Shops["temu"].Cached {
		t.Fatalf("first search = %+v", res)
	}

	// Same query with different case and spacing hits the same entries.
	res = e.search(t, "  s   PEN ", false)
	if !res.Cached || res.Stale || e.up.calls.Load() != 4 || len(res.Results) != 4 || res.TookMS != 0 {
		t.Fatalf("second search cached=%v stale=%v calls=%d results=%d", res.Cached, res.Stale, e.up.calls.Load(), len(res.Results))
	}
	if s := res.Shops["amazon"]; !s.Cached || s.Status != search.StatusOK || s.FetchedAt == nil {
		t.Errorf("amazon details = %+v", s)
	}
	if hits, _, misses := e.cache.Stats(); hits != 1 || misses != 1 {
		t.Errorf("stats hits=%d misses=%d", hits, misses)
	}
}

func TestSharedBetweenCountries(t *testing.T) {
	e := newEnv(t, testOpts())
	e.search(t, "s pen", false) // jor: amazon.com, aliexpress, temu, ar.shein.com

	// usa: amazon.com, aliexpress and temu are the same requests; ebay and
	// us.shein.com are new.
	e.up.clearAsked()
	res := e.searchIn(t, "usa", "s pen", false)
	if got := e.up.allAsked(); !slices.Equal(got, []string{"ebay", "shein"}) {
		t.Errorf("usa searched %v live, want only ebay and shein", got)
	}
	if res.Cached || !res.Shops["amazon"].Cached || res.Shops["ebay"].Cached || len(res.Results) != 5 {
		t.Errorf("usa answer = %+v", res.Shops)
	}

	// A shop whose source depends on the region is not shared.
	opts := testOpts()
	opts.Shops = map[string]Shop{"amazon": {Setup: "a", ByRegion: true}}
	e = newEnv(t, opts)
	e.search(t, "s pen", false)
	e.up.clearAsked()
	e.searchIn(t, "usa", "s pen", false)
	if got := e.up.allAsked(); !slices.Contains(got, "amazon") {
		t.Errorf("usa searched %v; amazon by region must not be shared", got)
	}
}

func TestOnlyMissingShopsSearchedLive(t *testing.T) {
	e := newEnv(t, testOpts())
	e.up.set(map[string]bool{"temu": true}, nil)
	res := e.search(t, "s pen", false)
	if res.Markets["temu"] != search.StatusError || res.Shops["temu"].TookMS != 7 {
		t.Fatalf("temu = %q %+v, want error", res.Markets["temu"], res.Shops["temu"])
	}
	e.up.set(nil, nil)
	e.up.clearAsked()
	res = e.search(t, "s pen", false)
	if got := e.up.allAsked(); !slices.Equal(got, []string{"temu"}) {
		t.Errorf("searched %v live, want only temu (the failure was not stored)", got)
	}
	if res.Cached || res.Markets["temu"] != search.StatusOK || !res.Shops["amazon"].Cached {
		t.Errorf("answer = %v %+v", res.Markets, res.Shops)
	}
}

func TestLastKnownResults(t *testing.T) {
	e := newEnv(t, testOpts())
	first := e.search(t, "s pen", false).FetchedAt

	// Past the stale time: searched live; temu fails and shows its last
	// known results with their date, the others are new.
	e.clock.Add(2 * 24 * time.Hour)
	e.up.set(map[string]bool{"temu": true}, nil)
	res := e.search(t, "s pen", false)
	s := res.Shops["temu"]
	if res.Markets["temu"] != search.StatusLastKnown || s.Status != search.StatusLastKnown || !s.Cached ||
		s.FetchedAt == nil || !s.FetchedAt.Equal(first) || s.Source != "fake" {
		t.Fatalf("temu = %q %+v, want last_known from %v", res.Markets["temu"], s, first)
	}
	if len(productsOf(res.Results, "temu")) != 1 {
		t.Error("last known products missing")
	}
	if res.Cached || res.Markets["amazon"] != search.StatusOK || res.Shops["amazon"].Cached {
		t.Errorf("amazon = %+v, want live", res.Shops["amazon"])
	}
	if !res.FetchedAt.Equal(first) {
		t.Errorf("fetched_at = %v, want the oldest shop's date %v", res.FetchedAt, first)
	}

	// Older than 7 days: gone, the shop is an error.
	e.clock.Add(6 * 24 * time.Hour)
	res = e.search(t, "s pen", false)
	if res.Markets["temu"] != search.StatusError {
		t.Errorf("temu after 8 days = %q, want error", res.Markets["temu"])
	}

	// Every shop fails and every shop has last known results: an answer,
	// counted as cached (nothing new was fetched).
	e = newEnv(t, testOpts())
	e.search(t, "s pen", false)
	e.clock.Add(4 * time.Hour)
	e.up.setErr(errors.New("network down"))
	res = e.search(t, "s pen", false)
	if !res.Cached || res.Markets["amazon"] != search.StatusLastKnown {
		t.Errorf("all failed = cached %v %v", res.Cached, res.Markets)
	}
}

func TestStaleWhileRevalidate(t *testing.T) {
	e := newEnv(t, testOpts())
	e.up.price.Store(10)
	e.search(t, "s pen", false)

	// Price changes at the shop; two hours later the entries are stale.
	e.up.price.Store(12)
	e.clock.Add(2 * time.Hour)
	res := e.search(t, "s pen", false)
	if !res.Cached || !res.Stale || *res.Results[0].Price != 10 {
		t.Fatalf("stale answer: cached=%v stale=%v price=%v", res.Cached, res.Stale, *res.Results[0].Price)
	}
	e.waitBackground(t)
	if e.up.calls.Load() != 8 {
		t.Fatalf("calls = %d, want 8 (background refresh of the 4 shops)", e.up.calls.Load())
	}
	res = e.search(t, "s pen", false)
	if res.Stale || *res.Results[0].Price != 12 {
		t.Errorf("after refresh: stale=%v price=%v", res.Stale, *res.Results[0].Price)
	}
}

func TestLookupNeverFetches(t *testing.T) {
	e := newEnv(t, testOpts())
	if _, ok := e.cache.Lookup(context.Background(), search.Request{Title: "s pen", Country: "jor"}); ok {
		t.Fatal("lookup hit on empty cache")
	}
	if e.up.calls.Load() != 0 {
		t.Fatal("lookup ran a search")
	}
	e.search(t, "s pen", false)
	if _, ok := e.cache.Lookup(context.Background(), search.Request{Title: "s pen", Country: "jor"}); !ok {
		t.Fatal("lookup missed a cached answer")
	}
	// One shop missing (usa has eBay): a live search is needed.
	if _, ok := e.cache.Lookup(context.Background(), search.Request{Title: "s pen", Country: "usa"}); ok {
		t.Error("lookup hit with ebay not cached")
	}
	// An allowed refresh needs a live search.
	e.clock.Add(20 * time.Minute)
	if _, ok := e.cache.Lookup(context.Background(), search.Request{Title: "s pen", Country: "jor", Refresh: true}); ok {
		t.Error("lookup hit although a refresh is allowed")
	}
	if e.up.calls.Load() != 4 {
		t.Errorf("calls = %d", e.up.calls.Load())
	}
}

func TestEmptyShopShortTTL(t *testing.T) {
	e := newEnv(t, testOpts())
	e.up.set(nil, map[string]bool{"shein": true})
	res := e.search(t, "s pen", false)
	if res.Markets["shein"] != search.StatusNoResults {
		t.Fatalf("shein = %q", res.Markets["shein"])
	}
	e.clock.Add(15 * time.Minute) // past EmptyTTL, within FreshTTL
	e.up.clearAsked()
	res = e.search(t, "s pen", false)
	if !res.Stale || res.Shops["amazon"].Status != search.StatusOK {
		t.Errorf("stale=%v; the empty shop should be stale after 15 min", res.Stale)
	}
	e.waitBackground(t)
	if got := e.up.allAsked(); !slices.Equal(got, []string{"shein"}) {
		t.Errorf("refreshed %v, want only shein", got)
	}
}

func TestForcedRefresh(t *testing.T) {
	e := newEnv(t, testOpts())
	e.search(t, "s pen", false)
	// Too soon: refresh ignored, cached answer returned.
	if res := e.search(t, "s pen", true); !res.Cached || e.up.calls.Load() != 4 {
		t.Fatalf("early refresh: cached=%v calls=%d", res.Cached, e.up.calls.Load())
	}
	// Old enough: live fetch of every shop.
	e.clock.Add(15 * time.Minute)
	e.up.clearAsked()
	if res := e.search(t, "s pen", true); res.Cached || !slices.Equal(e.up.allAsked(), jorShops) {
		t.Fatalf("refresh: cached=%v asked=%v", res.Cached, e.up.allAsked())
	}
}

func TestErrorsNotCached(t *testing.T) {
	e := newEnv(t, testOpts())
	e.up.setErr(errors.New("down"))
	if _, err := e.cache.Search(context.Background(), search.Request{Title: "s pen", Country: "jor"}); !errors.Is(err, search.ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
	e.up.setErr(nil)
	e.search(t, "s pen", false)
	if e.up.calls.Load() != 8 {
		t.Errorf("calls = %d, failure was cached", e.up.calls.Load())
	}
}

func TestOffShopsNotSearched(t *testing.T) {
	cat, err := markets.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cat, err = cat.WithProviders(map[string]string{"shein": search.OffProvider})
	if err != nil {
		t.Fatal(err)
	}
	e := newEnvCatalog(t, cat, testOpts())
	res := e.search(t, "s pen", false)
	if slices.Contains(e.up.allAsked(), "shein") || res.Markets["shein"] != search.StatusOff || res.Shops["shein"].Status != search.StatusOff {
		t.Fatalf("asked %v, shein %q", e.up.allAsked(), res.Markets["shein"])
	}
	res = e.search(t, "s pen", false)
	if !res.Cached || e.up.calls.Load() != 3 || res.Markets["shein"] != search.StatusOff {
		t.Errorf("second search cached=%v calls=%d", res.Cached, e.up.calls.Load())
	}
}

func TestConcurrentMissesShareOneFetch(t *testing.T) {
	e := newEnv(t, testOpts())
	e.up.delay = 50 * time.Millisecond
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			res, err := e.cache.Search(context.Background(), search.Request{Title: "s pen", Country: "jor"})
			if err != nil || len(res.Results) != 4 {
				t.Errorf("Search = %v, %v", res, err)
				return
			}
			res.Results[0].Title = "changed" // callers must get their own copies
		})
	}
	wg.Wait()
	if e.up.calls.Load() != 4 {
		t.Errorf("calls = %d, want 4 (one per shop)", e.up.calls.Load())
	}
}

func TestCallerCancelDoesNotCancelSharedFetch(t *testing.T) {
	e := newEnv(t, testOpts())
	e.up.delay = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := e.cache.Search(ctx, search.Request{Title: "s pen", Country: "jor"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	// A second caller joins the same flight and still gets the result.
	res, err := e.cache.Search(context.Background(), search.Request{Title: "s pen", Country: "jor"})
	if err != nil || len(res.Results) != 4 || e.up.calls.Load() != 4 {
		t.Fatalf("Search = %v, %v, calls %d", res, err, e.up.calls.Load())
	}
}

func TestForget(t *testing.T) {
	e := newEnv(t, testOpts())
	req := search.Request{Title: "s pen", Country: "jor"}
	if found, err := e.cache.Forget(context.Background(), req); found || err != nil {
		t.Fatalf("Forget on empty = %v, %v", found, err)
	}
	e.search(t, "s pen", false)
	if found, err := e.cache.Forget(context.Background(), req); !found || err != nil {
		t.Fatalf("Forget = %v, %v", found, err)
	}
	e.clock.Add(time.Minute) // past the queue's "just finished, join it" window
	if res := e.search(t, "s pen", false); res.Cached || e.up.calls.Load() != 8 {
		t.Errorf("after Forget: cached=%v calls=%d", res.Cached, e.up.calls.Load())
	}
	if _, err := e.cache.Forget(context.Background(), search.Request{Title: "x", Country: "jor"}); !errors.Is(err, search.ErrInvalidTitle) {
		t.Errorf("invalid title err = %v", err)
	}
}

type brokenStore struct{}

func (brokenStore) Get(context.Context, string) (*Entry, error) { return nil, errors.New("down") }
func (brokenStore) Set(context.Context, string, *Entry, time.Duration) error {
	return errors.New("down")
}
func (brokenStore) Delete(context.Context, string) (bool, error) { return false, errors.New("down") }

func TestBrokenStoreStillSearches(t *testing.T) {
	e := newEnv(t, testOpts())
	e.cache.store = brokenStore{}
	if res := e.search(t, "s pen", false); len(res.Results) != 4 {
		t.Fatalf("results = %d", len(res.Results))
	}
}

func TestInvalidInputPassesThrough(t *testing.T) {
	e := newEnv(t, testOpts())
	_, _ = e.cache.Search(context.Background(), search.Request{Title: "x", Country: "jor"})
	_, _ = e.cache.Search(context.Background(), search.Request{Title: "s pen", Country: "xx"})
	if e.up.calls.Load() != 2 {
		t.Errorf("calls = %d, invalid input should reach the service", e.up.calls.Load())
	}
}

func TestKeyDependsOnSetupAndGeneration(t *testing.T) {
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor")
	amazon := jor.Targets[0]
	k := func(opts Options) string { return (&Cache{opts: opts}).key(jor, amazon, "s pen") }
	base := k(testOpts())
	o := testOpts()
	o.Shops = map[string]Shop{"amazon": {Setup: "other"}}
	if k(o) == base {
		t.Error("key ignores the shop's setup")
	}
	o = testOpts()
	o.Generation = "2"
	if k(o) == base {
		t.Error("key ignores the generation")
	}
	o = testOpts()
	o.Shops = map[string]Shop{}
	if k(o) != "" {
		t.Error("a shop without setup is cached")
	}
}

func TestCloseStopsBackgroundRefresh(t *testing.T) {
	e := newEnv(t, testOpts())
	e.search(t, "s pen", false)
	_ = e.cache.Close(context.Background())
	e.clock.Add(2 * time.Hour)
	e.search(t, "s pen", false) // stale, but no refresh after Close
	if e.up.calls.Load() != 4 {
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
		Market: "temu", Status: search.StatusOK, Source: "apify",
		Products:   []search.Product{{Market: "temu", Price: &price}},
		FetchedAt:  time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
		FreshUntil: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.Set(ctx, "k", in, time.Hour); err != nil {
		t.Fatal(err)
	}
	out, err := s.Get(ctx, "k")
	if err != nil || out == nil || *out.Products[0].Price != 9.5 || !out.FreshUntil.Equal(in.FreshUntil) || out.Source != "apify" {
		t.Fatalf("round trip = %+v, %v", out, err)
	}
	if ttl := mr.TTL("k"); ttl != time.Hour {
		t.Errorf("ttl = %v", ttl)
	}
	if ok, err := s.Delete(ctx, "k"); !ok || err != nil || mr.Exists("k") {
		t.Errorf("Delete = %v, %v", ok, err)
	}
	if ok, _ := s.Delete(ctx, "k"); ok {
		t.Error("second Delete found the key")
	}
	mr.Set("bad", "not json")
	if e, err := s.Get(ctx, "bad"); e != nil || err != nil {
		t.Errorf("corrupt entry = %v, %v; want miss", e, err)
	}
}

func TestSearchProgress(t *testing.T) {
	e := newEnv(t, testOpts())
	e.up.setDelays(map[string]time.Duration{"temu": 150 * time.Millisecond})
	var updates []*search.Result
	res, err := e.cache.SearchProgress(context.Background(), search.Request{Title: "s pen", Country: "jor"}, 0,
		func(r *search.Result) { updates = append(updates, r) })
	if err != nil || res.Markets["temu"] != search.StatusOK || len(res.Results) != 4 || len(res.Pending()) != 0 {
		t.Fatalf("final = %v, %v", res, err)
	}
	if len(updates) != 4 { // at start + after 3 of the 4 shops
		t.Fatalf("updates = %d, want 4", len(updates))
	}
	if got := updates[0].Pending(); !slices.Equal(got, jorShops) {
		t.Errorf("first update pending %v, want all", got)
	}
	last := updates[3]
	if !slices.Equal(last.Pending(), []string{"temu"}) || last.Markets["amazon"] != search.StatusOK ||
		len(productsOf(last.Results, "amazon")) != 1 || last.Shops["temu"].Status != search.StatusPending || last.Cached {
		t.Errorf("last update = %v %+v", last.Markets, last.Shops)
	}

	// Cached shops are in the first update already.
	e.clock.Add(time.Minute)
	e.up.clearAsked()
	updates = nil
	e.cache.SearchProgress(context.Background(), search.Request{Title: "s pen", Country: "usa"}, 0,
		func(r *search.Result) { updates = append(updates, r) })
	if len(updates) == 0 || updates[0].Markets["amazon"] != search.StatusOK || updates[0].Markets["ebay"] != search.StatusPending {
		t.Errorf("usa first update = %v", updates[0].Markets)
	}
}

// A slow shop that misses the wait shows its last known results; its
// search goes on and is stored for the next caller.
func TestSlowShopMissesTheWait(t *testing.T) {
	opts := testOpts()
	opts.FetchTimeout = 60 * time.Millisecond
	e := newEnv(t, opts)
	first := e.search(t, "s pen", false).FetchedAt

	e.clock.Add(4 * time.Hour) // past stale: searched live again
	e.up.setDelays(map[string]time.Duration{"temu": 200 * time.Millisecond})
	res := e.search(t, "s pen", false)
	if res.Markets["temu"] != search.StatusLastKnown || !res.Shops["temu"].FetchedAt.Equal(first) || res.Markets["amazon"] != search.StatusOK {
		t.Fatalf("answer = %v", res.Markets)
	}
	time.Sleep(300 * time.Millisecond) // temu's task finishes and is stored
	e.up.setDelays(nil)
	calls := e.up.calls.Load()
	res = e.search(t, "s pen", false)
	if !res.Cached || e.up.calls.Load() != calls || res.Markets["temu"] != search.StatusOK {
		t.Errorf("next search = cached %v, calls %d→%d, %v", res.Cached, calls, e.up.calls.Load(), res.Markets)
	}

	// No last known results: an error.
	e.up.setDelays(map[string]time.Duration{"temu": 200 * time.Millisecond})
	res = e.search(t, "new title", false)
	if res.Markets["temu"] != search.StatusError {
		t.Errorf("temu without last known = %q, want error", res.Markets["temu"])
	}
}

func TestCurrencyNormalizedFromCache(t *testing.T) {
	e := newEnv(t, testOpts())
	e.search(t, "s pen", false)
	cat, _ := markets.Load("")
	jor, _ := cat.Country("jor")
	key := e.cache.key(jor, jor.Targets[0], "s pen")
	entry, _ := e.store.Get(context.Background(), key)
	for i := range entry.Products {
		entry.Products[i].Currency = "$"
	}
	e.store.Set(context.Background(), key, entry, time.Hour)
	res := e.search(t, "s pen", false)
	for _, p := range res.Results {
		if p.Market == jor.Targets[0].Market && p.Currency != "USD" {
			t.Errorf("currency = %q, want USD", p.Currency)
		}
	}
}
