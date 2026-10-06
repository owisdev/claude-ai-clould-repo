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
   - answer already cached → served **free** (not counted), even when the
     allowance is used up;
   - within limits → counter incremented, live search runs; failed
     searches are refunded;
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

- **Private network** (✅ in `docker-compose.yml`): the Go service listens
  only on Docker networks; Redis is on an internal network. The only public entry point is a reverse proxy
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
| `serpapi-amazon` | **Amazon** via SerpApi's Amazon engine: price, rating, direct `/dp/` link | 1 SerpApi search per live search | ✅ default when `SERPAPI_KEY` is set (`SERPAPI_MARKETS`) |
| `apify` | AliExpress / Temu / SHEIN scrapers from the Apify store (real prices, direct item links), per marketplace | $5 free credit/month, then ~$0.7–5 / 1,000 results | ✅ optional (`APIFY_MARKETS`; AliExpress tested) |
| `serpapi` | **Google Shopping** (default) or Google web search, any market — see the comparison below | 250 free/month, then ~$25 / 1,000 | ✅ fallback for the shops above |
| `searxng` (self-hosted) | site-restricted metasearch, any market | free | ✅ last fallback only: search engines block it quickly from one IP (seen in the local test) |
| `ebay` (Browse API) | eBay with real prices | free (5,000 calls/day) | when keys arrive |
| `aliexpress` (Affiliate API) | AliExpress with prices + affiliate links | free | when keys arrive |

Implemented behaviour:
- `SEARCH_COMBINED=true` (default): **one** SerpApi call per search,
  `"<title>" (site:amazon.com OR site:temu.com OR ...)`.
  `false`: one call per marketplace, run in parallel.
- **Fallback chain** (`SEARCH_PROVIDERS=searxng,serpapi`): the first
  provider that succeeds answers; each attempt has its own timeout, and a
  provider failing repeatedly is skipped for a cooldown (circuit breaker).
- Provider calls run on a **bounded worker pool** (goroutines + buffered
  channels, `SEARCH_MAX_CONCURRENCY`), all under one `context` timeout.
- A failing marketplace never fails the request: `markets` reports
  `ok`/`error` for each one; `502` only when all fail.
- Results are mapped to their marketplace by link domain (Google
  Shopping: by seller name), numbered per marketplace, and interleaved
  (best result of each shop first).
- **Google Shopping** (`SERPAPI_ENGINE=google_shopping`): one query for
  all shops (no `site:` support there); offers from other sellers are
  dropped; if nothing from our shops is found, one web search retry.
- **Per-marketplace providers**: shops listed in `APIFY_MARKETS` use
  their own chain `Apify scraper → web providers`, with `APIFY_TIMEOUT`
  per attempt (the search timeout is raised to fit it). The scraper and
  its input are configuration, so a broken scraper can be swapped without
  a code change.

### SerpApi engine: `google_shopping` (current) vs `google`

`SERPAPI_ENGINE` picks what the SerpApi fallback searches. Decision (local
test, October 2026): **keep `google_shopping`**. Amazon and AliExpress have
dedicated sources with direct links and prices, so SerpApi only answers
when those fail and for shops without one.

| | `google_shopping` (current) | `google` (web search) |
|---|---|---|
| Price | ✅ almost always (structured) | ✗ rarely (only when Google shows a rich snippet) |
| Image | ✅ | sometimes |
| Link | ✗ no direct item link: Google's own offer pages (`google.com/search?ibp=oshop…`) only open inside a Google session, so the service links to **the shop's search for that product** (`"link_type": "search"`) | ✅ the shop's product page (`/item/…`, `/dp/…`, `-g-….html`, `-p-….html`), filtered by `product_pages` |
| Shops covered | shops Google Shopping lists for the region; for Jordan (`shopping_region: us`) mostly AliExpress, rarely Temu / SHEIN | any shop Google indexes, via `site:` |
| Relevance | Google may list merely similar products; the relevance filter drops most | `site:` keeps results on the shop; the relevance and product-page filters drop the rest |
| Country | not every country (no Jordan → US listings) | every country (`gl=jo` works) |
| Cost per live search | 1 search (+0 when a free provider follows: no web retry) | 1 search with `SEARCH_COMBINED=true` (one `site:a OR site:b` query, big shops can crowd out small ones); 1 per shop with `false` |
| Speed | 5–15 s uncached (`SERPAPI_TIMEOUT=25s`) | 1–3 s |

