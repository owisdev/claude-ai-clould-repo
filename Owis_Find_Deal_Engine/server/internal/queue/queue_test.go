package queue

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"owis_find_deal_engine/internal/search"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type backends struct {
	name string
	make func(t *testing.T) func(Options) *Queue // same storage for every queue made
}

func allBackends() []backends {
	return []backends{
		{"memory", func(*testing.T) func(Options) *Queue {
			b := newMemBackend()
			return func(o Options) *Queue { return New(b, o, quiet) }
		}},
		{"redis", func(t *testing.T) func(Options) *Queue {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { rdb.Close() })
			return func(o Options) *Queue { return NewRedis(rdb, o, quiet) }
		}},
	}
}

func start(t *testing.T, q *Queue, run RunFunc) *Queue {
	t.Helper()
	q.SetRunner(run)
	q.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		q.Close(ctx)
	})
	return q
}

func okRun(calls *atomic.Int32, delay time.Duration) RunFunc {
	return func(ctx context.Context, t Task) Outcome {
		calls.Add(1)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return Failed(ctx.Err().Error())
		}
		return Outcome{Status: search.StatusOK, Source: "fake", Products: []search.Product{{Market: t.Market, Title: t.Title}}}
	}
}

func task(id, class string) Task {
	return Task{ID: id, Market: "temu", Country: "usa", Title: "usb hub", Class: class}
}

func ctxT(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestDoAndJoin(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			var calls atomic.Int32
			q := start(t, b.make(t)(Options{}), okRun(&calls, 100*time.Millisecond))
			var wg sync.WaitGroup
			for range 5 {
				wg.Go(func() {
					o, err := q.Do(ctxT(t, 5*time.Second), task("a", ClassApify))
					if err != nil || o.Status != search.StatusOK || len(o.Products) != 1 {
						t.Errorf("Do = %+v, %v", o, err)
					}
				})
			}
			wg.Wait()
			if n := calls.Load(); n != 1 {
				t.Errorf("runs = %d, want 1 (joined)", n)
			}
			// Just finished: still joined. Later: a new run.
			if _, err := q.Do(ctxT(t, 5*time.Second), task("a", ClassApify)); err != nil || calls.Load() != 1 {
				t.Errorf("rejoin: %v, runs %d", err, calls.Load())
			}
		})
	}
}

func TestCapPerClass(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			var cur, peak atomic.Int32
			run := func(ctx context.Context, tk Task) Outcome {
				n := cur.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				time.Sleep(80 * time.Millisecond)
				cur.Add(-1)
				return Outcome{Status: search.StatusNoResults}
			}
			q := start(t, b.make(t)(Options{Limits: map[string]int{ClassApify: 2}}), run)
			var wg sync.WaitGroup
			for i := range 6 {
				wg.Go(func() {
					if _, err := q.Do(ctxT(t, 5*time.Second), task(string(rune('a'+i)), ClassApify)); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if p := peak.Load(); p != 2 {
				t.Errorf("peak runs at once = %d, want 2", p)
			}
		})
	}
}

func TestMaxWait(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			var calls atomic.Int32
			q := start(t, b.make(t)(Options{Limits: map[string]int{ClassApify: 1}, MaxWait: 50 * time.Millisecond}),
				okRun(&calls, 300*time.Millisecond))
			go q.Do(ctxT(t, 5*time.Second), task("first", ClassApify))
			time.Sleep(30 * time.Millisecond)
			o, err := q.Do(ctxT(t, 5*time.Second), task("second", ClassApify))
			if err != nil || o.Status != search.StatusError || !strings.Contains(o.Error, "in the queue") {
				t.Errorf("second = %+v, %v; want error waited in the queue", o, err)
			}
		})
	}
}

func TestSearchesBeforeBackground(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			var mu sync.Mutex
			var order []string
			release := make(chan struct{})
			run := func(ctx context.Context, tk Task) Outcome {
				if tk.ID == "block" {
					<-release
				}
				mu.Lock()
				order = append(order, tk.ID)
				mu.Unlock()
				return Outcome{Status: search.StatusOK}
			}
			q := start(t, b.make(t)(Options{Limits: map[string]int{ClassSerpAPI: 1}}), run)
			go q.Do(ctxT(t, 5*time.Second), task("block", ClassSerpAPI))
			time.Sleep(50 * time.Millisecond)
			bg := task("bg", ClassSerpAPI)
			bg.Background = true
			if err := q.Enqueue(ctxT(t, time.Second), bg); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { q.Do(ctxT(t, 5*time.Second), task("user", ClassSerpAPI)); close(done) }()
			time.Sleep(50 * time.Millisecond)
			close(release)
			<-done
			q.Wait(ctxT(t, 5*time.Second), "bg")
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(order, ",") != "block,user,bg" {
				t.Errorf("order = %v, want block,user,bg", order)
			}
		})
	}
}

