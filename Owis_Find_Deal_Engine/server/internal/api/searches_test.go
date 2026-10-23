package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/jobs"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/webhook"
)

func decodeJob(t *testing.T, rec *httptest.ResponseRecorder) jobView {
	t.Helper()
	var v jobView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("job: %v %s", err, rec.Body)
	}
	return v
}

// poll reads the job until it is no longer running.
func (e *testEnv) poll(t *testing.T, token, id string) jobView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := e.do("GET", "/api/v1/searches/"+id, token, "")
		if rec.Code != 200 {
			t.Fatalf("poll = %d %s", rec.Code, rec.Body)
		}
		v := decodeJob(t, rec)
		if v.Status != jobs.StatusRunning && (v.Webhook == nil || v.Webhook.Status != webhook.StatusPending) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s still running", id)
	return jobView{}
}

func TestAsyncSearch(t *testing.T) {
	e := newEnv(t, envOpts{plans: "free:3"})
	tok := e.provider.Token(t, "u1", nil)

	rec := e.do("POST", "/api/v1/searches", tok, searchBody)
	if rec.Code != 202 {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	v := decodeJob(t, rec)
	if !jobs.ValidID(v.ID) || rec.Header().Get("Location") != "/api/v1/searches/"+v.ID || v.Request.Country != "jor" {
		t.Fatalf("started = %+v %v", v, rec.Header())
	}
	if v.Status == jobs.StatusRunning && rec.Header().Get("Retry-After") == "" {
		t.Error("running job without a Retry-After hint")
	}
	done := e.poll(t, tok, v.ID)
	if done.Status != jobs.StatusDone || done.Result == nil || len(done.Result.Results) != 1 || done.FinishedAt == nil ||
		done.Quota == nil || done.Quota.Remaining != 2 || done.Quota.Plan != "free" {
		t.Fatalf("done = %+v", done)
	}
	if strings.Contains(e.do("GET", "/api/v1/searches/"+v.ID, tok, "").Body.String(), "u1") {
		t.Error("the owner is shown")
	}

	// Someone else's job, a made-up id and a malformed one: 404.
	other := e.provider.Token(t, "u2", nil)
	for _, path := range []string{"/api/v1/searches/" + v.ID, "/api/v1/searches/" + jobs.NewID(), "/api/v1/searches/nope"} {
		tk := tok
		if strings.HasSuffix(path, v.ID) {
			tk = other
		}
		if rec := e.do("GET", path, tk, ""); rec.Code != 404 || errorCode(t, rec) != CodeNotFound {
			t.Errorf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := e.do("GET", "/api/v1/searches/"+v.ID, "", ""); rec.Code != 401 {
		t.Errorf("without token = %d", rec.Code)
	}
}

func TestAsyncSearchRefusedAtOnce(t *testing.T) {
	e := newEnv(t, envOpts{plans: "free:1"})
	tok := e.provider.Token(t, "u1", nil)
	for body, want := range map[string]string{
		`{"title":"x","country":"jor"}`:                                           CodeInvalidRequest,
		`{"title":"s pen","country":"xyz"}`:                                       CodeUnsupportedCountry,
		`{"title":"s pen","country":"jor","extra":true}`:                          CodeInvalidRequest,
		`{"title":"s pen","country":"jor","webhook_url":"https://example.com/h"}`: CodeInvalidRequest, // users poll
	} {
		if rec := e.do("POST", "/api/v1/searches", tok, body); rec.Code != 400 || errorCode(t, rec) != want {
			t.Errorf("%s = %d %s, want 400 %s", body, rec.Code, rec.Body, want)
		}
	}
	if rec := e.do("POST", "/api/v1/searches", "", searchBody); rec.Code != 401 {
		t.Errorf("without token = %d", rec.Code)
	}
	// Allowance used up: 402 before any job starts, as on POST /search.
	if rec := e.do("POST", "/api/v1/search", tok, searchBody); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec := e.do("POST", "/api/v1/searches", tok, searchBody); rec.Code != 402 || errorCode(t, rec) != CodePaymentRequired ||
		rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("used up = %d %s", rec.Code, rec.Body)
	}
}

func TestAsyncSearchCachedIsDoneAtOnce(t *testing.T) {
	cached := okSearcher
	cached.cached = &search.Result{Query: "s pen", Cached: true, Results: okSearcher.res.Results}
	e := newEnv(t, envOpts{searcher: cached, plans: "free:1"})
	tok := e.provider.Token(t, "u1", nil)
	_ = e.do("POST", "/api/v1/search", tok, searchBody) // used up: cached answers stay free
	rec := e.do("POST", "/api/v1/searches", tok, searchBody)
	v := decodeJob(t, rec)
	if rec.Code != 202 || v.Status != jobs.StatusDone || v.Result == nil || !v.Result.Cached || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("cached = %d %+v", rec.Code, v)
	}
}

func TestAsyncSearchFailed(t *testing.T) {
	e := newEnv(t, envOpts{searcher: fakeSearcher{err: search.ErrAllFailed}})
	tok := e.provider.Token(t, "u1", nil)
	v := decodeJob(t, e.do("POST", "/api/v1/searches", tok, searchBody))
	done := e.poll(t, tok, v.ID)
	if done.Status != jobs.StatusFailed || done.Result != nil || done.Error == nil || done.Error.HTTPStatus != 502 ||
		done.Error.Code != CodeUpstream || done.Quota == nil || done.Quota.Remaining != 100 {
		t.Fatalf("failed = %+v %+v", done, done.Error)
	}
}

// slowSearcher blocks every live search until released.
type slowSearcher struct {
	fakeSearcher
	release chan struct{}
}

func (s slowSearcher) Search(ctx context.Context, req search.Request) (*search.Result, error) {
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.fakeSearcher.Search(ctx, req)
}

func TestAsyncSearchJobLimit(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, envOpts{searcher: slowSearcher{fakeSearcher: okSearcher, release: release}})
	defer close(release)
	tok := e.provider.Token(t, "u1", nil)
	for i := range jobs.MaxPerOwner {
		if rec := e.do("POST", "/api/v1/searches", tok, searchBody); rec.Code != 202 {
			t.Fatalf("job %d = %d %s", i, rec.Code, rec.Body)
		}
	}
	if rec := e.do("POST", "/api/v1/searches", tok, searchBody); rec.Code != 429 || errorCode(t, rec) != CodeTooManyJobs {
		t.Errorf("over the job limit = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/api/v1/searches", e.provider.Token(t, "u2", nil), searchBody); rec.Code != 202 {
		t.Errorf("another user = %d", rec.Code)
	}
}

func TestViewLostJob(t *testing.T) {
	j := jobs.Job{ID: jobs.NewID(), Status: jobs.StatusRunning, Deadline: time.Now().Add(-time.Hour)}
	v := viewJob(j, time.Now())
	if v.Status != jobs.StatusFailed || v.Error == nil || v.Error.Code != CodeJobLost {
		t.Fatalf("lost = %+v", v)
	}
}

// receiver records the webhooks it gets.
type receiver struct {
	mu     sync.Mutex
	calls  []http.Header
	bodies [][]byte
	srv    *httptest.Server
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.calls = append(r.calls, req.Header.Clone())
		r.bodies = append(r.bodies, b)
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func TestAsyncSearchWebhook(t *testing.T) {
	admin := newAdminEnv(t)
	tok := admin.token(t, "uid-1", "owner@example.com", nil)
	authn := apikeys.NewAuthenticator(admin.clients, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sender := webhook.NewSender(webhook.Options{AllowPrivate: true, Backoff: []time.Duration{}})
	pub := newEnv(t, envOpts{keys: authn, webhooks: Webhooks{Sender: sender, Secrets: admin.secrets, AllowPrivate: true}})
	strict := newEnv(t, envOpts{keys: authn, webhooks: Webhooks{Sender: sender, Secrets: admin.secrets}})
	rcv := newReceiver(t)
	body := `{"title":"s pen","country":"jor","webhook_url":"` + rcv.srv.URL + `/hooks/search"}`

	_, out := admin.do(t, "POST", "/admin/v1/clients", tok, `{"name":"agent"}`)
	key := out["key"].(string)
	if c := out["client"].(map[string]any); c["webhook_secret_prefix"] != "" || c["webhook_secret_at"] != nil {
		t.Errorf("new client has a webhook secret: %v", c)
	}

	// No secret yet: refused.
	if rec := pub.do("POST", "/api/v1/searches", key, body); rec.Code != 400 || errorCode(t, rec) != CodeWebhookNotConfigured {
		t.Fatalf("without secret = %d %s", rec.Code, rec.Body)
	}

	rec, out := admin.do(t, "POST", "/admin/v1/clients/1/webhook-secret", tok, "")
	secret, _ := out["webhook_secret"].(string)
	if rec.Code != 201 || !strings.HasPrefix(secret, webhook.SecretPrefix) {
		t.Fatalf("webhook secret = %d %v", rec.Code, out)
	}
	if c := out["client"].(map[string]any); !strings.HasPrefix(secret, c["webhook_secret_prefix"].(string)) || c["webhook_secret_at"] == nil {
		t.Errorf("client view = %v", c)
	}
	if a := admin.store.lastAudit(); a.Action != "clients.webhook_secret" || strings.Contains(string(a.Detail), secret) {
		t.Errorf("audit = %+v %s", a, a.Detail)
	}
	if rec, _ := admin.do(t, "POST", "/admin/v1/clients/9/webhook-secret", tok, ""); rec.Code != 404 {
		t.Errorf("unknown client = %d", rec.Code)
	}
	authn.Forget()

	// Plain http / internal addresses only when allowed (development).
	if rec := strict.do("POST", "/api/v1/searches", key, body); rec.Code != 400 || !strings.Contains(rec.Body.String(), "https") {
		t.Errorf("http webhook on a strict server = %d %s", rec.Code, rec.Body)
	}

	rec = pub.do("POST", "/api/v1/searches", key, body)
	if rec.Code != 202 {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	v := pub.poll(t, key, decodeJob(t, rec).ID)
	if v.Status != jobs.StatusDone || v.Webhook == nil || v.Webhook.Status != webhook.StatusDelivered || v.Webhook.Attempts != 1 ||
		v.Webhook.HTTPStatus != 200 {
		t.Fatalf("job = %+v webhook %+v", v, v.Webhook)
	}
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.calls) != 1 {
		t.Fatalf("webhook calls = %d", len(rcv.calls))
	}
	h, b := rcv.calls[0], rcv.bodies[0]
	if h.Get(webhook.HeaderEvent) != webhook.EventCompleted || h.Get(webhook.HeaderDelivery) != v.ID {
		t.Errorf("headers = %v", h)
	}
	if err := webhook.Verify(secret, h.Get(webhook.HeaderSignature), b, time.Now(), 5*time.Minute); err != nil {
		t.Errorf("signature: %v", err)
	}
	var sent jobView
	if err := json.Unmarshal(b, &sent); err != nil || sent.ID != v.ID || sent.Status != jobs.StatusDone || sent.Result == nil ||
		strings.Contains(string(b), "client:1") {
		t.Errorf("payload = %s (%v)", b, err)
	}

	// A new secret replaces the old one at once.
	_, out = admin.do(t, "POST", "/admin/v1/clients/1/webhook-secret", tok, "")
	if out["webhook_secret"] == secret {
		t.Error("the new secret is the old one")
	}
}
