package settings

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"owis_find_deal_engine/internal/config"
	"owis_find_deal_engine/internal/store"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestCipher(t *testing.T) {
	c, err := NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.Encrypt("SERPAPI_KEY", "secret-value")
	b, _ := c.Encrypt("SERPAPI_KEY", "secret-value")
	if a == b || strings.Contains(a, "secret") || !strings.HasPrefix(a, "v1:") {
		t.Fatalf("encrypted values must differ and hide the secret: %q %q", a, b)
	}
	if got, err := c.Decrypt("SERPAPI_KEY", a); err != nil || got != "secret-value" {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}
	// Bound to the setting's name, the key and the exact bytes.
	if _, err := c.Decrypt("APIFY_TOKEN", a); !errors.Is(err, ErrDecrypt) {
		t.Errorf("value moved to another setting: %v", err)
	}
	other, _ := NewCipher(strings.Repeat("ab", 32))
	if _, err := other.Decrypt("SERPAPI_KEY", a); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong key: %v", err)
	}
	// Tampered: one character inside the value always changes (the last
	// ones may only hold padding bits, or already be the replacement).
	i := len(a) - 6
	repl := "A"
	if a[i] == 'A' {
		repl = "B"
	}
	tampered := a[:i] + repl + a[i+1:]
	for _, bad := range []string{"", "plain", "v1:!!", "v1:AAAA", tampered} {
		if _, err := c.Decrypt("SERPAPI_KEY", bad); !errors.Is(err, ErrDecrypt) {
			t.Errorf("Decrypt(%q): %v", bad, err)
		}
	}
	// base64 keys (openssl rand -base64 32) work too.
	if _, err := NewCipher("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="); err != nil {
		t.Errorf("base64 key: %v", err)
	}
	for _, bad := range []string{"", "short", strings.Repeat("ab", 16), strings.Repeat("zz", 32)} {
		if _, err := NewCipher(bad); err == nil {
			t.Errorf("NewCipher(%q) accepted", bad)
		}
	}
}

// memStore is an in-memory Store.
type memStore struct {
	mu      sync.Mutex
	rows    map[string]store.Setting
	history []store.Change
}

func newMemStore() *memStore { return &memStore{rows: map[string]store.Setting{}} }

func (m *memStore) Settings(context.Context) ([]store.Setting, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Setting
	for _, s := range m.rows {
		out = append(out, s)
	}
	return out, int64(len(m.history)), nil
}

func (m *memStore) SettingsVersion(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.history)), nil
}

func (m *memStore) SetSettings(_ context.Context, by string, ups []store.Update) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range ups {
		if u.Value == nil {
			delete(m.rows, u.Key)
		} else {
			m.rows[u.Key] = store.Setting{Key: u.Key, Value: *u.Value, Secret: u.Secret, UpdatedBy: by}
		}
		m.history = append(m.history, store.Change{ID: int64(len(m.history) + 1), Key: u.Key, ChangedBy: by})
	}
	return int64(len(m.history)), nil
}

func bootstrapEnv(k string) (string, bool) {
	v, ok := map[string]string{
		"AUTH_JWKS_URL": "https://example.com/jwks", "AUTH_ISSUER": "i", "AUTH_AUDIENCE": "a",
		"SEARXNG_URL": "http://searxng:8080",
		// Runtime keys in the environment are ignored once there is a database.
		"SEARCH_PROVIDERS": "serpapi",
	}[k]
	return v, ok
}

func ptr(s string) *string { return &s }

func newManager(t *testing.T) (*Manager, *memStore) {
	t.Helper()
	c, err := NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	st := newMemStore()
	return NewManager(st, c, bootstrapEnv), st
}

