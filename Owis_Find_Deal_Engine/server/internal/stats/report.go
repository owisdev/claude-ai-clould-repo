package stats

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/store"
)

// Report is the usage and cost dashboard for a range of UTC days.
type Report struct {
	From       string        `json:"from"` // 2006-01-02, inclusive
	To         string        `json:"to"`
	Totals     Totals        `json:"totals"`
	Days       []DayTotals   `json:"days"`
	Countries  []CountryRow  `json:"countries"`
	Sources    []SourceRow   `json:"sources"`
	Fallbacks  []FallbackRow `json:"fallbacks"`
	TopQueries []QueryView   `json:"top_queries"`
	// Clients are the searches of each API client (step 12).
	Clients []ClientRow `json:"clients"`
	Budget  Budget      `json:"budget"`
}

// ClientRow are the searches of one API client in the range.
type ClientRow struct {
	Client   string `json:"client"` // client:<id>
	ClientID int64  `json:"client_id"`
	Name     string `json:"name"` // filled by the admin API
	SearchCounts
	// CostUSD estimates the client's share of the source cost: its live
	// searches × the average cost of a live search in the range.
	CostUSD float64 `json:"cost_usd_estimate"`
	// LastDay is the last day with a search (2006-01-02).
	LastDay string `json:"last_day"`
}

// SearchCounts counts the searches of users.
type SearchCounts struct {
	Searches int64 `json:"searches"` // answered + refused + failed
	Hits     int64 `json:"cache_hits"`
	Stale    int64 `json:"cache_stale"`
	Live     int64 `json:"live"`
	Quota    int64 `json:"quota_refused"`
	Errors   int64 `json:"errors"`
	// HitRate is the share of answered searches served from the cache
	// (fresh or stale), 0-1.
	HitRate float64 `json:"cache_hit_rate"`
}

// Totals counts the searches of users and the calls and cost of sources.
type Totals struct {
	SearchCounts
	SourceCalls int64   `json:"source_calls"`
	Fallbacks   int64   `json:"fallbacks"`
	CostUSD     float64 `json:"cost_usd"` // estimated
}

// DayTotals are the totals of one day.
type DayTotals struct {
	Day string `json:"day"`
	Totals
}

// CountryRow are the searches of one country.
type CountryRow struct {
	Country string `json:"country"`
	SearchCounts
}

// SourceRow is one source for one shop.
type SourceRow struct {
	Provider  string  `json:"provider"`
	Market    string  `json:"market"`
	Calls     int64   `json:"calls"`
	OK        int64   `json:"ok"`
	NoResults int64   `json:"no_results"`
	Errors    int64   `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
	AvgMS     int64   `json:"avg_ms"`
	CostUSD   float64 `json:"cost_usd"`
}

// FallbackRow counts a chain moving from one provider to the next.
type FallbackRow struct {
	Chain  string `json:"chain"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"` // error, partial, circuit_open
	Count  int64  `json:"count"`
}

// QueryView is one of the most searched queries.
type QueryView struct {
	Country  string `json:"country"`
	Query    string `json:"query"`
	Searches int64  `json:"searches"`
	Live     int64  `json:"live"`
}

// Budget statuses.
const (
	BudgetNone     = "none"     // no monthly budget set
	BudgetOK       = "ok"       // below the alert level
	BudgetWarning  = "warning"  // alert level reached
	BudgetExceeded = "exceeded" // budget used up
)

