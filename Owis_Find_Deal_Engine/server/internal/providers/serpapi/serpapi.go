// Package serpapi searches marketplaces through SerpApi, using either
// Google Shopping (structured prices, seller and image) or the Google web
// engine with site-restricted queries.
package serpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

const (
	defaultBaseURL = "https://serpapi.com/search.json"
	maxBodyBytes   = 5 << 20

	// EngineShopping is Google Shopping: prices, seller name and image for
	// every result. The site: operator does not work there, so results are
	// matched to marketplaces by seller name.
	EngineShopping = "google_shopping"
	// EngineWeb is Google web search with site:-restricted queries.
	EngineWeb = "google"
)

// Config configures the client.
type Config struct {
	APIKey  string
	BaseURL string // defaults to SerpApi; overridden in tests
	// Engine is EngineShopping (default) or EngineWeb. With Shopping, a
	// search that finds nothing from the requested marketplaces (e.g.
	// Google Shopping is thin in that country) is retried once on the web
	// engine.
	Engine string
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
	switch cfg.Engine {
	case "":
		cfg.Engine = EngineShopping
	case EngineShopping, EngineWeb:
	default:
		return nil, fmt.Errorf("serpapi: unknown engine %q (use %s or %s)", cfg.Engine, EngineShopping, EngineWeb)
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

// Search searches q.Targets with the configured engine.
func (c *Client) Search(ctx context.Context, q search.Query) ([]search.Product, error) {
	if len(q.Targets) == 0 {
		return nil, nil
	}
	if c.cfg.Engine == EngineWeb {
		return c.searchWeb(ctx, q)
	}
	products, err := c.searchShopping(ctx, q)
	if err != nil || len(products) > 0 {
		return products, err
	}
	return c.searchWeb(ctx, q)
}

// ---------------------------------------------------------------- shopping

type shoppingResponse struct {
	Error           string           `json:"error"`
	ShoppingResults []shoppingResult `json:"shopping_results"`
}

type shoppingResult struct {
	Title          string   `json:"title"`
	Link           string   `json:"link"`         // shop page, not always present
	ProductLink    string   `json:"product_link"` // Google product page
	Source         string   `json:"source"`       // seller, e.g. "Amazon.com"
	Price          string   `json:"price"`        // e.g. "$29.99"
	ExtractedPrice *float64 `json:"extracted_price"`
	Thumbnail      string   `json:"thumbnail"`
	Delivery       any      `json:"delivery"`
	Rating         any      `json:"rating"`
	Reviews        any      `json:"reviews"`
}

func (c *Client) searchShopping(ctx context.Context, q search.Query) ([]search.Product, error) {
	query := q.Title
	if len(q.Targets) == 1 {
		// One marketplace per call: name the shop to steer Google Shopping.
		query += " " + q.Targets[0].Name
	}
	params := url.Values{}
	params.Set("engine", EngineShopping)
	params.Set("q", query)
	c.setLocale(params, q)

	var body shoppingResponse
	if err := c.get(ctx, params, &body, &body.Error); err != nil {
		return nil, err
	}
	if body.Error != "" && len(body.ShoppingResults) == 0 {
		if noResults(body.Error) {
			return nil, nil
		}
		return nil, fmt.Errorf("serpapi: %s", body.Error)
	}
	return shoppingProducts(body.ShoppingResults, q.Targets), nil
}

// shoppingProducts keeps results sold by the requested marketplaces and
// numbers them per marketplace.
func shoppingProducts(results []shoppingResult, targets []markets.Target) []search.Product {
	products := make([]search.Product, 0, len(results))
	positions := make(map[string]int, len(targets))
	for _, r := range results {
		if r.Title == "" {
			continue
		}
		target, ok := matchSeller(r.Source, r.Link, targets)
		if !ok {
			continue // sold by a shop we do not cover
		}
		link := r.ProductLink
		if r.Link != "" && hostMatches(r.Link, target) {
			link = r.Link // direct shop page is better than Google's page
		}
		if link == "" {
			continue
		}
		positions[target.Market]++

		p := search.Product{
			Market:    target.Market,
			Title:     r.Title,
			Link:      link,
			Snippet:   r.Source,
			Thumbnail: r.Thumbnail,
			Position:  positions[target.Market],
			Provider:  "serpapi",
		}
		if r.ExtractedPrice != nil {
			price := *r.ExtractedPrice
			p.Price = &price
			p.Currency = currencyOf(r.Price)
		}
		if s := text(r.Delivery); s != "" {
			p.Extensions = append(p.Extensions, s)
		}
		if s := text(r.Rating); s != "" {
			rating := "rating " + s
			if n := text(r.Reviews); n != "" {
				rating += " (" + n + " reviews)"
			}
			p.Extensions = append(p.Extensions, rating)
		}
		products = append(products, p)
	}
	return products
}

// matchSeller finds the marketplace of a Google Shopping offer: by the shop
// link's domain when present, otherwise by seller name ("Amazon.com",
// "eBay - seller123", "SHEIN").
func matchSeller(source, link string, targets []markets.Target) (markets.Target, bool) {
	for _, t := range targets {
		if link != "" && hostMatches(link, t) {
			return t, true
		}
	}
	seller := normalize(source)
	if seller == "" {
		return markets.Target{}, false
	}
	for _, t := range targets {
		if strings.Contains(seller, normalize(t.Name)) || strings.Contains(seller, normalize(t.Market)) {
			return t, true
		}
	}
	return markets.Target{}, false
}

func hostMatches(link string, t markets.Target) bool {
	u, err := url.Parse(link)
	return err == nil && u.Hostname() != "" && t.Matches(u.Hostname())
}

func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// currencyOf extracts the currency part of a price label: "$29.99" → "$",
// "SAR 120.00" → "SAR", "29,99 €" → "€".
func currencyOf(price string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			return -1
		}
		return r
	}, price))
}

