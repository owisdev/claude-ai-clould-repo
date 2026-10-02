# owis_find_deal_engine — Server Plan

Status: **v2** · Scope: **server / service first**, user features after,
client apps later.

## 1. Goal

A backend service that receives a product name and a country, searches the
online shops that deliver to that country, and returns one merged list of
results. Logged-in users later get a saved cart, purchase reports and
notifications. Running cost should stay as close to **$0** as possible,
with a free quota and paid plans for heavier use.

Consumers: mobile app, web app, and later AI agents (as a tool / MCP server).

## 2. Countries and marketplaces ✅ implemented

Supported countries: `usa`, `ksa`, `jor`. Marketplaces: `amazon`, `ebay`,
`aliexpress`, `temu`, `shein`. A search only queries the marketplaces
enabled for the requested country.

| Marketplace | usa | ksa | jor |
|---|---|---|---|
| Amazon     | amazon.com   | amazon.sa    | amazon.com |
| eBay       | ebay.com     | ebay.com     | ❌ no delivery |
| AliExpress | aliexpress.com | aliexpress.com | aliexpress.com |
| Temu       | temu.com     | temu.com     | temu.com |
| SHEIN      | us.shein.com | ar.shein.com | ar.shein.com |

This is configuration, not code:
[`server/internal/markets/markets.json`](server/internal/markets/markets.json).
Adding a country or a shop means editing that file.

## 3. Security model

No API keys for apps: every protected call carries the **user's JWT**.

### 3.1 End-to-end flow ✅ implemented

1. **Sign-in** — the app signs the user in with Google, Apple or Facebook
   through an auth provider (Firebase Auth, Supabase Auth, Clerk or Auth0;
   the server works with any of them).
2. **JWT** — the provider returns a signed JWT whose `sub` is the user id.
3. **API call** — `Authorization: Bearer <JWT>`.
4. **Middleware check** (in the Go service, before any business logic):
   - verify the signature **locally** with the provider's public keys
     (JWKS, RS256/ES256, cached and refreshed hourly; key rotation handled);
   - check `iss`, `aud`, `exp`, `iat`; reject `alg: none` and HMAC tokens;
   - take the user id (`sub`) and the plan (custom claim, default `free`);
   - per-user burst limit (token bucket);
   - daily allowance by plan in **Redis** (atomic increment).
5. **Execute or reject**
   - within limits → counter incremented, search runs; failed searches are
     refunded;
   - free plan used up → **402 Payment Required** (show the upgrade screen);
   - paid plan used up → **429 Too Many Requests** with `Retry-After`;
   - Redis unreachable → **503** (fail closed, protects paid search quota).

Public endpoints: `/health`, `/countries`. Everything else needs a JWT.

Where the plan comes from: a custom claim in the token (e.g. Firebase
custom claims set by a payment webhook). Later it can move to the database;
only the plan lookup changes.

### 3.2 Deployment hardening (step 14)

The JWT check runs **inside** the service, so there is no separate gateway
to secure internally for now. When deploying:

- **Private network**: the Go service listens only on an internal Docker
  network / private subnet. The only public entry point is a reverse proxy
  (Caddy or Nginx) or a **Cloudflare Tunnel** — then the server has no open
  inbound port at all.
- **HTTPS only**, terminated at the proxy / Cloudflare.
- Redis and Postgres on the private network only, with passwords.
- Secrets (SerpApi key, Redis URL) from the host's secret store / env, never
  in the repo.
- **If** a separate API gateway is added later (gateway → backend on
  different machines): authenticate that hop with mTLS or a shared internal
  token, and allow inbound traffic to the backend only from the gateway.
- Optional later: Firebase App Check / Play Integrity / App Attest so only
  the genuine app can call the API.

## 4. Search providers

Every source sits behind one Go interface:

```go
type Provider interface {
    Name() string
    Batch() bool // true: all marketplaces in one call
    Search(ctx context.Context, q Query) ([]Product, error)
}
```

| Provider | Used for | Cost | Status |
|---|---|---|---|
| `serpapi` | site-restricted Google search, any market | 250 free/month, then paid | ✅ implemented |
| `searxng` (self-hosted) | same, free | free | step 4 |
| `ebay` (Browse API) | eBay with real prices | free (5,000 calls/day) | when keys arrive |
| `aliexpress` (Affiliate API) | AliExpress with prices + affiliate links | free | when keys arrive |

Implemented behaviour:
- `SEARCH_COMBINED=true` (default): **one** SerpApi call per search,
  `"<title>" (site:amazon.com OR site:temu.com OR ...)`.
  `false`: one call per marketplace, run in parallel.
- Provider calls run on a **bounded worker pool** (goroutines + buffered
  channels, `SEARCH_MAX_CONCURRENCY`), all under one `context` timeout.
- A failing marketplace never fails the request: `markets` reports
  `ok`/`error` for each one; `502` only when all fail.
- Results are mapped to their marketplace by link domain, numbered per
  marketplace, and interleaved (best result of each shop first).

## 5. Users and features (after the service is finished)

### 5.1 Saved cart
The user saves a search result with notes ("check size", "wait for sale"),
and can list, edit and delete saved items.

### 5.2 Purchase reports
The user reports **marketplace, purchase date, price, currency**, optionally
linked to a saved item.
- When the user opens a product link the app calls `POST /clicks`. About
  24 h later the server creates a friendly "Did you buy it? Tell us the
  price" notification. Filling it in is optional.
- This data later powers price insights.

