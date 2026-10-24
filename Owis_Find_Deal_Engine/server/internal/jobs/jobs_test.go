package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/webhook"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestIDs(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b || !ValidID(a) || len(a) != len(IDPrefix)+26 {
		t.Fatalf("ids %q %q", a, b)
	}
	for _, id := range []string{"", "srch_", "srch_ABCDEFGHIJKLMNOPQRSTUVWXYZ", "job_" + a[5:], a + "a", a[:len(a)-1] + "1"} {
		if ValidID(id) {
			t.Errorf("%q valid", id)
		}
	}
}

func testStores(t *testing.T) map[string]Store {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return map[string]Store{"memory": NewMemoryStore(), "redis": NewRedisStore(rdb)}
}

func TestStores(t *testing.T) {
	ctx := context.Background()
	for name, s := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			j := Job{ID: NewID(), Owner: "client:1", Status: StatusDone, Request: Request{Title: "usb hub", Country: "jor"},
				Result: &search.Result{Query: "usb hub", Results: []search.Product{{Title: "hub", Price: new(9.5)}}}}
			if _, err := s.Get(ctx, j.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			if err := s.Save(ctx, j, time.Minute); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, j.ID)
			if err != nil || got.Owner != "client:1" || *got.Result.Results[0].Price != 9.5 {
				t.Fatalf("got %+v %v", got, err)
			}
		})
	}
	m := NewMemoryStore()
	now := time.Now()
	m.now = func() time.Time { return now }
	_ = m.Save(ctx, Job{ID: "x"}, time.Second)
	now = now.Add(2 * time.Second)
	if _, err := m.Get(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired job returned")
	}
}

func waitFor(t *testing.T, s Store, id string, status string) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := s.Get(context.Background(), id)
		if err == nil && j.Status == status && (j.Webhook == nil || j.Webhook.Status != webhook.StatusPending) {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", id, status)
	return Job{}
}

type ctxKey struct{}

func TestRunnerRunsAndNotifies(t *testing.T) {
	r := NewRunner(NewMemoryStore(), discard)
	values := context.WithValue(context.Background(), ctxKey{}, "req-1")
	var sawValue atomic.Bool
	release := make(chan struct{})
	work := func(ctx context.Context, j Job) Job {
		sawValue.Store(ctx.Value(ctxKey{}) == "req-1")
		<-release
		return Done(j, &search.Result{Query: j.Request.Title}, time.Now())
	}
	var notified atomic.Int32
	notify := func(_ context.Context, j Job) webhook.Result {
		if j.Status == StatusDone {
			notified.Add(1)
		}
		return webhook.Result{Status: webhook.StatusDelivered, Attempts: 1, HTTPStatus: 200}
	}
	j, err := r.Start(values, Job{ID: NewID(), Owner: "u1", Request: Request{Title: "hub"},
		Webhook: &Webhook{URL: "https://example.com", Status: webhook.StatusPending}}, time.Minute, 0, work, notify)
	if err != nil || j.Status != StatusRunning || j.Deadline.Sub(j.CreatedAt) != time.Minute {
		t.Fatalf("start: %+v %v", j, err)
	}
	if got, _ := r.Store().Get(context.Background(), j.ID); got.Status != StatusRunning {
		t.Fatalf("saved %+v", got)
	}
	close(release)
	got := waitFor(t, r.Store(), j.ID, StatusDone)
	if got.Result.Query != "hub" || got.FinishedAt == nil || got.Webhook.Status != webhook.StatusDelivered || notified.Load() != 1 {
		t.Fatalf("done %+v", got)
	}
	if !sawValue.Load() {
		t.Error("job context lost the request's values")
	}
	r.Close(context.Background())
}

func TestRunnerLimits(t *testing.T) {
	r := NewRunner(NewMemoryStore(), discard)
	block := make(chan struct{})
	work := func(ctx context.Context, j Job) Job {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return Fail(j, 503, "service_unavailable", "stopped", time.Now())
	}
	for range MaxPerOwner {
		if _, err := r.Start(context.Background(), Job{ID: NewID(), Owner: "busy"}, time.Minute, 0, work, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Start(context.Background(), Job{ID: NewID(), Owner: "busy"}, time.Minute, 0, work, nil); !errors.Is(err, ErrOwnerBusy) {
		t.Fatalf("owner limit: %v", err)
	}
	if _, err := r.Start(context.Background(), Job{ID: NewID(), Owner: "other"}, time.Minute, 0, work, nil); err != nil {
		t.Fatalf("other owner: %v", err)
	}
	close(block)
	r.Close(context.Background())
	if _, err := r.Start(context.Background(), Job{ID: NewID(), Owner: "busy"}, time.Minute, 0, work, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}

func TestRunnerCloseCancels(t *testing.T) {
	r := NewRunner(NewMemoryStore(), discard)
	work := func(ctx context.Context, j Job) Job {
		<-ctx.Done()
		return Fail(j, 503, "service_unavailable", "the server restarted; please retry", time.Now())
	}
	j, _ := r.Start(context.Background(), Job{ID: NewID(), Owner: "u"}, time.Hour, 0, work, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r.Close(ctx)
	got, _ := r.Store().Get(context.Background(), j.ID)
	if got.Status != StatusFailed || got.Error.HTTPStatus != 503 {
		t.Fatalf("after close %+v", got)
	}
}

func TestRunnerPanic(t *testing.T) {
	r := NewRunner(NewMemoryStore(), discard)
	j, _ := r.Start(context.Background(), Job{ID: NewID(), Owner: "u"}, time.Minute, 0, func(context.Context, Job) Job { panic("boom") }, nil)
	r.Close(context.Background())
	if got, _ := r.Store().Get(context.Background(), j.ID); got.Status != StatusFailed || got.Error.Code != "internal_error" {
		t.Fatalf("panic job %+v", got)
	}
}

func TestLost(t *testing.T) {
	now := time.Now()
	j := Job{Status: StatusRunning, Deadline: now}
	if j.Lost(now.Add(lostGrace / 2)) {
		t.Error("lost too early")
	}
	if !j.Lost(now.Add(lostGrace + time.Second)) {
		t.Error("not lost after the grace")
	}
	j.Status = StatusDone
	if j.Lost(now.Add(time.Hour)) {
		t.Error("finished job lost")
	}
}

func TestRunnerWait(t *testing.T) {
	r := NewRunner(NewMemoryStore(), discard)
	defer r.Close(context.Background())
	quick := func(_ context.Context, j Job) Job { return Done(j, &search.Result{Cached: true}, time.Now()) }
	j, err := r.Start(context.Background(), Job{ID: NewID(), Owner: "u"}, time.Minute, time.Second, quick, nil)
	if err != nil || j.Status != StatusDone || !j.Result.Cached {
		t.Fatalf("waited: %+v %v", j, err)
	}
	slow := func(ctx context.Context, j Job) Job {
		<-ctx.Done()
		return Fail(j, 503, "x", "y", time.Now())
	}
	if j, _ := r.Start(context.Background(), Job{ID: NewID(), Owner: "u"}, 50*time.Millisecond, time.Millisecond, slow, nil); j.Status != StatusRunning {
		t.Fatalf("slow: %+v", j)
	}
}
