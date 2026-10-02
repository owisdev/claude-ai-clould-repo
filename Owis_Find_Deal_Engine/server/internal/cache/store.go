package cache

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// MemoryStore is a bounded LRU cache in process memory (single instance).
type MemoryStore struct {
	mu    sync.Mutex
	max   int
	ll    *list.List // front = most recently used
	items map[string]*list.Element
	now   func() time.Time
}

type memItem struct {
	key     string
	entry   Entry
	expires time.Time
}

// NewMemoryStore keeps at most maxEntries entries.
func NewMemoryStore(maxEntries int) *MemoryStore {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	return &MemoryStore{max: maxEntries, ll: list.New(), items: map[string]*list.Element{}, now: time.Now}
}

func (m *MemoryStore) Get(_ context.Context, key string) (*Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.items[key]
	if !ok {
		return nil, nil
	}
	it := el.Value.(*memItem)
	if m.now().After(it.expires) {
		m.ll.Remove(el)
		delete(m.items, key)
		return nil, nil
	}
	m.ll.MoveToFront(el)
	e := it.entry
	return &e, nil
}

func (m *MemoryStore) Set(_ context.Context, key string, e *Entry, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it := &memItem{key: key, entry: *e, expires: m.now().Add(ttl)}
	if el, ok := m.items[key]; ok {
		el.Value = it
		m.ll.MoveToFront(el)
		return nil
	}
	m.items[key] = m.ll.PushFront(it)
	for m.ll.Len() > m.max {
		oldest := m.ll.Back()
		m.ll.Remove(oldest)
		delete(m.items, oldest.Value.(*memItem).key)
	}
	return nil
}

// Len returns the number of stored entries.
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ll.Len()
}

// RedisStore keeps entries in Redis as JSON, shared by all instances.
type RedisStore struct {
	rdb redis.UniversalClient
}

// NewRedisStore wraps a Redis client.
func NewRedisStore(rdb redis.UniversalClient) *RedisStore { return &RedisStore{rdb: rdb} }

func (r *RedisStore) Get(ctx context.Context, key string) (*Entry, error) {
	b, err := r.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache get: %w", err)
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		// Corrupt or old-format entry: treat as a miss; it will be rewritten.
		return nil, nil
	}
	return &e, nil
}

func (r *RedisStore) Set(ctx context.Context, key string, e *Entry, ttl time.Duration) error {
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("cache encode: %w", err)
	}
	if err := r.rdb.Set(ctx, key, b, ttl).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}
