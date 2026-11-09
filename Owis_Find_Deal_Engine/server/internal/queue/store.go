package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"owis_find_deal_engine/internal/search"
)

// NewRedis returns a queue kept in Redis: shared by every server and kept
// over restarts.
func NewRedis(rdb redis.UniversalClient, opts Options, log *slog.Logger) *Queue {
	return New(&redisBackend{rdb: rdb}, opts, log)
}

// ---- memory ----

type memTask struct {
	t      Task
	state  string
	out    *Outcome
	doneAt time.Time
}

type memBackend struct {
	mu    sync.Mutex
	tasks map[string]*memTask
	hi    map[string][]string // per class: searches
	lo    map[string][]string // per class: background refreshes
}

func newMemBackend() *memBackend {
	return &memBackend{tasks: map[string]*memTask{}, hi: map[string][]string{}, lo: map[string][]string{}}
}

func (m *memBackend) gc(now time.Time) {
	for id, e := range m.tasks {
		if e.state == stateDone && now.Sub(e.doneAt) > keepDone {
			delete(m.tasks, id)
		}
	}
}

func (m *memBackend) claim(_ context.Context, t Task, now time.Time, join time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gc(now)
	if e, ok := m.tasks[t.ID]; ok && (e.state != stateDone || (e.out.Status != search.StatusError && now.Sub(e.doneAt) < join)) {
		if e.state == stateQueued && e.t.Background && !t.Background {
			if i := slices.Index(m.lo[e.t.Class], t.ID); i >= 0 {
				m.lo[e.t.Class] = slices.Delete(m.lo[e.t.Class], i, i+1)
				m.hi[e.t.Class] = append(m.hi[e.t.Class], t.ID)
			}
			e.t.Background = false
		}
		return false, nil
	}
	m.tasks[t.ID] = &memTask{t: t, state: stateQueued}
	if t.Background {
		m.lo[t.Class] = append(m.lo[t.Class], t.ID)
	} else {
		m.hi[t.Class] = append(m.hi[t.Class], t.ID)
	}
	return true, nil
}

func (m *memBackend) pop(_ context.Context, class string) (Task, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, lane := range []map[string][]string{m.hi, m.lo} {
		for len(lane[class]) > 0 {
			id := lane[class][0]
			lane[class] = lane[class][1:]
			if e, ok := m.tasks[id]; ok && e.state == stateQueued {
				e.state = stateRunning
				return e.t, true, nil
			}
		}
	}
	return Task{}, false, nil
}

func (m *memBackend) beat(context.Context, []string) error { return nil }

func (m *memBackend) finish(_ context.Context, id string, o Outcome, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.tasks[id]
	if !ok {
		e = &memTask{}
		m.tasks[id] = e
	}
	e.state, e.out, e.doneAt = stateDone, &o, now
	return nil
}

func (m *memBackend) requeue(_ context.Context, t Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[t.ID] = &memTask{t: t, state: stateQueued}
	m.hi[t.Class] = append([]string{t.ID}, m.hi[t.Class]...)
	return nil
}

func (m *memBackend) state(_ context.Context, id string) (string, *Outcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.tasks[id]
	if !ok {
		return "", nil, nil
	}
	return e.state, e.out, nil
}

// orphans: a memory queue lives and dies with its server.
func (m *memBackend) orphans(context.Context) ([]Task, error) { return nil, nil }

// ---- Redis ----
//
// owis:q:task:<id>        hash: task (JSON), state, bg, done_ms, outcome
//                         (JSON), good (1 = finished without error)
// owis:q:hi:<class>       list of queued searches
// owis:q:lo:<class>       list of queued background refreshes
// owis:q:run              list of running task ids (every server)
// owis:q:beat:<id>        set while the running task's server lives

type redisBackend struct {
	rdb redis.UniversalClient
}

const (
	kTask = "owis:q:task:"
	kHi   = "owis:q:hi:"
	kLo   = "owis:q:lo:"
	kRun  = "owis:q:run"
	kBeat = "owis:q:beat:"
)

// claimScript: KEYS task, hi, lo; ARGV task JSON, now ms, join ms, ttl s,
// background "1"/"0", id. Returns 1 when the task was added.
var claimScript = redis.NewScript(`
local st = redis.call('HGET', KEYS[1], 'state')
if st then
  local done = tonumber(redis.call('HGET', KEYS[1], 'done_ms') or '0')
  local good = redis.call('HGET', KEYS[1], 'good') == '1'
  if st ~= 'done' or (good and tonumber(ARGV[2]) - done < tonumber(ARGV[3])) then
    if st == 'queued' and ARGV[5] == '0' and redis.call('HGET', KEYS[1], 'bg') == '1' then
      if redis.call('LREM', KEYS[3], 1, ARGV[6]) > 0 then redis.call('RPUSH', KEYS[2], ARGV[6]) end
      redis.call('HSET', KEYS[1], 'bg', '0')
    end
    return 0
  end
  redis.call('DEL', KEYS[1])
end
redis.call('HSET', KEYS[1], 'task', ARGV[1], 'state', 'queued', 'bg', ARGV[5])
redis.call('EXPIRE', KEYS[1], ARGV[4])
if ARGV[5] == '1' then redis.call('RPUSH', KEYS[3], ARGV[6]) else redis.call('RPUSH', KEYS[2], ARGV[6]) end
return 1
`)

