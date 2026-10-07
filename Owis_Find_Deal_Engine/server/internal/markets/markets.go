// Package markets holds the catalog of supported countries and the online
// marketplaces that deliver to each of them.
package markets

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

//go:embed markets.json
var defaultCatalog []byte

// Target is one marketplace as searched for one country.
type Target struct {
	Market   string `json:"id"`
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Provider string `json:"-"`

	products *productRule // nil: every page of the domain is accepted
	search   string       // shop search link template, e.g. "https://{host}/s?k={query}"
}

// SearchLink returns a link to the marketplace's own search for text,
// or "" when the marketplace has no search_link. {host} is the domain
// (with "www." for a bare domain), {query} the URL-encoded text (for the
// query string), {query_path} the same encoded for a path and {slug}
// its lowercase words joined by "-". Long titles are cut to their first
// words so the shop's search still finds the product.
func (t Target) SearchLink(text string) string {
	if t.search == "" {
		return ""
	}
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) > 8 {
		words = words[:8]
	}
	if len(words) == 0 {
		return ""
	}
	slugWords := make([]string, len(words))
	for i, w := range words {
		slugWords[i] = url.PathEscape(strings.ToLower(w))
	}
	host := t.Domain
	if strings.Count(host, ".") == 1 {
		host = "www." + host
	}
	return strings.NewReplacer(
		"{host}", host,
		"{query}", url.QueryEscape(strings.Join(words, " ")),
		"{query_path}", url.PathEscape(strings.Join(words, " ")),
		"{slug}", strings.Join(slugWords, "-"),
	).Replace(t.search)
}

// productRule recognizes a marketplace's product pages by their path and
// optionally rebuilds a clean link from the product id.
type productRule struct {
	paths   []*regexp.Regexp
	link    string // e.g. "https://{host}/dp/{id}"; empty keeps the path
	idParam string // query parameter holding the id, e.g. "goods_id"
}

// ProductLink reports whether link is a product page of this marketplace
// (not a search, store, category or home page) and returns a clean link:
// rebuilt from the product id when the marketplace defines a link
// template, otherwise the original without query string and fragment
// (tracking parameters).
func (t Target) ProductLink(link string) (string, bool) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !t.Matches(u.Hostname()) {
		return "", false
	}
	if t.products == nil {
		return link, true
	}
	host := strings.ToLower(u.Hostname())
	for _, re := range t.products.paths {
		m := re.FindStringSubmatch(u.Path)
		if m == nil {
			continue
		}
		id, fromQuery := "", false
		if i := re.SubexpIndex("id"); i > 0 {
			id = m[i]
		} else if t.products.idParam != "" {
			id = u.Query().Get(t.products.idParam)
			if !isDigits(id) {
				continue // e.g. goods.html without a numeric goods_id
			}
			fromQuery = true
		}
		if t.products.link != "" && id != "" {
			return strings.NewReplacer("{host}", host, "{id}", id).Replace(t.products.link), true
		}
		if fromQuery {
			// Keep only the id parameter (drops tracking parameters).
			return "https://" + host + u.EscapedPath() + "?" + t.products.idParam + "=" + id, true
		}
		return "https://" + host + u.EscapedPath(), true
	}
	return "", false
}

// GenericTitle reports whether a result title is only the shop's name or
// domain (e.g. "Temu"), which says nothing about the product.
func (t Target) GenericTitle(title string) bool {
	title = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(title)), "www.")
	return title == "" || title == strings.ToLower(t.Name) || title == t.Domain || title == baseDomain(t.Domain)
}

// Matches reports whether a link host belongs to this target's marketplace.
// Regional subdomains count as a match (e.g. sa.shein.com for ar.shein.com).
func (t Target) Matches(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	if host == t.Domain || strings.HasSuffix(host, "."+t.Domain) {
		return true
	}
	base := baseDomain(t.Domain)
	return host == base || strings.HasSuffix(host, "."+base)
}

