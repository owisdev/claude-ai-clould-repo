// Package stats counts what the service does for the usage and cost
// dashboard: searches (cache hits, live, refused, failed), every call of a
// source with its estimated cost, fallbacks between sources, and the most
// searched queries. Counts are kept in memory and added to the database
// every flush interval, per UTC day, so the hot path never waits for the
// database and several instances add up.
package stats

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/store"
)

// Kinds of usage counters (store.UsageRow.Kind).
const (
	KindSearch   = "search"
	KindSource   = "source"
	KindFallback = "fallback"
)

// FlushInterval is how often each server adds its counts to the database.
const FlushInterval = 30 * time.Second

// maxKeys bounds the counters kept between flushes (when the database is
// down for long): new keys are dropped beyond it, existing ones still count.
const maxKeys = 50_000

// Sink stores counters (store.DB).
type Sink interface {
	AddUsage(ctx context.Context, usage []store.UsageRow, queries []store.QueryRow) error
}

type usageKey struct {
	day              string // 2006-01-02
	kind, k1, k2, k3 string
}

type usageVal struct {
	count   int64
	costUSD float64
	totalMS int64
}

type queryKey struct{ day, country, query string }

type queryVal struct{ searches, live int64 }

// Recorder counts events. It implements metering.SearchRecorder and
// search.Recorder. Safe for concurrent use.
type Recorder struct {
	now func() time.Time

	mu      sync.Mutex
	usage   map[usageKey]*usageVal
	queries map[queryKey]*queryVal
	dropped int64
}

var _ search.Recorder = (*Recorder)(nil)

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{now: time.Now, usage: map[usageKey]*usageVal{}, queries: map[queryKey]*queryVal{}}
}

func (r *Recorder) day() string { return r.now().UTC().Format(time.DateOnly) }

// add must be called with r.mu held.
func (r *Recorder) add(k usageKey, count int64, cost float64, ms int64) {
	v, ok := r.usage[k]
	if !ok {
		if len(r.usage) >= maxKeys {
			r.dropped++
			return
		}
		v = &usageVal{}
		r.usage[k] = v
	}
	v.count += count
	v.costUSD += cost
	v.totalMS += ms
}

// Search counts one user search (metering.SearchRecorder) and its query.
func (r *Recorder) Search(country, title, outcome string) {
	country = strings.ToLower(strings.TrimSpace(country))
	query := strings.ToLower(search.NormalizeTitle(title))
	if utf8.RuneCountInString(query) > search.MaxTitleLen {
		return
	}
	day := r.day()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.add(usageKey{day: day, kind: KindSearch, k1: country, k2: outcome}, 1, 0, 0)
	if query == "" || (outcome != "hit" && outcome != "stale" && outcome != "live") {
		return
	}
	k := queryKey{day, country, query}
	v, ok := r.queries[k]
	if !ok {
		if len(r.queries) >= maxKeys {
			r.dropped++
			return
		}
		v = &queryVal{}
		r.queries[k] = v
	}
	v.searches++
	if outcome == "live" {
		v.live++
	}
}

// SourceCall counts one call of a source (search.Recorder). A call for
// several shops counts once per shop with that shop's outcome; its cost
// is split between them.
func (r *Recorder) SourceCall(provider string, outcomes map[string]string, took time.Duration, costUSD float64) {
	if len(outcomes) == 0 {
		return
	}
	day := r.day()
	n := float64(len(outcomes))
	ms := took.Milliseconds()
	r.mu.Lock()
	defer r.mu.Unlock()
	for m, outcome := range outcomes {
		r.add(usageKey{day: day, kind: KindSource, k1: provider, k2: m, k3: outcome}, 1, costUSD/n, ms)
	}
}

// Fallback counts a chain moving on to its next provider (search.Recorder).
func (r *Recorder) Fallback(chain, from, to, reason string) {
	day := r.day()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.add(usageKey{day: day, kind: KindFallback, k1: chain, k2: from, k3: to + ":" + reason}, 1, 0, 0)
}

// Flush adds the counts so far to sink. On failure they are kept and
// added with the next flush.
func (r *Recorder) Flush(ctx context.Context, sink Sink) error {
	r.mu.Lock()
	usage, queries := r.usage, r.queries
	r.usage, r.queries = map[usageKey]*usageVal{}, map[queryKey]*queryVal{}
	r.mu.Unlock()
	if len(usage) == 0 && len(queries) == 0 {
		return nil
	}

	rows := make([]store.UsageRow, 0, len(usage))
	for k, v := range usage {
		day, _ := time.Parse(time.DateOnly, k.day)
		rows = append(rows, store.UsageRow{Day: day, Kind: k.kind, K1: k.k1, K2: k.k2, K3: k.k3,
			Count: v.count, CostUSD: v.costUSD, TotalMS: v.totalMS})
	}
	qrows := make([]store.QueryRow, 0, len(queries))
	for k, v := range queries {
		day, _ := time.Parse(time.DateOnly, k.day)
		qrows = append(qrows, store.QueryRow{Day: day, Country: k.country, Query: k.query, Searches: v.searches, Live: v.live})
	}
	if err := sink.AddUsage(ctx, rows, qrows); err != nil {
		r.mu.Lock()
		for k, v := range usage {
			r.add(k, v.count, v.costUSD, v.totalMS)
		}
		for k, v := range queries {
			if cur, ok := r.queries[k]; ok {
				cur.searches += v.searches
				cur.live += v.live
			} else if len(r.queries) < maxKeys {
				r.queries[k] = v
			}
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

// Run flushes to sink every interval until ctx ends, then once more.
func (r *Recorder) Run(ctx context.Context, sink Sink, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := r.Flush(fctx, sink); err != nil {
				log.Warn("usage counters lost at shutdown", "err", err)
			}
			cancel()
			return
		case <-t.C:
		}
		if err := r.Flush(ctx, sink); err != nil && ctx.Err() == nil {
			log.Warn("usage counters not saved, retrying at the next flush", "err", err)
		}
		r.mu.Lock()
		dropped := r.dropped
		r.dropped = 0
		r.mu.Unlock()
		if dropped > 0 {
			log.Warn("usage counters dropped (too many distinct keys before a flush)", "dropped", dropped)
		}
	}
}