// popScript: KEYS hi, lo, run; ARGV task prefix, beat prefix, beat ttl s,
// task ttl s. Returns {id, task JSON} or false.
var popScript = redis.NewScript(`
for i = 1, 2 do
  while true do
    local id = redis.call('LPOP', KEYS[i])
    if not id then break end
    local k = ARGV[1] .. id
    if redis.call('HGET', k, 'state') == 'queued' then
      redis.call('HSET', k, 'state', 'running')
      redis.call('EXPIRE', k, ARGV[4])
      redis.call('RPUSH', KEYS[3], id)
      redis.call('SET', ARGV[2] .. id, '1', 'EX', ARGV[3])
      return {id, redis.call('HGET', k, 'task')}
    end
  end
end
return false
`)

// orphanScript: KEYS run, task; ARGV id, beat key. Removes a running task
// whose heartbeat is gone; returns its task JSON (once, for one server).
var orphanScript = redis.NewScript(`
if redis.call('EXISTS', ARGV[2]) == 1 then return false end
if redis.call('LREM', KEYS[1], 0, ARGV[1]) == 0 then return false end
return redis.call('HGET', KEYS[2], 'task') or ''
`)

func secs(d time.Duration) int { return int(d / time.Second) }

func (r *redisBackend) claim(ctx context.Context, t Task, now time.Time, join time.Duration) (bool, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return false, err
	}
	bg := "0"
	if t.Background {
		bg = "1"
	}
	n, err := claimScript.Run(ctx, r.rdb, []string{kTask + t.ID, kHi + t.Class, kLo + t.Class},
		string(b), now.UnixMilli(), join.Milliseconds(), secs(keepActive), bg, t.ID).Int()
	return n == 1, err
}

func (r *redisBackend) pop(ctx context.Context, class string) (Task, bool, error) {
	v, err := popScript.Run(ctx, r.rdb, []string{kHi + class, kLo + class, kRun},
		kTask, kBeat, secs(beatTTL), secs(keepActive)).StringSlice()
	if errors.Is(err, redis.Nil) {
		return Task{}, false, nil
	}
	if err != nil || len(v) != 2 {
		return Task{}, false, err
	}
	var t Task
	if err := json.Unmarshal([]byte(v[1]), &t); err != nil {
		return Task{}, false, err
	}
	return t, true, nil
}

func (r *redisBackend) beat(ctx context.Context, ids []string) error {
	_, err := r.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, id := range ids {
			p.Set(ctx, kBeat+id, "1", beatTTL)
			p.Expire(ctx, kTask+id, keepActive)
		}
		return nil
	})
	return err
}

func (r *redisBackend) finish(ctx context.Context, id string, o Outcome, now time.Time) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = r.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		good := "0"
		if o.Status != search.StatusError {
			good = "1"
		}
		p.HSet(ctx, kTask+id, "state", stateDone, "done_ms", strconv.FormatInt(now.UnixMilli(), 10), "outcome", string(b), "good", good)
		p.Expire(ctx, kTask+id, keepDone)
		p.LRem(ctx, kRun, 0, id)
		p.Del(ctx, kBeat+id)
		return nil
	})
	return err
}

func (r *redisBackend) requeue(ctx context.Context, t Task) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = r.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, kTask+t.ID, "task", string(b), "state", stateQueued, "bg", "0")
		p.Expire(ctx, kTask+t.ID, keepActive)
		p.LRem(ctx, kRun, 0, t.ID)
		p.Del(ctx, kBeat+t.ID)
		p.LPush(ctx, kHi+t.Class, t.ID)
		return nil
	})
	return err
}

func (r *redisBackend) state(ctx context.Context, id string) (string, *Outcome, error) {
	v, err := r.rdb.HMGet(ctx, kTask+id, "state", "outcome").Result()
	if err != nil {
		return "", nil, err
	}
	st, _ := v[0].(string)
	if st != stateDone {
		return st, nil, nil
	}
	raw, _ := v[1].(string)
	var o Outcome
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return "", nil, err
	}
	return st, &o, nil
}

func (r *redisBackend) orphans(ctx context.Context) ([]Task, error) {
	ids, err := r.rdb.LRange(ctx, kRun, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	var out []Task
	for _, id := range ids {
		v, err := orphanScript.Run(ctx, r.rdb, []string{kRun, kTask + id}, id, kBeat+id).Text()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return out, err
		}
		var t Task
		if v == "" || json.Unmarshal([]byte(v), &t) != nil {
			continue // its record expired: nobody waits for it
		}
		out = append(out, t)
	}
	return out, nil
}
