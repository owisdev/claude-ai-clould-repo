# owis_find_deal_engine (server)

Go service that searches the marketplaces delivering to a country and
returns one merged list of products. Users sign in through an auth provider
(Google, Apple, Facebook via Firebase/Supabase/Clerk/Auth0) and call the API
with the provider's JWT. Own servers and AI agents call the same API with
an **API key** made in the admin panel (see "API clients and keys").

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

agent / server ──POST /api/v1/search, Authorization: Bearer ofd_live_…──► server
        1. key looked up by its SHA-256 hash (cached 10 s); unknown,
           revoked or expired                → 401 unauthorized
        2. allowed address, scope "search"  → 403 forbidden
        3. allowed country                  → 403 country_not_allowed
        4. the client's own burst limit     → 429 rate_limited
        5. as 4.–6. above, with the client's own daily quota
           (used up → 429 quota_exceeded, never 402)
```

## Run

### Full stack in Docker (production-like)

```sh
cp .env.example .env        # AUTH_*, SEARXNG_SECRET, REDIS_PASSWORD, POSTGRES_PASSWORD,
                            # SETTINGS_ENCRYPTION_KEY (+ SERPAPI_KEY)
docker compose up -d --build
curl localhost:3002/api/v1/health
```

- `server`, `searxng`, `redis`, `postgres` run as non-root users, with
  read-only file systems, all Linux capabilities dropped and memory limits.
- Networks: `edge` (server + SearXNG, internet access) and `data`
  (server + Redis + Postgres, **internal**: no internet, nothing published).
- Postgres holds the runtime settings (see [Settings](#settings)); its
  data is in the `postgres-data` volume (backups: phase 4).
- Only the API is published, on `127.0.0.1:3002` (`API_BIND`/`API_PORT`).
  In production put Caddy/Nginx or a Cloudflare Tunnel in front for HTTPS.
- Redis needs a password, keeps usage counters across restarts (AOF), and
  under memory pressure evicts cache entries before usage counters.
- `docker compose stop` shuts the server down gracefully.

### Server on your machine (development)

Requires Go 1.26+.

```sh
docker compose -f docker-compose.yml -f compose.dev.yml up -d searxng redis postgres
set -a; . ./.env; set +a    # SEARXNG_URL / REDIS_URL / DATABASE_URL point to 127.0.0.1
go run ./cmd/owis_find_deal_engine
```

## Settings

Two kinds ([`internal/config/keys.go`](internal/config/keys.go)):

- **bootstrap** — environment (`.env`) only, fixed while the server runs:
  `PORT`, `DATABASE_URL`, `SETTINGS_ENCRYPTION_KEY`, `SETTINGS_POLL_INTERVAL`,
  `ADMIN_EMAIL`, `ADMIN_REQUIRE_2FA`, `ADMIN_REQUIRE_VERIFIED_EMAIL`, `ADMIN_SESSION_MAX_AGE`,
  `ADMIN_FIREBASE_API_KEY`, `ADMIN_FIREBASE_AUTH_DOMAIN`, `AUTH_*`, `REDIS_URL`, `SEARXNG_URL`, `*_BASE_URL`,
  `MARKETS_FILE`, `CACHE_MAX_ENTRIES`, `LOG_LEVEL`, `SHUTDOWN_TIMEOUT`;
- **runtime** — everything about searches: countries and shops
  (`MARKETS`), providers, SerpApi / Apify sources per shop
  (`APIFY_<SHOP>_*` included), estimated costs (`SERPAPI_SEARCH_COST_USD`,
  `APIFY_<SHOP>_RUN_COST_USD`), monthly budget (`BUDGET_MONTHLY_USD`,
  `BUDGET_ALERT_PERCENT`), timeouts, cache times and generation,
  plans, rate limits, CORS.

With `DATABASE_URL` (always in docker compose) the runtime settings live in
Postgres:

- **First start:** migrations run, and the runtime settings found in the
  environment are copied into the database once (history author
  `import:env`). Afterwards `.env` no longer changes them; the server logs
  a warning listing runtime keys in `.env` that differ from the database.
- **Change without restart:** every change is validated as the whole
  configuration it leads to and refused if invalid. The server checks the
  settings version every `SETTINGS_POLL_INTERVAL` (10 s), builds a new
  provider chain, cache front and router, and swaps them in at once;
  searches already running finish on the old ones.
- **Secrets** (`SERPAPI_KEY`, `APIFY_TOKEN`) are encrypted with AES-256-GCM
  (`SETTINGS_ENCRYPTION_KEY`), write-only (never printed) and never written
  to the history.
- **History:** every change with who, when, old → new.

Change them with the [admin API](#admin-api) (`PATCH /admin/v1/settings`)
or, for setup and recovery, the command line in the container:

```sh
S="docker compose exec server /owis_find_deal_engine"
$S settings list                                  # value or (default), who changed it
$S settings set APIFY_SHEIN_MAX_ITEMS 5 CACHE_FRESH_TTL 24h
$S settings set -by owner APIFY_MARKETS aliexpress,temu,shein
docker compose exec -T server /owis_find_deal_engine settings set SERPAPI_KEY - < serpapi.key
$S settings unset CACHE_FRESH_TTL                 # back to the default
$S settings history [KEY]
$S admins list
$S admins add|disable|enable|unlink EMAIL
$S audit [ACTION-PREFIX]                          # latest admin actions
$S clients list                                   # API clients, key prefixes, last use
$S clients revoke ID                              # stop a client and all its keys
```

Git Bash on Windows rewrites arguments starting with `/` into Windows
paths ("no such file"): run `export MSYS_NO_PATHCONV=1` first, or write
`//owis_find_deal_engine`. In PowerShell pipe secrets in instead of `<`:
`"key" | docker compose exec -T server /owis_find_deal_engine settings set SERPAPI_KEY -`.

