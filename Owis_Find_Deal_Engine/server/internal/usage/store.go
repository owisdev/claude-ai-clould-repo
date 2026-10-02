package usage

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// MemoryStore keeps counters in process memory. Use only when running a
// single instance; counters reset on restart.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]*memEntry
	now     func() time.Time
}

type memEntry struct {
	n       int64
	expires time.Time
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]*memEntry{}, now: time.Now}
}

func (m *MemoryStore) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	e, ok := m.entries[key]
	if !ok || now.After(e.expires) {
		e = &memEntry{expires: now.Add(ttl)}
		m.entries[key] = e
	}
	e.n++
	return e.n, nil
}

func (m *MemoryStore) Decr(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[key]; ok && e.n > 0 {
		e.n--
	}
	return nil
}

// Cleanup drops expired counters every interval until ctx is done.
func (m *MemoryStore) Cleanup(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			now := m.now()
			for k, e := range m.entries {
				if now.After(e.expires) {
					delete(m.entries, k)
				}
			}
			m.mu.Unlock()
		}
	}
}

// RedisStore keeps counters in Redis, shared by all server instances.
type RedisStore struct {
	rdb redis.UniversalClient
}

// NewRedisStore wraps a Redis client.
func NewRedisStore(rdb redis.UniversalClient) *RedisStore {
	return &RedisStore{rdb: rdb}
}

// incrScript increments and sets the TTL only on creation, atomically.
var incrScript = redis.NewScript(`
local n = redis.call("INCR", KEYS[1])
if n == 1 then redis.call("PEXPIRE", KEYS[1], ARGV[1]) end
return n`)

func (r *RedisStore) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	return incrScript.Run(ctx, r.rdb, []string{key}, ttl.Milliseconds()).Int64()
}

// decrScript never lets a counter go below zero.
var decrScript = redis.NewScript(`
local n = tonumber(redis.call("GET", KEYS[1]) or "0")
if n > 0 then return redis.call("DECR", KEYS[1]) end
return 0`)

func (r *RedisStore) Decr(ctx context.Context, key string) error {
	return decrScript.Run(ctx, r.rdb, []string{key}).Err()
}