// Budget compares this month's estimated cost with the monthly budget.
type Budget struct {
	Month        string  `json:"month"` // 2006-01 (UTC)
	LimitUSD     float64 `json:"limit_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	Percent      float64 `json:"percent"`
	AlertPercent int     `json:"alert_percent"`
	Status       string  `json:"status"`
}

// NewBudget compares spent with the limit (0 = no budget).
func NewBudget(month time.Time, spent, limit float64, alertPercent int) Budget {
	b := Budget{Month: month.UTC().Format("2006-01"), LimitUSD: limit, SpentUSD: round(spent, 4), AlertPercent: alertPercent, Status: BudgetNone}
	if limit <= 0 {
		return b
	}
	b.Percent = round(100*spent/limit, 1)
	switch {
	case spent >= limit:
		b.Status = BudgetExceeded
	case b.Percent >= float64(alertPercent):
		b.Status = BudgetWarning
	default:
		b.Status = BudgetOK
	}
	return b
}

// MonthStart returns the first day of t's month (UTC).
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// BuildReport turns stored counters into the dashboard for from..to.
func BuildReport(from, to time.Time, rows []store.UsageRow, queries []store.QueryRow, budget Budget) Report {
	rep := Report{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Budget: budget,
		Days: []DayTotals{}, Countries: []CountryRow{}, Sources: []SourceRow{}, Fallbacks: []FallbackRow{}, TopQueries: []QueryView{},
		Clients: []ClientRow{}}

	days := map[string]*Totals{}
	countries := map[string]*SearchCounts{}
	sources := map[[2]string]*SourceRow{}
	fallbacks := map[[4]string]*FallbackRow{}
	sourceMS := map[[2]string]int64{}
	clients := map[string]*ClientRow{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days[d.Format(time.DateOnly)] = &Totals{}
	}
	for _, r := range rows {
		day := days[r.Day.Format(time.DateOnly)]
		if day == nil {
			day = &Totals{}
			days[r.Day.Format(time.DateOnly)] = day
		}
		switch r.Kind {
		case KindSearch:
			c := countries[r.K1]
			if c == nil {
				c = &SearchCounts{}
				countries[r.K1] = c
			}
			for _, t := range []*SearchCounts{&rep.Totals.SearchCounts, &day.SearchCounts, c} {
				t.add(r.K2, r.Count)
			}
		case KindSource:
			for _, t := range []*Totals{&rep.Totals, day} {
				t.SourceCalls += r.Count
				t.CostUSD += r.CostUSD
			}
			k := [2]string{r.K1, r.K2}
			s := sources[k]
			if s == nil {
				s = &SourceRow{Provider: r.K1, Market: r.K2}
				sources[k] = s
			}
			s.Calls += r.Count
			s.CostUSD += r.CostUSD
			sourceMS[k] += r.TotalMS
			switch r.K3 {
			case "ok":
				s.OK += r.Count
			case "no_results":
				s.NoResults += r.Count
			default:
				s.Errors += r.Count
			}
		case KindClient:
			c := clients[r.K1]
			if c == nil {
				id, _ := strconv.ParseInt(strings.TrimPrefix(r.K1, "client:"), 10, 64)
				c = &ClientRow{Client: r.K1, ClientID: id}
				clients[r.K1] = c
			}
			c.add(r.K3, r.Count)
			if d := r.Day.Format(time.DateOnly); d > c.LastDay {
				c.LastDay = d
			}
		case KindFallback:
			for _, t := range []*Totals{&rep.Totals, day} {
				t.Fallbacks += r.Count
			}
			next, reason, _ := strings.Cut(r.K3, ":")
			k := [4]string{r.K1, r.K2, next, reason}
			f := fallbacks[k]
			if f == nil {
				f = &FallbackRow{Chain: r.K1, From: r.K2, To: next, Reason: reason}
				fallbacks[k] = f
			}
			f.Count += r.Count
		}
	}

	rep.Totals.finish()
	for d, t := range days {
		t.finish()
		rep.Days = append(rep.Days, DayTotals{Day: d, Totals: *t})
	}
	slices.SortFunc(rep.Days, func(a, b DayTotals) int { return cmp.Compare(a.Day, b.Day) })
	for c, t := range countries {
		t.finish()
		rep.Countries = append(rep.Countries, CountryRow{Country: c, SearchCounts: *t})
	}
	slices.SortFunc(rep.Countries, func(a, b CountryRow) int {
		return cmp.Or(cmp.Compare(b.Searches, a.Searches), cmp.Compare(a.Country, b.Country))
	})
	for k, s := range sources {
		if s.Calls > 0 {
			s.AvgMS = sourceMS[k] / s.Calls
			s.ErrorRate = round(float64(s.Errors)/float64(s.Calls), 3)
		}
		s.CostUSD = round(s.CostUSD, 4)
		rep.Sources = append(rep.Sources, *s)
	}
	slices.SortFunc(rep.Sources, func(a, b SourceRow) int {
		return cmp.Or(cmp.Compare(b.CostUSD, a.CostUSD), cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Market, b.Market))
	})
	for _, f := range fallbacks {
		rep.Fallbacks = append(rep.Fallbacks, *f)
	}
	slices.SortFunc(rep.Fallbacks, func(a, b FallbackRow) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Chain, b.Chain), cmp.Compare(a.From, b.From), cmp.Compare(a.Reason, b.Reason))
	})
	var perLive float64
	if rep.Totals.Live > 0 {
		perLive = rep.Totals.CostUSD / float64(rep.Totals.Live)
	}
	for _, c := range clients {
		c.finish()
		c.CostUSD = round(float64(c.Live)*perLive, 4)
		rep.Clients = append(rep.Clients, *c)
	}
	slices.SortFunc(rep.Clients, func(a, b ClientRow) int {
		return cmp.Or(cmp.Compare(b.Searches, a.Searches), cmp.Compare(a.ClientID, b.ClientID))
	})
	for _, q := range queries {
		rep.TopQueries = append(rep.TopQueries, QueryView{Country: q.Country, Query: q.Query, Searches: q.Searches, Live: q.Live})
	}
	return rep
}

func (t *SearchCounts) add(outcome string, n int64) {
	t.Searches += n
	switch outcome {
	case "hit":
		t.Hits += n
	case "stale":
		t.Stale += n
	case "live":
		t.Live += n
	case "quota":
		t.Quota += n
	default:
		t.Errors += n
	}
}

func (t *SearchCounts) finish() {
	if answered := t.Hits + t.Stale + t.Live; answered > 0 {
		t.HitRate = round(float64(t.Hits+t.Stale)/float64(answered), 3)
	}
}

func (t *Totals) finish() {
	t.SearchCounts.finish()
	t.CostUSD = round(t.CostUSD, 4)
}

func round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}