Without `DATABASE_URL` all settings come from the environment, as before,
and need a restart to change.

Tables ([`internal/store/migrations`](internal/store/migrations)):
`settings`, `settings_history`, `admins`, `audit_log`, `usage_daily`,
`query_daily` (+ `schema_migrations`).

## Admin web app

**<http://localhost:3002/admin/>** (with a database). A small React +
TypeScript app ([`web/admin`](web/admin)) built into the server image and
embedded in the binary — one deployment, same origin as the admin API.

- **Sign-in:** Firebase email + password (needs `ADMIN_FIREBASE_API_KEY`,
  the project's public Web API key). The session lives in the browser tab
  only. Who may enter is decided by the server (admin list, verified email,
  sign-in age, two-factor when `ADMIN_REQUIRE_2FA=true`); a refused account
  sees why, and can send itself the email verification.
- **Pages:** Dashboard (usage, cost per day, budget, sources per shop,
  fallbacks, top queries), Settings (grouped, with help, secrets write-only,
  saved together and checked as a whole, change history), Countries & shops
  (overview + JSON editor), Test search, Cache & sources (server status,
  forget / drop cached answers, circuit breakers), API clients (create,
  change, new key with grace period, revoke; the key is shown once with a
  copy button and a `curl` example), Admins, Audit log.
- **Security:** strict Content-Security-Policy (own files + Firebase sign-in
  endpoints only), no frames, `no-store` API answers, no third-party
  scripts; Firebase is used without popups or iframes.

Develop: `cd web/admin && npm ci && npm run dev` (Vite on
<http://localhost:5173/admin/>, proxies the API to a server on
localhost:3002). `npm run build` writes the app to
`internal/adminui/dist` (not committed); `go build` embeds whatever is
there, and without a build `/admin/` explains how to build it. The Docker
image always builds it.

## Admin API

`/admin/v1/...`, separate from the public API, only with a database. Every
request is checked on the server:

1. a valid Firebase ID token (`Authorization: Bearer …`, same project as
   the app);
2. **signed in with a second factor** (`firebase.sign_in_second_factor`
   in the token; Firebase multi-factor needs *Identity Platform* turned on
   in the Firebase console — SMS or authenticator app). Without it:
   `403 second_factor_required`. **Currently disabled**
   (`ADMIN_REQUIRE_2FA=false`, the default, owner decision 2026-10-11):
   the check and the setting stay in place; **set `ADMIN_REQUIRE_2FA=true`
   before production** (phase 4);
3. a **recent sign-in**: older than `ADMIN_SESSION_MAX_AGE` (12 h) →
   `401 session_expired`, sign in again;
4. an **active entry in the admin list**. The first sign-in with the
   entry's email links the login (Firebase uid) to the entry; afterwards
   only that uid counts. A login created again for the same email is
   refused until `admins unlink EMAIL`. With
   `ADMIN_REQUIRE_VERIFIED_EMAIL=true` the email must be verified for
   that first link. **Currently disabled** (default `false`, owner
   decision 2026-10-11, so test admins can use made-up emails); **set it
   to `true` before production** (phase 4) — otherwise whoever first
   creates a Firebase login with an admin's email could claim that entry.

Rate limits: per IP before sign-in (10/s), per admin (5/s), test search
(one per ~6 s, 5 in a row). Every change, test search and refused login
of a valid token (at most once a minute per login) goes to the **audit log** (who, when, IP, request id;
never secret values). Answers carry `Cache-Control: no-store`.

| Call | What |
|---|---|
| `GET /admin/v1/me` | the signed-in admin |
| `GET /admin/v1/status` | build, applied and stored settings version, markets source, cache generation |
| `GET /admin/v1/settings` | every runtime setting: value or default, who changed it; secrets only `"set": true` |
| `PATCH /admin/v1/settings` | `{"changes": {"KEY": "value", "OTHER": null}}` — validated as a whole, `null` = back to default, secrets write-only |
| `GET /admin/v1/settings/history?key=&limit=` | old → new per change (secrets: only that they changed) |
| `GET /admin/v1/markets` | countries and shops (`markets.json` format) and where they come from |
| `PUT /admin/v1/markets` | replace them (whole JSON, validated); `DELETE` = back to the file / built-in |
| `GET /admin/v1/admins`, `POST /admin/v1/admins` `{"email"}` | admin list, add one |
| `POST /admin/v1/admins/{email}/disable\|enable\|unlink` | not yourself; never the last active admin |
| `GET /admin/v1/audit?actor=&action=&before=&limit=` | audit log, newest first (`before` = id for paging) |
| `POST /admin/v1/tools/test-search` | `{"title", "country", "markets": [..], "all_sources": false}` — asks each shop's source directly (no cache, quota, breaker or fallback): its products, time, error, estimated cost. `all_sources: true` also asks the fallbacks (each paid one is charged) |
| `POST /admin/v1/tools/cache/forget` | `{"title", "country"}` — next search for it is live |
| `POST /admin/v1/tools/cache/bump` | raises `CACHE_GENERATION`: every cached answer becomes a miss |
| `GET /admin/v1/tools/providers` | circuit breaker per source: `closed`, `open` (skipped until …), `half_open` |
| `GET /admin/v1/usage?days=30&top=20` | usage and cost dashboard (see below) |
| `GET /admin/v1/clients`, `POST /admin/v1/clients` | API clients; create one — the answer holds its key, **shown once** (see "API clients and keys") |
| `GET` / `PATCH /admin/v1/clients/{id}` | one client; change its settings (fields left out stay) |
| `POST /admin/v1/clients/{id}/rotate` | `{"grace": "24h"}` — new key (shown once); the old keys keep working until then (`0s` = stop now, at most `720h`) |
| `POST /admin/v1/clients/{id}/revoke` | the client and all its keys stop at once (final) |
| `POST /admin/v1/clients/{id}/keys/{key}/revoke` | one key stops at once (e.g. an old key before its grace period ends) |

Changes apply within `SETTINGS_POLL_INTERVAL` on every instance. Errors
have the usual shape; extra codes: `forbidden`, `second_factor_required`,
`session_expired`, `not_found`, `conflict`, `cache_disabled`.

### Usage and cost dashboard

`GET /admin/v1/usage?days=30&top=20` — the last `days` UTC days (today
included):

- `totals`, `days` (one row per day), `countries`: user searches split into
  `cache_hits`, `cache_stale`, `live`, `quota_refused`, `errors`, the
  `cache_hit_rate` (cached share of answered searches), plus source calls,
  fallbacks and estimated `cost_usd`;
- `sources`: per source and shop — calls, `ok` / `no_results` / `errors`,
  `error_rate`, `avg_ms`, `cost_usd` (admin test searches included: they
  cost money too);
- `fallbacks`: how often a chain moved on (`from` → `to`, reason `error`,
  `partial` or `circuit_open`);
- `top_queries`: most searched queries (normalized, per country, no user
  ids; kept 90 days);
- `clients`: per API client (step 12) — its searches split the same way
  (sync and asynchronous), `cost_usd_estimate` (its live searches × the
  average cost of a live search in these days) and the last day with a
  search; the base for billing partners later. Counted in `usage_daily`
  (kind `client`), so kept 400 days;
- `budget`: this month's estimated cost against `BUDGET_MONTHLY_USD`,
  `status` `none` / `ok` / `warning` (≥ `BUDGET_ALERT_PERCENT`) / `exceeded`.
  Each level reached raises one alert per month: an `ERROR` log line and
  an audit entry `budget.alert` (actor `system`). Nothing is blocked.

Costs are **estimates** from `SERPAPI_SEARCH_COST_USD` (per SerpApi call)
and `APIFY_<SHOP>_RUN_COST_USD` (per scraper run); the bill is in the
SerpApi / Apify consoles. Each server keeps its counts in memory and adds
them to Postgres every 30 s and at shutdown (`usage_daily`, kept 400 days).

Try it (token of an admin):

```sh
curl -H "Authorization: Bearer $ADMIN_TOKEN" localhost:3002/admin/v1/settings
curl -X PATCH -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
     -d '{"changes":{"APIFY_SHEIN_MAX_ITEMS":"5"}}' localhost:3002/admin/v1/settings
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
     -d '{"title":"usb hub","country":"jor","markets":["temu"]}' localhost:3002/admin/v1/tools/test-search
```

## API clients and keys

For **own servers and AI agents** (owner decision D4). The mobile / web
app keeps using the user's login token: a key inside an app could be
extracted. Partners with self-service sign-up and billing come later on
the same model.

- **Made in the admin panel** (page *API clients*, or
  `POST /admin/v1/clients`): name, owner, scopes, live searches per day,
  requests per second + burst, allowed countries, allowed addresses
  (IPs / CIDRs), expiry. Defaults: scopes `search` + `countries`, 100
  live searches per day, 1/s with bursts of 5, every country, any address,
  no expiry.
- **The key** looks like `ofd_live_` + 40 random characters (200 bits).
  It is shown **once**; the database keeps only its SHA-256 hash and the
  first 8 characters (`ofd_live_abcd1234…`) to recognise it. Never in logs
  or the audit log.
- **Use:** `Authorization: Bearer ofd_live_…` on `POST /api/v1/search`
  (scope `search`). The `countries` scope is for the agent tools of step
  13; `GET /api/v1/countries` stays public.
- **Metering:** each client has its own daily counter (`client:<id>`),
  separate from app users and other clients. Cached answers are free, as
  for users; a used-up quota answers `429 quota_exceeded` (`X-Plan: api`).
- **Rotate:** a new key; the old one keeps working for the grace period
  (default 24 h), then stops — time to deploy the new key. *Stop now* ends
  an old key early. **Revoke** stops the client and all its keys at once.
- A change is seen at once by the server that made it and within 10 s by
  other instances (keys are cached that long). When each key was last
  used is saved every minute.
- Without a database, keys are refused (`401`); user tokens work as before.
- Command line (for emergencies, e.g. a leaked key while no admin can sign
  in): `clients list`, `clients revoke ID`.
- Audit log actions: `clients.create`, `clients.change`, `clients.rotate`,
  `clients.revoke`, `clients.key_revoke`, `clients.webhook_secret`.

```sh
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
     -d '{"name":"price agent","owner":"me","daily_quota":200,"countries":["jor","ksa"]}' \
     localhost:3002/admin/v1/clients            # → {"client": {...}, "key": "ofd_live_…"}

curl -H "Authorization: Bearer ofd_live_…" -H 'Content-Type: application/json' \
     -d '{"title":"usb c hub","country":"jor"}' localhost:3002/api/v1/search
```

Behind a reverse proxy (step 14) the allowed-address check sees the
proxy's address until the proxy's client header is trusted; leave the
list empty until then.

## Asynchronous search and webhooks

A live search takes 25–50 s (cached answers are instant). Servers and
agents that do not want to hold a connection that long start it as a
**job** and fetch the result later, or get it by a **signed webhook**.
App users (login token) can use it too, without webhooks.

```sh
curl -i -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer $KEY_OR_JWT" \
     -H 'Content-Type: application/json' -d '{"title":"usb c hub","country":"jor"}'
# 202 Accepted, Location: /api/v1/searches/srch_…, Retry-After: 5
# {"id":"srch_…","status":"running","request":{…},"created_at":"…","finished_at":null}

curl localhost:3002/api/v1/searches/srch_… -H "Authorization: Bearer $KEY_OR_JWT"
# {"id":"srch_…","status":"done","result":{…same as POST /search…},"quota":{"plan":"api","limit":100,"remaining":99,"reset":"…"},…}
```

- Same body, rules and metering as `POST /api/v1/search` (scope `search`,
  allowed countries, burst limit; cached answers free, a live search
  counts once). Bad input, a used-up allowance (`402` / `429`) and missing
  permissions are answered **at once**, before a job starts.
- Status `running` → `done` (with `result`) or `failed` (with `error`:
  `http_status`, `code`, `message` — the answer `POST /search` would have
  given, e.g. `502 upstream_error`; `504 timeout`; `503 job_lost` when the
  server running it stopped). A cached answer is already `done` in the
  `202`.
- Poll every few seconds (`Retry-After: 5` while running); reading jobs
  has its own limit (5/s, burst 20) and does not use up the search limit.
- Only the caller who started a job can read it (anyone else: `404`).
  Finished jobs are kept **1 hour**. Jobs live in Redis (any instance
  answers) or, without Redis, in memory.
- At most **10 running jobs per caller** (`429 too_many_jobs`) and 200 per
  server. At shutdown running jobs get the shutdown time, then end as
  `failed` (`503`) — start them again.

**Webhooks (API keys only).** Add `"webhook_url": "https://…"`; when the
job ends the service POSTs the job (the same JSON as `GET`) to it:

```
POST /your/hook
Content-Type: application/json
X-OFD-Event: search.completed            (or search.failed)
X-OFD-Delivery: srch_…                   (the job id; the same on every retry)
X-OFD-Signature: t=1792757906,v1=905c03bb…
```

- **Signing secret per client**, made in the admin panel (*API clients →
  Webhook secret*, or `POST /admin/v1/clients/{id}/webhook-secret`),
  shown **once** (`whsec_…`). It is not stored: it is derived from
  `SETTINGS_ENCRYPTION_KEY` and a random salt in the database, so a copy
  of the database alone cannot sign. A new secret replaces the old one at
  once. Without a secret, `webhook_url` is refused
  (`400 webhook_not_configured`).
- `v1` = hex HMAC-SHA256 of `"<t>.<raw body>"` with the secret. Check it
  in constant time and refuse a `t` older than 5 minutes. Use
  `X-OFD-Delivery` to ignore repeats.
- Any `2xx` answer within 10 s counts as delivered; network errors, `408`,
  `429` and `5xx` are retried after 5 s and 30 s (3 attempts). Redirects
  are not followed. The job shows how it went: `webhook.status`
  (`pending` / `delivered` / `failed`), `attempts`, `http_status`, `error`.
- Only `https` to public addresses; internal ones (localhost, 10.x,
  192.168.x, 169.254.x, …) are refused, also when a host name resolves to
  them. For a test receiver on your PC set `WEBHOOK_ALLOW_PRIVATE=true`
  (development only: it also allows plain `http`).

Check a webhook (Python):

```python
import hashlib, hmac, time

def verify(secret: str, header: str, body: bytes, tolerance=300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    if abs(time.time() - int(parts["t"])) > tolerance:
        return False
    want = hmac.new(secret.encode(), parts["t"].encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, parts["v1"])
```

## AI agents (MCP) and developer docs

**MCP endpoint `POST /mcp`** — AI agents call the search as tools over the
[Model Context Protocol](https://modelcontextprotocol.io) (Streamable
HTTP), with the same **API keys** as the REST API (login tokens are
refused: `401`).

| Tool | Scope | Arguments | Result |
|---|---|---|---|
| `list_countries` | `countries` | none | countries this key may use, currency, marketplaces (free) |
| `search_products` | `search` | `title`, `country`, optional `refresh` | same as `POST /api/v1/search` + `quota`; metered the same way |

- A key needs only the scope of the tools it calls; another tool answers
  with a tool error (`isError: true`, `{"error":{"code":"forbidden",…}}`).
  All tool failures come back that way (allowance used up with
  `retry_after_seconds`, country not allowed, bad arguments, every
  marketplace failed), so the agent can read them. Unknown tool or method:
  a JSON-RPC error.
- Results: `structuredContent` (with an `outputSchema`) and the same JSON
  in a text block. `search_products` waits for the search (20–60 s for a
  live one): clients need a timeout of ~90 s.
- **Protocol versions**: `2026-07-28` (stateless: `server/discover`,
  `_meta` protocol version per request, `MCP-Protocol-Version` /
  `Mcp-Method` / `Mcp-Name` headers checked against the body → `400`
  `-32020` on a mismatch) and `2025-03-26` / `2025-06-18` / `2025-11-25`
  (`initialize` handshake). Unknown version → `400` `-32022` with
  `data.supported`. Tested with the official TypeScript SDK (1.32.2 and
  1.12.0) and Go SDK (1.8.0, both eras).
- Stateless: one JSON answer per request, no sessions (`Mcp-Session-Id`
  never issued), no SSE, no batches; `GET` / `DELETE /mcp` → `405`.
- Limits: every MCP message 5/s (burst 20) per client; `search_products`
  also takes from the client's search burst limit (shared with the REST
  API), and the daily allowance is the same counter.
