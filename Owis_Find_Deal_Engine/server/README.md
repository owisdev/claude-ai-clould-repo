# owis_find_deal_engine (server)

Go service that searches the marketplaces delivering to a country and
returns one merged list of products.

## Run

Requires Go 1.22+.

```sh
go run ./cmd/keygen mobile          # prints an API key and an API_KEYS entry
export API_KEYS='mobile:<hash>'
export SERPAPI_KEY='<your SerpApi key>'
go run ./cmd/owis_find_deal_engine
```

All settings are environment variables; see [`.env.example`](.env.example).

## API

Every request except `/health` needs the header `X-API-Key: <key>`.

```sh
curl localhost:3002/api/v1/health

curl -H "X-API-Key: $KEY" localhost:3002/api/v1/countries

curl -H "X-API-Key: $KEY" -H 'Content-Type: application/json' \
     -d '{"title":"samsung s pen","country":"jor"}' \
     localhost:3002/api/v1/search
```

Search response:

```json
{
  "query": "samsung s pen",
  "country": "jor",
  "results": [
    {"market": "amazon", "title": "...", "link": "...", "snippet": "...",
     "price": 29.99, "currency": "$", "position": 1, "provider": "serpapi"}
  ],
  "markets": {"amazon": "ok", "aliexpress": "ok", "temu": "ok", "shein": "error"},
  "took_ms": 812
}
```

`markets` reports each marketplace searched. The request still succeeds if
some fail; it returns `502` only when all of them fail.

Errors always look like `{"error": {"code": "...", "message": "..."}}`:

| Status | code |
|---|---|
| 400 | `invalid_request`, `unsupported_country` |
| 401 | `unauthorized` |
| 429 | `rate_limited` (see `Retry-After`) |
| 502 | `upstream_error` |
| 500 | `internal_error` |

## Countries and marketplaces

Defined in [`internal/markets/markets.json`](internal/markets/markets.json)
and embedded in the binary. To add a country or a shop, edit that file (or
point `MARKETS_FILE` at another one); no code changes are needed.

## Layout

```
cmd/owis_find_deal_engine   main: config, wiring, graceful shutdown
cmd/keygen                  creates client API keys
internal/config             environment variables
internal/markets            countries -> marketplaces catalog
internal/search             search service: worker pool, merge, ordering
internal/providers/serpapi  SerpApi Google provider
internal/auth               API key store (SHA-256 hashes only)
internal/ratelimit          per-client token bucket
internal/api                router, handlers, middleware
```

## Test

```sh
go vet ./... && go test -race ./...
```
