package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"owis_find_deal_engine/internal/apikeys"
	"owis_find_deal_engine/internal/devdocs"
)

// mcpEnv is the public API with API keys made through the admin API.
type mcpEnv struct {
	pub    *testEnv
	create func(body string) string
}

func newMCPEnv(t *testing.T) *mcpEnv {
	t.Helper()
	admin := newAdminEnv(t)
	tok := admin.token(t, "uid-1", "owner@example.com", nil)
	authn := apikeys.NewAuthenticator(admin.clients, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &mcpEnv{
		pub: newEnv(t, envOpts{keys: authn}),
		create: func(body string) string {
			rec, out := admin.do(t, "POST", "/admin/v1/clients", tok, body)
			if rec.Code != 201 {
				t.Fatalf("create %s = %d %v", body, rec.Code, out)
			}
			return out["key"].(string)
		},
	}
}

// post sends one MCP message. headers: name, value pairs.
func (e *mcpEnv) post(key, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.pub.handler.ServeHTTP(rec, req)
	return rec
}

// modern sends a 2026-07-28 request with matching headers.
func (e *mcpEnv) modern(key string, id int, method string, params map[string]any) *httptest.ResponseRecorder {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{metaProtocolVersion: "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	h := []string{"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", method}
	if name, ok := params["name"].(string); ok {
		h = append(h, "Mcp-Name", name)
	}
	return e.post(key, string(body), h...)
}

type rpcOut struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  map[string]any  `json:"result"`
	Error   *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	} `json:"error"`
}

func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) rpcOut {
	t.Helper()
	var out rpcOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %d %q: %v", rec.Code, rec.Body, err)
	}
	if out.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc = %q in %s", out.JSONRPC, rec.Body)
	}
	return out
}

// toolText returns the JSON in a tool result's text block.
func toolText(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", res["content"])
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" {
		t.Fatalf("block = %v", block)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(block["text"].(string)), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func toolErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	out := decodeRPC(t, rec)
	if rec.Code != 200 || out.Result == nil || out.Result["isError"] != true {
		t.Fatalf("want a tool error, got %d %s", rec.Code, rec.Body)
	}
	if _, ok := out.Result["structuredContent"]; ok {
		t.Errorf("tool error with structuredContent: %s", rec.Body)
	}
	e, _ := toolText(t, out.Result)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestMCPAuth(t *testing.T) {
	e := newMCPEnv(t)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

	if rec := e.post("", init); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no key = %d", rec.Code)
	}
	user := e.pub.provider.Token(t, "u1", nil)
	if rec := e.post(user, init); rec.Code != 401 || !strings.Contains(rec.Body.String(), "API key") {
		t.Errorf("login token = %d %s, want 401 (API keys only)", rec.Code, rec.Body)
	}
	bad, _ := apikeys.Generate()
	if rec := e.post(bad, init); rec.Code != 401 {
		t.Errorf("unknown key = %d", rec.Code)
	}
	walled := e.create(`{"name":"walled","scopes":["search"],"ip_allow":["203.0.113.0/24"]}`)
	if rec := e.post(walled, init); rec.Code != 403 {
		t.Errorf("address not allowed = %d", rec.Code)
	}
	// A key with only one of the scopes still connects; tools check theirs.
	lister := e.create(`{"name":"lister","scopes":["countries"]}`)
	if rec := e.post(lister, init); rec.Code != 200 {
		t.Errorf("key with scope countries = %d %s", rec.Code, rec.Body)
	}

	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(m, "/mcp", nil)
		rec := httptest.NewRecorder()
		e.pub.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "POST") {
			t.Errorf("%s /mcp = %d %v, want 405", m, rec.Code, rec.Header())
		}
	}
}