- A browser `Origin` must be the server itself or in `CORS_ORIGINS`
  (otherwise `403`, DNS rebinding protection).

Connect Claude Code:

```sh
claude mcp add --transport http owis-find-deal http://localhost:3002/mcp \
  --header "Authorization: Bearer ofd_live_…"
```

Raw call (2026-07-28):

```sh
curl localhost:3002/mcp -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2026-07-28' -H 'Mcp-Method: tools/call' -H 'Mcp-Name: list_countries' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_countries",
       "_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
```

**Developer docs** (public, embedded in the binary,
[`internal/devdocs`](internal/devdocs)):

- <http://localhost:3002/docs/> — the page for developers: quick start,
  keys, search, asynchronous search, webhooks (with signature checks in
  Python and Node.js), MCP client setup, limits, errors. No scripts;
  strict Content-Security-Policy.
- <http://localhost:3002/api/v1/openapi.json> — OpenAPI 3.1 description
  of the public API, including the webhook (`webhooks`, `callbacks`) and
  `/mcp`. Hand-written: **update it with every API change** (a test
  checks that every route and error code is in it and every `$ref`
  resolves; `npx @redocly/cli lint` passes).

## Search providers

`SEARCH_PROVIDERS` lists providers in order; the first that succeeds
answers (default `searxng,serpapi`):

