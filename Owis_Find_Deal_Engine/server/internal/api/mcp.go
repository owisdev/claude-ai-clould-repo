package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/auth"
	"owis_find_deal_engine/internal/markets"
	"owis_find_deal_engine/internal/search"
	"owis_find_deal_engine/internal/store"
)

// The MCP endpoint (POST /mcp) lets AI agents call the search as tools,
// with the same API keys as the REST API. It speaks the Model Context
// Protocol over Streamable HTTP, statelessly (no sessions, no SSE: every
// request is answered with one JSON object):
//
//   - 2026-07-28 ("modern"): no handshake; each request carries its
//     protocol version in params._meta and the headers MCP-Protocol-Version,
//     Mcp-Method and Mcp-Name, which must match the body.
//   - 2025-03-26 … 2025-11-25 ("legacy"): the client starts with
//     initialize; later requests carry MCP-Protocol-Version (or nothing,
//     which means 2025-03-26). No Mcp-Session-Id is ever issued.
//
// Tools: search_products (scope "search", metered like POST
// /api/v1/search) and list_countries (scope "countries").

// MCP protocol versions, newest first.
var (
	mcpModernVersions = []string{"2026-07-28"}
	mcpLegacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}
	mcpVersions       = append(slices.Clone(mcpModernVersions), mcpLegacyVersions...)
)

// mcpDefaultLegacy is the version of a legacy request without
// MCP-Protocol-Version (clients older than 2025-06-18 did not send it).
const mcpDefaultLegacy = "2025-03-26"

// JSON-RPC and MCP error codes.
const (
	rpcParseError         = -32700
	rpcInvalidRequest     = -32600
	rpcMethodNotFound     = -32601
	rpcInvalidParams      = -32602
	rpcInternalError      = -32603
	rpcHeaderMismatch     = -32020
	rpcUnsupportedVersion = -32022
)

const (
	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaServerInfo      = "io.modelcontextprotocol/serverInfo"
)

// Tool names.
const (
	toolSearchProducts = "search_products"
	toolListCountries  = "list_countries"
)

// mcpListTTL is how long clients may cache tools/list and server/discover
// (milliseconds): the tools only change with a new server version or new
// countries.
const mcpListTTL = 3_600_000

const mcpInstructions = "Owis Find Deal searches online marketplaces (Amazon, AliExpress, Temu, SHEIN, eBay, … depending on the country) " +
	"for a product and returns the offers per marketplace with title, price, currency and link. " +
	"Call list_countries once to learn the country codes and which marketplaces each covers, then search_products. " +
	"A new search takes 20-60 seconds; the same search (title + country) within 48 hours is answered at once from the cache and does not " +
	"count against the API key's daily allowance. Prefer short product titles (brand + model) and do not repeat a search with refresh=true " +
	"unless fresh prices are needed: a live search counts against the allowance."

// mcpServer is what the MCP endpoint needs beyond the handlers.
type mcpServer struct {
	version string
	origins []string // allowed browser origins (Origin header)
	tools   []map[string]any
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type rpcParams struct {
	Meta            map[string]json.RawMessage `json:"_meta"`
	Name            string                     `json:"name"`
	Arguments       json.RawMessage            `json:"arguments"`
	ProtocolVersion string                     `json:"protocolVersion"` // initialize
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func writeRPC(w http.ResponseWriter, status int, id json.RawMessage, result any, rerr *rpcError) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rerr})
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string, data any) {
	writeRPC(w, status, id, nil, &rpcError{Code: code, Message: msg, Data: data})
}

// mcpCall is one JSON-RPC request being served.
type mcpCall struct {
	id     json.RawMessage
	method string
	params rpcParams
	modern bool
}

