// Package serpapi searches marketplaces through SerpApi's Google engine using
// site-restricted queries.
package serpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

const (
	defaultBaseURL = "https://serpapi.com/search.json"
	maxBodyBytes   = 5 << 20
)

// Config configures the client.
type Config struct {
	APIKey  string
	BaseURL string // defaults to SerpApi; overridden in tests
	// Combined searches all marketplaces with one query (cheapest).
	// When false, one query per marketplace runs in parallel.
	Combined   bool
	HTTPClient *http.Client
}

// Client implements search.Provider.
type Client struct {
	cfg Config
}

var _ search.Provider = (*Client)(nil)

// New returns a SerpApi client.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("serpapi: API key is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{cfg: cfg}, nil
}

func (c *Client) Name() string { return "serpapi" }
func (c *Client) Batch() bool  { return c.cfg.Combined }

type response struct {
	Error          string          `json:"error"`
	OrganicResults []organicResult `json:"organic_results"`
}

type organicResult struct {
	Position    int    `json:"position"`
	Title       string `json:"title"`
	Link        string `json:"link"`
	Snippet     string `json:"snippet"`
	Thumbnail   string `json:"thumbnail"`
	RichSnippet struct {
		Top struct {
			Extensions         []any          `json:"extensions"`
			DetectedExtensions map[string]any `json:"detected_extensions"`
		} `json:"top"`
	} `json:"rich_snippet"`
}

// Search runs one Google query restricted to q.Targets' domains.
func (c *Client) Search(ctx context.Context, q search.Query) ([]search.Product, error) {
	if len(q.Targets) == 0 {
		return nil, nil
	}

	params := url.Values{}
	params.Set("engine", "google")
	params.Set("q", markets.SiteQuery(q.Title, q.Targets))
	if q.Region != "" {
		params.Set("gl", q.Region)
	}
	if q.Language != "" {
		params.Set("hl", q.Language)
	}
	params.Set("api_key", c.cfg.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("serpapi: build request: %w", err)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		// url.Error embeds the request URL, which contains the API key.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("serpapi: request failed: %w", err)
	}
	defer resp.Body.Close()

	var body response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return nil, fmt.Errorf("serpapi: decode response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("serpapi: status %d: %s", resp.StatusCode, body.Error)
	}
	if body.Error != "" && len(body.OrganicResults) == 0 {
		// SerpApi reports "Google hasn't returned any results" this way.
		if strings.Contains(body.Error, "hasn't returned any results") {
			return nil, nil
		}
		return nil, fmt.Errorf("serpapi: %s", body.Error)
	}
	return toProducts(body.OrganicResults, q.Targets), nil
}

// toProducts maps results to their marketplace and numbers them per market.
func toProducts(results []organicResult, targets []markets.Target) []search.Product {
	products := make([]search.Product, 0, len(results))
	positions := make(map[string]int, len(targets))
	for _, r := range results {
		if r.Link == "" || r.Title == "" {
			continue
		}
		u, err := url.Parse(r.Link)
		if err != nil {
			continue
		}
		target, ok := markets.MatchTarget(u.Hostname(), targets)
		if !ok {
			continue // result outside the requested marketplaces
		}
		positions[target.Market]++

		p := search.Product{
			Market:    target.Market,
			Title:     r.Title,
			Link:      r.Link,
			Snippet:   r.Snippet,
			Thumbnail: r.Thumbnail,
			Position:  positions[target.Market],
			Provider:  "serpapi",
		}
		for _, e := range r.RichSnippet.Top.Extensions {
			if s, ok := e.(string); ok {
				p.Extensions = append(p.Extensions, s)
			}
		}
		if price, ok := r.RichSnippet.Top.DetectedExtensions["price"].(float64); ok {
			p.Price = &price
			p.Currency, _ = r.RichSnippet.Top.DetectedExtensions["currency"].(string)
		}
		products = append(products, p)
	}
	return products
}