- **SearXNG** (free, self-hosted): metasearch over Google, Bing,
  DuckDuckGo, Brave, Startpage and Mojeek. Config:
  [`deploy/searxng/settings.yml`](deploy/searxng/settings.yml) (JSON output
  on, bot limiter off because only our service calls it). It sends one
  `site:` query per marketplace, in parallel, so Amazon cannot fill all the
  results. Web search returns **links and snippets, no prices**: prices
  come from SerpApi Google Shopping or Apify.

**Only product pages are returned.** Web search also finds store, seller,
category and search pages; each marketplace in `markets.json` has
`product_pages` patterns (Amazon `/dp/<ASIN>`, AliExpress `/item/<id>.html`,
eBay `/itm/<id>`, Temu `…-g-<id>.html`, SHEIN `…-p-<id>.html`) and every
other link is dropped. Links are cleaned (tracking parameters removed,
Amazon `/clp/<ASIN>` → `/dp/<ASIN>`) and the same product found twice is
listed once. A marketplace without `product_pages` accepts any page of its
domain. Results must also match the query (at least two thirds of its words in
the title, snippet or link), and each shop returns at most
`RESULTS_PER_MARKET` (10) results. With `LOG_LEVEL=debug` the server logs,
per shop, how many results were dropped and why.
- **SerpApi** (paid): `SERPAPI_ENGINE=google_shopping` (default) returns
  **prices, seller and image** from Google Shopping; offers are matched to
  our shops by seller name, and when Google Shopping has nothing from our
  shops it retries once as a normal web search. `google` = web search only.
  Put it first (`serpapi,searxng`) if prices matter more than cost: shops
  with no offers on Google Shopping are then searched by SearXNG, for free.
  Shopping results must match the query like web results; when Google gives
  no direct shop link, the link is the shop's search for that product
  (`link_type: "search"`). When a free provider follows SerpApi, shops without
  Shopping offers are searched there instead of with a paid web search.
  Google Shopping does not cover every country; `shopping_region` in
  `markets.json` picks another one (Jordan uses `us`: its shops are
  international, prices in USD).
