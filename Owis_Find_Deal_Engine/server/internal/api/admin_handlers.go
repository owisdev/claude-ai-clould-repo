package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/settings"
	"owis_find_deal_engine/internal/stats"
	"owis_find_deal_engine/internal/store"
)

// maxMarketsBytes bounds the countries-and-shops JSON.
const maxMarketsBytes = 256 << 10

func (h *adminHandlers) me(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r.Context())
	u, _ := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"email":         a.Email,
		"uid":           u.ID,
		"second_factor": u.SecondFactor,
		"signed_in_at":  u.AuthTime.UTC(),
		"admin_since":   a.CreatedAt.UTC(),
	})
}

func (h *adminHandlers) status(w http.ResponseWriter, r *http.Request) {
	stored, err := h.d.Store.SettingsVersion(r.Context())
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime":                 h.d.Runtime().Status(),
		"stored_settings_version": stored,
	})
}

// settingView is one runtime setting. Value is never set for secrets.
type settingView struct {
	Key       string     `json:"key"`
	Value     *string    `json:"value,omitempty"`
	Secret    bool       `json:"secret,omitempty"`
	Set       bool       `json:"set"` // false: the built-in default applies
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

func (h *adminHandlers) listSettings(w http.ResponseWriter, r *http.Request) {
	rows, version, err := h.d.Store.Settings(r.Context())
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	stored := make(map[string]store.Setting, len(rows))
	for _, s := range rows {
		stored[s.Key] = s
	}
	keys := config.RuntimeKeys()
	for k := range stored {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]settingView, 0, len(keys))
	for _, k := range keys {
		v := settingView{Key: k, Secret: config.Secret(k)}
		if s, ok := stored[k]; ok {
			v.Set, v.UpdatedBy = true, s.UpdatedBy
			at := s.UpdatedAt.UTC()
			v.UpdatedAt = &at
			if !v.Secret {
				val := s.Value
				v.Value = &val
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings_version": version, "settings": out})
}

type settingsChange struct {
	// Changes maps keys to new values; null resets a key to its default.
	Changes map[string]*string `json:"changes"`
}

// changeSettings applies a change set: validated as a whole, refused if
// the configuration it leads to is invalid. Secrets are accepted here
// and never returned.
func (h *adminHandlers) changeSettings(w http.ResponseWriter, r *http.Request) {
	var body settingsChange
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	if len(body.Changes) == 0 {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, `"changes" must name at least one setting`)
		return
	}
	h.applySettings(w, r, "settings.change", body.Changes)
}

// applySettings stores changes, audits them and answers with the result.
func (h *adminHandlers) applySettings(w http.ResponseWriter, r *http.Request, action string, changes map[string]*string) {
	keys := slices.Sorted(maps.Keys(changes))
	version, changed, err := h.d.Settings.Set(r.Context(), actor(r), changes)
	if errors.Is(err, settings.ErrInvalid) {
		h.audit(r, store.AuditEntry{Action: action, Target: strings.Join(keys, ","), Outcome: store.OutcomeFailed,
			Detail: jsonDetail(map[string]any{"keys": keys, "error": err.Error()})})
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	if changed == nil {
		changed = []string{}
	}
	if len(changed) > 0 {
		secrets := []string{}
		for _, k := range changed {
			if config.Secret(k) {
				secrets = append(secrets, k)
			}
		}
		// Values are in the settings history (never for secrets).
		h.audit(r, store.AuditEntry{Action: action, Target: strings.Join(changed, ","),
			Detail: jsonDetail(map[string]any{"keys": changed, "secrets": secrets, "settings_version": version})})
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings_version": version, "changed": changed})
}

type changeView struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Old       *string   `json:"old"`
	New       *string   `json:"new"`
	Secret    bool      `json:"secret,omitempty"`
	ChangedBy string    `json:"changed_by"`
	ChangedAt time.Time `json:"changed_at"`
}

func (h *adminHandlers) settingsHistory(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 100, 1, 1000)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	rows, err := h.d.Store.SettingsHistory(r.Context(), r.URL.Query().Get("key"), limit)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	out := make([]changeView, len(rows))
	for i, c := range rows {
		out[i] = changeView{ID: c.ID, Key: c.Key, Old: c.Old, New: c.New, Secret: c.Secret,
			ChangedBy: c.ChangedBy, ChangedAt: c.ChangedAt.UTC()}
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": out})
}

// getMarkets returns the countries and shops: the stored MARKETS setting
// (which may not be applied yet), else the running catalog.
func (h *adminHandlers) getMarkets(w http.ResponseWriter, r *http.Request) {
	rows, _, err := h.d.Store.Settings(r.Context())
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	for _, s := range rows {
		if s.Key == "MARKETS" {
			writeJSON(w, http.StatusOK, map[string]any{"source": "setting", "markets": json.RawMessage(s.Value)})
			return
		}
	}
	st := h.d.Runtime().Status()
	source := st.MarketsSource
	if source == "setting" {
		source = "built-in" // removed, not applied yet; MARKETS_FILE may apply instead
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "markets": json.RawMessage(st.MarketsJSON)})
}

// putMarkets replaces the countries and shops (the whole catalog JSON, as
// in markets.json).
func (h *adminHandlers) putMarkets(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "Content-Type must be application/json")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMarketsBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, fmt.Sprintf("body must not exceed %d bytes", maxMarketsBytes))
		return
	}
	if _, err := markets.Parse(data); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	v := string(data)
	h.applySettings(w, r, "markets.change", map[string]*string{"MARKETS": &v})
}