Switch with `SERPAPI_ENGINE=google` in `.env`; no code change needed.

### Tools evaluated (October 2026)

| Tool | Verdict |
|---|---|
| Apify | ✅ added (optional) for Temu / SHEIN, which have no official API |
| Google Shopping (via SerpApi) | ✅ added as SerpApi's default engine |
| Google Lens (via SerpApi) | ⏳ future feature — see section 5.5 |
| ShoppingScraper | ✗ from €49/month, no Temu / SHEIN / AliExpress, built for retailers |
| AfterShip | ✗ for search (shipment tracking); maybe phase 2 for delivery tracking |
| ChatGPT shopping | ✗ no API for its results; we reach assistants via step 15 instead |

## 4b. Cache and price freshness ✅ implemented

Goal: popular searches cost nothing, while prices shown stay reasonably
current. Strategy, in layers:

1. **Stale-while-revalidate** (per country + normalized title):
   fresh for 2 h → served from cache; 2–24 h → served instantly *and*
   refreshed in the background so the next user gets new prices; > 24 h →
   fetched live. Identical concurrent searches share one upstream call.
2. **Shorter life for doubtful answers**: a result where a shop failed is
   fresh 10 min, an empty result 30 min.
3. **User-driven refresh**: `"refresh": true` (pull to refresh) fetches
   live, at most once per 10 min per query, so it cannot be abused.
4. **Stale-if-error**: if a live fetch fails, the last good answer is
   returned (marked stale) instead of an error.
5. **Transparency**: `fetched_at` / `stale` in every response; the app
   shows "prices updated X ago", and the product link always opens the
   shop with its live price.
6. **Automatic invalidation** when `markets.json` changes (the shop list
   is part of the cache key).
7. **Cached answers are free** for users: only live searches count against
   the daily allowance (they are the only ones that cost us anything).

Later (phase 2+):
- **Saved-item price tracker**: a scheduled job re-checks items in users'
  saved carts (e.g. every 6–12 h, popular ones more often) and sends a
  price-drop notification. Uses the same cache, so shared items are
  fetched once.
- **Pre-warm** the most searched queries per country before they expire.
- **Shop APIs with real prices** (eBay, AliExpress) get their own, shorter
  TTL since their prices are exact.
- **Purchase reports** feed back real paid prices per shop.

## 4c. Background jobs and queues (phase 2)

The search request itself stays **synchronous**: the user waits for
results, and its robustness comes from the fallback chain, circuit
breaker, cache (stale-if-error) and request merging, not from a queue.

A queue becomes useful for work that does not need to answer the user
immediately:

| Job | Trigger |
|---|---|
| saved-item price tracker | schedule (every 6–12 h) |
| price-drop / "did you buy it?" notifications | tracker, link clicks |
| pre-warming popular searches | schedule |
| analytics events (searches, clicks, purchases) | every request |

Choice: start with a **Redis-backed job queue** (e.g. `asynq`, or Redis
Streams) because Redis is already deployed: retries with backoff,
scheduled jobs, dead-letter queue, at no extra cost. Jobs go through a
small Go interface, so moving to NATS JetStream or Kafka later is a
swap of one adapter. Kafka is worth it only at high event volume (many
services consuming the same event stream); for this service it would
add a cluster to run and pay for without a matching benefit today.

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

### 5.6 App login: Firebase email links (reminder)

Only relevant when the app offers **email** sign-in; Google, Apple and
Facebook sign-in do not use email links, and the search service itself is
not affected (it only verifies ID tokens with the public JWKS).

- Firebase Dynamic Links are shut down. Email action links (verify
  email, reset password, passwordless sign-in link) must use the
  **Firebase Hosting domain** instead, e.g.
  `owis-find-deal-engine.firebaseapp.com` (or a verified custom domain).