- **SerpApi Amazon engine** (`SERPAPI_MARKETS=amazon`, on by default when
  `SERPAPI_KEY` is set): Amazon is searched with SerpApi's Amazon Search
  API on the country's Amazon (amazon.com for usa/jor, amazon.sa for ksa):
  real results with price, rating, image and direct `/dp/<ASIN>` links. No
  Amazon account needed; one SerpApi search per live search. If it fails,
  Amazon falls back to the providers above.
- **Apify** (optional, per marketplace): `APIFY_MARKETS=temu,shein` sends
  those shops to a scraper from the Apify store (real prices and images);
  if it fails or exceeds `APIFY_TIMEOUT`, that shop falls back to the
  providers above. Setup steps are in [`.env.example`](.env.example).

```
amazon                   ──► SerpApi Amazon engine ──► web providers
aliexpress, ebay         ──► web providers (SEARCH_PROVIDERS order)
temu, shein (if Apify)   ──► Apify scraper ──► web providers
```

Each provider gets `PROVIDER_ATTEMPT_TIMEOUT` (Apify: `APIFY_TIMEOUT`);
after `PROVIDER_FAILURE_THRESHOLD` failures in a row it is skipped for
`PROVIDER_COOLDOWN` (circuit breaker), so a broken source adds no delay.
Every result carries `"provider"` (`searxng`, `serpapi`, `apify`).