func TestJoinPromotesBackground(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			var mu sync.Mutex
			var order []string
			release := make(chan struct{})
			run := func(ctx context.Context, tk Task) Outcome {
				if tk.ID == "block" {
					<-release
				}
				mu.Lock()
				order = append(order, tk.ID)
				mu.Unlock()
				return Outcome{Status: search.StatusOK}
			}
			q := start(t, b.make(t)(Options{Limits: map[string]int{ClassSerpAPI: 1}}), run)
			go q.Do(ctxT(t, 5*time.Second), task("block", ClassSerpAPI))
			time.Sleep(50 * time.Millisecond)
			for _, id := range []string{"bg1", "bg2"} {
				bg := task(id, ClassSerpAPI)
				bg.Background = true
				q.Enqueue(ctxT(t, time.Second), bg)
			}
			go q.Do(ctxT(t, 5*time.Second), task("bg2", ClassSerpAPI)) // a search joins bg2
			time.Sleep(50 * time.Millisecond)
			close(release)
			q.Wait(ctxT(t, 5*time.Second), "bg1")
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(order, ",") != "block,bg2,bg1" {
				t.Errorf("order = %v, want block,bg2,bg1", order)
			}
		})
	}
}

func TestWaiterGivesUpTaskGoesOn(t *testing.T) {
	var calls atomic.Int32
	q := start(t, NewMemory(Options{}, quiet), okRun(&calls, 150*time.Millisecond))
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := q.Do(short, task("a", ClassWeb)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline", err)
	}
	o, err := q.Wait(ctxT(t, 5*time.Second), "a")
	if err != nil || o.Status != search.StatusOK {
		t.Errorf("later Wait = %+v, %v", o, err)
	}
}

// A server stops during a task: the next server runs it again once.
func TestRestartRunsAgainOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	started := make(chan struct{}, 1)
	first := NewRedis(rdb, Options{}, quiet)
	first.SetRunner(func(ctx context.Context, tk Task) Outcome {
		started <- struct{}{}
		<-ctx.Done()
		return Failed("cut")
	})
	first.Start()
	go first.Do(context.Background(), task("a", ClassApify))
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	first.Close(ctx)
	cancel()

	var calls atomic.Int32
	var attempt atomic.Int32
	second := start(t, NewRedis(rdb, Options{}, quiet), func(ctx context.Context, tk Task) Outcome {
		calls.Add(1)
		attempt.Store(int32(tk.Attempt))
		return Outcome{Status: search.StatusOK}
	})
	o, err := second.Wait(ctxT(t, 5*time.Second), "a")
	if err != nil || o.Status != search.StatusOK || calls.Load() != 1 || attempt.Load() != 2 {
		t.Errorf("after restart: %+v, %v, runs %d, attempt %d", o, err, calls.Load(), attempt.Load())
	}
}

// A server crashes (no clean stop): its heartbeat ends and another server
// picks the task up; cut off a second time, it ends as an error.
func TestCrashRecoveryAndSecondCutOff(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	b := &redisBackend{rdb: rdb}
	ctx := ctxT(t, 5*time.Second)

	tk := task("a", ClassApify)
	tk.CreatedAt, tk.QueuedAt, tk.Attempt = time.Now().UTC(), time.Now().UTC(), 1
	if _, err := b.claim(ctx, tk, time.Now(), joinRecent); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.pop(ctx, ClassApify); !ok || err != nil {
		t.Fatalf("pop: %v %v", ok, err)
	}
	mr.Del(kBeat + "a") // the crashed server's heartbeat is gone

	var calls atomic.Int32
	q := start(t, NewRedis(rdb, Options{}, quiet), func(ctx context.Context, tk Task) Outcome {
		calls.Add(1)
		return Outcome{Status: search.StatusOK}
	})
	if o, err := q.Wait(ctx, "a"); err != nil || o.Status != search.StatusOK || calls.Load() != 1 {
		t.Fatalf("recovered: %+v, %v, runs %d", o, err, calls.Load())
	}

	// Attempt 2 cut off again: no third run.
	tk2 := task("b", ClassApify)
	tk2.CreatedAt, tk2.QueuedAt, tk2.Attempt = time.Now().UTC(), time.Now().UTC(), 2
	b.claim(ctx, tk2, time.Now(), joinRecent)
	b.pop(ctx, ClassApify)
	mr.Del(kBeat + "b")
	q2 := start(t, NewRedis(rdb, Options{}, quiet), func(context.Context, Task) Outcome {
		calls.Add(1)
		return Outcome{Status: search.StatusOK}
	})
	if o, err := q2.Wait(ctx, "b"); err != nil || o.Status != search.StatusError || calls.Load() != 1 {
		t.Errorf("second cut-off: %+v, %v, runs %d", o, err, calls.Load())
	}
}

func TestTooOldNotRunAgain(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	b := &redisBackend{rdb: rdb}
	ctx := ctxT(t, 5*time.Second)
	tk := task("old", ClassApify)
	tk.CreatedAt, tk.QueuedAt, tk.Attempt = time.Now().Add(-5*time.Minute).UTC(), time.Now().UTC(), 1
	b.claim(ctx, tk, time.Now(), joinRecent)
	b.pop(ctx, ClassApify)
	mr.Del(kBeat + "old")
	var calls atomic.Int32
	q := start(t, NewRedis(rdb, Options{}, quiet), okRun(&calls, 0))
	if o, err := q.Wait(ctx, "old"); err != nil || o.Status != search.StatusError || calls.Load() != 0 {
		t.Errorf("old task: %+v, %v, runs %d", o, err, calls.Load())
	}
}

func TestClassOf(t *testing.T) {
	for p, want := range map[string]string{"apify-temu": ClassApify, "serpapi-amazon": ClassSerpAPI, "web": ClassWeb, "x": ClassWeb} {
		if got := ClassOf(p); got != want {
			t.Errorf("ClassOf(%q) = %q, want %q", p, got, want)
		}
	}
}
