// Package apify searches one marketplace (e.g. Temu or SHEIN, which have no
// official API) by running a scraper ("Actor") from the Apify store and
// reading its results: real prices, images and product links.
//
// Every Apify scraper has its own input format, so the Actor and its input
// are configuration: an input JSON template where {{query}} is replaced by
// the search text, {{max}} by the result limit and {{country}} /
// {{COUNTRY}} by the country's region code. The output reader accepts the
// field names most scrapers use (title/goods_name, price/salePrice, ...).
package apify

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
	"unicode"

	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
)

const (
	defaultBaseURL = "https://api.apify.com"
	maxBodyBytes   = 10 << 20
)

// Config configures one Actor for one marketplace.
type Config struct {
	Token string
	// Actor is "owner~actor-name" or "owner/actor-name" (or the Actor ID).
	Actor string
	// InputTemplate is the Actor input JSON with placeholders, e.g.
	// {"searchQueries":["{{query}}"],"maxItems":{{max}}}
	InputTemplate string
	// MaxItems caps results per run; also sent as maxItems so pay-per-result
	// Actors never charge for more.
	MaxItems int
	// Timeout bounds one Actor run (Apify's synchronous limit is 300 s).
	Timeout time.Duration
	// Combined only mirrors the search mode so this provider can be chained
	// with the web providers; each run searches one marketplace.
	Combined bool
	// EmptyFallback hands a market the scraper found nothing relevant for
	// to the next provider. Off: that market simply has no results (the
	// shop's own search found nothing; saves time and paid searches).
	// Errors and timeouts always fall back.
	EmptyFallback bool
	BaseURL       string // defaults to Apify; overridden in tests
	HTTPClient    *http.Client
}

// Client implements search.Provider for one Actor.
type Client struct {
	cfg Config
}

var _ search.Provider = (*Client)(nil)

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	if cfg.Token == "" || cfg.Actor == "" {
		return nil, errors.New("apify: token and actor are required")
	}
	// The store shows "owner/actor-name"; the API path needs "owner~actor-name".
	cfg.Actor = strings.Replace(strings.TrimSpace(cfg.Actor), "/", "~", 1)
	if !hasQuery(cfg.InputTemplate) {
		return nil, errors.New("apify: input template must contain {{query}}, {{query_url}} or {{query_slug}}")
	}
	if _, err := renderInput(cfg.InputTemplate, "test \"quoted\"", 10, "us"); err != nil {
		return nil, fmt.Errorf("apify: input template for %s: %w", cfg.Actor, err)
	}
	if cfg.MaxItems <= 0 {
		cfg.MaxItems = 10
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout + 10*time.Second}
	}
	return &Client{cfg: cfg}, nil
}

func (c *Client) Name() string { return "apify" }
func (c *Client) Batch() bool  { return c.cfg.Combined }

// Search runs the Actor once per target (normally exactly one). With
// EmptyFallback, a target the scraper found nothing for is reported as
// uncovered, so the next provider can try it (useful when a scraper covers
// only one region of the shop).
func (c *Client) Search(ctx context.Context, q search.Query) ([]search.Product, error) {
	var out []search.Product
	uncovered := map[string]bool{}
	for _, t := range q.Targets {
		products, err := c.run(ctx, q, t)
		if err != nil {
			return nil, err
		}
		if len(products) == 0 && c.cfg.EmptyFallback {
			uncovered[t.Market] = true
		}
		out = append(out, products...)
	}
	if len(uncovered) > 0 {
		return nil, &search.PartialError{Products: out, Uncovered: uncovered}
	}
	return out, nil
}

func (c *Client) run(ctx context.Context, q search.Query, t markets.Target) ([]search.Product, error) {
	input, err := renderInput(c.cfg.InputTemplate, q.Title, c.cfg.MaxItems, q.Region)
	if err != nil {
		return nil, fmt.Errorf("apify: %w", err)
	}

	params := url.Values{}
	params.Set("timeout", strconv.Itoa(int(c.cfg.Timeout.Seconds())))
	params.Set("maxItems", strconv.Itoa(c.cfg.MaxItems))
	params.Set("limit", strconv.Itoa(c.cfg.MaxItems))
	params.Set("clean", "1")
	endpoint := fmt.Sprintf("%s/v2/actors/%s/run-sync-get-dataset-items?%s",
		c.cfg.BaseURL, url.PathEscape(c.cfg.Actor), params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("apify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token) // never in the URL
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apify %s: request failed: %w", c.cfg.Actor, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("apify %s: read response: %w", c.cfg.Actor, err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("apify %s: status %d: %s", c.cfg.Actor, resp.StatusCode, apiError(body))
	}
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("apify %s: decode items: %w", c.cfg.Actor, err)
	}
	return toProducts(items, q.Title, t, c.cfg.MaxItems), nil
}

// hasQuery reports whether a template contains one of the query placeholders.
func hasQuery(tmpl string) bool {
	return strings.Contains(tmpl, "{{query}}") || strings.Contains(tmpl, "{{query_url}}") ||
		strings.Contains(tmpl, "{{query_slug}}")
}

// renderInput fills the template. The query is JSON-escaped, so quotes or
// backslashes typed by a user cannot break or alter the Actor input. For
// Actors that take a search URL instead of a keyword:
//
//	{{query_url}}  URL query encoding: "samsung s pen" -> "samsung+s+pen"
//	{{query_slug}} lowercase words joined by "-": "samsung-s-pen"
func renderInput(tmpl, query string, max int, region string) (string, error) {
	jsonText := func(s string) string {
		escaped, _ := json.Marshal(s)
		return string(escaped[1 : len(escaped)-1])
	}
	r := strings.NewReplacer(
		"{{query}}", jsonText(query),
		"{{query_url}}", jsonText(url.QueryEscape(query)),
		"{{query_slug}}", jsonText(slug(query)),
		"{{max}}", strconv.Itoa(max),
		"{{country}}", strings.ToLower(region),
		"{{COUNTRY}}", strings.ToUpper(region),
	)
	out := r.Replace(tmpl)
	if !json.Valid([]byte(out)) {
		return "", errors.New("input template is not valid JSON after filling placeholders")
	}
	return out, nil
}