## Cache and price freshness

Searches are cached per country + normalized title (case and spacing do
not matter), in Redis when `REDIS_URL` is set:

| Age of the cached answer | What happens | `X-Cache` |
|---|---|---|
| < `CACHE_FRESH_TTL` (48h) | served from cache | `HIT` |
| up to `CACHE_STALE_TTL` (72h) | served instantly, refreshed in the background for the next user | `STALE` |
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

# AI agents: POST /mcp, see "AI agents (MCP) and developer docs"
curl localhost:3002/api/v1/openapi.json           # OpenAPI 3.1, public
# developer docs: http://localhost:3002/docs/

# asynchronous (slow live searches), see "Asynchronous search and webhooks"
curl -X POST -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' \
     -d '{"title":"samsung s pen","country":"jor"}' localhost:3002/api/v1/searches
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
  "markets": {"amazon": "ok", "aliexpress": "ok", "temu": "no_results", "shein": "error"},
  "took_ms": 812,
  "fetched_at": "2026-10-02T12:00:00Z",
  "cached": false,
  "stale": false
}
```

`link` is the shop's product page. When a source gives no usable product
link (Google Shopping's own links only open inside Google), `link` opens
the shop's search for that product and `"link_type": "search"` is set, so
the app can label it ("find on AliExpress").

`markets` reports each marketplace searched: `ok` (has results),
`no_results` (searched, nothing matched) or `error` (failed). The request
still succeeds if some fail; it returns `502` only when all of them fail.
When the free provider fails for only some shops, just those shops are
retried with the next provider (e.g. SerpApi).

Quota headers on every search response: `X-Plan`, `X-RateLimit-Limit`,
`X-RateLimit-Remaining`, `X-RateLimit-Reset` (Unix time, UTC midnight).

Errors always look like `{"error": {"code": "...", "message": "..."}}`:

| Status | code | meaning |
|---|---|---|
| 400 | `invalid_request`, `unsupported_country` | bad input |
| 401 | `unauthorized` | missing, invalid or expired JWT; unknown, revoked or expired API key |
| 403 | `forbidden` | API key: lacks the scope, or not allowed from this address |
| 403 | `country_not_allowed` | API key limited to other countries |
| 402 | `payment_required` | free daily searches used up — show upgrade |
| 429 | `rate_limited` | too fast; see `Retry-After` |
| 429 | `quota_exceeded` | paid plan's (or API client's) daily searches used up |
| 429 | `too_many_jobs` | 10 asynchronous searches already running for this caller |
| 400 | `webhook_not_configured` | `webhook_url` given, but the API client has no webhook secret |
| 404 | `not_found` | no such search job (or someone else's, or older than 1 h) |
| 502 | `upstream_error` | all marketplaces failed |
| 503 | `service_unavailable` | usage store unreachable (fails closed) |
| 500 | `internal_error` | bug |

## Countries and marketplaces

Defined in [`internal/markets/markets.json`](internal/markets/markets.json)
and embedded in the binary. With a database they are edited with
`PUT /admin/v1/markets` (stored as the `MARKETS` setting, applied without
a restart); otherwise edit that file or point `MARKETS_FILE` at another
one. No code changes are needed.

## Layout

```
cmd/owis_find_deal_engine   main: config, wiring, settings swap, graceful
                            shutdown; settings / admins command line
