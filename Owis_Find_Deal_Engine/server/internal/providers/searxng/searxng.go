// Package searxng searches marketplaces through a self-hosted SearXNG
// instance (free metasearch over Google, Bing, DuckDuckGo, Brave, ...)
// using site-restricted queries.
package searxng

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

const maxBodyBytes = 5 << 20

// Config configures the client.
type Config struct {
	BaseURL string // e.g. http://searxng:8080
	// Combined takes all marketplaces in one provider call (Batch). Each
	// marketplace still gets its own site: query, run in parallel, so a big
	// shop cannot crowd the others out of the results.
	Combined bool
	// OneQuery sends a single "(site:a OR site:b ...)" query instead: fewer
	// requests to the search engines, but the biggest shop tends to fill
	// all the results.
	OneQuery bool
	// MaxPages is how many result pages one query may read (default 2);
	// the next page is read only while too few product pages were found.
	MaxPages   int
	Logger     *slog.Logger // optional: debug log of filtered results
	HTTPClient *http.Client
}

// Client implements search.Provider.
type Client struct {
	cfg      Config
	endpoint string
}

var _ search.Provider = (*Client)(nil)

// New returns a SearXNG client. The instance must have the JSON format
// enabled (search.formats in settings.yml).
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("searxng: invalid base URL %q", cfg.BaseURL)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 2
	}
	return &Client{cfg: cfg, endpoint: strings.TrimSuffix(u.String(), "/") + "/search"}, nil
}

func (c *Client) Name() string { return "searxng" }
func (c *Client) Batch() bool  { return c.cfg.Combined }

type response struct {
	Results []result `json:"results"`
	// Pairs of [engine, reason], e.g. [["google", "CAPTCHA"]].
	UnresponsiveEngines [][]string `json:"unresponsive_engines"`
}

type result struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Thumbnail string `json:"thumbnail"`
	ImgSrc    string `json:"img_src"`
	Engine    string `json:"engine"`
}

// Search searches q.Targets: one site: query per marketplace, in parallel
// (or a single combined query with OneQuery). Marketplaces that fail are
// reported in a *search.PartialError when others succeed.
func (c *Client) Search(ctx context.Context, q search.Query) ([]search.Product, error) {
	if len(q.Targets) <= 1 || c.cfg.OneQuery {
		return c.query(ctx, q)
	}

	type answer struct {
		market   string
		products []search.Product
		err      error
	}
	answers := make(chan answer, len(q.Targets))
	for _, t := range q.Targets {
		go func() {
			one := q
			one.Targets = []markets.Target{t}
			products, err := c.query(ctx, one)
			answers <- answer{t.Market, products, err}
		}()
	}

	var products []search.Product
	failed := map[string]error{}
	for range q.Targets {
		a := <-answers
		if a.err != nil {
			failed[a.market] = a.err
			continue
		}
		products = append(products, a.products...)
	}
	switch {
	case len(failed) == len(q.Targets):
		errs := make([]error, 0, len(failed))
		for _, t := range q.Targets {
			errs = append(errs, failed[t.Market])
		}
		return nil, errors.Join(errs...)
	case len(failed) > 0:
		return nil, &search.PartialError{Products: products, Failed: failed}
	}
	return products, nil
}

// minProducts is how many product pages a query should yield before no
// further result pages are requested.
const minProducts = 5