// slug turns a query into lowercase words of letters and digits joined by
// "-" (URL-safe for any script, e.g. Arabic letters are percent-encoded).
func slug(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for i, w := range words {
		words[i] = url.PathEscape(w)
	}
	return strings.Join(words, "-")
}

// apiError extracts Apify's error message ({"error":{"type","message"}}).
func apiError(body []byte) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Type + ": " + e.Error.Message
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return string(body)
}

// Field names used by common Temu/SHEIN/marketplace scrapers, in order of
// preference.
var (
	titleKeys = []string{"title", "name", "goods_name", "goodsName", "productName", "product_title", "productTitle"}
	linkKeys  = []string{"url", "link", "productUrl", "product_url", "goods_url", "goodsUrl", "detailUrl", "detail_url",
		"link_url", "linkUrl"}
	// Formatted labels first: some scrapers give "price" in cents
	// (Temu: "price": 10361 with "price_str": "$103.61").
	priceKeys = []string{"price_str", "priceStr", "price_text", "priceText", "formatted_price", "formattedPrice",
		"price", "salePrice", "sale_price", "currentPrice", "current_price", "finalPrice", "retailPrice", "extracted_price"}
	currencyKeys = []string{"currency", "currencyCode", "currency_code", "priceCurrency"}
	imageKeys    = []string{"image", "imageUrl", "image_url", "thumbnail", "img", "goods_img", "goodsImg", "mainImage", "main_image", "images", "imageUrls"}
)

// toProducts maps dataset items to products of target, skipping items
// without a title or a link on the marketplace's domain, and items that do
// not match the query (shop searches also return merely related products,
// e.g. other products of the same brand).
func toProducts(items []map[string]any, query string, t markets.Target, max int) []search.Product {
	products := make([]search.Product, 0, min(len(items), max))
	for _, it := range items {
		if len(products) >= max {
			break
		}
		title := firstString(it, titleKeys)
		link := firstString(it, linkKeys)
		if title == "" || link == "" || !search.Relevant(query, title) {
			continue
		}
		u, err := url.Parse(link)
		if err != nil || u.Hostname() == "" || !t.Matches(u.Hostname()) {
			continue // not a page of this marketplace
		}
		// Scrapers return product pages; clean the link when the format is
		// known, but do not drop items over an unfamiliar URL format.
		if clean, ok := t.ProductLink(link); ok {
			link = clean
		}
		p := search.Product{
			Market:    t.Market,
			Title:     title,
			Link:      link,
			Thumbnail: firstString(it, imageKeys),
			Position:  len(products) + 1,
			Provider:  "apify",
		}
		if price, currency, ok := firstPrice(it); ok {
			p.Price = &price
			p.Currency = currency
		}
		if p.Currency == "" && p.Price != nil {
			p.Currency = firstString(it, currencyKeys)
		}
		products = append(products, p)
	}
	return products
}

// firstString returns the first non-empty string among keys. Arrays yield
// their first string element (or first element's "url"), e.g. image lists.
func firstString(it map[string]any, keys []string) string {
	for _, k := range keys {
		if s := asString(it[k]); s != "" {
			return s
		}
	}
	return ""
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		if len(x) > 0 {
			return asString(x[0])
		}
	case map[string]any:
		for _, k := range []string{"url", "src", "href"} {
			if s := asString(x[k]); s != "" {
				return s
			}
		}
	}
	return ""
}

// firstPrice reads a price given as a number, a label ("$12.99",
// "SAR 45"), or an object ({"amount": 12.99, "currency": "USD"}).
func firstPrice(it map[string]any) (float64, string, bool) {
	for _, k := range priceKeys {
		if p, cur, ok := parsePrice(it[k]); ok {
			return p, cur, true
		}
	}
	return 0, "", false
}

func parsePrice(v any) (float64, string, bool) {
	switch x := v.(type) {
	case float64:
		return x, "", x > 0
	case string:
		return parsePriceLabel(x)
	case map[string]any:
		cur := asString(x["currency"])
		if cur == "" {
			cur = asString(x["currencyCode"])
		}
		for _, k := range []string{"amount", "value", "price", "amountWithSymbol"} {
			if p, c, ok := parsePrice(x[k]); ok {
				if cur == "" {
					cur = c
				}
				return p, cur, true
			}
		}
	}
	return 0, "", false
}

// parsePriceLabel parses "$12.99", "SAR 1,245.50" or "12,99 €".
func parsePriceLabel(s string) (float64, string, bool) {
	var num, cur strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r == '.', r == ',':
			num.WriteRune(r)
		case r != ' ':
			cur.WriteRune(r)
		}
	}
	n := num.String()
	// "12,99" (decimal comma) vs "1,245.50" (thousands comma).
	if strings.Count(n, ",") == 1 && !strings.Contains(n, ".") && len(n)-strings.Index(n, ",") == 3 {
		n = strings.Replace(n, ",", ".", 1)
	} else {
		n = strings.ReplaceAll(n, ",", "")
	}
	p, err := strconv.ParseFloat(n, 64)
	if err != nil || p <= 0 {
		return 0, "", false
	}
	return p, cur.String(), true
}
