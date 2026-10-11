package config

import (
	"slices"
	"testing"
)

func TestRuntimeKeys(t *testing.T) {
	for _, k := range []string{"SEARCH_PROVIDERS", "SERPAPI_KEY", "APIFY_TEMU_INPUT", "APIFY_SHEIN_MAX_ITEMS",
		"APIFY_ALIEXPRESS_CURRENCY", "APIFY_MAX_ITEMS", "CACHE_FRESH_TTL", "CORS_ALLOWED_ORIGINS"} {
		if !Runtime(k) {
			t.Errorf("Runtime(%s) = false", k)
		}
	}
	for _, k := range []string{"PORT", "DATABASE_URL", "SETTINGS_ENCRYPTION_KEY", "AUTH_ISSUER", "REDIS_URL",
		"SEARXNG_URL", "APIFY_BASE_URL", "CACHE_MAX_ENTRIES", "APIFY__INPUT", "APIFY_TEMU_OTHER", "apify_temu_input"} {
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
	if !slices.Contains(RuntimeKeys(), "PLANS") {
		t.Error("RuntimeKeys misses PLANS")
	}
}