// text renders a loosely typed JSON value ("Free delivery", 4.5, 123).
func text(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return ""
	}
}

// ---------------------------------------------------------------- web

type webResponse struct {
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

// searchWeb runs one Google query restricted to q.Targets' domains.
func (c *Client) searchWeb(ctx context.Context, q search.Query) ([]search.Product, error) {
	params := url.Values{}
	params.Set("engine", EngineWeb)
	params.Set("q", markets.SiteQuery(q.Title, q.Targets))
	c.setLocale(params, q)

	var body webResponse
	if err := c.get(ctx, params, &body, &body.Error); err != nil {
		return nil, err
	}
	if body.Error != "" && len(body.OrganicResults) == 0 {
		if noResults(body.Error) {
			return nil, nil
		}
		return nil, fmt.Errorf("serpapi: %s", body.Error)
	}
	return webProducts(body.OrganicResults, q.Targets), nil
}

// webProducts maps results to their marketplace and numbers them per market.
func webProducts(results []organicResult, targets []markets.Target) []search.Product {
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

// ---------------------------------------------------------------- HTTP

func (c *Client) setLocale(params url.Values, q search.Query) {
	if q.Region != "" {
		params.Set("gl", q.Region)
	}
	if q.Language != "" {
		params.Set("hl", q.Language)
	}
}

// get calls SerpApi and decodes the JSON body into dst. apiErr points at
// dst's "error" field so non-200 answers can report SerpApi's message.
func (c *Client) get(ctx context.Context, params url.Values, dst any, apiErr *string) error {
	params.Set("api_key", c.cfg.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("serpapi: build request: %w", err)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		// url.Error embeds the request URL, which contains the API key.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("serpapi: request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("serpapi: decode response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("serpapi: status %d: %s", resp.StatusCode, *apiErr)
	}
	return nil
}

// noResults reports SerpApi's "Google hasn't returned any results" message.
func noResults(msg string) bool {
	return strings.Contains(msg, "hasn't returned any results")
}
