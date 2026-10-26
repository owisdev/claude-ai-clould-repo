// Package jobs runs the asynchronous searches of the public API: a slow
// live search (25-50 s) is started as a job, the caller gets its id at
// once and asks for the result later, or is called back by a signed
// webhook when it is done.
//
// Jobs are kept in Redis when configured (any server instance answers
// GET /api/v1/searches/{id}), otherwise in memory. A job runs on the
// instance that accepted it; if that instance stops, the job is reported
// as lost once its deadline has passed.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/webhook"

	"github.com/redis/go-redis/v9"
)

// Job statuses.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// IDPrefix starts every job id.
const IDPrefix = "srch_"

// ResultTTL is how long a finished job can be read.
const ResultTTL = time.Hour

// Limits of the runner.
const (
	// MaxPerOwner is the number of jobs one user or client may have
	// running on one server at once.
	MaxPerOwner = 10
	// MaxRunning is the number of jobs one server runs at once.
	MaxRunning = 200
	// lostGrace is added to a job's deadline before a job that is still
	// "running" is reported as lost (its server stopped).
	lostGrace = 30 * time.Second
)

// Job is one asynchronous search.
type Job struct {
	ID         string         `json:"id"`
	Owner      string         `json:"owner"` // user id or client:<id>; never shown
	Status     string         `json:"status"`
	Request    Request        `json:"request"`
	CreatedAt  time.Time      `json:"created_at"`
	Deadline   time.Time      `json:"deadline"` // a running job is lost after this
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Result     *search.Result `json:"result,omitempty"`
	Error      *Error         `json:"error,omitempty"`
	Quota      *Quota         `json:"quota,omitempty"`
	Webhook    *Webhook       `json:"webhook,omitempty"`
}

// Request is what was searched.
type Request struct {
	Title   string `json:"title"`
	Country string `json:"country"`
	Refresh bool   `json:"refresh"`
}

// Error is why a job failed: the HTTP status and error of the same search
// on POST /api/v1/search.
type Error struct {
	HTTPStatus int    `json:"http_status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

// Quota is the caller's daily allowance after the search.
type Quota struct {
	Plan      string    `json:"plan"`
	Limit     int64     `json:"limit"`
	Remaining int64     `json:"remaining"`
	Reset     time.Time `json:"reset"`
}

// Webhook is the callback of a job and how its delivery went.
type Webhook struct {
	URL        string `json:"url"`
	Status     string `json:"status"` // pending, delivered, failed
	Attempts   int    `json:"attempts"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Lost reports whether a running job's server stopped before finishing it.
func (j Job) Lost(now time.Time) bool {
	return j.Status == StatusRunning && now.After(j.Deadline.Add(lostGrace))
}

// ErrNotFound means no job has this id (or it expired).
var ErrNotFound = errors.New("no search job with this id")

// Store keeps jobs for a time.
type Store interface {
	Save(ctx context.Context, j Job, ttl time.Duration) error
	Get(ctx context.Context, id string) (Job, error)
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewID makes a job id (128 random bits).
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return IDPrefix + idEncoding.EncodeToString(b)
}

// ValidID reports whether id has the shape of a job id.
func ValidID(id string) bool {
	r, ok := strings.CutPrefix(id, IDPrefix)
	if !ok || len(r) != 26 {
		return false
	}
	for _, c := range r {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// MemoryStore keeps jobs in memory (one server, lost on restart).
type MemoryStore struct {
	mu   sync.Mutex
	jobs map[string]memJob
	now  func() time.Time
}

type memJob struct {
	data  []byte
	until time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{jobs: map[string]memJob{}, now: time.Now}
}

// Save stores a copy of j for ttl.
func (m *MemoryStore) Save(_ context.Context, j Job, ttl time.Duration) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.jobs[j.ID] = memJob{data: b, until: m.now().Add(ttl)}
	m.mu.Unlock()
	return nil
}

// Get returns the job with id.
func (m *MemoryStore) Get(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	e, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok || !m.now().Before(e.until) {
		return Job{}, ErrNotFound
	}
	var j Job
	err := json.Unmarshal(e.data, &j)
	return j, err
}

// Cleanup deletes expired jobs every interval until ctx ends.
func (m *MemoryStore) Cleanup(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := m.now()
		m.mu.Lock()
		for id, e := range m.jobs {
			if !now.Before(e.until) {
				delete(m.jobs, id)
			}
		}
		m.mu.Unlock()
	}
}

// RedisStore keeps jobs in Redis, shared by every server instance.
type RedisStore struct {
	rdb redis.UniversalClient
}

// NewRedisStore returns a RedisStore.
func NewRedisStore(rdb redis.UniversalClient) *RedisStore { return &RedisStore{rdb: rdb} }

func redisKey(id string) string { return "owis:job:" + id }

// Save stores j for ttl.
func (s *RedisStore) Save(ctx context.Context, j Job, ttl time.Duration) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, redisKey(j.ID), b, ttl).Err()
}