### 5.3 Notifications
- In-app inbox stored in the database (list, mark read).
- Push via **Firebase Cloud Messaging** (free); the app registers its device
  token with the server.
- v1 types: `purchase_prompt`, `system`. Later: price drops on saved items.

### 5.4 Plans and payments
- Daily quotas per plan are already enforced (section 3.1, `PLANS` env).
- Later: plans in the database, monthly limits, payment provider and a
  webhook that updates the user's plan.

More features may be added here before this phase starts.

## 6. API

All paths under `/api/v1`. Errors: `{"error": {"code", "message"}}`.

| Method | Path | Auth | Status |
|---|---|---|---|
| GET  | `/health` | – | ✅ |
| GET  | `/countries` | – | ✅ |
| POST | `/search` `{"title","country"}` | JWT + quota | ✅ |
| GET/PATCH | `/me` | JWT | step 7 |
| GET/POST | `/cart`, PATCH/DELETE `/cart/{id}` | JWT | step 9 |
| POST | `/clicks` | JWT | step 10 |
| GET/POST | `/purchases` | JWT | step 10 |
| GET | `/notifications`, POST `/notifications/{id}/read` | JWT | step 11 |
| POST | `/devices` | JWT | step 11 |

## 7. Data model (PostgreSQL, from step 6)

```
users          id, firebase_uid (unique), email, country, language, plan_id, created_at
plans          id, name, searches_per_day, searches_per_month, price
usage          user_id, day, searches
saved_items    id, user_id, market, title, link, thumbnail, price, currency, notes, created_at, updated_at
link_clicks    id, user_id, market, link, title, clicked_at, prompted_at
purchases      id, user_id, saved_item_id NULL, market, title, price, currency, purchased_at, created_at
notifications  id, user_id, type, title, body, data JSONB, read_at NULL, created_at
devices        id, user_id, fcm_token (unique), platform, created_at
```

## 8. Layout

```
Owis_Find_Deal_Engine/
├── PLAN.md
├── README.md
├── server/                          # Go service: owis_find_deal_engine
│   ├── cmd/owis_find_deal_engine/   # main: config, wiring, graceful shutdown
│   └── internal/
│       ├── config/                  # environment variables
│       ├── markets/                 # countries -> marketplaces (markets.json)
│       ├── search/                  # worker pool, merge, ordering
│       ├── providers/serpapi/       # + searxng, ebay, aliexpress later
│       ├── auth/                    # JWT verification, JWKS cache
│       ├── usage/                   # plans, daily quotas (Redis / memory)
│       ├── ratelimit/               # per-user token bucket
│       ├── api/                     # router, handlers, middleware
│       ├── store/                   # Postgres (step 6)
│       └── notify/                  # inbox + FCM (step 10)
└── app/                             # client apps (later)
```

## 9. Steps

Each step ends with code that builds, passes `go vet` and `go test -race`,
and is pushed.

**Phase 1 — search service**
1. ✅ Folder + module `owis_find_deal_engine`.
2. ✅ Restructure + best practices: `cmd/` + `internal/` layout; env config
   with validation; structured JSON logs (`slog`) with request IDs; panic
   recovery; strict JSON decoding with body limit; consistent JSON errors;
   HTTP server timeouts; graceful shutdown on SIGINT/SIGTERM; bounded
   worker pool with `context` cancellation and no goroutine leaks; typed
   SerpApi client (no panics on missing fields, API key never in errors or
   logs); CORS by allow-list; tests for every package.
3. ✅ Countries & marketplaces: `markets.json`, `GET /countries`, country
   validation, search limited to the country's marketplaces.
4. SearXNG provider (free) + provider selection/fallback (SearXNG first,
   SerpApi if it fails); `docker-compose` with SearXNG for local dev.
5. Cache: in-memory TTL cache keyed by country + normalized title, with
   request coalescing (`singleflight`) so identical concurrent searches make
   one upstream call.
6. Dockerfile + `docker-compose` (server, SearXNG, Redis, Postgres on an
   internal network); CI running vet + tests on every push.

3b. ✅ Security: JWT auth middleware (JWKS, RS256/ES256), per-user burst
   limit, daily quota by plan in Redis with 402/429, refunds on failure.
   (Replaced the earlier per-app API keys.)

**Phase 2 — users**
7. `users` table (created on first login), `/me`.
8. Plans in the database + payment webhook.
9. Saved cart.
10. Clicks + purchase reports.
11. Notifications: inbox, devices, FCM push, purchase-prompt background job.

**Phase 3 — more sources and reach**
12. eBay provider (usa, ksa) — when keys are approved.
13. AliExpress provider — when affiliate keys are approved.
14. Deploy with the hardening in section 3.2 (see section 10).
15. AI-agent access: OpenAPI spec + MCP tool.

## 10. Hosting (not decided yet)

Nothing to decide until step 14. The service ships as one Docker image, so
any of these work and can be switched later:

| Option | Cost | Notes |
|---|---|---|
| Small VPS (Hetzner, DigitalOcean, ...) | ~$4–6/month | runs server + SearXNG + Postgres together; full control |
| Oracle Cloud Always Free | $0 | free ARM VM; sign-up can be strict |
| Fly.io / Render / Railway | free tier or a few $ | easy deploys; SearXNG needs its own service |
| Postgres: Neon / Supabase free tier | $0 | if the server host has no database |

Recommendation for now: develop locally with `docker-compose` (step 6) and
choose when the first app is ready to test with real users.