func (h *adminHandlers) resetMarkets(w http.ResponseWriter, r *http.Request) {
	h.applySettings(w, r, "markets.reset", map[string]*string{"MARKETS": nil})
}

type adminView struct {
	ID        int64      `json:"id"`
	Email     string     `json:"email"`
	Linked    bool       `json:"linked"` // has signed in (login linked)
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	Disabled  *time.Time `json:"disabled_at,omitempty"`
}

func (h *adminHandlers) listAdmins(w http.ResponseWriter, r *http.Request) {
	list, err := h.d.Store.Admins(r.Context())
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	out := make([]adminView, len(list))
	for i, a := range list {
		out[i] = adminView{ID: a.ID, Email: a.Email, Linked: a.FirebaseUID != nil,
			CreatedAt: a.CreatedAt.UTC(), CreatedBy: a.CreatedBy, Disabled: a.DisabledAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": out})
}

func (h *adminHandlers) addAdmin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(email) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "email must be an email address")
		return
	}
	a, err := h.d.Store.AddAdmin(r.Context(), email, actor(r))
	if errors.Is(err, store.ErrAdminExists) {
		writeError(w, http.StatusConflict, CodeConflict, "already on the admin list")
		return
	}
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	h.audit(r, store.AuditEntry{Action: "admins.add", Target: email})
	writeJSON(w, http.StatusCreated, adminView{ID: a.ID, Email: a.Email, CreatedAt: a.CreatedAt.UTC(), CreatedBy: a.CreatedBy})
}