internal/config             settings (environment or database), validation
internal/settings           settings store: encryption, validated changes,
                            import from .env, change watcher
internal/store              Postgres: migrations, settings, history, admins,
                            audit log, API clients and keys
internal/markets            countries -> marketplaces catalog
internal/search             search service: worker pool, merge, ordering
internal/search/fallback.go provider chain + circuit breaker
internal/cache              stale-while-revalidate cache (Redis / LRU)
internal/metering           what a search costs: cached free, live counted
internal/providers/searxng  SearXNG provider (free)
internal/providers/serpapi  SerpApi: Google Shopping / web (paid fallback)
internal/providers/apify    Apify store scrapers, per marketplace (optional)
internal/auth               JWT verification + JWKS key cache
internal/auth/authtest      fake auth provider for tests
internal/apikeys            API keys: format, hashing, cached check, last
                            use, client settings validation
internal/usage              plans, daily quotas, Redis/memory counters
internal/ratelimit          token bucket per user / API client
internal/adminui            serves the admin web app (embedded build) at /admin/
web/admin                   admin web app source (React + TypeScript, Vite)
internal/api                router, handlers, middleware; admin.go +
                            admin_handlers.go: the admin API;
                            admin_clients.go: API clients;
                            searches.go: asynchronous search;
                            mcp.go: MCP endpoint for AI agents
