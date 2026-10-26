package stats

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"owis_find_deal_engine/internal/store"
)

type memSink struct {
	usage   []store.UsageRow
	queries []store.QueryRow
	fail    bool
}

func (m *memSink) AddUsage(_ context.Context, u []store.UsageRow, q []store.QueryRow) error {
	if m.fail {
		return errors.New("database down")
	}
	m.usage = append(m.usage, u...)
	m.queries = append(m.queries, q...)
	return nil
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func day(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

func TestRecorderFlush(t *testing.T) {
	r := NewRecorder()
	now := time.Date(2026, 10, 11, 23, 59, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	r.Search("", "JOR", "  USB  Hub ", "live")
	r.Search("", "jor", "usb hub", "hit")
	r.Search("", "jor", "usb hub", "quota") // counted, not a top query
	r.Search("client:7", "jor", "usb hub", "hit")
	r.SourceCall("apify", map[string]string{"temu": "ok"}, 40*time.Second, 0.10)
	r.SourceCall("searxng", map[string]string{"amazon": "error", "ebay": "error"}, 2*time.Second, 0.02)
	r.Fallback("searxng>serpapi", "searxng", "serpapi", "error")
	now = now.Add(2 * time.Minute) // next UTC day
	r.Search("", "jor", "usb hub", "live")

	sink := &memSink{fail: true}
	if err := r.Flush(context.Background(), sink); err == nil {
		t.Fatal("flush to a failing sink succeeded")
	}
	sink.fail = false
	if err := r.Flush(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	got := map[string]store.UsageRow{}
	for _, u := range sink.usage {
		got[u.Day.Format(time.DateOnly)+"|"+u.Kind+"|"+u.K1+"|"+u.K2+"|"+u.K3] = u
	}
	if len(got) != 9 {
		t.Fatalf("rows = %+v", got)
	}
	if u := got["2026-10-11|search|jor|live|"]; u.Count != 1 {
		t.Errorf("live search = %+v", u)
	}
	if u := got["2026-10-11|search|jor|hit|"]; u.Count != 2 {
		t.Errorf("hits (user + client) = %+v", u)
	}
	if u := got["2026-10-11|client|client:7|jor|hit"]; u.Count != 1 {
		t.Errorf("client = %+v", u)
	}
	if u := got["2026-10-12|search|jor|live|"]; u.Count != 1 {
		t.Errorf("next day = %+v", u)
	}
	if u := got["2026-10-11|source|apify|temu|ok"]; u.Count != 1 || u.CostUSD != 0.10 || u.TotalMS != 40000 {
		t.Errorf("source = %+v", u)
	}
	if u := got["2026-10-11|source|searxng|ebay|error"]; u.CostUSD != 0.01 {
		t.Errorf("split cost = %+v", u)
	}
	if u := got["2026-10-11|fallback|searxng>serpapi|searxng|serpapi:error"]; u.Count != 1 {
		t.Errorf("fallback = %+v", u)
	}
	queries := map[string]store.QueryRow{}
	for _, q := range sink.queries {
		queries[q.Day.Format(time.DateOnly)+"|"+q.Query] = q
	}
	if q := queries["2026-10-11|usb hub"]; len(queries) != 2 || q.Searches != 3 || q.Live != 1 || q.Country != "jor" {
		t.Errorf("queries = %+v", queries)
	}
	// Flushed: nothing left.
	sink.usage = nil
	_ = r.Flush(context.Background(), sink)
	if len(sink.usage) != 0 {
		t.Error("counts flushed twice")
	}
}

func TestBuildReport(t *testing.T) {
	rows := []store.UsageRow{
		{Day: day("2026-10-10"), Kind: KindSearch, K1: "jor", K2: "hit", Count: 6},
		{Day: day("2026-10-10"), Kind: KindSearch, K1: "jor", K2: "live", Count: 2},
		{Day: day("2026-10-11"), Kind: KindSearch, K1: "usa", K2: "stale", Count: 1},
		{Day: day("2026-10-11"), Kind: KindSearch, K1: "usa", K2: "live", Count: 1},
		{Day: day("2026-10-11"), Kind: KindSearch, K1: "usa", K2: "error", Count: 1},
		{Day: day("2026-10-10"), Kind: KindSource, K1: "apify", K2: "temu", K3: "ok", Count: 2, CostUSD: 0.2, TotalMS: 80000},
		{Day: day("2026-10-11"), Kind: KindSource, K1: "apify", K2: "temu", K3: "error", Count: 2, CostUSD: 0.2, TotalMS: 120000},
		{Day: day("2026-10-11"), Kind: KindSource, K1: "serpapi-amazon", K2: "amazon", K3: "ok", Count: 3, CostUSD: 0.045, TotalMS: 9000},
		{Day: day("2026-10-11"), Kind: KindFallback, K1: "apify>searxng", K2: "apify", K3: "searxng:error", Count: 2},
		{Day: day("2026-10-10"), Kind: KindClient, K1: "client:3", K2: "jor", K3: "live", Count: 1},
		{Day: day("2026-10-11"), Kind: KindClient, K1: "client:3", K2: "usa", K3: "hit", Count: 1},
		{Day: day("2026-10-11"), Kind: KindClient, K1: "client:3", K2: "usa", K3: "quota", Count: 1},
		{Day: day("2026-10-10"), Kind: KindClient, K1: "client:12", K2: "jor", K3: "live", Count: 2},
	}
	queries := []store.QueryRow{{Country: "jor", Query: "usb hub", Searches: 8, Live: 2}}
	b := NewBudget(day("2026-10-01"), 0.445, 5, 80)
	rep := BuildReport(day("2026-10-09"), day("2026-10-11"), rows, queries, b)

	tot := rep.Totals
	if tot.Searches != 11 || tot.Hits != 6 || tot.Stale != 1 || tot.Live != 3 || tot.Errors != 1 || tot.HitRate != 0.7 {
		t.Errorf("totals = %+v", tot)
	}
	if tot.SourceCalls != 7 || tot.CostUSD != 0.445 || tot.Fallbacks != 2 {
		t.Errorf("source totals = %+v", tot)
	}
	if len(rep.Days) != 3 || rep.Days[0].Day != "2026-10-09" || rep.Days[0].Searches != 0 || rep.Days[1].CostUSD != 0.2 {
		t.Errorf("days = %+v", rep.Days)
	}
	if len(rep.Countries) != 2 || rep.Countries[0].Country != "jor" || rep.Countries[0].HitRate != 0.75 {
		t.Errorf("countries = %+v", rep.Countries)
	}
	temu := rep.Sources[0]
	if temu.Provider != "apify" || temu.Calls != 4 || temu.Errors != 2 || temu.ErrorRate != 0.5 || temu.AvgMS != 50000 || temu.CostUSD != 0.4 {
		t.Errorf("sources = %+v", rep.Sources)
	}
	if f := rep.Fallbacks; len(f) != 1 || f[0].To != "searxng" || f[0].Reason != "error" || f[0].Count != 2 {
		t.Errorf("fallbacks = %+v", f)
	}
	// 0.445 cost / 3 live searches = 0.14833 per live search.
	if c := rep.Clients; len(c) != 2 || c[0].ClientID != 3 || c[0].Searches != 3 || c[0].Live != 1 || c[0].Quota != 1 ||
		c[0].HitRate != 0.5 || c[0].CostUSD != 0.1483 || c[0].LastDay != "2026-10-11" ||
		c[1].ClientID != 12 || c[1].CostUSD != 0.2967 || c[1].LastDay != "2026-10-10" {
		t.Errorf("clients = %+v", c)
	}
	if tot.Searches != 11 {
		t.Error("client rows must not change the totals")
	}
	if len(rep.TopQueries) != 1 || rep.TopQueries[0].Searches != 8 || rep.Budget.Status != BudgetOK {
		t.Errorf("queries / budget = %+v %+v", rep.TopQueries, rep.Budget)
	}
}

func TestNewBudget(t *testing.T) {
	m := day("2026-10-01")
	for _, tc := range []struct {
		spent, limit float64
		status       string
		percent      float64
	}{
		{1, 0, BudgetNone, 0},
		{1, 5, BudgetOK, 20},
		{4, 5, BudgetWarning, 80},
		{5, 5, BudgetExceeded, 100},
	} {
		b := NewBudget(m, tc.spent, tc.limit, 80)
		if b.Status != tc.status || b.Percent != tc.percent || b.Month != "2026-10" {
			t.Errorf("NewBudget(%v, %v) = %+v", tc.spent, tc.limit, b)
		}
	}
}

type budgetStore struct {
	spent float64
	audit []store.AuditEntry
}

func (b *budgetStore) UsageCost(context.Context, time.Time, time.Time) (float64, error) {
	return b.spent, nil
}

func (b *budgetStore) HasAudit(_ context.Context, action, target string) (bool, error) {
	for _, e := range b.audit {
		if e.Action == action && e.Target == target {
			return true, nil
		}
	}
	return false, nil
}

func (b *budgetStore) AddAudit(_ context.Context, e store.AuditEntry) error {
	b.audit = append(b.audit, e)
	return nil
}

func TestCheckBudgetAlertsOncePerLevel(t *testing.T) {
	st := &budgetStore{spent: 1}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	check := func() Budget {
		b, err := CheckBudget(context.Background(), st, now, 5, 80, discard)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if b := check(); b.Status != BudgetOK || len(st.audit) != 0 {
		t.Fatalf("below level: %+v, %d alerts", b, len(st.audit))
	}
	st.spent = 4.2
	check()
	check()
	if len(st.audit) != 1 || st.audit[0].Target != "2026-10:80%" || !strings.Contains(string(st.audit[0].Detail), `"spent_usd":4.2`) {
		t.Fatalf("warning alerts = %+v", st.audit)
	}
	st.spent = 5.1
	check()
	if len(st.audit) != 2 || st.audit[1].Target != "2026-10:100%" {
		t.Errorf("exceeded alerts = %+v", st.audit)
	}
	if _, err := CheckBudget(context.Background(), st, now, 0, 80, discard); err != nil || len(st.audit) != 2 {
		t.Error("no budget must not alert")
	}
}
