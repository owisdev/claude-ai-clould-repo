// Package cache puts a stale-while-revalidate cache in front of the search
// service so repeated searches cost nothing and prices still stay current.
//
// Freshness of an entry, by age since it was fetched:
//
//	0 ── FreshTTL ──────────► served from cache
//	FreshTTL ── StaleTTL ───► served from cache (stale=true) and refreshed
//	                          in the background for the next caller
//	after StaleTTL ─────────► gone; fetched live
//
// Results where a marketplace failed (PartialTTL) or nothing was found
// (EmptyTTL) are fresh for a shorter time so they are retried sooner.
// A client may force a live fetch (Request.Refresh), at most once per
// MinRefresh per query. Identical concurrent fetches are merged into one
// upstream call (singleflight).
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

// Searcher is what the cache wraps (search.Service).
type Searcher interface {
	Search(ctx context.Context, req search.Request) (*search.Result, error)
}

// Entry is a stored search result.
type Entry struct {
	Result     search.Result `json:"result"`
	FreshUntil time.Time     `json:"fresh_until"`
}

// Store persists entries; Redis when shared by several instances,
// memory otherwise. Get returns (nil, nil) on a miss.
type Store interface {
	Get(ctx context.Context, key string) (*Entry, error)
	Set(ctx context.Context, key string, e *Entry, ttl time.Duration) error
}

// Options tunes freshness.
type Options struct {
	FreshTTL   time.Duration // full results
	PartialTTL time.Duration // some marketplaces failed
	EmptyTTL   time.Duration // no results at all
	StaleTTL   time.Duration // how long an entry may be served at all
	MinRefresh time.Duration // forced refresh is ignored for younger entries
	// FetchTimeout bounds a fetch that runs detached from the caller
	// (merged requests and background refreshes).
	FetchTimeout time.Duration
	// MaxBackgroundRefresh caps concurrent background refreshes.
	MaxBackgroundRefresh int
	// Variant describes the search setup (provider order, results per
	// market, ...). It is part of every key, so answers cached under one
	// setup are not served after the setup changes.
	Variant string
}

func (o *Options) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&o.FreshTTL, 2*time.Hour)
	set(&o.PartialTTL, 10*time.Minute)
	set(&o.EmptyTTL, 30*time.Minute)
	set(&o.StaleTTL, 24*time.Hour)
	set(&o.MinRefresh, 10*time.Minute)
	set(&o.FetchTimeout, 20*time.Second)
	// Partial and empty results are never fresh for longer than full ones.
	o.PartialTTL = min(o.PartialTTL, o.FreshTTL)
	o.EmptyTTL = min(o.EmptyTTL, o.FreshTTL)
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

	flights singleflight.Group
	sem     chan struct{} // background refresh slots

	mu     sync.Mutex // guards closed + wg.Add
	closed bool
	wg     sync.WaitGroup

	hits, stale, misses atomic.Int64
}

// New returns a Cache in front of next.
func New(next Searcher, store Store, catalog *markets.Catalog, opts Options, log *slog.Logger) *Cache {
	opts.defaults()
	return &Cache{
		next:    next,
		store:   store,
		catalog: catalog,
		opts:    opts,
		log:     log,
		now:     time.Now,
		sem:     make(chan struct{}, opts.MaxBackgroundRefresh),
	}
}

// Lookup answers from the cache only; it never runs a live search for the
// caller. ok is false when a live search is needed (not cached, expired,
// or a refresh was asked for and is allowed). A stale answer is returned
// and refreshed in the background.
func (c *Cache) Lookup(ctx context.Context, req search.Request) (res *search.Result, ok bool) {
	key, valid := c.key(req)
	if !valid {
		return nil, false
	}
	res, _, ok = c.lookup(ctx, key, req)
	return res, ok
}

func (c *Cache) lookup(ctx context.Context, key string, req search.Request) (*search.Result, *Entry, bool) {
	entry := c.get(ctx, key)
	if entry == nil {
		return nil, nil, false
	}
	now := c.now()
	if req.Refresh && now.Sub(entry.Result.FetchedAt) >= c.opts.MinRefresh {
		return nil, entry, false
	}
	if now.Before(entry.FreshUntil) {
		c.hits.Add(1)
		return entry.response(false), entry, true
	}
	c.stale.Add(1)
	c.refreshInBackground(key, req)
	return entry.response(true), entry, true
}

