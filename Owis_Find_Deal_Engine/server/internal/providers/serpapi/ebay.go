package serpapi

import (
	"context"
	"fmt"
	"net/url"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

// EngineEbay is SerpApi's eBay Search API: eBay's own search results with
// item id, price, condition and shipping. It needs only the SerpApi key (no
// eBay developer account) and costs one SerpApi search per call.
const EngineEbay = "ebay"

// Ebay searches eBay (ebay.com for usa and ksa) through SerpApi's eBay
// engine. It only handles the "ebay" marketplace.
type Ebay struct {
	c *Client
}

var _ search.Provider = (*Ebay)(nil)

// NewEbay returns the eBay provider. Engine in cfg is ignored.
func NewEbay(cfg Config) (*Ebay, error) {
	cfg.Engine = EngineWeb // any valid value; this provider always uses EngineEbay
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &Ebay{c: c}, nil
}

func (e *Ebay) Name() string { return "serpapi-ebay" }
func (e *Ebay) Batch() bool  { return e.c.cfg.Combined }

type ebayResponse struct {
	Error          string       `json:"error"`
	OrganicResults []ebayResult `json:"organic_results"`
}

type ebayResult struct {
	Title     string `json:"title"`
	Link      string `json:"link"`
	Thumbnail string `json:"thumbnail"`
	Condition string `json:"condition"`
	// {"raw": "$9.99", "extracted": 9.99} or a range
	// {"from": {...}, "to": {...}} (variants).
	Price    any `json:"price"`
	Shipping any `json:"shipping"`
	Rating   any `json:"rating"`
	Reviews  any `json:"reviews"`
}

// Search searches each eBay target (normally exactly one).
func (e *Ebay) Search(ctx context.Context, q search.Query) ([]search.Product, error) {
	var products []search.Product
	for _, t := range q.Targets {
		if t.Market != "ebay" {
			return nil, fmt.Errorf("serpapi-ebay: cannot search marketplace %q", t.Market)
		}
		found, err := e.search(ctx, q.Title, t)
		if err != nil {
			return nil, err
		}
		products = append(products, found...)
	}
	return products, nil
}

func (e *Ebay) search(ctx context.Context, title string, t markets.Target) ([]search.Product, error) {
	params := url.Values{}
	params.Set("engine", EngineEbay)
	params.Set("_nkw", title)
	params.Set("ebay_domain", t.Domain)

	var body ebayResponse
	if err := e.c.get(ctx, params, &body, &body.Error); err != nil {
		return nil, err
	}
	if body.Error != "" && len(body.OrganicResults) == 0 {
		if noResults(body.Error) {
			return nil, nil
		}
		return nil, fmt.Errorf("serpapi-ebay: %s", body.Error)
	}
	return ebayProducts(body.OrganicResults, title, t), nil
}

// ebayProducts keeps relevant results with an item link, in eBay's order.
// Links are cleaned to https://<host>/itm/<id> (drops tracking parameters).
func ebayProducts(results []ebayResult, title string, t markets.Target) []search.Product {
	products := make([]search.Product, 0, len(results))
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if r.Title == "" || !search.Relevant(title, r.Title) {
			continue // also drops eBay's "Shop on eBay" placeholder item
		}
		link, ok := t.ProductLink(r.Link)
		if !ok || seen[link] {
			continue
		}
		seen[link] = true

		p := search.Product{
			Market:    t.Market,
			Title:     r.Title,
			Link:      link,
			Thumbnail: r.Thumbnail,
			Position:  len(products) + 1,
			Provider:  "serpapi-ebay",
		}
		if price, raw, ok := ebayPrice(r.Price); ok {
			p.Price = &price
			p.Currency = currencyOf(raw)
		}
		if r.Condition != "" {
			p.Extensions = append(p.Extensions, r.Condition)
		}
		if s := shippingText(r.Shipping); s != "" {
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

// ebayPrice reads {"raw","extracted"}; for a range (variants) the lowest
// price ("from") is used, as the app shows "from $X".
func ebayPrice(v any) (float64, string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, "", false
	}
	if from, ok := m["from"]; ok {
		return ebayPrice(from)
	}
	price, ok := m["extracted"].(float64)
	if !ok || price <= 0 {
		return 0, "", false
	}
	raw, _ := m["raw"].(string)
	return price, raw, true
}

// shippingText renders shipping given as text or as {"raw": "..."}.
func shippingText(v any) string {
	if m, ok := v.(map[string]any); ok {
		return text(m["raw"])
	}
	return text(v)
}
