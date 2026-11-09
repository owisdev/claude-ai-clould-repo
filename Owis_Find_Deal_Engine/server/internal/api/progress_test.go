package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/jobs"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/metering"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"
	"owis_find_deal_engine/internal/webhook"
)

// stepSearcher answers amazon at once (reported as progress) and temu
// when released.
type stepSearcher struct {
	release chan struct{}
	mu      sync.Mutex
	asked   [][]string
}

func newStepSearcher() *stepSearcher { return &stepSearcher{release: make(chan struct{})} }

func (s *stepSearcher) Lookup(context.Context, search.Request) (*search.Result, bool) {
	return nil, false
}

func (s *stepSearcher) Search(ctx context.Context, req search.Request) (*search.Result, error) {
	return s.SearchProgress(ctx, req, 0, nil)
}

func shopResult(req search.Request, shops map[string]string) *search.Result {
	now := time.Now().UTC()
	r := &search.Result{Query: req.Title, Country: req.Country, Results: []search.Product{},
		Markets: map[string]string{}, Shops: map[string]search.ShopInfo{}, FetchedAt: now}
	for m, st := range shops {
		r.Markets[m] = st
		info := search.ShopInfo{Status: st}
		if st == search.StatusOK {
			info.FetchedAt = &now
			r.Results = append(r.Results, search.Product{Market: m, Title: "S Pen " + m, Link: "https://" + m + ".example/x", Position: 1})
		}
		r.Shops[m] = info
	}
	return r
}

