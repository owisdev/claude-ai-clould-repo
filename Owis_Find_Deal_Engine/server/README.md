# owis_find_deal_engine (server)

Go service that searches the marketplaces delivering to a country and
returns one merged list of products. Users sign in through an auth provider
(Google, Apple, Facebook via Firebase/Supabase/Clerk/Auth0) and call the API
with the provider's JWT.

## Request flow

```
app ──(sign in)──► auth provider ──► JWT (RS256/ES256, sub = user id)
app ──POST /api/v1/search, Authorization: Bearer <JWT>──► server
        1. verify JWT signature locally with the provider's JWKS (cached)
        2. check iss, aud, exp, iat; take user id (sub) and plan claim
        3. per-user burst limit            → 429 rate_limited
        4. daily allowance by plan (Redis) → 402 free plan used up
                                             429 paid plan used up
        5. search; failed searches are refunded
```

## Run

Requires Go 1.24+.

```sh
cp .env.example .env    # fill in AUTH_* and SERPAPI_KEY
set -a; . ./.env; set +a
go run ./cmd/owis_find_deal_engine
```

All settings are environment variables; see [`.env.example`](.env.example).

## API

```sh
curl localhost:3002/api/v1/health                 # public
curl localhost:3002/api/v1/countries              # public

curl -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' \
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

Quota headers on every search response: `X-Plan`, `X-RateLimit-Limit`,
`X-RateLimit-Remaining`, `X-RateLimit-Reset` (Unix time, UTC midnight).

Errors always look like `{"error": {"code": "...", "message": "..."}}`:

| Status | code | meaning |
|---|---|---|
| 400 | `invalid_request`, `unsupported_country` | bad input |
| 401 | `unauthorized` | missing, invalid or expired JWT |
| 402 | `payment_required` | free daily searches used up — show upgrade |
| 429 | `rate_limited` | too fast; see `Retry-After` |
| 429 | `quota_exceeded` | paid plan's daily searches used up |
| 502 | `upstream_error` | all marketplaces failed |
| 503 | `service_unavailable` | usage store unreachable (fails closed) |
| 500 | `internal_error` | bug |

## Countries and marketplaces

Defined in [`internal/markets/markets.json`](internal/markets/markets.json)
and embedded in the binary. To add a country or a shop, edit that file (or
point `MARKETS_FILE` at another one); no code changes are needed.

## Layout

```
cmd/owis_find_deal_engine   main: config, wiring, graceful shutdown
internal/config             environment variables
internal/markets            countries -> marketplaces catalog
internal/search             search service: worker pool, merge, ordering
internal/providers/serpapi  SerpApi Google provider
internal/auth               JWT verification + JWKS key cache
internal/auth/authtest      fake auth provider for tests
internal/usage              plans, daily quotas, Redis/memory counters
internal/ratelimit          per-user token bucket
internal/api                router, handlers, middleware
```

## Test

```sh
go vet ./... && go test -race ./...
```
