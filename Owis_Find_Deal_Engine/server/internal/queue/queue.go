// Package queue runs the live search of each shop as its own task (step
// 14b part 3), so a search shows the fast shops first and slow ones fill
// in later.
//
//   - One task per shop and title: a second search for the same shop and
//     title while one is queued or running joins it (not paid twice).
//   - A cap on runs at once per source class (apify, serpapi, web); a task
//     waits in the queue for a free slot. A task that waits longer than
//     MaxWait is given up (the shop shows its last known results).
//   - Searches go before background refreshes.
//   - Tasks are kept in Redis when configured: a task that was running
//     when the server stopped is run again once if it is young enough
//     (RetryMaxAge), otherwise it ends as an error.
//
// The time limit of a shop starts when its task starts running, not while
// it waits.
package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"owis_find_deal_engine/internal/search"
)

// Source classes: each has its own cap on runs at once.
const (
	ClassApify   = "apify"
	ClassSerpAPI = "serpapi"
	ClassWeb     = "web"
)

// Classes are all source classes.
var Classes = []string{ClassApify, ClassSerpAPI, ClassWeb}

// ClassOf returns the class of a catalog provider name ("apify-temu",
// "serpapi-amazon", "web").
func ClassOf(provider string) string {
	switch {
	case strings.HasPrefix(provider, "apify"):
		return ClassApify
	case strings.HasPrefix(provider, "serpapi"):
		return ClassSerpAPI
	}
	return ClassWeb
}

