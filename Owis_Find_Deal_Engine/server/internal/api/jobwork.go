package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/jobs"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/webhook"
)

// Continuer goes on with a search already counted (metering.Service).
type Continuer interface {
	Continue(ctx context.Context, req search.Request, progress func(*search.Result)) (*search.Result, error)
}

// jobHooks sends a job's webhooks: first_results while the slow shops are
// still searched (when asked for), then completed / failed. One per job.
type jobHooks struct {
	h      *handlers
	url    string
	secret string

	mu     sync.Mutex
	first  *jobs.Delivery // nil = not asked for
	sent   bool           // first_results started (or not needed any more)
	wg     sync.WaitGroup // the first_results delivery
	latest *jobs.Job      // the job as last saved while running
	save   func(jobs.Job) // the runner's progress save
	final  bool           // the search is over: no progress saves any more
}

// newJobHooks returns the hooks of a job with a webhook (nil without one).
func (h *handlers) newJobHooks(hook *jobs.Webhook, secret string) *jobHooks {
	if hook == nil {
		return nil
	}
	k := &jobHooks{h: h, url: hook.URL, secret: secret}
	switch {
	case hook.First != nil && hook.First.Status != webhook.StatusPending:
		d := *hook.First // already sent (a resumed job)
		k.first, k.sent = &d, true
	case hook.Wants(jobs.EventFirstResults):
		k.first = &jobs.Delivery{Status: webhook.StatusPending}
	}
	return k
}

// firstState is the first_results delivery as it is now (nil = not asked).
func (k *jobHooks) firstState() *jobs.Delivery {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.first == nil {
		return nil
	}
	d := *k.first
	return &d
}

// progress saves p (the answer so far) with the first_results delivery
// recorded, and starts that delivery when the fast shops are done and
// other shops are still searched.
func (k *jobHooks) progress(ctx context.Context, p jobs.Job, save func(jobs.Job)) {
	if k == nil || p.Webhook == nil {
		save(p)
		return
	}
	k.mu.Lock()
	start := k.first != nil && !k.sent && p.Result != nil && k.h.fastDone(p.Result)
	if start {
		k.sent = true
		k.wg.Add(1)
	}
	hook := *p.Webhook
	if k.first != nil {
		d := *k.first
		hook.First = &d
	}
	p.Webhook = &hook
	k.latest, k.save = &p, save
	save(p)
	k.mu.Unlock()
	if !start {
		return
	}
	v := viewJob(p, time.Now())
	v.Webhook = nil
	payload, err := json.Marshal(v)
	go func() {
		defer k.wg.Done()
		res := webhook.Result{Status: webhook.StatusFailed}
		if err != nil {
			res.Error = err.Error()
		} else {
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			res = k.h.webhooks.Sender.Deliver(wctx, webhook.Message{URL: k.url, Secret: k.secret, Event: webhook.EventFirstResults,
				Delivery: p.ID + "." + jobs.EventFirstResults, Body: payload})
			cancel()
		}
		if res.Status != webhook.StatusDelivered {
			k.h.log.WarnContext(ctx, "webhook not delivered", "job", p.ID, "event", webhook.EventFirstResults,
				"attempts", res.Attempts, "http_status", res.HTTPStatus, "err", res.Error)
		}
		k.mu.Lock()
		defer k.mu.Unlock()
		k.first = &jobs.Delivery{Status: res.Status, Attempts: res.Attempts, HTTPStatus: res.HTTPStatus, Error: res.Error}
		if k.final || k.latest == nil {
			return // the final save records it
		}
		// Shown at once, not only with the next shop.
		j := *k.latest
		hook := *j.Webhook
		d := *k.first
		hook.First = &d
		j.Webhook = &hook
		k.latest = &j
		k.save(j)
	}()
}

// finish marks the search over (no more progress saves) and returns the
// first_results delivery as it is now.
func (k *jobHooks) finish() *jobs.Delivery {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	k.final = true
	k.mu.Unlock()
	return k.firstState()
}

// notify sends completed / failed after first_results has gone out.
func (k *jobHooks) notify(ctx context.Context, j jobs.Job) jobs.Job {
	k.wg.Wait()
	k.mu.Lock()
	if k.first != nil && !k.sent {
		k.first = &jobs.Delivery{Status: jobs.DeliverySkipped}
		k.sent = true
	}
	k.mu.Unlock()
	hook := *j.Webhook
	hook.First = k.firstState()
	j.Webhook = &hook
	if !hook.Wants(jobs.EventCompleted) {
		hook.Status = jobs.DeliveryNotRequested
		return j
	}
	event := webhook.EventCompleted
	if j.Status != jobs.StatusDone {
		event = webhook.EventFailed
	}
	v := viewJob(j, time.Now())
	v.Webhook = nil // its delivery is not known yet
	payload, err := json.Marshal(v)
	res := webhook.Result{Status: webhook.StatusFailed}
	if err != nil {
		res.Error = err.Error()
	} else {
		res = k.h.webhooks.Sender.Deliver(ctx, webhook.Message{URL: k.url, Secret: k.secret, Event: event, Delivery: j.ID, Body: payload})
	}
	hook.Status, hook.Attempts, hook.HTTPStatus, hook.Error = res.Status, res.Attempts, res.HTTPStatus, res.Error
	return j
}

func (k *jobHooks) notifyFunc() jobs.Notify {
	if k == nil {
		return nil
	}
	return k.notify
}