// mcp serves POST /mcp. Runs after requireCaller (API keys only).
func (h *handlers) mcp(w http.ResponseWriter, r *http.Request) {
	if !h.mcpServer.originAllowed(r) {
		writeRPCError(w, http.StatusForbidden, nil, rpcInvalidRequest, "origin not allowed", nil)
		return
	}
	if !acceptsJSON(r.Header.Get("Accept")) {
		writeRPCError(w, http.StatusNotAcceptable, nil, rpcInvalidRequest, "the MCP endpoint answers with application/json; add it to Accept", nil)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		writeRPCError(w, http.StatusUnsupportedMediaType, nil, rpcParseError, "Content-Type must be application/json", nil)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, rpcParseError, fmt.Sprintf("body must not exceed %d bytes", maxBodyBytes), nil)
		return
	}
	if b := bytes.TrimLeft(body, " \t\r\n"); len(b) > 0 && b[0] == '[' {
		writeRPCError(w, http.StatusBadRequest, nil, rpcInvalidRequest, "JSON-RPC batches are not supported; send one message per request", nil)
		return
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, rpcParseError, "invalid JSON: "+err.Error(), nil)
		return
	}
	if msg.JSONRPC != "2.0" {
		writeRPCError(w, http.StatusBadRequest, validID(msg.ID), rpcInvalidRequest, `jsonrpc must be "2.0"`, nil)
		return
	}
	if msg.Method == "" {
		if msg.Result != nil || msg.Error != nil {
			w.WriteHeader(http.StatusAccepted) // a response from the client: nothing to do
			return
		}
		writeRPCError(w, http.StatusBadRequest, validID(msg.ID), rpcInvalidRequest, "method is required", nil)
		return
	}
	if msg.ID == nil {
		// A notification (notifications/initialized, notifications/cancelled, …):
		// accepted, nothing to answer.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if validID(msg.ID) == nil {
		writeRPCError(w, http.StatusBadRequest, nil, rpcInvalidRequest, "id must be a string or a number", nil)
		return
	}

	c := mcpCall{id: msg.ID, method: msg.Method}
	if len(msg.Params) > 0 && string(msg.Params) != "null" {
		if err := json.Unmarshal(msg.Params, &c.params); err != nil {
			writeRPCError(w, http.StatusBadRequest, c.id, rpcInvalidParams, "params must be an object: "+err.Error(), nil)
			return
		}
	}
	if !h.mcpVersion(w, r, &c) {
		return
	}

	switch c.method {
	case "initialize":
		h.mcpInitialize(w, c)
	case "server/discover":
		h.mcpResult(w, c, map[string]any{
			"supportedVersions": mcpVersions,
			"capabilities":      mcpCapabilities(),
			"instructions":      mcpInstructions,
		}, true)
	case "ping":
		h.mcpResult(w, c, map[string]any{}, false)
	case "tools/list":
		h.mcpResult(w, c, map[string]any{"tools": h.mcpServer.tools}, true)
	case "tools/call":
		h.mcpCallTool(w, r, c)
	default:
		status := http.StatusOK
		if c.modern {
			status = http.StatusNotFound
		}
		writeRPCError(w, status, c.id, rpcMethodNotFound, "method not found: "+c.method, nil)
	}
}

// mcpVersion checks the protocol version and, for modern requests, that
// the headers match the body. It answers the error itself (false).
func (h *handlers) mcpVersion(w http.ResponseWriter, r *http.Request, c *mcpCall) bool {
	header := r.Header.Get("MCP-Protocol-Version")
	unsupported := func(v string) bool {
		writeRPCError(w, http.StatusBadRequest, c.id, rpcUnsupportedVersion, "Unsupported protocol version",
			map[string]any{"supported": mcpVersions, "requested": v})
		return false
	}
	mismatch := func(format string, a ...any) bool {
		writeRPCError(w, http.StatusBadRequest, c.id, rpcHeaderMismatch, "Header mismatch: "+fmt.Sprintf(format, a...), nil)
		return false
	}

	if c.method == "initialize" {
		return true // legacy handshake; the version is negotiated in its answer
	}
	raw, hasMeta := c.params.Meta[metaProtocolVersion]
	if !hasMeta {
		v := header
		if v == "" {
			v = mcpDefaultLegacy
		}
		switch {
		case slices.Contains(mcpModernVersions, v):
			return mismatch("MCP-Protocol-Version is %s but params._meta lacks %q", v, metaProtocolVersion)
		case !slices.Contains(mcpLegacyVersions, v):
			return unsupported(v)
		}
		return true
	}

	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		writeRPCError(w, http.StatusBadRequest, c.id, rpcInvalidParams, metaProtocolVersion+" must be a string", nil)
		return false
	}
	if !slices.Contains(mcpVersions, v) {
		return unsupported(v)
	}
	if header != v {
		return mismatch("MCP-Protocol-Version header %q does not match body value %q", header, v)
	}
	if !slices.Contains(mcpModernVersions, v) {
		return true // a legacy version with _meta: served as legacy
	}
	c.modern = true
	if m := r.Header.Get("Mcp-Method"); m != c.method {
		return mismatch("Mcp-Method header %q does not match body value %q", m, c.method)
	}
	if c.method == "tools/call" {
		name, ok := decodeHeaderValue(r.Header.Get("Mcp-Name"))
		if !ok || name != c.params.Name {
			return mismatch("Mcp-Name header does not match body value %q", c.params.Name)
		}
	}
	return true
}