// Task is the live search of one shop for one title.
type Task struct {
	// ID names the task; tasks with the same ID are joined (the shop's
	// cache key).
	ID         string    `json:"id"`
	Market     string    `json:"market"`
	Country    string    `json:"country"`
	Title      string    `json:"title"`
	Class      string    `json:"class"`
	Background bool      `json:"background,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	QueuedAt   time.Time `json:"queued_at"`
	Attempt    int       `json:"attempt"` // 1 = first run
}

// Outcome is how a task ended.
type Outcome struct {
	Status    string           `json:"status"` // ok, no_results or error
	Source    string           `json:"source,omitempty"`
	TookMS    int64            `json:"took_ms"`
	Products  []search.Product `json:"products,omitempty"`
	FetchedAt time.Time        `json:"fetched_at"`
	Error     string           `json:"error,omitempty"`
}

// Failed returns an error outcome.
func Failed(msg string) Outcome { return Outcome{Status: search.StatusError, Error: msg} }

// RunFunc runs one task (the cache: search the shop and store its results).
// It must respect ctx.
type RunFunc func(ctx context.Context, t Task) Outcome

// Task states.
const (
	stateQueued  = "queued"
	stateRunning = "running"
	stateDone    = "done"
)

// backend keeps tasks: Redis (shared, survives restarts) or memory.
type backend interface {
	// claim adds t as queued unless a task with its ID is queued, running
	// or finished well less than joinRecent ago (then t joins it; a search
	// joining a queued background refresh moves it to the front lane).
	claim(ctx context.Context, t Task, now time.Time, joinRecent time.Duration) (created bool, err error)
	// pop takes the next task of class (searches first) and marks it
	// running with a heartbeat. ok is false when none is waiting.
	pop(ctx context.Context, class string) (t Task, ok bool, err error)
	beat(ctx context.Context, ids []string) error
	finish(ctx context.Context, id string, o Outcome, now time.Time) error
	// requeue puts a running task back in front of its class's queue.
	requeue(ctx context.Context, t Task) error
	// state returns a task's state and, when done, its outcome; "" = no
	// such task.
	state(ctx context.Context, id string) (string, *Outcome, error)
	// orphans removes and returns the running tasks whose heartbeat
	// stopped (their server stopped).
	orphans(ctx context.Context) ([]Task, error)
}

// Options tunes the queue.
type Options struct {
	// Limits are the runs at once per class (default 4 apify, 10 others).
	Limits map[string]int
	// MaxWait is how long a task may wait for a free slot (default 60 s).
	MaxWait time.Duration
	// RetryMaxAge: a task cut off by a restart is run again only when it
	// is younger than this (default 3 min), and only once.
	RetryMaxAge time.Duration
}

const (
	maxAttempts  = 2
	joinRecent   = 10 * time.Second // a task that just finished well is still joined
	keepDone     = 10 * time.Minute // a finished task's outcome stays readable
	keepActive   = 15 * time.Minute // a queued or running task's record
	beatEvery    = 5 * time.Second
	beatTTL      = 20 * time.Second
	recoverEvery = 10 * time.Second
	idlePoll     = 500 * time.Millisecond // looking for tasks queued by another server
	waitPoll     = time.Second            // looking for outcomes of another server
)

// DefaultLimits are the runs at once per class agreed with the owner
// (2026-11-09): Apify's free plan has 8 GB of memory = 4 runs of 2048 MB.
func DefaultLimits() map[string]int {
	return map[string]int{ClassApify: 4, ClassSerpAPI: 10, ClassWeb: 10}
}

// Queue runs tasks on a worker pool. Safe for concurrent use; it outlives
// settings changes (SetRunner, SetOptions).
type Queue struct {
	b   backend
	log *slog.Logger
	now func() time.Time

	run atomic.Pointer[RunFunc]

	ctx    context.Context // cancelled by Close: running tasks stop
	cancel context.CancelFunc
	wg     sync.WaitGroup
	start  sync.Once

	mu      sync.Mutex
	opts    Options
	busy    map[string]int             // runs in flight per class
	local   map[string]Task            // running here, by id
	waiters map[string][]chan struct{} // local waiters, by id
	wake    map[string]chan struct{}   // per class: a task was queued or a slot freed
	closed  bool
}

// New returns a queue on backend b (NewRedis, NewMemory). Start it with
// Start.
func New(b backend, opts Options, log *slog.Logger) *Queue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{b: b, log: log, now: time.Now, ctx: ctx, cancel: cancel,
		busy: map[string]int{}, local: map[string]Task{}, waiters: map[string][]chan struct{}{}, wake: map[string]chan struct{}{}}
	for _, c := range Classes {
		q.wake[c] = make(chan struct{}, 1)
	}
	q.opts = withDefaults(opts)
	return q
}

// NewMemory returns a queue kept in memory (one server, lost on restart).
func NewMemory(opts Options, log *slog.Logger) *Queue { return New(newMemBackend(), opts, log) }

func withDefaults(o Options) Options {
	limits := DefaultLimits()
	for c, n := range o.Limits {
		if n > 0 {
			limits[c] = n
		}
	}
	o.Limits = limits
	if o.MaxWait <= 0 {
		o.MaxWait = 60 * time.Second
	}
	if o.RetryMaxAge <= 0 {
		o.RetryMaxAge = 3 * time.Minute
	}
	return o
}

// SetOptions changes the caps and waits (settings change).
func (q *Queue) SetOptions(opts Options) {
	q.mu.Lock()
	q.opts = withDefaults(opts)
	q.mu.Unlock()
	for _, c := range Classes {
		q.signal(c)
	}
}

// SetClock replaces the queue's clock (tests: a fake clock). Call it
// before the first task.
func (q *Queue) SetClock(now func() time.Time) { q.now = now }

// SetRunner sets what runs a task (the current search setup).
func (q *Queue) SetRunner(run RunFunc) { q.run.Store(&run) }

// Start starts the workers (once). Orphaned tasks of a stopped server are
// picked up at once and then every few seconds.
func (q *Queue) Start() {
	q.start.Do(func() {
		for _, c := range Classes {
			q.wg.Go(func() { q.dispatch(c) })
		}
		q.wg.Go(q.heartbeat)
		q.wg.Go(q.recoverLoop)
	})
}

// Do runs t (or joins the same task already queued or running) and waits
// for its outcome until ctx ends; the task goes on when the caller stops
// waiting.
func (q *Queue) Do(ctx context.Context, t Task) (Outcome, error) {
	if err := q.Enqueue(ctx, t); err != nil {
		return Outcome{}, err
	}
	return q.Wait(ctx, t.ID)
}

// ErrClosed means the queue is shutting down.
var ErrClosed = errors.New("queue: shutting down")

// Enqueue queues t unless the same task is already queued or running.
func (q *Queue) Enqueue(ctx context.Context, t Task) error {
	q.mu.Lock()
	closed := q.closed
	q.mu.Unlock()
	if closed {
		return ErrClosed
	}
	now := q.now().UTC()
	t.CreatedAt, t.QueuedAt, t.Attempt = now, now, 1
	if t.Class == "" {
		t.Class = ClassWeb
	}
	created, err := q.b.claim(ctx, t, now, joinRecent)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}
	if created {
		q.signal(t.Class)
	}
	return nil
}

// Wait waits for the outcome of task id until ctx ends.
func (q *Queue) Wait(ctx context.Context, id string) (Outcome, error) {
	ch := make(chan struct{}, 1)
	q.mu.Lock()
	q.waiters[id] = append(q.waiters[id], ch)
	q.mu.Unlock()
	defer q.removeWaiter(id, ch)

	t := time.NewTicker(waitPoll)
	defer t.Stop()
	for {
		st, o, err := q.b.state(ctx, id)
		switch {
		case err == nil && st == stateDone && o != nil:
			return *o, nil
		case err == nil && st == "":
			return Outcome{}, errors.New("queue: the task is gone")
		case err != nil && ctx.Err() == nil:
			q.log.WarnContext(ctx, "queue: reading a task failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		case <-ch:
		case <-t.C:
		}
	}
}

func (q *Queue) removeWaiter(id string, ch chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ws := q.waiters[id]
	for i, w := range ws {
		if w == ch {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(ws) == 0 {
		delete(q.waiters, id)
	} else {
		q.waiters[id] = ws
	}
}

func (q *Queue) notify(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, ch := range q.waiters[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (q *Queue) signal(class string) {
	ch, ok := q.wake[class]
	if !ok {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// dispatch starts the tasks of one class while slots are free.
func (q *Queue) dispatch(class string) {
	for {
		if q.ctx.Err() != nil {
			return
		}
		q.mu.Lock()
		free := q.busy[class] < q.opts.Limits[class]
		maxWait := q.opts.MaxWait
		q.mu.Unlock()
		if free {
			t, ok, err := q.b.pop(q.ctx, class)
			if err != nil && q.ctx.Err() == nil {
				q.log.Warn("queue: taking a task failed", "class", class, "err", err)
			}
			if ok {
				if wait := q.now().Sub(t.QueuedAt); wait > maxWait {
					q.end(t, Failed(fmt.Sprintf("waited %s in the queue for a free %s slot", wait.Round(time.Second), class)))
					continue
				}
				q.mu.Lock()
				q.busy[class]++
				q.local[t.ID] = t
				q.mu.Unlock()
				q.wg.Go(func() { q.work(t) })
				continue
			}
		}
		timer := time.NewTimer(idlePoll)
		select {
		case <-q.ctx.Done():
			timer.Stop()
			return
		case <-q.wake[class]:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (q *Queue) work(t Task) {
	defer func() {
		q.mu.Lock()
		q.busy[t.Class]--
		delete(q.local, t.ID)
		q.mu.Unlock()
		q.signal(t.Class)
	}()
	o := q.runTask(t)
	if q.ctx.Err() != nil {
		// Shutting down: the task was cut off, not finished.
		q.retryOrFail(t, "the server stopped during this shop's search")
		return
	}
	q.end(t, o)
}

func (q *Queue) runTask(t Task) (o Outcome) {
	defer func() {
		if p := recover(); p != nil {
			q.log.Error("queue: task panicked", "market", t.Market, "panic", p)
			o = Failed("internal error")
		}
	}()
	run := q.run.Load()
	if run == nil {
		return Failed("no search setup")
	}
	return (*run)(q.ctx, t)
}

// end saves a task's outcome and wakes its waiters.
func (q *Queue) end(t Task, o Outcome) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q.b.finish(ctx, t.ID, o, q.now().UTC()); err != nil {
		q.log.Error("queue: saving a task's outcome failed", "market", t.Market, "err", err)
	}
	q.notify(t.ID)
}

// retryOrFail runs a cut-off task again once if it is young enough.
func (q *Queue) retryOrFail(t Task, msg string) {
	q.mu.Lock()
	maxAge := q.opts.RetryMaxAge
	q.mu.Unlock()
	if t.Attempt < maxAttempts && q.now().Sub(t.CreatedAt) < maxAge {
		t.Attempt++
		t.QueuedAt = q.now().UTC()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := q.b.requeue(ctx, t)
		if err == nil {
			q.log.Info("queue: task cut off, queued again", "market", t.Market, "attempt", t.Attempt)
			q.signal(t.Class)
			return
		}
		q.log.Error("queue: queuing a task again failed", "market", t.Market, "err", err)
	}
	q.end(t, Failed(msg))
}

func (q *Queue) heartbeat() {
	tk := time.NewTicker(beatEvery)
	defer tk.Stop()
	for {
		select {
		case <-q.ctx.Done():
			return
		case <-tk.C:
		}
		q.mu.Lock()
		ids := make([]string, 0, len(q.local))
		for id := range q.local {
			ids = append(ids, id)
		}
		q.mu.Unlock()
		if len(ids) == 0 {
			continue
		}
		if err := q.b.beat(q.ctx, ids); err != nil && q.ctx.Err() == nil {
			q.log.Warn("queue: heartbeat failed", "err", err)
		}
	}
}

func (q *Queue) recoverLoop() {
	tk := time.NewTicker(recoverEvery)
	defer tk.Stop()
	for {
		orphans, err := q.b.orphans(q.ctx)
		if err != nil && q.ctx.Err() == nil {
			q.log.Warn("queue: looking for cut-off tasks failed", "err", err)
		}
		for _, t := range orphans {
			q.retryOrFail(t, "the server stopped during this shop's search")
		}
		select {
		case <-q.ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// Stats are the tasks running on this server per class.
func (q *Queue) Stats() map[string]int {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[string]int, len(q.busy))
	for c, n := range q.busy {
		out[c] = n
	}
	return out
}

// Close stops taking tasks and stops the running ones: each is queued
// again (run again once by the next server start, see RetryMaxAge) or
// ends as an error. It waits until ctx ends.
func (q *Queue) Close(ctx context.Context) {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cancel()
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
