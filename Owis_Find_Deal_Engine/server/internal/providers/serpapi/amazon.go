package serpapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

// EngineAmazon is SerpApi's Amazon Search API: Amazon's own search results
// with ASIN, price, rating and image. It needs only the SerpApi key (no
// Amazon account) and costs one SerpApi search per call.
const EngineAmazon = "amazon"

// Amazon searches the Amazon marketplace of a country (amazon.com,
// amazon.sa, ...) through SerpApi's Amazon engine. It only handles the
// "amazon" marketplace.
type Amazon struct {
	c *Client
}

var _ search.Provider = (*Amazon)(nil)

// NewAmazon returns the Amazon provider. Engine in cfg is ignored.
func NewAmazon(cfg Config) (*Amazon, error) {
	cfg.Engine = EngineWeb // any valid value; this provider always uses EngineAmazon
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &Amazon{c: c}, nil
}

func (a *Amazon) Name() string { return "serpapi-amazon" }
func (a *Amazon) Batch() bool  { return a.c.cfg.Combined }

type amazonResponse struct {
	Error          string         `json:"error"`
	OrganicResults []amazonResult `json:"organic_results"`
}

type amazonResult struct {
	ASIN           string   `json:"asin"`
	Title          string   `json:"title"`
	Link           string   `json:"link"`
	LinkClean      string   `json:"link_clean"`
	Thumbnail      string   `json:"thumbnail"`
	Price          string   `json:"price"`
	ExtractedPrice *float64 `json:"extracted_price"`
	Rating         any      `json:"rating"`
	Reviews        any      `json:"reviews"`
	Delivery       any      `json:"delivery"`
	Sponsored      bool     `json:"sponsored"`
}

// Search searches each Amazon target (normally exactly one).
func (a *Amazon) Search(ctx context.Context, q search.Query) ([]search.Product, error) {
	var products []search.Product
	for _, t := range q.Targets {
		if t.Market != "amazon" {
			return nil, fmt.Errorf("serpapi-amazon: cannot search marketplace %q", t.Market)
		}
		found, err := a.search(ctx, q.Title, t)
		if err != nil {
			return nil, err
		}
		products = append(products, found...)
	}
	return products, nil
}

func (a *Amazon) search(ctx context.Context, title string, t markets.Target) ([]search.Product, error) {
	params := url.Values{}
	params.Set("engine", EngineAmazon)
	params.Set("k", title)
	params.Set("amazon_domain", t.Domain)

	var body amazonResponse
	if err := a.c.get(ctx, params, &body, &body.Error); err != nil {
		return nil, err
	}
	if body.Error != "" && len(body.OrganicResults) == 0 {
		if noResults(body.Error) {
			return nil, nil
		}
		return nil, fmt.Errorf("serpapi-amazon: %s", body.Error)
	}
	return amazonProducts(body.OrganicResults, title, t), nil
}

// amazonProducts keeps relevant results with a product link, in Amazon's
// order. Links are cleaned to https://<domain>/dp/<ASIN>.
func amazonProducts(results []amazonResult, title string, t markets.Target) []search.Product {
	products := make([]search.Product, 0, len(results))
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if r.Title == "" || !search.Relevant(title, r.Title) {
			continue
		}
		link := ""
		for _, l := range []string{r.LinkClean, r.Link} {
			if clean, ok := t.ProductLink(l); ok {
				link = clean
				break
			}
		}
		if link == "" && isASIN(r.ASIN) {
			link = "https://www." + t.Domain + "/dp/" + r.ASIN
		}
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true

		p := search.Product{
			Market:    t.Market,
			Title:     r.Title,
			Link:      link,
			Thumbnail: r.Thumbnail,
			Position:  len(products) + 1,
			Provider:  "serpapi-amazon",
		}
		if r.ExtractedPrice != nil && *r.ExtractedPrice > 0 {
			price := *r.ExtractedPrice
			p.Price = &price
			p.Currency = currencyOf(r.Price)
		}
		if r.Sponsored {
			p.Extensions = append(p.Extensions, "Sponsored")
		}
		if s := text(r.Rating); s != "" {
			rating := "rating " + s
			if n := text(r.Reviews); n != "" {
				rating += " (" + n + " reviews)"
			}
			p.Extensions = append(p.Extensions, rating)
		}
		if s := deliveryText(r.Delivery); s != "" {
			p.Extensions = append(p.Extensions, s)
		}
		products = append(products, p)
	}
	return products
}

// deliveryText renders delivery info given as a string or a list of strings.
func deliveryText(v any) string {
	if list, ok := v.([]any); ok {
		parts := make([]string, 0, len(list))
		for _, x := range list {
			if s := text(x); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return text(v)
}

func isASIN(s string) bool {
	if len(s) != 10 {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
