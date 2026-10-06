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
		"bad pattern":    `{"markets":{"a":{"name":"A","provider":"web","product_pages":{"paths":["("]}}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"}]}}}`,
		"no patterns":    `{"markets":{"a":{"name":"A","provider":"web","product_pages":{"paths":[]}}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"}]}}}`,
		"link no id":     `{"markets":{"a":{"name":"A","provider":"web","product_pages":{"paths":["^/p/"],"link":"https://{host}/p/{id}"}}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"}]}}}`,
		"bad link":       `{"markets":{"a":{"name":"A","provider":"web","product_pages":{"paths":["^/p/(?P<id>[0-9]+)"],"link":"http://evil.com/{id}"}}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"}]}}}`,
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

func TestProductLink(t *testing.T) {
	cat, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	usa, _ := cat.Country("usa")
	ksa, _ := cat.Country("ksa")
	target := func(c Country, market string) Target {
		for _, t := range c.Targets {
			if t.Market == market {
				return t
			}
		}
		t.Fatalf("no %s in %s", market, c.Code)
		return Target{}
	}

	tests := []struct {
		target Target
		link   string
		want   string // "" = not a product page
	}{
		// Amazon: product pages, cleaned; /clp/ points at a product too.
		{target(usa, "amazon"), "https://www.amazon.com/SSK-Enclosure-USB-C/dp/B07MNFH1PX/ref=sr_1_1?keywords=x", "https://www.amazon.com/dp/B07MNFH1PX"},
		{target(usa, "amazon"), "https://www.amazon.com/dp/B07MNFH1PX", "https://www.amazon.com/dp/B07MNFH1PX"},
		{target(usa, "amazon"), "https://www.amazon.com/gp/product/B07MNFH1PX?th=1", "https://www.amazon.com/dp/B07MNFH1PX"},
		{target(usa, "amazon"), "https://www.amazon.com/clp/B07MNFH1PX", "https://www.amazon.com/dp/B07MNFH1PX"},
		{target(ksa, "amazon"), "https://www.amazon.sa/-/en/dp/B0FD38XB93", "https://www.amazon.sa/dp/B0FD38XB93"},
		// Amazon: store, seller, search and home pages are dropped.
		{target(usa, "amazon"), "https://www.amazon.com/stores/SSK/page/3FFC2A12-0613-42C4-9CC7-C0A4025BCD41", ""},
		{target(usa, "amazon"), "https://www.amazon.com/s?i=merchant-items&me=A41S1C1L96T2O", ""},
		{target(usa, "amazon"), "https://www.amazon.com/samsung-s-pen/s?k=samsung+s+pen", ""},
		{target(usa, "amazon"), "https://www.amazon.com/", ""},
		// AliExpress, including language subdomains.
		{target(usa, "aliexpress"), "https://www.aliexpress.com/item/1005008495498271.html?spm=a2g0o", "https://www.aliexpress.com/item/1005008495498271.html"},
		{target(usa, "aliexpress"), "https://ar.aliexpress.com/item/1005011664921405.html", "https://ar.aliexpress.com/item/1005011664921405.html"},
		{target(usa, "aliexpress"), "https://www.aliexpress.com/w/wholesale-ssk-enclosure.html", ""},
		{target(usa, "aliexpress"), "https://www.aliexpress.com/store/1101234567", ""},
		// eBay.
		{target(usa, "ebay"), "https://www.ebay.com/itm/SSK-M-2-Enclosure/256123456789?hash=x", "https://www.ebay.com/itm/256123456789"},
		{target(usa, "ebay"), "https://www.ebay.com/itm/256123456789", "https://www.ebay.com/itm/256123456789"},
		{target(usa, "ebay"), "https://www.ebay.com/sch/i.html?_nkw=ssk", ""},
		{target(usa, "ebay"), "https://www.ebay.com/b/SSD-Enclosures/bn_7116", ""},
		// Temu: product pages keep their path, tracking parameters removed.
		{target(usa, "temu"), "https://www.temu.com/ssk-m2-enclosure-g-601099512345678.html?_x_ads=1", "https://www.temu.com/ssk-m2-enclosure-g-601099512345678.html"},
		{target(usa, "temu"), "https://www.temu.com/sa-en/ssk-enclosure-g-601099512345678.html", "https://www.temu.com/sa-en/ssk-enclosure-g-601099512345678.html"},
		{target(usa, "temu"), "https://www.temu.com/ssd-enclosures-o3-123.html", ""},
		{target(usa, "temu"), "https://www.temu.com/", ""},
		// SHEIN.
		{target(ksa, "shein"), "https://ar.shein.com/Phone-Case-p-12345678-cat-1234.html?src=x", "https://ar.shein.com/Phone-Case-p-12345678-cat-1234.html"},
		{target(ksa, "shein"), "https://ar.shein.com/Phone-Case-p-12345678.html", "https://ar.shein.com/Phone-Case-p-12345678.html"},
		{target(ksa, "shein"), "https://ar.shein.com/Phone-Cases-c-2345.html", ""},
		{target(ksa, "shein"), "https://ar.shein.com/pdsearch/phone%20case/", ""},
		// Other domains and schemes never pass.
		{target(usa, "amazon"), "https://evil.com/dp/B07MNFH1PX", ""},
		{target(usa, "amazon"), "javascript:alert(1)//www.amazon.com/dp/B07MNFH1PX", ""},
	}
	for _, tt := range tests {
		got, ok := tt.target.ProductLink(tt.link)
		if tt.want == "" {
			if ok {
				t.Errorf("%s: accepted as product page (%s)", tt.link, got)
			}
			continue
		}
		if !ok || got != tt.want {
			t.Errorf("%s: got %q, %v; want %q", tt.link, got, ok, tt.want)
		}
	}
}

