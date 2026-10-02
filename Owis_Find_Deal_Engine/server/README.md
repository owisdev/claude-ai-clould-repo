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
        4. answer in cache?                → served, FREE (not counted,
                                             even when the allowance is used up)
        5. daily allowance by plan (Redis) → 402 free plan used up
                                             429 paid plan used up
        6. live search; failed searches are not counted
```

## Run

### Full stack in Docker (production-like)

```sh
cp .env.example .env        # AUTH_*, SEARXNG_SECRET, REDIS_PASSWORD (+ SERPAPI_KEY)
docker compose up -d --build
curl localhost:3002/api/v1/health
```

- `server`, `searxng`, `redis` run as non-root users, with read-only file
  systems, all Linux capabilities dropped and memory limits.
- Networks: `edge` (server + SearXNG, internet access) and `data`
  (server + Redis, **internal**: no internet, nothing published).
- Only the API is published, on `127.0.0.1:3002` (`API_BIND`/`API_PORT`).
  In production put Caddy/Nginx or a Cloudflare Tunnel in front for HTTPS.
- Redis needs a password, keeps usage counters across restarts (AOF), and
  under memory pressure evicts cache entries before usage counters.
- `docker compose stop` shuts the server down gracefully.

### Server on your machine (development)

Requires Go 1.26+.

```sh
docker compose -f docker-compose.yml -f compose.dev.yml up -d searxng redis
set -a; . ./.env; set +a    # SEARXNG_URL / REDIS_URL point to 127.0.0.1
go run ./cmd/owis_find_deal_engine
```

## Search providers

`SEARCH_PROVIDERS` lists providers in order; the first that succeeds
answers (default `searxng,serpapi`):

- **SearXNG** (free, self-hosted): metasearch over Google, Bing,
  DuckDuckGo, Brave, Startpage and Mojeek. Config:
  [`deploy/searxng/settings.yml`](deploy/searxng/settings.yml) (JSON output
  on, bot limiter off because only our service calls it).
- **SerpApi** (paid): used only when SearXNG fails or returns nothing
  because its engines were blocked.

Each provider gets `PROVIDER_ATTEMPT_TIMEOUT`; after
`PROVIDER_FAILURE_THRESHOLD` failures in a row it is skipped for
`PROVIDER_COOLDOWN` (circuit breaker), so a broken SearXNG adds no delay.
Every result carries `"provider"` so you can see who answered.

## Cache and price freshness

Searches are cached per country + normalized title (case and spacing do
not matter), in Redis when `REDIS_URL` is set:

| Age of the cached answer | What happens | `X-Cache` |
|---|---|---|
| < `CACHE_FRESH_TTL` (2h) | served from cache | `HIT` |
| up to `CACHE_STALE_TTL` (24h) | served instantly, refreshed in the background for the next user | `STALE` |
| older / not cached | fetched live | `MISS` |

- Results with a failed shop are fresh only 10 min, empty results 30 min.
- `"refresh": true` in the request forces a live fetch (pull to refresh),
  at most once per `CACHE_MIN_REFRESH` (10 min) per query.
- If a live fetch fails but an older answer exists, the older answer is
  returned (`stale: true`) instead of an error.
- Identical searches arriving together make one upstream call.
- Editing `markets.json` changes the cache keys, so old answers are not
  served for a changed shop list.
- Responses carry `fetched_at`, `cached`, `stale`: show "prices updated 3h
  ago" in the app. The shop's page always has the live price.
- **Cached answers are free**: only live searches count against the daily
  allowance, and cached answers are served even when it is used up.

All settings are environment variables; see [`.env.example`](.env.example).

## API

```sh
curl localhost:3002/api/v1/health                 # public
curl localhost:3002/api/v1/countries              # public

curl -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' \
     -d '{"title":"samsung s pen","country":"jor"}' \
     localhost:3002/api/v1/search

# pull to refresh: add "refresh": true
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
  "took_ms": 812,
  "fetched_at": "2026-10-02T12:00:00Z",
  "cached": false,
  "stale": false
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
internal/search/fallback.go provider chain + circuit breaker
internal/cache              stale-while-revalidate cache (Redis / LRU)
internal/metering           what a search costs: cached free, live counted
internal/providers/searxng  SearXNG provider (free)
internal/providers/serpapi  SerpApi Google provider (paid fallback)
internal/auth               JWT verification + JWKS key cache
internal/auth/authtest      fake auth provider for tests
internal/usage              plans, daily quotas, Redis/memory counters
internal/ratelimit          per-user token bucket
internal/api                router, handlers, middleware
deploy/searxng              SearXNG settings
Dockerfile                  distroless, non-root image (~19 MB)
docker-compose.yml          full stack on private networks
compose.dev.yml             dev override: SearXNG + Redis on 127.0.0.1
```

## Test

```sh
gofmt -l . && go vet ./... && go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

CI ([`.github/workflows/server.yml`](../../.github/workflows/server.yml))
runs these on every push touching the server, then validates the compose
files and builds and smoke-tests the Docker image.