// decodeHeaderValue decodes the "=?base64?…?=" form of Mcp-Name values.
func decodeHeaderValue(v string) (string, bool) {
	inner, ok := strings.CutPrefix(v, "=?base64?")
	if !ok {
		return v, v != ""
	}
	inner, ok = strings.CutSuffix(inner, "?=")
	if !ok {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (h *handlers) mcpInitialize(w http.ResponseWriter, c mcpCall) {
	v := c.params.ProtocolVersion
	if !slices.Contains(mcpLegacyVersions, v) {
		v = mcpLegacyVersions[0] // the client may disconnect if it cannot speak it
	}
	writeRPC(w, http.StatusOK, c.id, map[string]any{
		"protocolVersion": v,
		"capabilities":    mcpCapabilities(),
		"serverInfo":      h.mcpServer.info(),
		"instructions":    mcpInstructions,
	}, nil)
}

func mcpCapabilities() map[string]any {
	return map[string]any{"tools": map[string]any{"listChanged": false}}
}

func (s *mcpServer) info() map[string]any {
	v := s.version
	if v == "" {
		v = "dev"
	}
	return map[string]any{"name": "owis_find_deal_engine", "title": "Owis Find Deal", "version": v}
}

// mcpResult answers a request. Modern answers carry resultType and the
// server's identity; cacheable ones (lists) also a cache hint.
func (h *handlers) mcpResult(w http.ResponseWriter, c mcpCall, result map[string]any, cacheable bool) {
	if c.modern {
		result["resultType"] = "complete"
		result["_meta"] = map[string]any{metaServerInfo: h.mcpServer.info()}
		if cacheable {
			result["ttlMs"] = mcpListTTL
			result["cacheScope"] = "public"
		}
	}
	writeRPC(w, http.StatusOK, c.id, result, nil)
}

// toolError is a failed tool call: the agent sees it (isError) and can
// correct itself, unlike a JSON-RPC error.
func (h *handlers) toolError(w http.ResponseWriter, c mcpCall, code, msg string, retryAfter int) {
	e := map[string]any{"code": code, "message": msg}
	if retryAfter > 0 {
		e["retry_after_seconds"] = retryAfter
	}
	text, _ := json.Marshal(map[string]any{"error": e})
	h.mcpResult(w, c, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
		"isError": true,
	}, false)
}

// toolResult answers a tool call with structured content (and the same
// JSON as text, for clients that only read text).
func (h *handlers) toolResult(w http.ResponseWriter, c mcpCall, v any) {
	text, err := json.Marshal(v)
	if err != nil {
		writeRPCError(w, http.StatusOK, c.id, rpcInternalError, "internal error", nil)
		return
	}
	h.mcpResult(w, c, map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": json.RawMessage(text),
	}, false)
}