func (s *stepSearcher) SearchProgress(ctx context.Context, req search.Request, _ time.Duration, progress func(*search.Result)) (*search.Result, error) {
	ms := req.Markets
	if len(ms) == 0 {
		ms = []string{"amazon", "temu"}
	}
	s.mu.Lock()
	s.asked = append(s.asked, ms)
	s.mu.Unlock()
	if progress != nil && slices.Contains(ms, "amazon") && slices.Contains(ms, "temu") {
		progress(shopResult(req, map[string]string{"amazon": search.StatusOK, "temu": search.StatusPending}))
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	done := map[string]string{}
	for _, m := range ms {
		done[m] = search.StatusOK
	}
	return shopResult(req, done), nil
}

var fastAmazon = map[string]string{"amazon": "fast", "ebay": "fast", "temu": "slow", "aliexpress": "slow"}

// pollUntil reads the job until ok says so.
func (e *testEnv) pollUntil(t *testing.T, token, id string, ok func(jobView) bool) jobView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := e.do("GET", "/api/v1/searches/"+id, token, "")
		v := decodeJob(t, rec)
		if ok(v) {
			if v.Status == jobs.StatusRunning && rec.Header().Get("Retry-After") != "2" {
				t.Errorf("Retry-After = %q, want 2", rec.Header().Get("Retry-After"))
			}
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never got there", id)
	return jobView{}
}

func TestAsyncJobFilledPerShop(t *testing.T) {
	s := newStepSearcher()
	e := newEnv(t, envOpts{searcher: s, groups: fastAmazon})
	tok := e.provider.Token(t, "app-user", nil) // an app user (Firebase)
	rec := e.do("POST", "/api/v1/searches", tok, searchBody)
	if rec.Code != 202 {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	id := decodeJob(t, rec).ID
	v := e.pollUntil(t, tok, id, func(v jobView) bool { return v.Result != nil })
	if v.Status != jobs.StatusRunning || v.Result.Markets["amazon"] != "ok" || v.Result.Markets["temu"] != search.StatusPending ||
		len(v.Result.Results) != 1 || !slices.Equal(v.Result.Pending(), []string{"temu"}) {
		t.Fatalf("so far = %+v %+v", v, v.Result)
	}
	close(s.release)
	done := e.poll(t, tok, id)
	if done.Status != jobs.StatusDone || done.Result.Markets["temu"] != "ok" || len(done.Result.Results) != 2 || done.Quota == nil {
		t.Fatalf("done = %+v", done)
	}
}

func TestWebhookEvents(t *testing.T) {
	admin := newAdminEnv(t)
	tok := admin.token(t, "uid-1", "owner@example.com", nil)
	authn := apikeys.NewAuthenticator(admin.clients, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sender := webhook.NewSender(webhook.Options{AllowPrivate: true, Backoff: []time.Duration{}})
	s := newStepSearcher()
	pub := newEnv(t, envOpts{searcher: s, keys: authn, groups: fastAmazon,
		webhooks: Webhooks{Sender: sender, Secrets: admin.secrets, AllowPrivate: true}})
	rcv := newReceiver(t)
	_, out := admin.do(t, "POST", "/admin/v1/clients", tok, `{"name":"agent"}`)
	key := out["key"].(string)
	admin.do(t, "POST", "/admin/v1/clients/1/webhook-secret", tok, "")
	authn.Forget()

	bad := []string{
		`{"title":"s pen","country":"jor","webhook_url":"` + rcv.srv.URL + `","webhook_events":["nope"]}`,
		`{"title":"s pen","country":"jor","webhook_url":"` + rcv.srv.URL + `","webhook_events":[]}`,
		`{"title":"s pen","country":"jor","webhook_events":["completed"]}`,
	}
	for _, b := range bad {
		if rec := pub.do("POST", "/api/v1/searches", key, b); rec.Code != 400 || errorCode(t, rec) != CodeInvalidRequest {
			t.Errorf("%s = %d %s", b, rec.Code, rec.Body)
		}
	}

	body := `{"title":"s pen","country":"jor","webhook_url":"` + rcv.srv.URL + `","webhook_events":["first_results","completed"]}`
	rec := pub.do("POST", "/api/v1/searches", key, body)
	if rec.Code != 202 {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	id := decodeJob(t, rec).ID
	pub.pollUntil(t, key, id, func(v jobView) bool {
		return v.Webhook != nil && v.Webhook.First != nil && v.Webhook.First.Status == webhook.StatusDelivered
	})
	close(s.release)
	v := pub.poll(t, key, id)
	if v.Status != jobs.StatusDone || v.Webhook.Status != webhook.StatusDelivered || v.Webhook.First.Status != webhook.StatusDelivered ||
		!slices.Equal(v.Webhook.Events, []string{"first_results", "completed"}) {
		t.Fatalf("job webhook = %+v first %+v", v.Webhook, v.Webhook.First)
	}
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.calls) != 2 {
		t.Fatalf("webhook calls = %d", len(rcv.calls))
	}
	first, last := rcv.calls[0], rcv.calls[1]
	if first.Get(webhook.HeaderEvent) != webhook.EventFirstResults || first.Get(webhook.HeaderDelivery) != id+".first_results" ||
		last.Get(webhook.HeaderEvent) != webhook.EventCompleted || last.Get(webhook.HeaderDelivery) != id {
		t.Errorf("events = %v / %v", first, last)
	}
	var sent jobView
	if err := json.Unmarshal(rcv.bodies[0], &sent); err != nil || sent.Status != jobs.StatusRunning || sent.Result == nil ||
		sent.Result.Markets["temu"] != search.StatusPending || sent.Result.Markets["amazon"] != "ok" {
		t.Errorf("first_results payload = %s", rcv.bodies[0])
	}
}

func TestWebhookOnlyFirstResults(t *testing.T) {
	admin := newAdminEnv(t)
	tok := admin.token(t, "uid-1", "owner@example.com", nil)
	authn := apikeys.NewAuthenticator(admin.clients, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sender := webhook.NewSender(webhook.Options{AllowPrivate: true, Backoff: []time.Duration{}})
	pub := newEnv(t, envOpts{keys: authn, groups: fastAmazon, webhooks: Webhooks{Sender: sender, Secrets: admin.secrets, AllowPrivate: true}})
	rcv := newReceiver(t)
	_, out := admin.do(t, "POST", "/admin/v1/clients", tok, `{"name":"agent"}`)
	key := out["key"].(string)
	admin.do(t, "POST", "/admin/v1/clients/1/webhook-secret", tok, "")
	authn.Forget()

	// okSearcher has one shop, answered at once: nothing to wait for, so
	// first_results is skipped and completed was not asked for.
	body := `{"title":"s pen","country":"jor","webhook_url":"` + rcv.srv.URL + `","webhook_events":["first_results"]}`
	rec := pub.do("POST", "/api/v1/searches", key, body)
	id := decodeJob(t, rec).ID
	v := pub.pollUntil(t, key, id, func(v jobView) bool {
		return v.Status == jobs.StatusDone && v.Webhook != nil && v.Webhook.First != nil && v.Webhook.First.Status != webhook.StatusPending
	})
	if v.Webhook.Status != jobs.DeliveryNotRequested || v.Webhook.First.Status != jobs.DeliverySkipped {
		t.Errorf("webhook = %+v first %+v", v.Webhook, v.Webhook.First)
	}
	time.Sleep(50 * time.Millisecond)
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.calls) != 0 {
		t.Errorf("webhook calls = %d, want none", len(rcv.calls))
	}
}

func TestFastDone(t *testing.T) {
	h := &handlers{groups: fastAmazon}
	for _, c := range []struct {
		markets map[string]string
		want    bool
	}{
		{map[string]string{"amazon": "ok", "temu": "pending"}, true},
		{map[string]string{"amazon": "last_known", "ebay": "pending", "temu": "pending"}, false},
		{map[string]string{"amazon": "error", "ebay": "no_results", "temu": "pending"}, true},
		{map[string]string{"amazon": "ok", "temu": "ok"}, false}, // all done: completed
		{map[string]string{"temu": "ok", "aliexpress": "pending"}, true},
		{map[string]string{"temu": "pending", "aliexpress": "pending"}, false},
		{map[string]string{"amazon": "off", "temu": "ok", "aliexpress": "pending"}, true},
	} {
		if got := h.fastDone(&search.Result{Markets: c.markets}); got != c.want {
			t.Errorf("fastDone(%v) = %v, want %v", c.markets, got, c.want)
		}
	}
}

func TestMCPWaitsThenGetResults(t *testing.T) {
	old := mcpWait
	mcpWait = 100 * time.Millisecond
	defer func() { mcpWait = old }()

	admin := newAdminEnv(t)
	tok := admin.token(t, "uid-1", "owner@example.com", nil)
	authn := apikeys.NewAuthenticator(admin.clients, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := newStepSearcher()
	e := &mcpEnv{pub: newEnv(t, envOpts{searcher: s, keys: authn, groups: fastAmazon})}
	_, out := admin.do(t, "POST", "/admin/v1/clients", tok, `{"name":"agent"}`)
	key := out["key"].(string)

	call := func(id int, name string, args map[string]any) map[string]any {
		t.Helper()
		rec := e.modern(key, id, "tools/call", map[string]any{"name": name, "arguments": args})
		o := decodeRPC(t, rec)
		if o.Result == nil || o.Result["isError"] == true {
			t.Fatalf("%s = %s", name, rec.Body)
		}
		return o.Result["structuredContent"].(map[string]any)
	}
	res := call(1, toolSearchProducts, map[string]any{"title": "s pen", "country": "jor"})
	jobID, _ := res["job_id"].(string)
	if res["status"] != jobs.StatusRunning || !jobs.ValidID(jobID) || len(res["pending"].([]any)) != 1 ||
		res["markets"].(map[string]any)["amazon"] != "ok" || len(res["results"].([]any)) != 1 {
		t.Fatalf("search_products = %v", res)
	}
	// Still running: waits up to mcpWait and answers what is ready.
	if res := call(2, toolSearchResults, map[string]any{"job_id": jobID}); res["status"] != jobs.StatusRunning {
		t.Fatalf("get_search_results while running = %v", res)
	}
	close(s.release)
	res = call(3, toolSearchResults, map[string]any{"job_id": jobID})
	if res["status"] != jobs.StatusDone || len(res["pending"].([]any)) != 0 || len(res["results"].([]any)) != 2 {
		t.Fatalf("get_search_results = %v", res)
	}

	// Another key cannot read it; a made-up id is not found.
	other := func() string {
		_, out := admin.do(t, "POST", "/admin/v1/clients", tok, `{"name":"other"}`)
		return out["key"].(string)
	}()
	for _, k := range []string{other, key} {
		id := jobID
		if k == key {
			id = jobs.NewID()
		}
		rec := e.modern(k, 4, "tools/call", map[string]any{"name": toolSearchResults, "arguments": map[string]any{"job_id": id}})
		if o := decodeRPC(t, rec); o.Result["isError"] != true || !strings.Contains(rec.Body.String(), CodeNotFound) {
			t.Errorf("foreign / unknown job = %s", rec.Body)
		}
	}
}

func TestResumeJobs(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := jobs.NewRedisStore(rdb)
	cat, _ := markets.Load("")

	// A job a stopped server left running: amazon done, temu pending.
	now := time.Now().UTC()
	req := search.Request{Title: "s pen", Country: "jor"}
	j := jobs.Job{ID: jobs.NewID(), Owner: "u1", Status: jobs.StatusRunning, Request: jobs.Request{Title: "s pen", Country: "jor"},
		CreatedAt: now, Deadline: now.Add(time.Minute),
		Result: shopResult(req, map[string]string{"amazon": "ok", "temu": search.StatusPending})}
	lost := jobs.Job{ID: jobs.NewID(), Owner: "u1", Status: jobs.StatusRunning, CreatedAt: now.Add(-time.Hour), Deadline: now.Add(-time.Minute)}
	for _, x := range []jobs.Job{j, lost} {
		if err := store.Save(context.Background(), x, time.Hour); err != nil {
			t.Fatal(err)
		}
	}

	s := newStepSearcher()
	close(s.release)
	runner := jobs.NewRunner(store, log)
	defer runner.Close(context.Background())
	plans, _ := usage.ParsePlans("free:10", "free")
	quota := usage.NewQuota(usage.NewMemoryStore(), plans)
	n := ResumeJobs(context.Background(), Deps{Jobs: runner, JobTimeout: time.Minute, Catalog: cat, Log: log,
		Searcher: metering.New(s, quota, log)})
	if n != 1 {
		t.Fatalf("resumed %d jobs, want 1", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	var got jobs.Job
	for time.Now().Before(deadline) {
		got, _ = store.Get(context.Background(), j.ID)
		if got.Status != jobs.StatusRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got.Status != jobs.StatusDone || got.Result.Markets["temu"] != "ok" || got.Result.Markets["amazon"] != "ok" || len(got.Result.Results) != 2 {
		t.Fatalf("resumed job = %+v %+v", got, got.Result)
	}
	s.mu.Lock()
	if len(s.asked) != 1 || !slices.Equal(s.asked[0], []string{"temu"}) {
		t.Errorf("searched %v, want only temu", s.asked)
	}
	s.mu.Unlock()
	if l, _ := store.Get(context.Background(), lost.ID); l.Status != jobs.StatusFailed || l.Error.Code != CodeJobLost {
		t.Errorf("job past its deadline = %+v", l)
	}
	if ids, _ := store.Running(context.Background()); len(ids) != 0 {
		t.Errorf("still listed as running: %v", ids)
	}
	// Not counted again.
	if d, _ := quota.Status(context.Background(), usage.Account{ID: "u1"}); d.Used != 0 {
		t.Errorf("resumed job counted: used %d", d.Used)
	}
}