// Get returns the job with id.
func (s *RedisStore) Get(ctx context.Context, id string) (Job, error) {
	b, err := s.rdb.Get(ctx, redisKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	var j Job
	err = json.Unmarshal(b, &j)
	return j, err
}

// Errors of Start.
var (
	ErrOwnerBusy  = fmt.Errorf("at most %d search jobs may run at once", MaxPerOwner)
	ErrServerBusy = errors.New("too many search jobs are running on this server")
	ErrClosed     = errors.New("the server is shutting down")
)

// Work runs a job's search and returns the job finished (status done or
// failed, with result or error). It must respect ctx.
type Work func(ctx context.Context, j Job) Job

// Notify delivers a finished job's webhook.
type Notify func(ctx context.Context, j Job) webhook.Result

// Runner runs jobs in the background. It outlives settings changes; the
// search itself is given per job. Safe for concurrent use.
type Runner struct {
	store Store
	log   *slog.Logger
	now   func() time.Time

	ctx    context.Context // cancelled when Close gives up waiting
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	running map[string]int // by owner
	total   int
}

// NewRunner returns a Runner saving jobs in store.
func NewRunner(store Store, log *slog.Logger) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{store: store, log: log, now: time.Now, ctx: ctx, cancel: cancel, running: map[string]int{}}
}

// Store returns the runner's job store.
func (r *Runner) Store() Store { return r.store }

// Start saves j as running and runs work in the background, bounded by
// timeout; then saves the finished job and, when notify is set and the
// job has a webhook, delivers it. values gives the job's context its
// values (request id, caller), never its cancellation. Start waits up to
// wait for the search (not the webhook) and returns the job as it is then
// (finished for a cached answer).
func (r *Runner) Start(values context.Context, j Job, timeout, wait time.Duration, work Work, notify Notify) (Job, error) {
	r.mu.Lock()
	switch {
	case r.closed:
		r.mu.Unlock()
		return Job{}, ErrClosed
	case r.running[j.Owner] >= MaxPerOwner:
		r.mu.Unlock()
		return Job{}, ErrOwnerBusy
	case r.total >= MaxRunning:
		r.mu.Unlock()
		return Job{}, ErrServerBusy
	}
	r.running[j.Owner]++
	r.total++
	r.wg.Add(1)
	r.mu.Unlock()

	j.Status = StatusRunning
	j.CreatedAt = r.now().UTC()
	j.Deadline = j.CreatedAt.Add(timeout)
	if err := r.store.Save(values, j, timeout+lostGrace+ResultTTL); err != nil {
		r.finish(j.Owner)
		return Job{}, err
	}
	finished := make(chan Job, 1)
	go r.run(values, j, timeout, work, notify, finished)
	if wait <= 0 {
		return j, nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case done := <-finished:
		return done, nil
	case <-t.C:
		return j, nil
	}
}

func (r *Runner) finish(owner string) {
	r.mu.Lock()
	r.running[owner]--
	if r.running[owner] <= 0 {
		delete(r.running, owner)
	}
	r.total--
	r.mu.Unlock()
	r.wg.Done()
}

func (r *Runner) run(values context.Context, j Job, timeout time.Duration, work Work, notify Notify, finished chan<- Job) {
	defer r.finish(j.Owner)
	ctx, cancel := context.WithTimeout(mergeValues(r.ctx, values), timeout)
	defer cancel()
	j = func() (out Job) {
		defer func() {
			if p := recover(); p != nil {
				r.log.ErrorContext(ctx, "search job panicked", "job", j.ID, "panic", p)
				out = Fail(j, 500, "internal_error", "internal server error", r.now())
			}
		}()
		return work(ctx, j)
	}()
	if j.Status == StatusRunning { // work must finish it; be safe
		j = Fail(j, 500, "internal_error", "internal server error", r.now())
	}
	r.save(ctx, j)
	finished <- j

	if notify == nil || j.Webhook == nil {
		return
	}
	// The webhook gets its own time: the search may have used all of it.
	wctx, wcancel := context.WithTimeout(mergeValues(r.ctx, values), 2*time.Minute)
	defer wcancel()
	res := notify(wctx, j)
	// A copy: the job sent on finished (and maybe being written as the
	// HTTP answer right now) shares the old Webhook.
	hook := *j.Webhook
	hook.Status, hook.Attempts, hook.HTTPStatus, hook.Error = res.Status, res.Attempts, res.HTTPStatus, res.Error
	j.Webhook = &hook
	r.save(wctx, j)
	if res.Status != webhook.StatusDelivered {
		r.log.WarnContext(wctx, "webhook not delivered", "job", j.ID, "attempts", res.Attempts, "http_status", res.HTTPStatus, "err", res.Error)
	}
}

func (r *Runner) save(ctx context.Context, j Job) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.store.Save(sctx, j, ResultTTL); err != nil {
		r.log.ErrorContext(ctx, "saving search job failed", "job", j.ID, "err", err)
	}
}

// Close stops accepting jobs and waits for the running ones until ctx
// ends; then cancels them (they are saved as failed) and waits for that.
func (r *Runner) Close(ctx context.Context) {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		r.cancel()
		<-done
	}
	r.cancel()
}

// Fail returns j finished with an error.
func Fail(j Job, status int, code, msg string, now time.Time) Job {
	t := now.UTC()
	j.Status, j.FinishedAt, j.Result = StatusFailed, &t, nil
	j.Error = &Error{HTTPStatus: status, Code: code, Message: msg}
	return j
}

// Done returns j finished with res.
func Done(j Job, res *search.Result, now time.Time) Job {
	t := now.UTC()
	j.Status, j.FinishedAt, j.Result, j.Error = StatusDone, &t, res, nil
	return j
}

// mergeValues is a context cancelled with parent that carries the values
// of values (not its cancellation or deadline).
func mergeValues(parent, values context.Context) context.Context {
	return valueCtx{Context: parent, values: values}
}

type valueCtx struct {
	context.Context
	values context.Context
}

func (c valueCtx) Value(key any) any {
	if v := c.values.Value(key); v != nil {
		return v
	}
	return c.Context.Value(key)
}
