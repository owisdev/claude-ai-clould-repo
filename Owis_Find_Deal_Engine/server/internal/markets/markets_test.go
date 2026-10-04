package markets

import (
	"strings"
	"testing"
)

func marketIDs(c Country) []string {
	ids := make([]string, len(c.Targets))
	for i, t := range c.Targets {
		ids[i] = t.Market
	}
	return ids
}

func TestDefaultCatalog(t *testing.T) {
	cat, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		code string
		want string
	}{
		{"usa", "amazon,ebay,aliexpress,temu,shein"},
		{"ksa", "amazon,ebay,aliexpress,temu,shein"},
		{"jor", "amazon,aliexpress,temu,shein"}, // eBay does not deliver to Jordan
	}
	for _, tt := range tests {
		c, ok := cat.Country(tt.code)
		if !ok {
			t.Fatalf("country %q missing", tt.code)
		}
		if got := strings.Join(marketIDs(c), ","); got != tt.want {
			t.Errorf("%s markets = %s, want %s", tt.code, got, tt.want)
		}
	}

	ksa, _ := cat.Country("KSA")
	if ksa.Targets[0].Domain != "amazon.sa" {
		t.Errorf("ksa amazon domain = %q, want amazon.sa", ksa.Targets[0].Domain)
	}
	if got := len(cat.Countries()); got != 3 {
		t.Errorf("Countries() len = %d, want 3", got)
	}
	if _, ok := cat.Country("fra"); ok {
		t.Error("unexpected country fra")
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	tests := map[string]string{
		"unknown market": `{"markets":{},"countries":{"usa":{"region":"us","markets":[{"market":"x","domain":"x.com"}]}}}`,
		"no countries":   `{"markets":{}}`,
		"no region":      `{"markets":{"a":{"name":"A","provider":"web"}},"countries":{"usa":{"markets":[{"market":"a","domain":"a.com"}]}}}`,
		"duplicate":      `{"markets":{"a":{"name":"A","provider":"web"}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"},{"market":"a","domain":"a.com"}]}}}`,
		"bad json":       `{`,
	}
	for name, input := range tests {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestTargetMatches(t *testing.T) {
	shein := Target{Domain: "ar.shein.com"}
	amazon := Target{Domain: "amazon.sa"}

	tests := []struct {
		target Target
		host   string
		want   bool
	}{
		{shein, "ar.shein.com", true},
		{shein, "sa.shein.com", true},
		{shein, "shein.com", true},
		{shein, "notshein.com", false},
		{amazon, "www.amazon.sa", true},
		{amazon, "amazon.com", false},
	}
	for _, tt := range tests {
		if got := tt.target.Matches(tt.host); got != tt.want {
			t.Errorf("%s.Matches(%q) = %v, want %v", tt.target.Domain, tt.host, got, tt.want)
		}
	}
}

func TestSiteQueryAndMatch(t *testing.T) {
	targets := []Target{{Market: "amazon", Domain: "amazon.com"}, {Market: "shein", Domain: "ar.shein.com"}}
	if got := SiteQuery("hub", targets[:1]); got != "hub site:amazon.com" {
		t.Errorf("single = %q", got)
	}
	if got := SiteQuery("hub", targets); got != "hub (site:amazon.com OR site:ar.shein.com)" {
		t.Errorf("multi = %q", got)
	}
	if m, ok := MatchTarget("sa.shein.com", targets); !ok || m.Market != "shein" {
		t.Errorf("MatchTarget = %v, %v", m, ok)
	}
	if _, ok := MatchTarget("example.com", targets); ok {
		t.Error("unexpected match")
	}
}

func TestWithProviders(t *testing.T) {
	cat, _ := Load("")
	over, err := cat.WithProviders(map[string]string{"temu": "apify-temu"})
	if err != nil {
		t.Fatal(err)
	}
	jor, _ := over.Country("jor")
	for _, tg := range jor.Targets {
		want := "web"
		if tg.Market == "temu" {
			want = "apify-temu"
		}
		if tg.Provider != want {
			t.Errorf("%s provider = %q, want %q", tg.Market, tg.Provider, want)
		}
	}
	orig, _ := cat.Country("jor")
	for _, tg := range orig.Targets {
		if tg.Provider != "web" {
			t.Error("original catalog was modified")
		}
	}
	if _, err := cat.WithProviders(map[string]string{"walmart": "x"}); err == nil {
		t.Error("unknown marketplace accepted")
	}
}