func (h *handlers) mcpCallTool(w http.ResponseWriter, r *http.Request, c mcpCall) {
	client, _ := clientFrom(r.Context())
	var scope string
	switch c.params.Name {
	case toolSearchProducts:
		scope = apikeys.ScopeSearch
	case toolListCountries:
		scope = apikeys.ScopeCountries
	case "":
		writeRPCError(w, http.StatusOK, c.id, rpcInvalidParams, "params.name (the tool) is required", nil)
		return
	default:
		writeRPCError(w, http.StatusOK, c.id, rpcInvalidParams, "unknown tool: "+c.params.Name, nil)
		return
	}
	if !apikeys.HasScope(client, scope) {
		h.toolError(w, c, CodeForbidden, fmt.Sprintf("this API key lacks the scope %q needed for %s; ask the service owner to add it", scope, c.params.Name), 0)
		return
	}
	if c.params.Name == toolListCountries {
		h.toolListCountries(w, c, client)
		return
	}
	h.toolSearchProducts(w, r, c)
}

func (h *handlers) toolListCountries(w http.ResponseWriter, c mcpCall, client store.Client) {
	var args struct{}
	if err := decodeArgs(c.params.Arguments, &args); err != nil {
		h.toolError(w, c, CodeInvalidRequest, err.Error(), 0)
		return
	}
	countries := []markets.Country{}
	for _, ct := range h.catalog.Countries() {
		if apikeys.AllowedCountry(client, ct.Code) {
			countries = append(countries, ct)
		}
	}
	h.toolResult(w, c, map[string]any{"countries": countries})
}

type searchArgs struct {
	Title   string `json:"title"`
	Country string `json:"country"`
	Refresh bool   `json:"refresh"`
}

// searchOutput is the structured result of search_products.
type searchOutput struct {
	*search.Result
	Quota any `json:"quota,omitempty"`
}

func (h *handlers) toolSearchProducts(w http.ResponseWriter, r *http.Request, c mcpCall) {
	ctx := r.Context()
	var args searchArgs
	if err := decodeArgs(c.params.Arguments, &args); err != nil {
		h.toolError(w, c, CodeInvalidRequest, err.Error(), 0)
		return
	}
	if n := len([]rune(search.NormalizeTitle(args.Title))); n < search.MinTitleLen || n > search.MaxTitleLen {
		h.toolError(w, c, CodeInvalidRequest, fmt.Sprintf("title must be %d-%d characters", search.MinTitleLen, search.MaxTitleLen), 0)
		return
	}
	user, _ := auth.UserFrom(ctx)
	client, _ := clientFrom(ctx)
	if !apikeys.AllowedCountry(client, args.Country) {
		h.toolError(w, c, CodeCountryNotAllowed, "this API key may not search in country "+strconv.Quote(args.Country)+"; list_countries shows the allowed ones", 0)
		return
	}
	if ok, wait := allowCaller(h.limiter, ctx); !ok {
		s := retryAfterSeconds(wait)
		h.toolError(w, c, CodeRateLimited, "too many searches in a short time; retry after "+strconv.Itoa(s)+" s", s)
		return
	}

	out, err := h.searcher.Search(ctx, user, search.Request{Title: args.Title, Country: args.Country, Refresh: args.Refresh})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return // the client went away
		}
		e := searchError(err)
		if errors.Is(err, search.ErrUnknownCountry) {
			e.msg = "unknown country; call list_countries for the codes"
		}
		h.toolError(w, c, e.code, e.msg, e.retryAfter)
		return
	}
	res := *out.Result
	if res.Results == nil {
		res.Results = []search.Product{}
	}
	if res.Markets == nil {
		res.Markets = map[string]string{}
	}
	o := searchOutput{Result: &res}
	if out.QuotaKnown {
		o.Quota = viewQuota(out.Quota)
	}
	h.toolResult(w, c, o)
}

// decodeArgs strictly decodes tool arguments (absent = {}).
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

// validID returns id if it is a JSON-RPC request id (string or number).
func validID(id json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(id, &v) != nil {
		return nil
	}
	switch v.(type) {
	case string, float64:
		return id
	}
	return nil
}

// acceptsJSON reports whether an Accept header allows application/json
// (no header = anything).
func acceptsJSON(accept string) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	for part := range strings.SplitSeq(accept, ",") {
		mt, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		switch strings.ToLower(strings.TrimSpace(mt)) {
		case "application/json", "application/*", "*/*":
			return true
		}
	}
	return false
}

