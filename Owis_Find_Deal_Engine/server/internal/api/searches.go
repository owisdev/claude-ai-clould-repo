package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/jobs"
	"owis_find_deal_engine/internal/metering"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/usage"
	"owis_find_deal_engine/internal/webhook"
)

// Error codes of the asynchronous search.
const (
	CodeTooManyJobs          = "too_many_jobs"          // too many searches running for this caller
	CodeWebhookNotConfigured = "webhook_not_configured" // the API client has no webhook secret yet
	CodeJobLost              = "job_lost"               // the server running the search stopped
	CodeTimeout              = "timeout"
)

// pollAfter is the Retry-After hint while a job is running: the app shows
// each shop's results as they come (step 14b part 3).
const pollAfter = 2

// cachedWait is how long POST /api/v1/searches waits for an answer that is
// in the cache, so the 202 already carries it.
const cachedWait = 3 * time.Second

// Checker tells before a job starts whether its search would be refused
// for the allowance (metering.Service). Optional.
type Checker interface {
	Check(ctx context.Context, user auth.User, req search.Request) (metering.Outcome, bool, error)
}

// WebhookSender delivers webhooks (webhook.Sender).
type WebhookSender interface {
	Deliver(ctx context.Context, m webhook.Message) webhook.Result
}

// Webhooks are what signed webhooks need; a nil Sender refuses
// webhook_url.
type Webhooks struct {
	Sender  WebhookSender
	Secrets *webhook.Secrets
	// AllowPrivate allows http and internal addresses (development only).
	AllowPrivate bool
}

type asyncRequest struct {
	Title      string `json:"title"`
	Country    string `json:"country"`
	Refresh    bool   `json:"refresh"`
	WebhookURL string `json:"webhook_url"`
	// WebhookEvents are the events to send: first_results and / or
	// completed (default: completed).
	WebhookEvents []string `json:"webhook_events"`
}