- One-time project setting (Admin SDK `projectConfigManager()
  .updateProjectConfig({ mobileLinksConfig: { domain: ... } })`, or the
  equivalent console/REST setting).
- Wherever action links are created (preferably the app via the Firebase
  client SDK, so Firebase sends the emails), set `linkDomain` in
  `ActionCodeSettings`, never the deprecated `dynamicLinkDomain`; add the
  iOS bundle ID and Android package name.
- Only if our backend ever generates these links itself: use a current
  Admin SDK that supports `linkDomain`, and send the emails through our
  own mail service.
- Check the domain is in Firebase Auth → Settings → Authorized domains.

### 5.5 Future features (noted, not planned yet)

- **Search by photo — Google Lens.** The user takes or uploads a photo of
  a product; the service finds it and returns offers from the shops of
  their country. Implementation sketch: `POST /api/v1/search/image`
  (image upload, size-limited) → SerpApi `engine=google_lens` → product
  name + matching offers → the same shop filtering, cache (keyed by image
  hash), metering and response format as text search. Same SerpApi
  account; counts as a live search. Best added together with the mobile
  app (camera), after phase 2.

- **Direct links for Google Shopping results, resolved on tap.** Google
  Shopping gives prices but no direct shop link (see section 4). SerpApi
  can resolve an offer to its shop link (Google immersive product API), but
  at **one search per product** — doing it for every result (10 products =
  10 searches) is ruled out. Cheaper later option: resolve only when a user
  taps a result (`GET /api/v1/link?offer=…`): one search per tap, the
  resolved link cached for everyone; until then the result keeps the
  shop-search link. Only worth it if Google Shopping becomes a main source
  again.

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
│       ├── search/                  # worker pool, merge, fallback chain
│       ├── cache/                   # stale-while-revalidate search cache
│       ├── metering/                # cached answers free, live searches counted
│       ├── providers/               # searxng, serpapi (+ ebay, aliexpress later)
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
4. ✅ SearXNG provider (free) + fallback chain (SearXNG first, SerpApi if
   it fails) with per-attempt timeout and circuit breaker;
   `docker-compose` with SearXNG + Redis for local dev.
5. ✅ Cache: stale-while-revalidate in Redis (or in-memory LRU), request
   coalescing, pull-to-refresh, stale-if-error (section 4b).
6. ✅ Distroless non-root Docker image; `docker-compose` with server,
   SearXNG and Redis on private networks (Redis on an internal network,
   only the API published, all containers read-only, no capabilities);
   GitHub Actions: gofmt, mod tidy, vet, race tests, govulncheck, compose
   validation, image build + smoke test. Go 1.26 (supported release).
   Postgres joins the stack in phase 2.

3b. ✅ Security: JWT auth middleware (JWKS, RS256/ES256), per-user burst
   limit, daily quota by plan in Redis with 402/429, refunds on failure.
   (Replaced the earlier per-app API keys.)

**Phase 2 — users**
7. `users` table (created on first login), `/me`. App email sign-in:
   see the Firebase email-link reminder in section 5.6.
8. Plans in the database + payment webhook.
9. Saved cart + saved-item price tracker (section 4b).
10. Clicks + purchase reports.
11. Notifications: inbox, devices, FCM push, purchase-prompt background job
    (Redis job queue, section 4c).

**Phase 3 — more sources and reach**
12. eBay provider (usa, ksa) — when keys are approved.
13. AliExpress provider — when affiliate keys are approved.
14. Deploy with the hardening in section 3.2 (see section 10).
    Monitoring: export metrics (searches, cache hits, provider errors,
    circuit state) and alerts. At this point replace the hand-written
    circuit breaker in `internal/search/fallback.go` with
    `github.com/sony/gobreaker` (v2): its `OnStateChange` callback and
    `Counts` feed the metrics/alerts, and `ReadyToTrip` allows a
    failure-ratio rule (e.g. 50% of the last 20 calls) instead of only
    "N failures in a row" once there are more sources and real traffic.
    Small change: the breaker is private to `fallback.go` behind
    allow/success/failure (~30 lines + its test); check gobreaker's Go
    version requirement and API at that time.
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
