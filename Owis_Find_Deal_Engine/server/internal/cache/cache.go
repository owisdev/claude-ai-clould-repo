// Package cache puts a stale-while-revalidate cache in front of the search
// service so repeated searches cost nothing and prices still stay current.
//
// Each shop's results are stored on their own (step 14b), keyed by the
// shop, its site, the setup of its source and the normalized title, so
//   - a search only runs live for the shops it has no answer for;
//   - countries share a shop's results when the request to the shop is the
//     same (AliExpress and Temu in every country, amazon.com for USA and
//     Jordan);
//   - a shop whose live search fails shows its last known results.
//
// Freshness of a shop's results, by age since they were fetched:
//
//	0 ── FreshTTL ──────────► served from cache
//	FreshTTL ── StaleTTL ───► served from cache (stale=true) and refreshed
//	                          in the background for the next caller
//	StaleTTL ── LastKnownTTL► searched live; shown (status last_known, with
//	                          their date) only when the live search fails
//	after LastKnownTTL ─────► gone
//
// A shop that found nothing (EmptyTTL) is fresh for a shorter time so it is
// retried sooner; failures are never stored. A client may force a live
// fetch (Request.Refresh), at most once per MinRefresh per shop.
//
// Each shop that needs a live search is one task of the queue (step 14b
// part 3): the same shop and title already queued or running is joined,
// runs at once are capped per source, and a search can report its answer
// so far each time a shop finishes (SearchProgress).
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/queue"
	"owis_find_deal_engine/internal/search"
)

// Searcher is what the cache wraps (search.Service).
type Searcher interface {
	Search(ctx context.Context, req search.Request) (*search.Result, error)
}

// Entry is one shop's stored results for one title.
type Entry struct {
	Market     string           `json:"market"`
	Status     string           `json:"status"` // ok or no_results
	Source     string           `json:"source"`
	TookMS     int64            `json:"took_ms"`
	Products   []search.Product `json:"products"`
	FetchedAt  time.Time        `json:"fetched_at"`
	FreshUntil time.Time        `json:"fresh_until"`
}

// Store persists entries; Redis when shared by several instances,
// memory otherwise. Get returns (nil, nil) on a miss.
type Store interface {
	Get(ctx context.Context, key string) (*Entry, error)
	Set(ctx context.Context, key string, e *Entry, ttl time.Duration) error
	// Delete removes key; a missing key is not an error. It reports
	// whether the key was there.
	Delete(ctx context.Context, key string) (bool, error)
}

// Shop describes what a shop's answers depend on besides its site and the
// title. A shop missing from Options.Shops is never cached.
type Shop struct {
	// Setup describes the shop's source and its settings (source, actor,
	// input, results kept, ...): changing it starts a new set of entries.
	Setup string
	// ByRegion is set when the source's answer depends on the country's
	// region or language (web search, a scraper input with {{country}}):
	// such entries are not shared between countries.
	ByRegion bool
}

// Options tunes freshness.
type Options struct {
	FreshTTL     time.Duration // a shop with results
	EmptyTTL     time.Duration // a shop that found nothing
	StaleTTL     time.Duration // how long results may be served at all
	LastKnownTTL time.Duration // how long they are kept as the backup
	MinRefresh   time.Duration // forced refresh is ignored for younger entries
	// FetchTimeout bounds a fetch that runs detached from the caller
	// (merged requests and background refreshes).
	FetchTimeout time.Duration
	// MaxBackgroundRefresh caps concurrent background refreshes.
	MaxBackgroundRefresh int
	// Generation is part of every key: changing it (admin tool "bump
	// results version") makes every entry a miss.
	Generation string
	// Shops holds each cached shop's setup, by market id.
	Shops map[string]Shop
	// Queue runs the live search of each shop. Nil: the cache makes its
	// own queue in memory (tests, one server).
	Queue *queue.Queue
}

func (o *Options) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&o.FreshTTL, 2*time.Hour)
	set(&o.EmptyTTL, 30*time.Minute)
	set(&o.StaleTTL, 24*time.Hour)
	set(&o.LastKnownTTL, 7*24*time.Hour)
	set(&o.MinRefresh, 10*time.Minute)
	set(&o.FetchTimeout, 20*time.Second)
	// Empty results are never fresh for longer than full ones, and the
	// backup is kept at least as long as results are served.
	o.EmptyTTL = min(o.EmptyTTL, o.FreshTTL)
	o.LastKnownTTL = max(o.LastKnownTTL, o.StaleTTL)
	if o.MaxBackgroundRefresh <= 0 {
		o.MaxBackgroundRefresh = 4
	}
}