// webhookEvents checks the events a client asks for.
func webhookEvents(events []string) ([]string, error) {
	if events == nil {
		return []string{jobs.EventCompleted}, nil
	}
	if len(events) == 0 {
		return nil, errors.New("webhook_events must name at least one event: first_results, completed")
	}
	var out []string
	for _, e := range events {
		if e != jobs.EventFirstResults && e != jobs.EventCompleted {
			return nil, errors.New("webhook_events: unknown event " + strconv.Quote(e) + " (first_results, completed)")
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

// jobView is a job as the caller sees it (and as the webhook sends it).
type jobView struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"` // running, done, failed
	Request    jobs.Request   `json:"request"`
	CreatedAt  time.Time      `json:"created_at"`
	FinishedAt *time.Time     `json:"finished_at"`
	Result     *search.Result `json:"result,omitempty"`
	Error      *jobs.Error    `json:"error,omitempty"`
	Quota      *jobs.Quota    `json:"quota,omitempty"`
	Webhook    *jobs.Webhook  `json:"webhook,omitempty"`
}

func viewJob(j jobs.Job, now time.Time) jobView {
	if j.Lost(now) {
		j = jobs.Fail(j, http.StatusServiceUnavailable, CodeJobLost,
			"the server running this search stopped before it finished; please start it again", j.Deadline)
	}
	return jobView{ID: j.ID, Status: j.Status, Request: j.Request, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt,
		Result: j.Result, Error: j.Error, Quota: j.Quota, Webhook: j.Webhook}
}

func jobPath(id string) string { return "/api/v1/searches/" + id }

// startSearch starts an asynchronous search: 202 with the job, whose
// status is "running" (poll GET /api/v1/searches/{id}) or already "done"
// for a cached answer. Bad input, a used-up allowance or a missing scope
// are refused at once, with the same answers as POST /api/v1/search.
func (h *handlers) startSearch(w http.ResponseWriter, r *http.Request) {
	var body asyncRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	ctx := r.Context()
	user, _ := auth.UserFrom(ctx)
	client, isClient := clientFrom(ctx)
	if isClient && !apikeys.AllowedCountry(client, body.Country) {
		writeError(w, http.StatusForbidden, CodeCountryNotAllowed, "this API key may not search in country "+strconv.Quote(body.Country))
		return
	}
	req := search.Request{Title: body.Title, Country: body.Country, Refresh: body.Refresh}
	if n := len([]rune(search.NormalizeTitle(req.Title))); n < search.MinTitleLen || n > search.MaxTitleLen {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid title: must be "+
			strconv.Itoa(search.MinTitleLen)+"-"+strconv.Itoa(search.MaxTitleLen)+" characters")
		return
	}
	if _, ok := h.catalog.Country(req.Country); !ok {
		e := searchError(search.ErrUnknownCountry)
		writeError(w, e.status, e.code, e.msg)
		return
	}

	var hook *jobs.Webhook
	var secret string
	if body.WebhookURL == "" && body.WebhookEvents != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "webhook_events needs a webhook_url")
		return
	}
	if body.WebhookURL != "" {
		switch {
		case h.webhooks.Sender == nil || h.webhooks.Secrets == nil:
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "webhooks are not enabled on this server")
			return
		case !isClient:
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "webhook_url is only for API keys; apps poll GET "+jobPath("{id}"))
			return
		case len(client.WebhookSalt) == 0:
			writeError(w, http.StatusBadRequest, CodeWebhookNotConfigured,
				"this API client has no webhook secret yet; an admin creates it in the admin panel (API clients → Webhook secret)")
			return
		}
		if err := webhook.CheckURL(body.WebhookURL, h.webhooks.AllowPrivate); err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
		events, err := webhookEvents(body.WebhookEvents)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
		hook = &jobs.Webhook{URL: body.WebhookURL, Events: events, Status: webhook.StatusPending}
		if !hook.Wants(jobs.EventCompleted) {
			hook.Status = jobs.DeliveryNotRequested
		}
		if hook.Wants(jobs.EventFirstResults) {
			hook.First = &jobs.Delivery{Status: webhook.StatusPending}
		}
		secret = h.webhooks.Secrets.Secret(client.WebhookSalt)
	}

	wait := time.Duration(0)
	if c, ok := h.searcher.(Checker); ok {
		out, cached, err := c.Check(ctx, user, req)
		if out.QuotaKnown {
			setQuotaHeaders(w, out.Quota)
		}
		if err != nil {
			e := searchError(err)
			if e.retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(e.retryAfter))
			}
			writeError(w, e.status, e.code, e.msg)
			return
		}
		if cached {
			wait = cachedWait
		}
	}

	j := jobs.Job{ID: jobs.NewID(), Owner: user.ID, Request: jobs.Request{Title: req.Title, Country: req.Country, Refresh: req.Refresh},
		Webhook: hook, Plan: user.Plan, DailyLimit: user.DailyLimit}
	k := h.newJobHooks(hook, secret)
	j, err := h.jobs.Start(ctx, j, h.jobTimeout, wait, h.jobWork(user, req, k, false), k.notifyFunc())
	switch {
	case errors.Is(err, jobs.ErrOwnerBusy):
		w.Header().Set("Retry-After", strconv.Itoa(pollAfter))
		writeError(w, http.StatusTooManyRequests, CodeTooManyJobs, err.Error()+"; wait for one to finish")
		return
	case errors.Is(err, jobs.ErrServerBusy), errors.Is(err, jobs.ErrClosed):
		w.Header().Set("Retry-After", strconv.Itoa(pollAfter))
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "service temporarily busy, please retry")
		return
	case err != nil:
		h.log.ErrorContext(ctx, "starting search job failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "service temporarily unavailable, please retry")
		return
	}
	w.Header().Set("Location", jobPath(j.ID))
	if j.Status == jobs.StatusRunning {
		w.Header().Set("Retry-After", strconv.Itoa(pollAfter))
	}
	writeJSON(w, http.StatusAccepted, viewJob(j, time.Now()))
}

func viewQuota(d usage.Decision) *jobs.Quota {
	return &jobs.Quota{Plan: d.Plan.Name, Limit: d.Plan.DailyLimit, Remaining: d.Remaining(), Reset: d.Reset.UTC()}
}

// getSearch returns a job of the caller (404 for anyone else's).
func (h *handlers) getSearch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, _ := auth.UserFrom(r.Context())
	if !jobs.ValidID(id) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no search job with this id (finished jobs are kept "+jobs.ResultTTL.String()+")")
		return
	}
	j, err := h.jobs.Store().Get(r.Context(), id)
	switch {
	case errors.Is(err, jobs.ErrNotFound) || (err == nil && j.Owner != user.ID):
		writeError(w, http.StatusNotFound, CodeNotFound, "no search job with this id (finished jobs are kept "+jobs.ResultTTL.String()+")")
		return
	case err != nil:
		h.log.ErrorContext(r.Context(), "reading search job failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "service temporarily unavailable, please retry")
		return
	}
	v := viewJob(j, time.Now())
	if v.Status == jobs.StatusRunning {
		w.Header().Set("Retry-After", strconv.Itoa(pollAfter))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, v)
}