// baseDomain returns the last two labels of a domain (us.shein.com -> shein.com).
func baseDomain(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) <= 2 {
		return domain
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// Country is a supported country with the marketplaces that deliver to it.
type Country struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
	Region   string `json:"-"`
	Language string `json:"-"`
	// ShoppingRegion is the Google Shopping country to use when Google
	// Shopping does not cover Region (e.g. Jordan): shops there sell
	// internationally, so another country's listings apply.
	ShoppingRegion string   `json:"-"`
	Targets        []Target `json:"markets"`
}

// Catalog is the immutable set of countries; safe for concurrent use.
type Catalog struct {
	countries map[string]Country
	codes     []string
	version   string
}

// Version identifies the catalog's content; it changes whenever the
// markets file changes (used in cache keys).
func (c *Catalog) Version() string { return c.version }

// Country returns the country for a code such as "jor". Codes are case-insensitive.
func (c *Catalog) Country(code string) (Country, bool) {
	country, ok := c.countries[strings.ToLower(strings.TrimSpace(code))]
	return country, ok
}

// Countries returns all countries sorted by code.
func (c *Catalog) Countries() []Country {
	out := make([]Country, 0, len(c.codes))
	for _, code := range c.codes {
		out = append(out, c.countries[code])
	}
	return out
}

type fileFormat struct {
	Markets map[string]struct {
		Name         string `json:"name"`
		Provider     string `json:"provider"`
		ProductPages *struct {
			Paths   []string `json:"paths"`
			Link    string   `json:"link"`
			IDParam string   `json:"id_param"`
		} `json:"product_pages"`
		SearchLink string `json:"search_link"`
	} `json:"markets"`
	Countries map[string]struct {
		Name           string `json:"name"`
		Currency       string `json:"currency"`
		Region         string `json:"region"`
		ShoppingRegion string `json:"shopping_region"`
		Language       string `json:"language"`
		Markets        []struct {
			Market string `json:"market"`
			Domain string `json:"domain"`
		} `json:"markets"`
	} `json:"countries"`
}

// Load reads the catalog from path, or the embedded default when path is empty.
func Load(path string) (*Catalog, error) {
	data := defaultCatalog
	if path != "" {
		var err error
		if data, err = os.ReadFile(path); err != nil {
			return nil, fmt.Errorf("read markets file: %w", err)
		}
	}
	return Parse(data)
}

// Parse builds and validates a catalog from JSON.
func Parse(data []byte) (*Catalog, error) {
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse markets: %w", err)
	}
	if len(f.Countries) == 0 {
		return nil, errors.New("markets: no countries defined")
	}

	rules := make(map[string]*productRule, len(f.Markets))
	for id, m := range f.Markets {
		if m.SearchLink != "" {
			u, err := url.Parse(strings.NewReplacer("{host}", "example.com", "{query}", "q", "{query_path}", "q", "{slug}", "q").Replace(m.SearchLink))
			if err != nil || u.Scheme != "https" || u.Host != "example.com" ||
				!(strings.Contains(m.SearchLink, "{query}") || strings.Contains(m.SearchLink, "{query_path}") || strings.Contains(m.SearchLink, "{slug}")) {
				return nil, fmt.Errorf("markets: market %q search_link must look like https://{host}/...{query}", id)
			}
		}
		if m.ProductPages == nil {
			continue
		}
		rule, err := compileRule(m.ProductPages.Paths, m.ProductPages.Link, m.ProductPages.IDParam)
		if err != nil {
			return nil, fmt.Errorf("markets: market %q product_pages: %w", id, err)
		}
		rules[id] = rule
	}

	sum := sha256.Sum256(data)
	cat := &Catalog{countries: make(map[string]Country, len(f.Countries)), version: hex.EncodeToString(sum[:8])}
	for code, fc := range f.Countries {
		code = strings.ToLower(code)
		if fc.Region == "" {
			return nil, fmt.Errorf("markets: country %q has no region", code)
		}
		country := Country{
			Code:     code,
			Name:     fc.Name,
			Currency: fc.Currency,
			Region:   fc.Region,
			Language: fc.Language,

			ShoppingRegion: fc.ShoppingRegion,
		}
		if country.ShoppingRegion == "" {
			country.ShoppingRegion = fc.Region
		}
		seen := make(map[string]bool)
		for _, fm := range fc.Markets {
			m, ok := f.Markets[fm.Market]
			if !ok {
				return nil, fmt.Errorf("markets: country %q uses unknown market %q", code, fm.Market)
			}
			if seen[fm.Market] {
				return nil, fmt.Errorf("markets: country %q lists market %q twice", code, fm.Market)
			}
			if fm.Domain == "" || m.Provider == "" {
				return nil, fmt.Errorf("markets: market %q in %q needs a domain and a provider", fm.Market, code)
			}
			seen[fm.Market] = true
			country.Targets = append(country.Targets, Target{
				Market:   fm.Market,
				Name:     m.Name,
				Domain:   strings.ToLower(fm.Domain),
				Provider: m.Provider,
				products: rules[fm.Market],
				search:   m.SearchLink,
			})
		}
		if len(country.Targets) == 0 {
			return nil, fmt.Errorf("markets: country %q has no markets", code)
		}
		cat.countries[code] = country
		cat.codes = append(cat.codes, code)
	}
	sort.Strings(cat.codes)
	return cat, nil
}

