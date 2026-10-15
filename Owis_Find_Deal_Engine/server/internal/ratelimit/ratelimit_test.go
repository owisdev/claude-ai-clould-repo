package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestAllowBurstThenLimit(t *testing.T) {
	l := New(1, 3, time.Minute)

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d denied within burst", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("request over burst allowed")
	}
	if wait <= 0 || wait > time.Second {
		t.Errorf("retry after = %v, want (0, 1s]", wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("other key must have its own bucket")
	}
}

func TestCleanupRemovesIdle(t *testing.T) {
	l := New(1, 1, time.Millisecond)
	l.Allow("a")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		l.Cleanup(ctx, 5*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		l.mu.Lock()
		n := len(l.buckets)
		l.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle bucket not removed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestAllowRatePerKey(t *testing.T) {
	l := New(1000, 1000, time.Minute)
	for i := range 2 {
		if ok, _ := l.AllowRate("client:1", 0.001, 2); !ok {
			t.Fatalf("request %d refused within the key's burst", i)
		}
	}
	if ok, wait := l.AllowRate("client:1", 0.001, 2); ok || wait <= 0 {
		t.Errorf("third request: ok=%v wait=%v, want refused", ok, wait)
	}
	// A changed limit applies at once.
	if ok, wait := l.AllowRate("client:1", 1000, 1000); !ok {
		t.Errorf("raised rate not applied (wait %v)", wait)
	}
	// Other keys keep the default.
	if ok, _ := l.Allow("user:a"); !ok {
		t.Error("default key refused")
	}
}