// originAllowed protects against DNS rebinding: a browser request (Origin
// set) must come from the server itself or a configured CORS origin.
func (s *mcpServer) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if slices.Contains(s.origins, "*") || slices.Contains(s.origins, origin) {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// mcpTools are the tool definitions (tools/list). The country codes come
// from the catalog the router was built with.
func mcpTools(cat *markets.Catalog) []map[string]any {
	var codes []string
	for _, c := range cat.Countries() {
		codes = append(codes, c.Code)
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	product := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"market":     str("marketplace id, e.g. amazon, aliexpress, temu, shein"),
			"title":      str("product title as the marketplace shows it"),
			"link":       str("link to the product page (or to the marketplace's search when link_type is \"search\")"),
			"link_type":  str("\"search\" when link opens the marketplace's search instead of a product page"),
			"snippet":    str("short description"),
			"thumbnail":  str("image URL"),
			"price":      map[string]any{"type": "number", "description": "price in currency"},
			"currency":   str("ISO 4217 code of price"),
			"position":   map[string]any{"type": "integer", "description": "rank within its marketplace (1 = first)"},
			"provider":   str("source that found it"),
			"extensions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "extra facts (rating, shipping, …)"},
		},
		"required": []string{"market", "title", "link"},
	}
	readOnly := map[string]any{"readOnlyHint": true, "openWorldHint": true}

	return []map[string]any{
		{
			"name":  toolListCountries,
			"title": "List countries and marketplaces",
			"description": "Lists the countries this API key may search in, with each country's currency and the marketplaces " +
				"searched there. Free: does not count against the daily allowance. Needs the API key scope \"countries\".",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			"outputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{"countries": map[string]any{"type": "array", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code":     str("country code to pass to search_products"),
						"name":     str("country name"),
						"currency": str("local currency (ISO 4217)"),
						"markets": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
							"id": str("marketplace id"), "name": str("marketplace name"), "domain": str("marketplace web site"),
						}}},
					},
					"required": []string{"code", "name"},
				}}},
				"required": []string{"countries"},
			},
			"annotations": readOnly,
		},
		{
			"name":  toolSearchProducts,
			"title": "Search products across marketplaces",
			"description": "Searches the marketplaces of one country for a product and returns the offers (title, price, currency, link) " +
				"per marketplace, plus each marketplace's status (ok, no_results, error). A new search takes 20-60 s; the same search " +
				"within 48 h comes from the cache at once (cached=true, fetched_at says how old the prices are) and is free. " +
				"A live search counts once against the API key's daily allowance (quota shows what is left). Needs the scope \"search\".",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string", "minLength": search.MinTitleLen, "maxLength": search.MaxTitleLen,
						"description": "what to search for, e.g. \"usb c hub\" or \"samsung galaxy s24 case\""},
					"country": map[string]any{"type": "string", "enum": codes,
						"description": "country code from list_countries (marketplaces and prices for that country)"},
					"refresh": map[string]any{"type": "boolean", "default": false,
						"description": "true = ignore the cache and search live (slower, counts against the allowance)"},
				},
				"required":             []string{"title", "country"},
				"additionalProperties": false,
			},
			"outputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":      str("the title searched"),
					"country":    str("the country searched"),
					"results":    map[string]any{"type": "array", "items": product},
					"markets":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "status per marketplace: ok, no_results or error"},
					"took_ms":    map[string]any{"type": "integer"},
					"fetched_at": map[string]any{"type": "string", "format": "date-time", "description": "when the prices were fetched"},
					"cached":     map[string]any{"type": "boolean"},
					"stale":      map[string]any{"type": "boolean", "description": "cached answer past its fresh period (refreshed in the background)"},
					"quota": map[string]any{"type": "object", "description": "the API key's daily allowance after this search", "properties": map[string]any{
						"plan": str("plan"), "limit": map[string]any{"type": "integer"}, "remaining": map[string]any{"type": "integer"},
						"reset": map[string]any{"type": "string", "format": "date-time"},
					}},
				},
				"required": []string{"query", "country", "results", "markets"},
			},
			"annotations": readOnly,
		},
	}
}
