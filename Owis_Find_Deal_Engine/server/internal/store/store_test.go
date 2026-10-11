package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// openTest connects to TEST_DATABASE_URL (an empty, throwaway database;
// its tables are dropped) or skips the test.
func openTest(t *testing.T) *DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.pool.Exec(ctx, `DROP TABLE IF EXISTS settings, settings_history, admins, audit_log, usage_daily, query_daily, schema_migrations`); err != nil {
		t.Fatal(err)
	}
	return db
}

func ptr(s string) *string { return &s }

func TestOpenInvalidURLHidesIt(t *testing.T) {
	_, err := Open(context.Background(), "postgres://user:hunter2@host:notaport/db")
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("Open error leaks or missing: %v", err)
	}
}

func TestMigrateConcurrentAndIdempotent(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for range 3 {
		wg.Go(func() {
			n, err := db.Migrate(ctx)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += n
			mu.Unlock()
		})
	}
	wg.Wait()
	if total != 3 {
		t.Fatalf("migrations applied %d times, want 3 (once each)", total)
	}
}

func TestSettingsAndHistory(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if v, err := db.SettingsVersion(ctx); err != nil || v != 0 {
		t.Fatalf("empty version = %d, %v", v, err)
	}

	v, err := db.SetSettings(ctx, "alice", []Update{
		{Key: "PLANS", Value: ptr("free:20")},
		{Key: "SERPAPI_KEY", Value: ptr("v1:sealed"), Secret: true},
		{Key: "CACHE_FRESH_TTL", Value: nil}, // not set: no-op
	})
	if err != nil || v != 2 {
		t.Fatalf("SetSettings = %d, %v", v, err)
	}
	v, err = db.SetSettings(ctx, "bob", []Update{
		{Key: "PLANS", Value: ptr("free:30")},
		{Key: "SERPAPI_KEY", Value: nil},
	})
	if err != nil || v != 4 {
		t.Fatalf("second SetSettings = %d, %v", v, err)
	}

	rows, version, err := db.Settings(ctx)
	if err != nil || version != 4 || len(rows) != 1 || rows[0].Key != "PLANS" || rows[0].Value != "free:30" || rows[0].UpdatedBy != "bob" {
		t.Fatalf("Settings = %+v %d %v", rows, version, err)
	}

	hist, err := db.SettingsHistory(ctx, "", 10)
	if err != nil || len(hist) != 4 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	// Newest first: SERPAPI_KEY removed (secret: no values), PLANS 20 -> 30.
	if h := hist[0]; h.Key != "SERPAPI_KEY" || !h.Secret || h.Old != nil || h.New != nil || h.ChangedBy != "bob" {
		t.Errorf("secret history row = %+v", h)
	}
	if h := hist[1]; h.Key != "PLANS" || *h.Old != "free:20" || *h.New != "free:30" {
		t.Errorf("history row = %+v", h)
	}
	if h := hist[3]; h.Key != "PLANS" || h.Old != nil || *h.New != "free:20" || h.ChangedBy != "alice" {
		t.Errorf("first history row = %+v", h)
	}
	if one, _ := db.SettingsHistory(ctx, "PLANS", 0); len(one) != 2 {
		t.Errorf("history of PLANS = %d rows", len(one))
	}
	if _, err := db.SetSettings(ctx, "", nil); err == nil {
		t.Error("change without author accepted")
	}
	if _, err := db.SetSettings(ctx, "x", []Update{{Key: "bad key", Value: ptr("1")}}); err == nil {
		t.Error("invalid key accepted")
	}
	if v, _ := db.SettingsVersion(ctx); v != 4 {
		t.Errorf("failed change moved the version to %d", v)
	}
}

func TestEnsureFirstAdmin(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if added, err := db.EnsureFirstAdmin(ctx, " Owner@Example.com "); err != nil || !added {
		t.Fatalf("first = %v, %v", added, err)
	}
	if added, err := db.EnsureFirstAdmin(ctx, "other@example.com"); err != nil || added {
		t.Fatalf("second = %v, %v", added, err)
	}
	list, err := db.Admins(ctx)
	if err != nil || len(list) != 1 || list[0].Email != "owner@example.com" || list[0].DisabledAt != nil {
		t.Fatalf("Admins = %+v, %v", list, err)
	}
}

func TestAdminLoginAndList(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureFirstAdmin(ctx, "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	// Unverified email: no link. Unknown email: not an admin.
	if _, err := db.AdminForLogin(ctx, "uid-1", "owner@example.com", false); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("unverified email: %v", err)
	}
	if _, err := db.AdminForLogin(ctx, "uid-9", "nobody@example.com", true); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("unknown email: %v", err)
	}
	// First login links the uid; afterwards the uid alone counts.
	a, err := db.AdminForLogin(ctx, "uid-1", "Owner@Example.com", true)
	if err != nil || a.FirebaseUID == nil || *a.FirebaseUID != "uid-1" {
		t.Fatalf("first login = %+v, %v", a, err)
	}
	if a, err := db.AdminForLogin(ctx, "uid-1", "changed@example.com", false); err != nil || a.Email != "owner@example.com" {
		t.Errorf("login by uid = %+v, %v", a, err)
	}
	// Same email, other login (account created again): refused until unlinked.
	if _, err := db.AdminForLogin(ctx, "uid-2", "owner@example.com", true); !errors.Is(err, ErrAdminLinked) {
		t.Errorf("other uid: %v", err)
	}
	if err := db.UnlinkAdmin(ctx, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if a, err := db.AdminForLogin(ctx, "uid-2", "owner@example.com", true); err != nil || *a.FirebaseUID != "uid-2" {
		t.Errorf("after unlink = %+v, %v", a, err)
	}

	if _, err := db.AddAdmin(ctx, " Second@Example.com", "admin:owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddAdmin(ctx, "second@example.com", "x"); !errors.Is(err, ErrAdminExists) {
		t.Errorf("duplicate add: %v", err)
	}
	if err := db.SetAdminDisabled(ctx, "second@example.com", true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAdminDisabled(ctx, "owner@example.com", true); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("disable last active admin: %v", err)
	}
	if err := db.SetAdminDisabled(ctx, "ghost@example.com", false); !errors.Is(err, ErrAdminNotFound) {
		t.Errorf("enable unknown: %v", err)
	}
	list, err := db.Admins(ctx)
	if err != nil || len(list) != 2 || list[1].DisabledAt == nil || list[1].CreatedBy != "admin:owner@example.com" {
		t.Fatalf("Admins = %+v, %v", list, err)
	}
	if err := db.SetAdminDisabled(ctx, "second@example.com", false); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.Admins(ctx); list[1].DisabledAt != nil {
		t.Error("enable did not clear disabled_at")
	}
}