// changeAdmin disables, enables or unlinks an admin.
func (h *adminHandlers) changeAdmin(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.PathValue("email")))
	action := r.PathValue("action")
	if email == adminFrom(r.Context()).Email && action != "enable" {
		writeError(w, http.StatusConflict, CodeConflict, "you cannot "+action+" yourself; ask another admin")
		return
	}
	var err error
	switch action {
	case "disable", "enable":
		err = h.d.Store.SetAdminDisabled(r.Context(), email, action == "disable")
	case "unlink":
		err = h.d.Store.UnlinkAdmin(r.Context(), email)
	default:
		writeError(w, http.StatusNotFound, CodeNotFound, "action must be disable, enable or unlink")
		return
	}
	switch {
	case errors.Is(err, store.ErrAdminNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no admin with this email")
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case err != nil:
		h.storeError(w, r, err)
	default:
		h.audit(r, store.AuditEntry{Action: "admins." + action, Target: email})
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (h *adminHandlers) auditLog(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 100, 1, 500)
	if err == nil {
		var before int
		before, err = queryInt(r, "before", 0, 0, math.MaxInt)
		if err == nil {
			q := r.URL.Query()
			var entries []store.AuditEntry
			entries, err = h.d.Store.AuditLog(r.Context(), store.AuditFilter{
				Actor: q.Get("actor"), Action: q.Get("action"), BeforeID: int64(before), Limit: limit})
			if err != nil {
				h.storeError(w, r, err)
				return
			}
			if entries == nil {
				entries = []store.AuditEntry{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
			return
		}
	}
	writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
}

type testSearchRequest struct {
	Title      string   `json:"title"`
	Country    string   `json:"country"`
	Markets    []string `json:"markets"`
	AllSources bool     `json:"all_sources"`
}

// testSearch asks each source of the country's shops directly and shows
// its answer, time and estimated cost. Costs money: rate-limited.
func (h *adminHandlers) testSearch(w http.ResponseWriter, r *http.Request) {
	var body testSearchRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	res, err := h.d.Runtime().Probe(r.Context(), search.ProbeRequest{
		Title: body.Title, Country: body.Country, Markets: body.Markets, AllSources: body.AllSources})
	if h.searchInputError(w, err) {
		return
	}
	detail := map[string]any{"title": search.NormalizeTitle(body.Title), "country": body.Country,
		"markets": body.Markets, "all_sources": body.AllSources}
	if err != nil {
		h.audit(r, store.AuditEntry{Action: "tools.test_search", Target: body.Country, Outcome: store.OutcomeFailed, Detail: jsonDetail(detail)})
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
		return
	}
	detail["cost_usd"], detail["took_ms"] = res.CostUSD, res.TookMS
	h.audit(r, store.AuditEntry{Action: "tools.test_search", Target: res.Country, Detail: jsonDetail(detail)})
	writeJSON(w, http.StatusOK, res)
}

// forgetCached removes the cached answer of one search.
func (h *adminHandlers) forgetCached(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title   string `json:"title"`
		Country string `json:"country"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	found, err := h.d.Runtime().ForgetCached(r.Context(), search.Request{Title: body.Title, Country: body.Country})
	if h.searchInputError(w, err) {
		return
	}
	switch {
	case errors.Is(err, ErrCacheDisabled):
		writeError(w, http.StatusConflict, CodeCacheDisabled, err.Error())
		return
	case err != nil:
		h.d.Log.ErrorContext(r.Context(), "cache forget failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "cache unavailable, please retry")
		return
	}
	h.audit(r, store.AuditEntry{Action: "tools.cache_forget", Target: body.Country,
		Detail: jsonDetail(map[string]any{"title": search.NormalizeTitle(body.Title), "country": body.Country, "found": found})})
	writeJSON(w, http.StatusOK, map[string]bool{"found": found})
}

// bumpCache raises CACHE_GENERATION: every cached answer becomes a miss
// once the servers apply the change.
func (h *adminHandlers) bumpCache(w http.ResponseWriter, r *http.Request) {
	rows, _, err := h.d.Store.Settings(r.Context())
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	gen := h.d.Runtime().Status().CacheGeneration
	for _, s := range rows {
		if s.Key == "CACHE_GENERATION" {
			if n, err := strconv.Atoi(s.Value); err == nil {
				gen = n
			}
		}
	}
	next := strconv.Itoa(gen + 1)
	h.applySettings(w, r, "tools.cache_bump", map[string]*string{"CACHE_GENERATION": &next})
}

func (h *adminHandlers) providers(w http.ResponseWriter, _ *http.Request) {
	rt := h.d.Runtime()
	writeJSON(w, http.StatusOK, map[string]any{
		"settings_version": rt.Status().SettingsVersion,
		"chains":           rt.Health(),
	})
}

// usage is the usage and cost dashboard for the last ?days= UTC days
// (today included), with the ?top= most searched queries and this month's
// budget.
func (h *adminHandlers) usage(w http.ResponseWriter, r *http.Request) {
	days, err := queryInt(r, "days", 30, 1, 400)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	top, err := queryInt(r, "top", 20, 1, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := to.AddDate(0, 0, -(days - 1))
	month := stats.MonthStart(now)

	ctx := r.Context()
	rows, err := h.d.Store.UsageRows(ctx, from, to)
	var queries []store.QueryRow
	if err == nil {
		queries, err = h.d.Store.TopQueries(ctx, from, to, top)
	}
	var spent float64
	if err == nil {
		spent, err = h.d.Store.UsageCost(ctx, month, to)
	}
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	st := h.d.Runtime().Status()
	rep := stats.BuildReport(from, to, rows, queries, stats.NewBudget(month, spent, st.BudgetMonthlyUSD, st.BudgetAlertPercent))
	writeJSON(w, http.StatusOK, map[string]any{
		"report": rep,
		"note": "Counts are saved by each server every " + stats.FlushInterval.String() +
			" (UTC days). Costs are estimates from SERPAPI_SEARCH_COST_USD and APIFY_<SHOP>_RUN_COST_USD; " +
			"check the Apify and SerpApi consoles for the bill.",
	})
}

// searchInputError answers bad search input and reports whether it did.
func (h *adminHandlers) searchInputError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, search.ErrInvalidTitle), errors.Is(err, search.ErrUnknownMarket):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, search.ErrUnknownCountry):
		writeError(w, http.StatusBadRequest, CodeUnsupportedCountry, "country must be one of the codes from GET /api/v1/countries")
	default:
		return false
	}
	return true
}

func (h *adminHandlers) storeError(w http.ResponseWriter, r *http.Request, err error) {
	h.d.Log.ErrorContext(r.Context(), "admin store error", "err", err)
	writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "database unavailable, please retry")
}

func queryInt(r *http.Request, name string, def, lo, hi int) (int, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be a number from %d to %d", name, lo, hi)
	}
	return n, nil
}

func validEmail(s string) bool {
	local, domain, ok := strings.Cut(s, "@")
	return ok && local != "" && strings.Contains(domain, ".") && len(s) <= 254 &&
		!strings.ContainsAny(s, " \t\r\n<>,;\"/")
}
