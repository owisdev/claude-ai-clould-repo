// Package searxng searches marketplaces through a self-hosted SearXNG
// instance (free metasearch over Google, Bing, DuckDuckGo, Brave, ...)
// using site-restricted queries.
package searxng

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

const maxBodyBytes = 5 << 20

// Config configures the client.
type Config struct {
	BaseURL string // e.g. http://searxng:8080
	// Combined searches all marketplaces with one query. When false, one
	// query per marketplace runs in parallel.
	Combined   bool
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

// Search runs one query restricted to q.Targets' domains.
func (c *Client) Search(ctx context.Context, q search.Query) ([]search.Product, error) {
	if len(q.Targets) == 0 {
		return nil, nil
	}

	params := url.Values{}
	params.Set("q", markets.SiteQuery(q.Title, q.Targets))
	params.Set("format", "json")
	params.Set("categories", "general")
	params.Set("safesearch", "1")
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

	products := toProducts(body.Results, q.Targets)
	if len(products) == 0 && len(body.UnresponsiveEngines) > 0 {
		// No results because the engines failed (CAPTCHA, timeout, ...),
		// not because nothing matched: report an error so a fallback runs.
		return nil, fmt.Errorf("searxng: no results, engines unresponsive: %s", engineErrors(body.UnresponsiveEngines))
	}
	return products, nil
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

// toProducts maps results to their marketplace and numbers them per market.
// SearXNG already merges and de-duplicates results across engines.
func toProducts(results []result, targets []markets.Target) []search.Product {
	products := make([]search.Product, 0, len(results))
	positions := make(map[string]int, len(targets))
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if r.URL == "" || r.Title == "" || seen[r.URL] {
			continue
		}
		u, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		target, ok := markets.MatchTarget(u.Hostname(), targets)
		if !ok {
			continue // engines that ignore site: return other domains
		}
		seen[r.URL] = true
		positions[target.Market]++

		thumb := r.Thumbnail
		if thumb == "" {
			thumb = r.ImgSrc
		}
		products = append(products, search.Product{
			Market:    target.Market,
			Title:     r.Title,
			Link:      r.URL,
			Snippet:   r.Content,
			Thumbnail: thumb,
			Position:  positions[target.Market],
			Provider:  "searxng",
		})
	}
	return products
}