func TestManagerSetValidatesAndEncrypts(t *testing.T) {
	m, st := newManager(t)
	ctx := context.Background()

	// Invalid as a whole: serpapi without a key. Nothing is stored.
	if _, _, err := m.Set(ctx, "t", map[string]*string{"SEARCH_PROVIDERS": ptr("serpapi")}); err == nil {
		t.Fatal("invalid setup accepted")
	}
	if _, _, err := m.Set(ctx, "t", map[string]*string{"PORT": ptr("1"), "NOPE": ptr("x")}); err == nil ||
		!strings.Contains(err.Error(), "PORT") || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("bootstrap / unknown keys: %v", err)
	}
	if v, _ := st.SettingsVersion(ctx); v != 0 {
		t.Fatalf("refused changes were stored: version %d", v)
	}

	v, changed, err := m.Set(ctx, "t", map[string]*string{
		"SEARCH_PROVIDERS": ptr("serpapi,searxng"), "SERPAPI_KEY": ptr("sk-123"), "CACHE_FRESH_TTL": ptr("24h"),
	})
	if err != nil || v != 3 || strings.Join(changed, ",") != "CACHE_FRESH_TTL,SEARCH_PROVIDERS,SERPAPI_KEY" {
		t.Fatalf("Set = %d %v %v", v, changed, err)
	}
	if row := st.rows["SERPAPI_KEY"]; !row.Secret || strings.Contains(row.Value, "sk-123") {
		t.Fatalf("secret stored in clear: %+v", row)
	}
	cfg, version, err := m.Config(ctx)
	if err != nil || version != 3 || cfg.SerpAPIKey != "sk-123" || cfg.CacheFreshTTL != 24*time.Hour ||
		cfg.SearchProviders[0] != "serpapi" || cfg.SearXNGURL != "http://searxng:8080" {
		t.Fatalf("Config = %+v %d %v", cfg, version, err)
	}

	// Same values: nothing written. Secret compared after decryption.
	if v, changed, err := m.Set(ctx, "t", map[string]*string{"SERPAPI_KEY": ptr("sk-123"), "APIFY_MARKETS": nil}); err != nil || v != 3 || changed != nil {
		t.Fatalf("no-op Set = %d %v %v", v, changed, err)
	}
	// Removing the key while serpapi is still used is refused.
	if _, _, err := m.Set(ctx, "t", map[string]*string{"SERPAPI_KEY": nil}); err == nil {
		t.Fatal("removing a needed secret accepted")
	}
	if _, changed, err := m.Set(ctx, "t", map[string]*string{"CACHE_FRESH_TTL": nil}); err != nil || len(changed) != 1 {
		t.Fatalf("unset: %v %v", changed, err)
	}
	if cfg, _, _ := m.Config(ctx); cfg.CacheFreshTTL != 48*time.Hour {
		t.Errorf("unset did not restore the default: %v", cfg.CacheFreshTTL)
	}
}

func TestImportEnvOnce(t *testing.T) {
	m, _ := newManager(t)
	ctx := context.Background()
	environ := []string{"PORT=3002", "SEARCH_PROVIDERS=searxng", "SERPAPI_KEY=sk", "SERPAPI_MARKETS=", "APIFY_TEMU_ACTOR=crw/temu"}
	keys, err := m.ImportEnv(ctx, environ)
	if err != nil || strings.Join(keys, ",") != "APIFY_TEMU_ACTOR,SEARCH_PROVIDERS,SERPAPI_KEY,SERPAPI_MARKETS" {
		t.Fatalf("ImportEnv = %v, %v", keys, err)
	}
	values, _, _ := m.Values(ctx)
	if values["SERPAPI_KEY"] != "sk" || values["SERPAPI_MARKETS"] != "" {
		t.Fatalf("values = %v", values)
	}
	if ignored := IgnoredEnv(values, environ); len(ignored) != 0 {
		t.Errorf("IgnoredEnv right after import = %v", ignored)
	}
	// Second start: nothing imported again, changed env values are reported.
	if keys, err := m.ImportEnv(ctx, []string{"SEARCH_PROVIDERS=serpapi"}); err != nil || keys != nil {
		t.Fatalf("second ImportEnv = %v, %v", keys, err)
	}
	if ignored := IgnoredEnv(values, []string{"SEARCH_PROVIDERS=serpapi", "PLANS=x:1", "PORT=1"}); strings.Join(ignored, ",") != "PLANS,SEARCH_PROVIDERS" {
		t.Errorf("IgnoredEnv = %v", ignored)
	}
}

func TestImportEnvInvalid(t *testing.T) {
	m, st := newManager(t)
	if _, err := m.ImportEnv(context.Background(), []string{"SEARCH_PROVIDERS=serpapi"}); err == nil {
		t.Fatal("invalid environment imported")
	}
	if len(st.history) != 0 {
		t.Fatal("partial import stored")
	}
}

func TestWatchAppliesChanges(t *testing.T) {
	m, st := newManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, err := m.Set(ctx, "t", map[string]*string{"SEARCH_PROVIDERS": ptr("searxng")}); err != nil {
		t.Fatal(err)
	}

	applied := make(chan config.Config, 4)
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&syncWriter{w: &logs}, nil))
	done := make(chan struct{})
	go func() {
		m.Watch(ctx, 5*time.Millisecond, 1, func(c config.Config, _ int64) error {
			applied <- c
			return nil
		}, log)
		close(done)
	}()

	if _, _, err := m.Set(ctx, "t", map[string]*string{"RESULTS_PER_MARKET": ptr("7")}); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-applied:
		if c.ResultsPerMarket != 7 {
			t.Fatalf("applied ResultsPerMarket = %d", c.ResultsPerMarket)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("change not applied")
	}

	// A change stored behind the manager's back that does not validate is
	// logged once and not applied.
	st.mu.Lock()
	st.rows["RESULTS_PER_MARKET"] = store.Setting{Key: "RESULTS_PER_MARKET", Value: "zero"}
	st.history = append(st.history, store.Change{})
	st.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	select {
	case c := <-applied:
		t.Fatalf("invalid settings applied: %+v", c)
	default:
	}
	cancel()
	<-done
	if n := strings.Count(logs.String(), "cannot be applied"); n != 1 {
		t.Errorf("logged %d times, want once:\n%s", n, logs.String())
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
