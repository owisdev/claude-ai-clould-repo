package config

import (
	"slices"
	"strings"
	"testing"
)

func TestRuntimeKeys(t *testing.T) {
	for _, k := range []string{"SEARCH_PROVIDERS", "SERPAPI_KEY", "APIFY_TEMU_INPUT", "APIFY_SHEIN_MAX_ITEMS",
		"APIFY_ALIEXPRESS_CURRENCY", "APIFY_MAX_ITEMS", "APIFY_TEMU_RUN_COST_USD", "MARKETS", "CACHE_GENERATION", "SERPAPI_SEARCH_COST_USD", "CACHE_FRESH_TTL", "CORS_ALLOWED_ORIGINS"} {
		if !Runtime(k) {
			t.Errorf("Runtime(%s) = false", k)
		}
	}
	for _, k := range []string{"PORT", "DATABASE_URL", "SETTINGS_ENCRYPTION_KEY", "AUTH_ISSUER", "REDIS_URL",
		"SEARXNG_URL", "APIFY_BASE_URL", "CACHE_MAX_ENTRIES", "MARKETS_FILE", "ADMIN_REQUIRE_2FA", "APIFY__INPUT", "APIFY_TEMU_OTHER", "apify_temu_input"} {
		if Runtime(k) {
			t.Errorf("Runtime(%s) = true", k)
		}
	}
	if !Secret("SERPAPI_KEY") || !Secret("APIFY_TOKEN") || Secret("SERPAPI_ENGINE") {
		t.Error("Secret: wrong keys")
	}
}

func TestRuntimeFromEnvAndLayered(t *testing.T) {
	got := RuntimeFromEnv([]string{"PORT=1", "SERPAPI_MARKETS=", "APIFY_TOKEN=", "APIFY_TEMU_INPUT={\"a\":\"b=c\"}", "PLANS=free:1", "junk"})
	want := map[string]string{"SERPAPI_MARKETS": "", "APIFY_TEMU_INPUT": `{"a":"b=c"}`, "PLANS": "free:1"}
	if len(got) != len(want) {
		t.Fatalf("RuntimeFromEnv = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	env := func(k string) (string, bool) { return "env-" + k, true }
	look := Layered(map[string]string{"PLANS": "db"}, env)
	if v, ok := look("PLANS"); !ok || v != "db" {
		t.Errorf("runtime key from db: %q %v", v, ok)
	}
	if _, ok := look("SEARCH_PROVIDERS"); ok {
		t.Error("runtime key missing from db must not fall back to the environment")
	}
	if v, _ := look("PORT"); v != "env-PORT" {
		t.Errorf("bootstrap key from env: %q", v)
	}
}

func TestLoadFromSettingsBootstrap(t *testing.T) {
	base := map[string]string{
		"AUTH_JWKS_URL": "https://example.com/jwks", "AUTH_ISSUER": "i", "AUTH_AUDIENCE": "a",
		"SEARCH_PROVIDERS": "searxng", "SEARXNG_URL": "http://s",
	}
	load := func(extra map[string]string) error {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		_, err := LoadFrom(func(k string) (string, bool) { v, ok := m[k]; return v, ok })
		return err
	}
	if err := load(nil); err != nil {
		t.Fatalf("no database: %v", err)
	}
	if err := load(map[string]string{"DATABASE_URL": "postgres://x"}); err == nil {
		t.Error("DATABASE_URL without SETTINGS_ENCRYPTION_KEY accepted")
	}
	if err := load(map[string]string{"DATABASE_URL": "postgres://x", "SETTINGS_ENCRYPTION_KEY": "k", "ADMIN_EMAIL": " Owner@Example.com "}); err != nil {
		t.Errorf("valid database settings: %v", err)
	}
	for _, bad := range []map[string]string{{"SETTINGS_POLL_INTERVAL": "100ms"}, {"ADMIN_EMAIL": "owner"}} {
		if err := load(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	for _, bad := range []map[string]string{{"ADMIN_SESSION_MAX_AGE": "1m"}, {"ADMIN_REQUIRE_2FA": "maybe"}} {
		if err := load(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if !slices.Contains(RuntimeKeys(), "PLANS") {
		t.Error("RuntimeKeys misses PLANS")
	}
}

func TestLoadMarketsSetting(t *testing.T) {
	base := map[string]string{
		"AUTH_JWKS_URL": "https://example.com/jwks", "AUTH_ISSUER": "i", "AUTH_AUDIENCE": "a",
		"SEARCH_PROVIDERS": "searxng", "SEARXNG_URL": "http://s",
	}
	load := func(extra map[string]string) (Config, error) {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return LoadFrom(func(k string) (string, bool) { v, ok := m[k]; return v, ok })
	}
	cfg, err := load(nil)
	if err != nil || cfg.MarketsSource != "built-in" || cfg.Catalog == nil || len(cfg.MarketsJSON) == 0 || cfg.AdminRequire2FA {
		t.Fatalf("default = %v %q %v", err, cfg.MarketsSource, cfg.AdminRequire2FA)
	}
	one := `{"markets":{"amazon":{"name":"Amazon","provider":"web"}},
		"countries":{"usa":{"name":"USA","currency":"USD","region":"us","markets":[{"market":"amazon","domain":"amazon.com"}]}}}`
	cfg, err = load(map[string]string{"MARKETS": one})
	if err != nil || cfg.MarketsSource != "setting" || len(cfg.Catalog.Countries()) != 1 {
		t.Fatalf("setting = %v %q", err, cfg.MarketsSource)
	}
	if _, err := load(map[string]string{"MARKETS": `{"countries":{}}`}); err == nil || !strings.Contains(err.Error(), "MARKETS") {
		t.Errorf("invalid MARKETS: %v", err)
	}
	// A dedicated source for a shop the catalog does not have.
	_, err = load(map[string]string{"MARKETS": one, "APIFY_MARKETS": "temu", "APIFY_TOKEN": "t",
		"APIFY_TEMU_ACTOR": "a/b", "APIFY_TEMU_INPUT": `{"q":"{{query}}"}`, "APIFY_TEMU_RUN_COST_USD": "0.1"})
	if err == nil || !strings.Contains(err.Error(), "temu is in APIFY_MARKETS") {
		t.Errorf("source for unknown shop: %v", err)
	}
	cfg, err = load(map[string]string{"APIFY_MARKETS": "temu", "APIFY_TOKEN": "t", "APIFY_TEMU_ACTOR": "a/b",
		"APIFY_TEMU_INPUT": `{"q":"{{query}}"}`, "APIFY_TEMU_RUN_COST_USD": "0.1", "CACHE_GENERATION": "3"})
	if err != nil || cfg.ApifyRunCostUSD["temu"] != 0.1 || cfg.CacheGeneration != 3 {
		t.Errorf("costs = %v %v %d", err, cfg.ApifyRunCostUSD, cfg.CacheGeneration)
	}
}