// fastDone reports whether the fast shops of an answer are done while
// other shops are still searched. Without a fast shop: when the first
// shop is done.
func (h *handlers) fastDone(res *search.Result) bool {
	pending, fast, fastPending, done := 0, 0, 0, 0
	for m, status := range res.Markets {
		isFast := h.groups[m] == config.GroupFast
		switch {
		case status == search.StatusPending:
			pending++
			if isFast {
				fastPending++
			}
		case status != search.StatusOff:
			done++
		}
		if isFast && status != search.StatusOff {
			fast++
		}
	}
	if pending == 0 {
		return false // all done: completed follows
	}
	if fast == 0 {
		return done > 0
	}
	return fastPending == 0
}

// jobWork is the search of a job: metered like POST /api/v1/search, saved
// each time a shop finishes. A resumed job (counted before the restart)
// only searches its pending shops, and is not counted again.
func (h *handlers) jobWork(user auth.User, req search.Request, k *jobHooks, resume bool) jobs.Work {
	return func(ctx context.Context, j jobs.Job, save func(jobs.Job)) jobs.Job {
		base := j.Result
		var targets []markets.Target
		if c, ok := h.catalog.Country(req.Country); ok {
			targets = c.Targets
		}
		merge := func(res *search.Result) *search.Result {
			if !resume {
				return res
			}
			return search.MergeShops(base, res, targets)
		}
		progress := func(res *search.Result) {
			p := j
			p.Result = merge(res)
			k.progress(ctx, p, save)
		}

		var (
			res *search.Result
			err error
		)
		if resume {
			r := req
			if base != nil {
				r.Markets = base.Pending()
			}
			cont, ok := h.searcher.(Continuer)
			if !ok {
				return jobs.Fail(j, http.StatusServiceUnavailable, CodeJobLost, "the server stopped during this search; please start it again", time.Now())
			}
			res, err = cont.Continue(ctx, r, progress)
			if res != nil {
				res = merge(res)
				if errors.Is(err, search.ErrAllFailed) && !search.AllFailed(res.Markets) {
					err = nil // other shops answered before the restart
				}
			}
		} else {
			out, e := h.searcher.SearchProgress(ctx, user, req, 0, progress)
			if out.QuotaKnown {
				j.Quota = viewQuota(out.Quota)
			}
			res, err = out.Result, e
		}
		now := time.Now()
		if first := k.finish(); first != nil && j.Webhook != nil {
			hook := *j.Webhook
			hook.First = first
			j.Webhook = &hook
		}
		switch {
		case err == nil:
			return jobs.Done(j, res, now)
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
			return jobs.Fail(j, http.StatusGatewayTimeout, CodeTimeout, "the search took too long; please retry", now)
		case errors.Is(err, context.Canceled) || ctx.Err() != nil:
			return jobs.Fail(j, http.StatusServiceUnavailable, CodeUnavailable, "the server stopped during the search; please start it again", now)
		}
		e := searchError(err)
		return jobs.Fail(j, e.status, e.code, e.msg, now)
	}
}

// ResumeJobs goes on with the search jobs that a stopped server left
// running (Redis only): each searches its pending shops, joining their
// queue tasks, and sends its webhooks. Jobs past their deadline are
// saved as lost. It returns how many jobs were resumed.
func ResumeJobs(ctx context.Context, d Deps) int {
	if d.Jobs == nil || !d.Jobs.Store().Persistent() {
		return 0
	}
	h := newHandlers(d)
	store := d.Jobs.Store()
	ids, err := store.Running(ctx)
	if err != nil {
		d.Log.Error("listing search jobs to resume failed", "err", err)
		return 0
	}
	n := 0
	for _, id := range ids {
		j, err := store.Get(ctx, id)
		if err != nil || j.Status != jobs.StatusRunning {
			continue
		}
		if !time.Now().Before(j.Deadline) {
			lost := viewJob(j, j.Deadline.Add(time.Hour))
			j.Status, j.Error, j.FinishedAt = lost.Status, lost.Error, lost.FinishedAt
			if err := store.Save(ctx, j, jobs.ResultTTL); err != nil {
				d.Log.Error("saving a lost search job failed", "job", id, "err", err)
			}
			continue
		}
		user := auth.User{ID: j.Owner, Plan: j.Plan, DailyLimit: j.DailyLimit}
		req := search.Request{Title: j.Request.Title, Country: j.Request.Country, Refresh: j.Request.Refresh}
		var secret string
		if j.Webhook != nil {
			secret = h.resumeSecret(ctx, d, j.Owner)
		}
		k := h.newJobHooks(j.Webhook, secret)
		if err := d.Jobs.Resume(context.WithoutCancel(ctx), j, h.jobWork(user, req, k, true), k.notifyFunc()); err != nil {
			d.Log.Error("resuming a search job failed", "job", id, "err", err)
			continue
		}
		n++
	}
	if n > 0 {
		d.Log.Info("search jobs resumed after the restart", "jobs", n)
	}
	return n
}

// resumeSecret derives the webhook secret of an API client again (it is
// never stored); "" when it cannot (the delivery then fails).
func (h *handlers) resumeSecret(ctx context.Context, d Deps, owner string) string {
	id, err := strconv.ParseInt(strings.TrimPrefix(owner, "client:"), 10, 64)
	if err != nil || d.ClientSalt == nil || h.webhooks.Secrets == nil {
		return ""
	}
	salt, err := d.ClientSalt(ctx, id)
	if err != nil || len(salt) == 0 {
		d.Log.Warn("webhook secret of a resumed job not found", "client", id, "err", err)
		return ""
	}
	return h.webhooks.Secrets.Secret(salt)
}