func compileRule(paths []string, link, idParam string) (*productRule, error) {
	if len(paths) == 0 {
		return nil, errors.New("needs at least one path pattern")
	}
	rule := &productRule{link: link, idParam: idParam}
	for _, p := range paths {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		if strings.Contains(link, "{id}") && re.SubexpIndex("id") < 0 && idParam == "" {
			return nil, fmt.Errorf("pattern %q needs an (?P<id>...) group for link %q", p, link)
		}
		rule.paths = append(rule.paths, re)
	}
	if link != "" {
		u, err := url.Parse(strings.NewReplacer("{host}", "example.com", "{id}", "1").Replace(link))
		if err != nil || u.Scheme != "https" || u.Host != "example.com" {
			return nil, fmt.Errorf("link %q must look like https://{host}/...", link)
		}
	}
	return rule, nil
}

func isDigits(s string) bool {
	if s == "" || len(s) > 25 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SiteQuery restricts title to the targets' domains, e.g.
// `usb hub (site:amazon.com OR site:temu.com)`.
func SiteQuery(title string, targets []Target) string {
	sites := make([]string, len(targets))
	for i, t := range targets {
		sites[i] = "site:" + t.Domain
	}
	if len(sites) == 1 {
		return title + " " + sites[0]
	}
	return title + " (" + strings.Join(sites, " OR ") + ")"
}

// MatchTarget returns the target whose marketplace owns host.
func MatchTarget(host string, targets []Target) (Target, bool) {
	for _, t := range targets {
		if t.Matches(host) {
			return t, true
		}
	}
	return Target{}, false
}

// WithProviders returns a copy of the catalog in which the given
// marketplaces use another provider, e.g. {"temu": "apify-temu"}.
// Marketplaces not in the catalog are an error.
func (c *Catalog) WithProviders(overrides map[string]string) (*Catalog, error) {
	known := map[string]bool{}
	out := &Catalog{countries: make(map[string]Country, len(c.countries)), codes: c.codes, version: c.version}
	for code, country := range c.countries {
		targets := make([]Target, len(country.Targets))
		for i, t := range country.Targets {
			known[t.Market] = true
			if p, ok := overrides[t.Market]; ok {
				t.Provider = p
			}
			targets[i] = t
		}
		country.Targets = targets
		out.countries[code] = country
	}
	for m := range overrides {
		if !known[m] {
			return nil, fmt.Errorf("markets: unknown marketplace %q", m)
		}
	}
	return out, nil
}
