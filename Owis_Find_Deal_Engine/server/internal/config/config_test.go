package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://example.com/jwks")
	t.Setenv("AUTH_ISSUER", "https://securetoken.google.com/p")
	t.Setenv("AUTH_AUDIENCE", "p")
	t.Setenv("SERPAPI_KEY", "k")
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://a.com, ,https://b.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Plans != "free:20,pro:500" || cfg.DefaultPlan != "free" || cfg.AuthPlanClaim != "plan" {
		t.Errorf("unexpected plan defaults: %+v", cfg)
	}
	if cfg.Port != "3002" || !cfg.SearchCombined || cfg.SearchTimeout != 15*time.Second {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Errorf("CORSOrigins = %v", cfg.CORSOrigins)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "")
	t.Setenv("SERPAPI_KEY", "")
	t.Setenv("SEARCH_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}