func TestAuditLog(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, e := range []AuditEntry{
		{Actor: "admin:a@x.y", Action: "settings.change", Target: "PLANS", Detail: json.RawMessage(`{"keys":["PLANS"]}`)},
		{Actor: "admin:b@x.y", Action: "admins.add", Target: "c@x.y", RequestID: "r1", RemoteIP: "10.0.0.1"},
		{Actor: "user:u1", Action: "login", Outcome: OutcomeDenied},
	} {
		if err := db.AddAudit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AddAudit(ctx, AuditEntry{Action: "x"}); err == nil {
		t.Error("entry without actor accepted")
	}

	all, err := db.AuditLog(ctx, AuditFilter{})
	if err != nil || len(all) != 3 || all[0].Action != "login" || all[0].Outcome != OutcomeDenied || string(all[2].Detail) != `{"keys": ["PLANS"]}` {
		t.Fatalf("AuditLog = %+v, %v", all, err)
	}
	if got, _ := db.AuditLog(ctx, AuditFilter{Action: "settings."}); len(got) != 1 || got[0].Target != "PLANS" {
		t.Errorf("by action = %+v", got)
	}
	if got, _ := db.AuditLog(ctx, AuditFilter{Actor: "admin:b@x.y"}); len(got) != 1 || got[0].RemoteIP != "10.0.0.1" {
		t.Errorf("by actor = %+v", got)
	}
	if got, _ := db.AuditLog(ctx, AuditFilter{BeforeID: all[0].ID, Limit: 1}); len(got) != 1 || got[0].ID != all[1].ID {
		t.Errorf("paging = %+v", got)
	}
}

func TestUsage(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	d := func(s string) time.Time { v, _ := time.Parse(time.DateOnly, s); return v }
	add := []UsageRow{
		{Day: d("2026-10-10"), Kind: "source", K1: "apify", K2: "temu", K3: "ok", Count: 1, CostUSD: 0.1, TotalMS: 40000},
		{Day: d("2026-10-11"), Kind: "search", K1: "jor", K2: "live", Count: 2},
	}
	q := []QueryRow{{Day: d("2026-10-11"), Country: "jor", Query: "usb hub", Searches: 2, Live: 1}}
	// Two instances flushing the same counters add up.
	for range 2 {
		if err := db.AddUsage(ctx, add, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AddUsage(ctx, nil, []QueryRow{{Day: d("2026-10-11"), Country: "usa", Query: "ssd", Searches: 1}}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.UsageRows(ctx, d("2026-10-10"), d("2026-10-11"))
	if err != nil || len(rows) != 2 || rows[0].Count != 2 || rows[0].CostUSD != 0.2 || rows[0].TotalMS != 80000 || !rows[0].Day.Equal(d("2026-10-10")) {
		t.Fatalf("UsageRows = %+v, %v", rows, err)
	}
	if rows, _ := db.UsageRows(ctx, d("2026-10-11"), d("2026-10-11")); len(rows) != 1 {
		t.Errorf("one day = %+v", rows)
	}
	top, err := db.TopQueries(ctx, d("2026-10-01"), d("2026-10-31"), 10)
	if err != nil || len(top) != 2 || top[0].Query != "usb hub" || top[0].Searches != 4 || top[0].Live != 2 {
		t.Fatalf("TopQueries = %+v, %v", top, err)
	}
	if c, err := db.UsageCost(ctx, d("2026-10-01"), d("2026-10-31")); err != nil || c < 0.199 || c > 0.201 {
		t.Errorf("UsageCost = %v, %v", c, err)
	}
	if err := db.PruneUsage(ctx, d("2026-10-11"), d("2026-10-12")); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.UsageRows(ctx, d("2026-01-01"), d("2026-12-31")); len(rows) != 1 {
		t.Errorf("after prune = %+v", rows)
	}
	if top, _ := db.TopQueries(ctx, d("2026-01-01"), d("2026-12-31"), 10); len(top) != 0 {
		t.Errorf("queries after prune = %+v", top)
	}

	if ok, err := db.HasAudit(ctx, "budget.alert", "2026-10:80%"); ok || err != nil {
		t.Fatalf("HasAudit = %v, %v", ok, err)
	}
	_ = db.AddAudit(ctx, AuditEntry{Actor: "system", Action: "budget.alert", Target: "2026-10:80%"})
	if ok, _ := db.HasAudit(ctx, "budget.alert", "2026-10:80%"); !ok {
		t.Error("HasAudit after AddAudit = false")
	}
}