// Cache wraps a Searcher. Safe for concurrent use; call Close on shutdown.
type Cache struct {
	next    Searcher
	store   Store
	catalog *markets.Catalog
	opts    Options
	log     *slog.Logger
	now     func() time.Time

	queue *queue.Queue
	ownQ  bool          // the queue is the cache's own: closed with it
	sem   chan struct{} // background refresh slots

	mu     sync.Mutex // guards closed + wg.Add
	closed bool
	wg     sync.WaitGroup

	hits, stale, misses atomic.Int64
}

// New returns a Cache in front of next.
func New(next Searcher, store Store, catalog *markets.Catalog, opts Options, log *slog.Logger) *Cache {
	opts.defaults()
	c := &Cache{
		next:    next,
		store:   store,
		catalog: catalog,
		opts:    opts,
		log:     log,
		now:     time.Now,
		queue:   opts.Queue,
		sem:     make(chan struct{}, opts.MaxBackgroundRefresh),
	}
	if c.queue == nil {
		c.queue, c.ownQ = queue.NewMemory(queue.Options{}, log), true
		c.queue.SetRunner(c.RunTask)
		c.queue.Start()
	}
	return c
}

// shopState is one shop of a search and what the cache has for it.
type shopState struct {
	target markets.Target
	key    string // "" = not cached
	entry  *Entry // stored results, nil if none (or too old to keep)
	// serve: entry answers this search (fresh or stale); stale: it is
	// past its fresh time and gets refreshed in the background.
	serve, stale bool
}

// plan is one search as the cache sees it.
type plan struct {
	req     search.Request
	country markets.Country
	title   string
	shops   []*shopState
}

// need returns the shops that must be searched live.
func (p *plan) need() []*shopState {
	var out []*shopState
	for _, s := range p.shops {
		if !s.serve && s.target.Provider != search.OffProvider {
			out = append(out, s)
		}
	}
	return out
}

func (p *plan) staleShops() []*shopState {
	var out []*shopState
	for _, s := range p.shops {
		if s.serve && s.stale {
			out = append(out, s)
		}
	}
	return out
}

// newPlan reads the cache for every shop of the search. ok is false for
// invalid input.
func (c *Cache) newPlan(ctx context.Context, req search.Request) (*plan, bool) {
	country, ok := c.catalog.Country(req.Country)
	title := strings.ToLower(search.NormalizeTitle(req.Title))
	if !ok || len([]rune(title)) < search.MinTitleLen || len([]rune(title)) > search.MaxTitleLen {
		return nil, false
	}
	p := &plan{req: req, country: country, title: title}
	now := c.now()
	for _, t := range country.Targets {
		if len(req.Markets) > 0 && !slices.Contains(req.Markets, t.Market) {
			continue
		}
		s := &shopState{target: t}
		p.shops = append(p.shops, s)
		if t.Provider == search.OffProvider {
			continue
		}
		s.key = c.key(country, t, title)
		if s.key == "" {
			continue
		}
		s.entry = c.get(ctx, s.key)
		if s.entry == nil {
			continue
		}
		age := now.Sub(s.entry.FetchedAt)
		if age >= c.opts.StaleTTL || (req.Refresh && age >= c.opts.MinRefresh) {
			continue // searched live; the entry stays as the backup
		}
		s.serve = true
		s.stale = !now.Before(s.entry.FreshUntil)
	}
	return p, true
}

// Lookup answers from the cache only; it never runs a live search for the
// caller. ok is false when a live search is needed for at least one shop
// (not cached, too old, or a refresh was asked for and is allowed). Stale
// shops are refreshed in the background.
func (c *Cache) Lookup(ctx context.Context, req search.Request) (res *search.Result, ok bool) {
	p, valid := c.newPlan(ctx, req)
	if !valid || len(p.need()) > 0 {
		return nil, false
	}
	return c.answerFromCache(p), true
}