// Search answers from the cache when possible, otherwise live.
func (c *Cache) Search(ctx context.Context, req search.Request) (*search.Result, error) {
	key, ok := c.key(req)
	if !ok {
		// Invalid input: let the service produce the right error.
		return c.next.Search(ctx, req)
	}

	res, entry, hit := c.lookup(ctx, key, req)
	if hit {
		return res, nil
	}

	c.misses.Add(1)
	res, err := c.fetch(ctx, key, req)
	if err != nil && entry != nil && !errors.Is(err, context.Canceled) {
		// Live fetch failed but we still have an answer: serve it stale
		// rather than an error (stale-if-error).
		c.log.WarnContext(ctx, "live search failed, serving cached result", "err", err)
		return entry.response(true), nil
	}
	return res, err
}

// fetch runs one live search per key at a time; concurrent callers for the
// same key share it. The search is detached from the first caller's
// context so its cancellation does not fail everyone else, while each
// caller can still stop waiting when its own context ends.
func (c *Cache) fetch(ctx context.Context, key string, req search.Request) (*search.Result, error) {
	ch := c.flights.DoChan(key, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.opts.FetchTimeout)
		defer cancel()
		return c.fetchAndStore(fctx, key, req)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		// Every caller gets its own copy: they must not share flags.
		res := *(r.Val.(*search.Result))
		return &res, nil
	}
}

func (c *Cache) fetchAndStore(ctx context.Context, key string, req search.Request) (*search.Result, error) {
	req.Refresh = false
	res, err := c.next.Search(ctx, req)
	if err != nil {
		return nil, err // never cache failures
	}
	ttl := c.opts.FreshTTL
	switch {
	case len(res.Results) == 0:
		ttl = c.opts.EmptyTTL
	case res.Partial():
		ttl = c.opts.PartialTTL
	}
	stored := *res
	stored.Cached, stored.Stale = false, false
	entry := &Entry{Result: stored, FreshUntil: res.FetchedAt.Add(ttl)}
	if err := c.store.Set(ctx, key, entry, c.opts.StaleTTL); err != nil {
		c.log.WarnContext(ctx, "cache write failed", "err", err)
	}
	return res, nil
}

// refreshInBackground refreshes a stale entry without blocking the caller.
// Refreshes are bounded; when all slots are busy the refresh is skipped
// and a later request will trigger it.
func (c *Cache) refreshInBackground(key string, req search.Request) {
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
		_, err, _ := c.flights.Do(key, func() (any, error) {
			return c.fetchAndStore(ctx, key, req)
		})
		if err != nil {
			c.log.Warn("background refresh failed", "err", err)
		}
	}()
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
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	if e != nil && c.now().Sub(e.Result.FetchedAt) >= c.opts.StaleTTL {
		return nil
	}
	return e
}

func (e *Entry) response(stale bool) *search.Result {
	res := e.Result
	res.Cached = true
	res.Stale = stale
	res.TookMS = 0
	return &res
}

// resultsVersion is part of every cache key. Bump it whenever the way
// results are found or filtered changes (relevance, duplicates, links), so
// answers cached by an older build are not served after an upgrade.
const resultsVersion = "v6"

// key identifies a search: country, the catalog version, the search setup
// (Variant) and marketplace domains (so editing markets.json or changing
// the providers invalidates old entries) and the normalized title. The title is hashed so
// user input never ends up raw in Redis keys.
func (c *Cache) key(req search.Request) (string, bool) {
	country, ok := c.catalog.Country(req.Country)
	title := strings.ToLower(search.NormalizeTitle(req.Title))
	if !ok || len([]rune(title)) < search.MinTitleLen || len([]rune(title)) > search.MaxTitleLen {
		return "", false
	}
	h := sha256.New()
	h.Write([]byte(c.catalog.Version() + ";" + c.opts.Variant + ";"))
	for _, t := range country.Targets {
		h.Write([]byte(t.Market + "=" + t.Domain + ";"))
	}
	h.Write([]byte{0})
	h.Write([]byte(title))
	return "search:" + resultsVersion + ":" + country.Code + ":" + hex.EncodeToString(h.Sum(nil)[:16]), true
}