// TestMCPLegacy: the initialize handshake of 2025-03-26 … 2025-11-25.
func TestMCPLegacy(t *testing.T) {
	e := newMCPEnv(t)
	key := e.create(`{"name":"agent","scopes":["search","countries"],"daily_quota":5,"countries":["jor","usa"]}`)

	for requested, want := range map[string]string{"2025-06-18": "2025-06-18", "2025-03-26": "2025-03-26", "2024-11-05": "2025-11-25", "2099-01-01": "2025-11-25"} {
		rec := e.post(key, `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"`+requested+`","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
		out := decodeRPC(t, rec)
		if rec.Code != 200 || out.Result["protocolVersion"] != want || string(out.ID) != `"a"` {
			t.Errorf("initialize %s = %d %s, want %s", requested, rec.Code, rec.Body, want)
		}
		if rec.Header().Get("Mcp-Session-Id") != "" {
			t.Error("a session id was issued")
		}
		info, _ := out.Result["serverInfo"].(map[string]any)
		caps, _ := out.Result["capabilities"].(map[string]any)
		if info["name"] != "owis_find_deal_engine" || caps["tools"] == nil || out.Result["instructions"] == "" {
			t.Errorf("initialize result = %v", out.Result)
		}
		if _, ok := out.Result["resultType"]; ok {
			t.Error("legacy answer with resultType")
		}
	}

	rec := e.post(key, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "MCP-Protocol-Version", "2025-06-18")
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("notification = %d %q", rec.Code, rec.Body)
	}
	if rec := e.post(key, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, "MCP-Protocol-Version", "2025-06-18"); rec.Code != 200 || decodeRPC(t, rec).Result == nil {
		t.Errorf("ping = %d %s", rec.Code, rec.Body)
	}

	// tools/list, without the header too (= 2025-03-26).
	for _, h := range [][]string{{"MCP-Protocol-Version", "2025-11-25"}, nil} {
		rec := e.post(key, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, h...)
		out := decodeRPC(t, rec)
		tools, _ := out.Result["tools"].([]any)
		if rec.Code != 200 || len(tools) != 2 {
			t.Fatalf("tools/list = %d %s", rec.Code, rec.Body)
		}
		var names []string
		for _, tl := range tools {
			names = append(names, tl.(map[string]any)["name"].(string))
		}
		if !slices.Equal(names, []string{toolListCountries, toolSearchProducts}) {
			t.Errorf("tools = %v", names)
		}
	}

	call := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_products","arguments":{"title":"s pen","country":"jor"}}}`
	rec = e.post(key, call, "MCP-Protocol-Version", "2025-06-18")
	out := decodeRPC(t, rec)
	if rec.Code != 200 || out.Result == nil || out.Result["isError"] == true {
		t.Fatalf("search_products = %d %s", rec.Code, rec.Body)
	}
	sc, _ := out.Result["structuredContent"].(map[string]any)
	results, _ := sc["results"].([]any)
	quota, _ := sc["quota"].(map[string]any)
	if len(results) != 1 || sc["country"] != "jor" || quota["remaining"] != float64(4) || quota["plan"] != "api" {
		t.Errorf("structured result = %v", sc)
	}
	if text := toolText(t, out.Result); text["query"] != "s pen" {
		t.Errorf("text block = %v", text)
	}

	if rec := e.post(key, `{"jsonrpc":"2.0","id":5,"method":"resources/list"}`, "MCP-Protocol-Version", "2025-06-18"); rec.Code != 200 || decodeRPC(t, rec).Error.Code != rpcMethodNotFound {
		t.Errorf("unknown method (legacy) = %d %s", rec.Code, rec.Body)
	}
	// Unknown legacy version in the header.
	rec = e.post(key, `{"jsonrpc":"2.0","id":6,"method":"tools/list"}`, "MCP-Protocol-Version", "2024-01-01")
	if out := decodeRPC(t, rec); rec.Code != 400 || out.Error.Code != rpcUnsupportedVersion || out.Error.Data["requested"] != "2024-01-01" {
		t.Errorf("unsupported version = %d %s", rec.Code, rec.Body)
	}
	// The modern version in the header needs _meta in the body.
	rec = e.post(key, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`, "MCP-Protocol-Version", "2026-07-28")
	if out := decodeRPC(t, rec); rec.Code != 400 || out.Error.Code != rpcHeaderMismatch {
		t.Errorf("modern header without _meta = %d %s", rec.Code, rec.Body)
	}
}

// TestMCPModern: the stateless 2026-07-28 protocol.
func TestMCPModern(t *testing.T) {
	e := newMCPEnv(t)
	key := e.create(`{"name":"agent","scopes":["search","countries"],"countries":["jor"]}`)

	rec := e.modern(key, 1, "server/discover", nil)
	out := decodeRPC(t, rec)
	versions, _ := out.Result["supportedVersions"].([]any)
	meta, _ := out.Result["_meta"].(map[string]any)
	if rec.Code != 200 || len(versions) != 4 || versions[0] != "2026-07-28" || out.Result["resultType"] != "complete" ||
		out.Result["ttlMs"] == nil || out.Result["cacheScope"] != "public" || meta[metaServerInfo] == nil {
		t.Fatalf("server/discover = %d %s", rec.Code, rec.Body)
	}

	rec = e.modern(key, 2, "tools/list", nil)
	out = decodeRPC(t, rec)
	tools, _ := out.Result["tools"].([]any)
	if rec.Code != 200 || len(tools) != 2 || out.Result["resultType"] != "complete" || out.Result["cacheScope"] != "public" {
		t.Fatalf("tools/list = %d %s", rec.Code, rec.Body)
	}
	search := tools[1].(map[string]any)
	enum := search["inputSchema"].(map[string]any)["properties"].(map[string]any)["country"].(map[string]any)["enum"].([]any)
	if len(enum) != 3 || enum[0] != "jor" || search["outputSchema"] == nil {
		t.Errorf("search_products schema = %v", search)
	}

	rec = e.modern(key, 3, "tools/call", map[string]any{"name": "search_products", "arguments": map[string]any{"title": "s pen", "country": "jor"}})
	out = decodeRPC(t, rec)
	if rec.Code != 200 || out.Result["resultType"] != "complete" || out.Result["isError"] == true || out.Result["structuredContent"] == nil {
		t.Fatalf("tools/call = %d %s", rec.Code, rec.Body)
	}

	// list_countries shows only the key's countries.
	rec = e.modern(key, 4, "tools/call", map[string]any{"name": "list_countries"})
	out = decodeRPC(t, rec)
	countries, _ := out.Result["structuredContent"].(map[string]any)["countries"].([]any)
	if rec.Code != 200 || len(countries) != 1 || countries[0].(map[string]any)["code"] != "jor" {
		t.Errorf("list_countries = %d %s", rec.Code, rec.Body)
	}

	// Base64 form of Mcp-Name.
	body := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_countries","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	enc := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("list_countries")) + "?="
	if rec := e.post(key, body, "MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call", "Mcp-Name", enc); rec.Code != 200 {
		t.Errorf("base64 Mcp-Name = %d %s", rec.Code, rec.Body)
	}

	mismatches := map[string][]string{
		"no version header": {"Mcp-Method", "tools/call", "Mcp-Name", "list_countries"},
		"other version":     {"MCP-Protocol-Version", "2025-11-25", "Mcp-Method", "tools/call", "Mcp-Name", "list_countries"},
		"no method":         {"MCP-Protocol-Version", "2026-07-28", "Mcp-Name", "list_countries"},
		"other method":      {"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/list", "Mcp-Name", "list_countries"},
		"no name":           {"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call"},
		"other name":        {"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call", "Mcp-Name", "search_products"},
		"bad base64":        {"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call", "Mcp-Name", "=?base64?!!?="},
	}
	for name, h := range mismatches {
		rec := e.post(key, body, h...)
		if out := decodeRPC(t, rec); rec.Code != 400 || out.Error == nil || out.Error.Code != rpcHeaderMismatch || string(out.ID) != "5" {
			t.Errorf("%s = %d %s, want 400 -32020", name, rec.Code, rec.Body)
		}
	}

	old := strings.Replace(body, "2026-07-28", "1900-01-01", 1)
	rec = e.post(key, old, "MCP-Protocol-Version", "1900-01-01", "Mcp-Method", "tools/call", "Mcp-Name", "list_countries")
	out = decodeRPC(t, rec)
	if rec.Code != 400 || out.Error.Code != rpcUnsupportedVersion || out.Error.Data["requested"] != "1900-01-01" || len(out.Error.Data["supported"].([]any)) != 4 {
		t.Errorf("unsupported version = %d %s", rec.Code, rec.Body)
	}

	if rec := e.modern(key, 6, "resources/list", nil); rec.Code != 404 || decodeRPC(t, rec).Error.Code != rpcMethodNotFound {
		t.Errorf("unknown method (modern) = %d %s, want 404 -32601", rec.Code, rec.Body)
	}
	if rec := e.modern(key, 7, "tools/call", map[string]any{"name": "delete_everything"}); decodeRPC(t, rec).Error.Code != rpcInvalidParams {
		t.Errorf("unknown tool = %d %s", rec.Code, rec.Body)
	}
}

func TestMCPToolErrors(t *testing.T) {
	e := newMCPEnv(t)
	key := e.create(`{"name":"agent","scopes":["search"],"daily_quota":1,"countries":["jor"]}`)
	call := func(name string, args any) *httptest.ResponseRecorder {
		return e.modern(key, 1, "tools/call", map[string]any{"name": name, "arguments": args})
	}

	cases := map[string]struct {
		tool string
		args any
		code string
	}{
		"no scope countries":   {toolListCountries, nil, CodeForbidden},
		"country not allowed":  {toolSearchProducts, map[string]any{"title": "s pen", "country": "usa"}, CodeCountryNotAllowed},
		"unknown argument":     {toolSearchProducts, map[string]any{"title": "s pen", "country": "jor", "price": 1}, CodeInvalidRequest},
		"title too short":      {toolSearchProducts, map[string]any{"title": "s", "country": "jor"}, CodeInvalidRequest},
		"no title":             {toolSearchProducts, map[string]any{"country": "jor"}, CodeInvalidRequest},
		"arguments not object": {toolSearchProducts, "s pen", CodeInvalidRequest},
	}
	for name, c := range cases {
		if code := toolErrorCode(t, call(c.tool, c.args)); code != c.code {
			t.Errorf("%s: code = %q, want %q", name, code, c.code)
		}
	}

	// The daily allowance: one live search, then quota_exceeded with a
	// retry hint; the cached answer of the same search stays free (here the
	// fake searcher has no cache, so any further live search is refused).
	if rec := call(toolSearchProducts, map[string]any{"title": "s pen", "country": "jor"}); decodeRPC(t, rec).Result["isError"] == true {
		t.Fatalf("first search = %s", rec.Body)
	}
	rec := call(toolSearchProducts, map[string]any{"title": "usb hub", "country": "jor"})
	if code := toolErrorCode(t, rec); code != CodeQuotaExceeded {
		t.Errorf("over the allowance = %q", code)
	}
	if e := toolText(t, decodeRPC(t, rec).Result)["error"].(map[string]any); e["retry_after_seconds"] == nil {
		t.Errorf("no retry hint: %v", e)
	}
	// The same counter as the REST API.
	if rec := e.pub.do("POST", "/api/v1/search", key, `{"title":"usb hub","country":"jor"}`); rec.Code != 429 {
		t.Errorf("REST after MCP used the allowance = %d", rec.Code)
	}
}

func TestMCPToolRateLimit(t *testing.T) {
	e := newMCPEnv(t)
	key := e.create(`{"name":"agent","scopes":["search","countries"],"rate_limit_rps":0.01,"rate_limit_burst":1}`)
	args := map[string]any{"title": "s pen", "country": "jor"}
	if rec := e.modern(key, 1, "tools/call", map[string]any{"name": "search_products", "arguments": args}); decodeRPC(t, rec).Result["isError"] == true {
		t.Fatalf("first = %s", rec.Body)
	}
	if code := toolErrorCode(t, e.modern(key, 2, "tools/call", map[string]any{"name": "search_products", "arguments": args})); code != CodeRateLimited {
		t.Errorf("second = %q, want rate_limited", code)
	}
	// Other messages are not limited by the search burst.
	if rec := e.modern(key, 3, "tools/call", map[string]any{"name": "list_countries"}); decodeRPC(t, rec).Result["isError"] == true {
		t.Errorf("list_countries after the burst = %s", rec.Body)
	}
}

func TestMCPTransport(t *testing.T) {
	e := newMCPEnv(t)
	key := e.create(`{"name":"agent","scopes":["search"]}`)
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	cases := map[string]struct {
		body    string
		headers []string
		status  int
		code    int
	}{
		"batch":         {"[" + list + "]", nil, 400, rpcInvalidRequest},
		"not JSON":      {"{", nil, 400, rpcParseError},
		"wrong jsonrpc": {`{"jsonrpc":"1.0","id":1,"method":"tools/list"}`, nil, 400, rpcInvalidRequest},
		"no method":     {`{"jsonrpc":"2.0","id":1}`, nil, 400, rpcInvalidRequest},
		"null id":       {`{"jsonrpc":"2.0","id":null,"method":"tools/list"}`, nil, 400, rpcInvalidRequest},
		"params array":  {`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":[1]}`, nil, 400, rpcInvalidParams},
		"bad origin":    {list, []string{"Origin", "https://evil.example"}, 403, rpcInvalidRequest},
		"accepts html":  {list, []string{"Accept", "text/html"}, 406, rpcInvalidRequest},
		"text body":     {list, []string{"Content-Type", "text/plain"}, 415, rpcParseError},
		"too big":       {`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"x":"` + strings.Repeat("a", maxBodyBytes) + `"}}`, nil, 400, rpcParseError},
	}
	for name, c := range cases {
		rec := e.post(key, c.body, c.headers...)
		if out := decodeRPC(t, rec); rec.Code != c.status || out.Error == nil || out.Error.Code != c.code {
			t.Errorf("%s = %d %s, want %d %d", name, rec.Code, rec.Body, c.status, c.code)
		}
	}

	for name, h := range map[string][]string{
		"own origin":  {"Origin", "http://example.com"}, // httptest's Host
		"CORS origin": {"Origin", "https://app.example"},
	} {
		if rec := e.post(key, list, h...); rec.Code != 200 {
			t.Errorf("%s = %d %s", name, rec.Code, rec.Body)
		}
	}
	// A response sent by the client is accepted and ignored.
	if rec := e.post(key, `{"jsonrpc":"2.0","id":9,"result":{}}`); rec.Code != 202 {
		t.Errorf("client response = %d", rec.Code)
	}
}

func TestDeveloperDocs(t *testing.T) {
	e := newEnv(t, envOpts{})

	rec := e.do(http.MethodGet, "/docs/", "", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") ||
		!strings.Contains(rec.Body.String(), "/mcp") {
		t.Errorf("docs page = %d %v", rec.Code, rec.Header())
	}
	if rec := e.do(http.MethodGet, "/docs", "", ""); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/docs/" {
		t.Errorf("/docs = %d %v", rec.Code, rec.Header())
	}
	if rec := e.do(http.MethodGet, "/docs/docs.css", "", ""); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("stylesheet = %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/docs/../openapi.json", "", ""); rec.Code == 200 {
		t.Errorf("path outside the page served: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/docs/other", "", ""); rec.Code != 404 {
		t.Errorf("unknown docs file = %d", rec.Code)
	}

	rec = e.do(http.MethodGet, "/api/v1/openapi.json", "", "")
	var spec struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
		Webhook map[string]any            `json:"webhooks"`
		Comp    struct {
			Schemas map[string]struct {
				Enum []string `json:"enum"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &spec) != nil || spec.OpenAPI != "3.1.0" {
		t.Fatalf("openapi.json = %d", rec.Code)
	}

	// Every public route is described.
	for path, method := range map[string]string{
		"/api/v1/health": "get", "/api/v1/countries": "get", "/api/v1/search": "post",
		"/api/v1/searches": "post", "/api/v1/searches/{id}": "get", "/mcp": "post", "/api/v1/openapi.json": "get",
	} {
		if spec.Paths[path][method] == nil {
			t.Errorf("spec misses %s %s", method, path)
		}
	}
	if spec.Webhook["searchFinished"] == nil {
		t.Error("spec misses the webhook")
	}
	// Every error code the API answers is in the spec.
	codes := spec.Comp.Schemas["ErrorCode"].Enum
	for _, c := range []string{CodeInvalidRequest, CodeUnsupportedCountry, CodeCountryNotAllowed, CodeUnauthorized, CodeForbidden,
		CodeNotFound, CodeRateLimited, CodeQuotaExceeded, CodePaymentRequired, CodeTooManyJobs, CodeWebhookNotConfigured,
		CodeJobLost, CodeTimeout, CodeUnavailable, CodeUpstream, CodeInternal} {
		if !slices.Contains(codes, c) {
			t.Errorf("ErrorCode enum misses %q", c)
		}
	}
	// All internal references resolve.
	raw, _ := devdocs.Bytes("openapi.json")
	var doc any
	_ = json.Unmarshal(raw, &doc)
	checkRefs(t, doc, doc)
}

// checkRefs fails for a "$ref" that does not point into root.
func checkRefs(t *testing.T, root, v any) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			cur := root
			for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
				m, _ := cur.(map[string]any)
				if cur = m[part]; cur == nil {
					t.Errorf("unresolved $ref %s", ref)
					break
				}
			}
		}
		for _, x := range v {
			checkRefs(t, root, x)
		}
	case []any:
		for _, x := range v {
			checkRefs(t, root, x)
		}
	}
}