func (c *Cache) answerFromCache(p *plan) *search.Result {
	stale := p.staleShops()
	if len(stale) > 0 {
		c.stale.Add(1)
		c.refreshInBackground(p, stale)
	} else {
		c.hits.Add(1)
	}
	return c.assemble(p, nil, time.Time{}, true)
}

// Search answers from the cache when possible; the shops it has no answer
// for are searched live, waiting up to FetchTimeout for them (a shop not
// done by then shows its last known results, else an error, and is still
// stored when it finishes).
func (c *Cache) Search(ctx context.Context, req search.Request) (*search.Result, error) {
	return c.SearchProgress(ctx, req, c.opts.FetchTimeout, nil)
}

// SearchProgress is Search with a longer wait (wait <= 0: until ctx ends)
// that calls progress with the answer so far, the shops not done yet
// marked pending: once when the live search starts and each time a shop
// finishes, except the last (the returned answer). progress runs on the
// caller's goroutine, one call at a time.
func (c *Cache) SearchProgress(ctx context.Context, req search.Request, wait time.Duration,
	progress func(*search.Result)) (*search.Result, error) {
	p, ok := c.newPlan(ctx, req)
	if !ok {
		// Invalid input: let the service produce the right error.
		return c.next.Search(ctx, req)
	}
	need := p.need()
	if len(need) == 0 {
		return c.answerFromCache(p), nil
	}

	c.misses.Add(1)
	start := c.now()
	if stale := p.staleShops(); len(stale) > 0 {
		c.refreshInBackground(p, stale)
	}
	live := c.fetch(ctx, p, need, wait, func(live map[string]*queue.Outcome) {
		if progress != nil {
			progress(c.assemble(p, live, start, false))
		}
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	res := c.assemble(p, live, start, true)
	if search.AllFailed(res.Markets) {
		return res, search.ErrAllFailed
	}
	return res, nil
}

// fetch searches the given shops live, one queue task per shop (joined
// with the same task already queued or running, also from other
// countries when the entries are shared), and waits for them until ctx
// ends or wait is up. A shop not answered in time is an error outcome.
// update gets the outcomes so far: first with none, then after each shop
// but the last.
func (c *Cache) fetch(ctx context.Context, p *plan, shops []*shopState, wait time.Duration,
	update func(map[string]*queue.Outcome)) map[string]*queue.Outcome {
	wctx, cancel := ctx, context.CancelFunc(func() {})
	if wait > 0 {
		wctx, cancel = context.WithTimeout(ctx, wait)
	}
	defer cancel()

	type done struct {
		market string
		out    queue.Outcome
	}
	ch := make(chan done, len(shops))
	for _, s := range shops {
		t := c.task(p, s, false)
		go func() {
			o, err := c.queue.Do(wctx, t)
			if err != nil {
				msg := "the shop did not answer in time"
				if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
					msg = err.Error()
				}
				o = queue.Failed(msg)
			}
			ch <- done{s.target.Market, o}
		}()
	}
	live := make(map[string]*queue.Outcome, len(shops))
	update(live)
	for i := range shops {
		d := <-ch
		live[d.market] = &d.out
		if d.out.Status == search.StatusError && d.out.Error != "" && ctx.Err() == nil {
			c.log.WarnContext(ctx, "shop search failed", "market", d.market, "err", d.out.Error)
		}
		if i < len(shops)-1 {
			update(live)
		}
	}
	return live
}

// task is the queue task of one shop's live search.
func (c *Cache) task(p *plan, s *shopState, background bool) queue.Task {
	id := s.key
	if id == "" {
		h := sha256.Sum256([]byte(p.country.Code + "|" + s.target.Market + "|" + p.title))
		id = "nocache:" + s.target.Market + ":" + hex.EncodeToString(h[:16])
	}
	return queue.Task{ID: id, Market: s.target.Market, Country: p.country.Code, Title: search.NormalizeTitle(p.req.Title),
		Class: queue.ClassOf(s.target.Provider), Background: background}
}

// RunTask is the queue's work: search one shop live and store its results
// (failures are never stored).
func (c *Cache) RunTask(ctx context.Context, t queue.Task) queue.Outcome {
	res, err := c.next.Search(ctx, search.Request{Title: t.Title, Country: t.Country, Markets: []string{t.Market}})
	if res == nil {
		msg := "search failed"
		if err != nil {
			msg = err.Error()
		}
		return queue.Failed(msg)
	}
	info, ok := res.Shops[t.Market]
	if !ok || (info.Status != search.StatusOK && info.Status != search.StatusNoResults) {
		o := queue.Failed("the shop's search failed")
		if err != nil && !errors.Is(err, search.ErrAllFailed) {
			o.Error = err.Error()
		}
		o.Source, o.TookMS = info.Source, info.TookMS
		return o
	}
	o := queue.Outcome{Status: info.Status, Source: info.Source, TookMS: info.TookMS,
		Products: productsOf(res.Results, t.Market), FetchedAt: res.FetchedAt}
	if key := c.keyOf(t); key != "" {
		e := &Entry{Market: t.Market, Status: o.Status, Source: o.Source, TookMS: o.TookMS, Products: o.Products, FetchedAt: o.FetchedAt}
		ttl := c.opts.FreshTTL
		if o.Status == search.StatusNoResults {
			ttl = c.opts.EmptyTTL
		}
		e.FreshUntil = e.FetchedAt.Add(ttl)
		if err := c.store.Set(ctx, key, e, c.opts.LastKnownTTL); err != nil {
			c.log.WarnContext(ctx, "cache write failed", "err", err)
		}
	}
	return o
}

// keyOf is the cache key of a task's shop with the current setup.
func (c *Cache) keyOf(t queue.Task) string {
	country, ok := c.catalog.Country(t.Country)
	if !ok {
		return ""
	}
	for _, tg := range country.Targets {
		if tg.Market == t.Market {
			return c.key(country, tg, strings.ToLower(search.NormalizeTitle(t.Title)))
		}
	}
	return ""
}

// assemble builds the answer from the cached shops and the live outcomes
// (nil when nothing ran live). start is when the live search began. When
// final is false, a live shop without an outcome yet is pending.
func (c *Cache) assemble(p *plan, live map[string]*queue.Outcome, start time.Time, final bool) *search.Result {
	res := &search.Result{
		Query:   search.NormalizeTitle(p.req.Title),
		Country: p.country.Code,
		Results: []search.Product{},
		Markets: make(map[string]string, len(p.shops)),
		Shops:   make(map[string]search.ShopInfo, len(p.shops)),
		Cached:  true,
	}
	var oldest time.Time
	use := func(e *Entry) {
		res.Results = append(res.Results, e.Products...)
		if oldest.IsZero() || e.FetchedAt.Before(oldest) {
			oldest = e.FetchedAt
		}
	}
	for _, s := range p.shops {
		m := s.target.Market
		switch {
		case s.target.Provider == search.OffProvider:
			res.Markets[m] = search.StatusOff
			res.Shops[m] = search.ShopInfo{Status: search.StatusOff}
		case s.serve:
			use(s.entry)
			at := s.entry.FetchedAt
			res.Markets[m] = s.entry.Status
			res.Shops[m] = search.ShopInfo{Status: s.entry.Status, Source: s.entry.Source, FetchedAt: &at, Cached: true}
			res.Stale = res.Stale || s.stale
		default:
			o := live[m]
			if o == nil && !final {
				res.Markets[m] = search.StatusPending
				res.Shops[m] = search.ShopInfo{Status: search.StatusPending}
				res.Cached = false
				continue
			}
			info := search.ShopInfo{}
			if o != nil {
				info.Source, info.TookMS = o.Source, o.TookMS
			}
			if o != nil && (o.Status == search.StatusOK || o.Status == search.StatusNoResults) {
				res.Results = append(res.Results, o.Products...)
				if oldest.IsZero() || o.FetchedAt.Before(oldest) {
					oldest = o.FetchedAt
				}
				at := o.FetchedAt
				info.Status, info.FetchedAt = o.Status, &at
				res.Markets[m] = o.Status
				res.Shops[m] = info
				res.Cached = false
				continue
			}
			if s.entry != nil {
				// The live search failed: the last known results, dated.
				use(s.entry)
				at := s.entry.FetchedAt
				res.Markets[m] = search.StatusLastKnown
				res.Shops[m] = search.ShopInfo{Status: search.StatusLastKnown, Source: s.entry.Source,
					FetchedAt: &at, TookMS: info.TookMS, Cached: true}
				continue
			}
			info.Status = search.StatusError
			res.Markets[m] = search.StatusError
			res.Shops[m] = info
		}
	}
	search.NormalizeCurrencies(res.Results)
	search.SortProducts(res.Results, p.country.Targets)
	res.FetchedAt = oldest
	if oldest.IsZero() {
		res.FetchedAt = c.now().UTC()
	}
	if !start.IsZero() {
		res.TookMS = c.now().Sub(start).Milliseconds()
	}
	return res
}

func productsOf(products []search.Product, market string) []search.Product {
	out := []search.Product{}
	for _, p := range products {
		if p.Market == market {
			out = append(out, p)
		}
	}
	return out
}

// refreshInBackground refreshes stale shops without blocking the caller:
// queue tasks behind the searches. At most MaxBackgroundRefresh refreshes
// are followed at once; when all slots are busy the refresh is skipped and
// a later request will trigger it.
func (c *Cache) refreshInBackground(p *plan, shops []*shopState) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	select {
	case c.sem <- struct{}{}:
	default:
		c.mu.Unlock()
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		defer func() { <-c.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), c.opts.FetchTimeout)
		defer cancel()
		var wg sync.WaitGroup
		for _, s := range shops {
			t := c.task(p, s, true)
			wg.Go(func() {
				if o, err := c.queue.Do(ctx, t); err == nil && o.Status == search.StatusError {
					c.log.Warn("background refresh failed", "market", t.Market, "err", o.Error)
				}
			})
		}
		wg.Wait()
	}()
}

