package store

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
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
	if _, err := db.pool.Exec(ctx, `DROP TABLE IF EXISTS settings, settings_history, admins, schema_migrations`); err != nil {
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
	if total != 1 {
		t.Fatalf("migrations applied %d times, want 1", total)
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