internal/devdocs            developer docs page + OpenAPI 3.1 (/docs/,
                            /api/v1/openapi.json)
internal/search/probe.go    admin test search, provider health
internal/search/instrument.go  reports source calls and fallbacks
internal/stats              usage counters (flushed to Postgres), dashboard
                            report (also per API client), monthly budget alert
internal/jobs               asynchronous searches: jobs (Redis / memory), runner
internal/webhook            webhook secrets, HMAC signing, sender (public
                            addresses only, retries)
deploy/searxng              SearXNG settings
Dockerfile                  distroless, non-root image (~19 MB)
docker-compose.yml          full stack on private networks
compose.dev.yml             dev override: SearXNG, Redis, Postgres on 127.0.0.1
```

## Test

```sh
gofmt -l . && go vet ./... && go test -race ./...
# store tests need an empty throwaway database (its tables are dropped):
TEST_DATABASE_URL=postgres://user:pass@127.0.0.1:5432/ofd_test?sslmode=disable go test -race ./internal/store
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
cd web/admin && npm ci && npm run build && npm audit --omit=dev
```

CI ([`.github/workflows/server.yml`](../../.github/workflows/server.yml))
runs these on every push touching the server (the admin web app in its own
job: typecheck, build, audit), then validates the compose files and builds
and smoke-tests the Docker image.