func TestProductLinkWithoutRuleAcceptsDomain(t *testing.T) {
	tg := Target{Market: "x", Domain: "x.com"}
	if got, ok := tg.ProductLink("https://www.x.com/anything?a=1"); !ok || got != "https://www.x.com/anything?a=1" {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestVersionChangesWithContent(t *testing.T) {
	a, _ := Parse([]byte(`{"markets":{"a":{"name":"A","provider":"web"}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"}]}}}`))
	b, _ := Parse([]byte(`{"markets":{"a":{"name":"A2","provider":"web"}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"}]}}}`))
	if a.Version() == "" || a.Version() == b.Version() {
		t.Errorf("versions %q and %q", a.Version(), b.Version())
	}
	c, _ := a.WithProviders(map[string]string{"a": "apify-a"})
	if c.Version() != a.Version() {
		t.Error("WithProviders lost the version")
	}
}

func TestSearchLink(t *testing.T) {
	cat, _ := Load("")
	jor, _ := cat.Country("jor")
	ksa, _ := cat.Country("ksa")
	get := func(c Country, m string) Target {
		for _, t := range c.Targets {
			if t.Market == m {
				return t
			}
		}
		return Target{}
	}
	title := "Stylus Pen For Samsung S Pen for Samsung Galaxy Tab S6 Lite SM-P620 P625"
	tests := map[string]string{
		get(jor, "aliexpress").SearchLink(title):       "https://www.aliexpress.com/w/wholesale-stylus-pen-for-samsung-s-pen-for-samsung.html",
		get(jor, "amazon").SearchLink("S Pen & case"):  "https://www.amazon.com/s?k=S+Pen+case",
		get(ksa, "amazon").SearchLink("s pen"):         "https://www.amazon.sa/s?k=s+pen",
		get(ksa, "shein").SearchLink("phone case"):     "https://ar.shein.com/pdsearch/phone%20case/",
		get(ksa, "temu").SearchLink("phone case"):      "https://www.temu.com/search_result.html?search_key=phone+case",
		get(ksa, "ebay").SearchLink("phone case"):      "https://www.ebay.com/sch/i.html?_nkw=phone+case",
		Target{Domain: "x.com"}.SearchLink("anything"): "",
	}
	for got, want := range tests {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if _, err := Parse([]byte(`{"markets":{"a":{"name":"A","provider":"web","search_link":"http://evil.com/{query}"}},"countries":{"usa":{"region":"us","markets":[{"market":"a","domain":"a.com"}]}}}`)); err == nil {
		t.Error("bad search_link accepted")
	}
}

func TestTemuGoodsLink(t *testing.T) {
	cat, _ := Load("")
	usa, _ := cat.Country("usa")
	var temu Target
	for _, t := range usa.Targets {
		if t.Market == "temu" {
			temu = t
		}
	}
	tests := map[string]string{
		"https://www.temu.com/goods.html?_bg_fs=1&goods_id=606284493507175&_oak_mp_inf=x": "https://www.temu.com/goods.html?goods_id=606284493507175",
		"https://www.temu.com/ssk-enclosure-g-601099512345678.html?_x=1":                  "https://www.temu.com/ssk-enclosure-g-601099512345678.html",
		"https://www.temu.com/goods.html?goods_id=abc":                                    "",
		"https://www.temu.com/goods.html":                                                 "",
	}
	for in, want := range tests {
		got, ok := temu.ProductLink(in)
		if (want == "") == ok || got != want && want != "" {
			t.Errorf("%s: got %q, %v; want %q", in, got, ok, want)
		}
	}
}