// Forget removes the cached results of one search (title and country) for
// every shop of the country, so the next search for it is live. Shared
// entries are gone for the other countries too. It reports whether
// anything was cached.
func (c *Cache) Forget(ctx context.Context, req search.Request) (bool, error) {
	country, ok := c.catalog.Country(req.Country)
	title := strings.ToLower(search.NormalizeTitle(req.Title))
	if !ok || len([]rune(title)) < search.MinTitleLen || len([]rune(title)) > search.MaxTitleLen {
		return false, search.ErrInvalidTitle
	}
	found := false
	for _, t := range country.Targets {
		key := c.key(country, t, title)
		if key == "" {
			continue
		}
		deleted, err := c.store.Delete(ctx, key)
		if err != nil {
			return found, err
		}
		found = found || deleted
	}
	return found, nil
}

// Close stops new background refreshes and waits for running ones until
// ctx ends.
func (c *Cache) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	var err error
	select {
	case <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if c.ownQ {
		c.queue.Close(ctx)
	}
	return err
}

// Stats returns hit/stale/miss counters since start.
func (c *Cache) Stats() (hits, stale, misses int64) {
	return c.hits.Load(), c.stale.Load(), c.misses.Load()
}

func (c *Cache) get(ctx context.Context, key string) *Entry {
	e, err := c.store.Get(ctx, key)
	if err != nil {
		// A broken cache must not break search: treat as a miss.
		c.log.WarnContext(ctx, "cache read failed", "err", err)
		return nil
	}
	if e != nil && c.now().Sub(e.FetchedAt) >= c.opts.LastKnownTTL {
		return nil
	}
	return e
}

// resultsVersion is part of every cache key. Bump it whenever the way
// results are found or filtered changes (relevance, duplicates, links), so
// answers cached by an older build are not served after an upgrade.
// v7: results stored per shop (step 14b).
const resultsVersion = "v7"

// key identifies one shop's results for a title: the shop, its site, the
// setup of its source (Options.Shops), the country's region when the
// source depends on it, and the normalized title. The title is hashed so
// user input never ends up raw in Redis keys. "" = the shop is not cached.
func (c *Cache) key(country markets.Country, t markets.Target, title string) string {
	shop, ok := c.opts.Shops[t.Market]
	if !ok || t.Provider == search.OffProvider {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(c.opts.Generation + ";" + t.Market + "=" + t.Domain + ";" + shop.Setup + ";"))
	if shop.ByRegion {
		h.Write([]byte("region=" + country.Region + "/" + country.ShoppingRegion + "/" + country.Language + ";"))
	}
	h.Write([]byte{0})
	h.Write([]byte(title))
	return "shop:" + resultsVersion + ":" + t.Market + ":" + hex.EncodeToString(h.Sum(nil)[:16])
}