// query runs one SearXNG query restricted to q.Targets' domains. Many
// results are dropped (store, search and category pages, irrelevant
// pages), so when the first page leaves fewer than minProducts, the next
// page is requested too, up to MaxPages.
func (c *Client) query(ctx context.Context, q search.Query) ([]search.Product, error) {
	if len(q.Targets) == 0 {
		return nil, nil
	}
	var (
		results  []result
		products []search.Product
		stats    filterStats
	)
	for page := 1; page <= c.cfg.MaxPages; page++ {
		body, err := c.fetch(ctx, q, page)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break // keep what the first pages found
		}
		if len(body.Results) == 0 {
			if page == 1 && len(body.UnresponsiveEngines) > 0 {
				// No results because the engines failed (CAPTCHA, timeout,
				// ...), not because nothing matched: report an error so a
				// fallback runs. Results filtered out below (not product
				// pages, not relevant) mean the engines worked: that is
				// "no results".
				return nil, fmt.Errorf("searxng: no results, engines unresponsive: %s", engineErrors(body.UnresponsiveEngines))
			}
			break
		}
		results = append(results, body.Results...)
		products, stats = toProducts(results, q.Title, q.Targets)
		stats.pages = page
		if len(products) >= minProducts {
			break
		}
	}
	if c.cfg.Logger != nil {
		// Logged at info when nothing is kept, to explain "no_results".
		level := slog.LevelDebug
		if len(products) == 0 {
			level = slog.LevelInfo
		}
		c.cfg.Logger.Log(ctx, level, "searxng results",
			"markets", marketIDs(q.Targets), "pages", stats.pages, "results", stats.results,
			"other_site", stats.otherSite, "not_product_page", stats.notProduct,
			"duplicate", stats.duplicate, "irrelevant", stats.irrelevant, "kept", len(products))
	}
	return products, nil
}

func (c *Client) fetch(ctx context.Context, q search.Query, page int) (*response, error) {
	params := url.Values{}
	params.Set("q", markets.SiteQuery(q.Title, q.Targets))
	params.Set("format", "json")
	params.Set("categories", "general")
	params.Set("safesearch", "1")
	if page > 1 {
		params.Set("pageno", strconv.Itoa(page))
	}
	if lang := locale(q.Language, q.Region); lang != "" {
		params.Set("language", lang)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("searxng: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 403 usually means the JSON format is not enabled in settings.yml;
		// 429 means the SearXNG limiter is blocking us.
		return nil, fmt.Errorf("searxng: status %d", resp.StatusCode)
	}
	var body response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return nil, fmt.Errorf("searxng: decode response: %w", err)
	}
	return &body, nil
}

func marketIDs(targets []markets.Target) []string {
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.Market
	}
	return ids
}

// locale builds a SearXNG language tag such as "en-US" or "en-SA".
func locale(lang, region string) string {
	switch {
	case lang != "" && region != "":
		return lang + "-" + strings.ToUpper(region)
	case lang != "":
		return lang
	default:
		return ""
	}
}

func engineErrors(pairs [][]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, strings.Join(p, ": "))
	}
	return strings.Join(parts, ", ")
}

// toProducts keeps relevant product pages of the requested marketplaces, maps them
// to their marketplace and numbers them per market. Different links to the
// same product (tracking parameters, /clp/ vs /dp/) count once.
// filterStats counts why results were dropped (logged at debug level).
type filterStats struct {
	pages, results, otherSite, notProduct, duplicate, irrelevant int
}

func toProducts(results []result, title string, targets []markets.Target) ([]search.Product, filterStats) {
	st := filterStats{results: len(results)}
	products := make([]search.Product, 0, len(results))
	positions := make(map[string]int, len(targets))
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if r.URL == "" || r.Title == "" {
			continue
		}
		u, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		target, ok := markets.MatchTarget(u.Hostname(), targets)
		if !ok {
			st.otherSite++ // engines that ignore site: return other domains
			continue
		}
		link, ok := target.ProductLink(r.URL)
		if !ok {
			st.notProduct++ // search, store or category page
			continue
		}
		if seen[link] {
			st.duplicate++
			continue
		}
		if !search.Relevant(title, r.Title, r.Content, u.Path) {
			st.irrelevant++ // the engine ignored the query and returned any shop page
			continue
		}
		if target.GenericTitle(r.Title) {
			st.irrelevant++ // title is only the shop name ("Temu"): nothing to show
			continue
		}
		seen[link] = true
		positions[target.Market]++

		thumb := r.Thumbnail
		if thumb == "" {
			thumb = r.ImgSrc
		}
		products = append(products, search.Product{
			Market:    target.Market,
			Title:     r.Title,
			Link:      link,
			Snippet:   r.Content,
			Thumbnail: thumb,
			Position:  positions[target.Market],
			Provider:  "searxng",
		})
	}
	return products, st
}
