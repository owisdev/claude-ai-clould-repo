package usage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestParsePlans(t *testing.T) {
	p, err := ParsePlans(" free:3, pro:100 ", "free")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Resolve("pro"); got.DailyLimit != 100 {
		t.Errorf("pro = %+v", got)
	}
	if got := p.Resolve("unknown"); got.Name != "free" {
		t.Errorf("unknown plan resolved to %+v, want free", got)
	}
	for _, spec := range []string{"free", "free:0", "free:x", ":3", "pro:5"} {
		if _, err := ParsePlans(spec, "free"); err == nil {
			t.Errorf("ParsePlans(%q): expected error", spec)
		}
	}
}

func stores(t *testing.T) map[string]Store {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return map[string]Store{"memory": NewMemoryStore(), "redis": NewRedisStore(rdb)}
}

func TestQuota(t *testing.T) {
	plans, _ := ParsePlans("free:2,pro:3", "free")
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			q := NewQuota(store, plans)
			ctx := context.Background()

			for i := int64(1); i <= 2; i++ {
				d, err := q.Take(ctx, "alice", "")
				if err != nil || !d.Allowed || d.Used != i || d.Remaining() != 2-i {
					t.Fatalf("take %d: %+v, %v", i, d, err)
				}
			}
			d, _ := q.Take(ctx, "alice", "")
			if d.Allowed || !d.UpgradeRequired || d.Remaining() != 0 {
				t.Errorf("free over quota: %+v", d)
			}

			// A paid plan over its quota gets 429, not 402.
			for i := 0; i < 3; i++ {
				_, _ = q.Take(ctx, "bob", "pro")
			}
			if d, _ := q.Take(ctx, "bob", "pro"); d.Allowed || d.UpgradeRequired {
				t.Errorf("pro over quota: %+v", d)
			}

			// Refund gives the search back.
			d, _ = q.Take(ctx, "carol", "")
			_ = q.Refund(ctx, d)
			d, _ = q.Take(ctx, "carol", "")
			if d.Used != 1 {
				t.Errorf("after refund used = %d, want 1", d.Used)
			}
		})
	}
}

func TestQuotaConcurrentNeverOvershoots(t *testing.T) {
	plans, _ := ParsePlans("free:10", "free")
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			q := NewQuota(store, plans)
			var wg sync.WaitGroup
			var mu sync.Mutex
			allowed := 0
			for i := 0; i < 50; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if d, err := q.Take(context.Background(), "dave", ""); err == nil && d.Allowed {
						mu.Lock()
						allowed++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			if allowed != 10 {
				t.Errorf("allowed = %d, want exactly 10", allowed)
			}
		})
	}
}

func TestQuotaResetsDaily(t *testing.T) {
	plans, _ := ParsePlans("free:1", "free")
	q := NewQuota(NewMemoryStore(), plans)
	day := time.Date(2026, 10, 2, 23, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return day }

	d, _ := q.Take(context.Background(), "erin", "")
	if !d.Reset.Equal(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("reset = %v", d.Reset)
	}
	if d, _ := q.Take(context.Background(), "erin", ""); d.Allowed {
		t.Fatal("second search on day 1 allowed")
	}
	day = day.Add(2 * time.Hour)
	if d, _ := q.Take(context.Background(), "erin", ""); !d.Allowed {
		t.Error("first search on day 2 denied")
	}
}

func TestMemoryStoreCleanup(t *testing.T) {
	m := NewMemoryStore()
	_, _ = m.Incr(context.Background(), "k", time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Cleanup(ctx, 2*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		n := len(m.entries)
		m.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("expired entry not removed")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
