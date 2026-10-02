# owis_find_deal_engine — Server Plan

Status: **draft v1** · Scope: **server / service only** (client apps come later)

## 1. Goal

A backend service that receives a product name and a country, searches the
online shops that deliver to that country, and returns one merged list of
results. Logged-in users can save items to a cart, report purchases, and get
notifications. Running cost should stay as close to **$0** as possible.

Consumers: mobile app, web app, and later AI agents (as a tool / MCP server).

## 2. Countries and marketplaces

Supported countries (v1): `usa`, `ksa`, `jor`. Supported marketplaces (v1):
`amazon`, `ebay`, `aliexpress`, `temu`, `shein`.

A search only queries the marketplaces enabled for the requested country.
This is **configuration, not code**: adding a country or a shop later means
editing one config file.

| Marketplace | usa | ksa | jor | Domain used per country |
|---|:-:|:-:|:-:|---|
| Amazon     | ✅ | ✅ | ✅ | usa: amazon.com · ksa: amazon.sa · jor: amazon.com (ships to Jordan) |
| eBay       | ✅ | ❓ | ❌ | ebay.com (eBay does not deliver to Jordan) |
| AliExpress | ✅ | ✅ | ✅ | aliexpress.com |
| Temu       | ✅ | ✅ | ✅ | temu.com |
| SHEIN      | ✅ | ✅ | ✅ | usa: us.shein.com · ksa/jor: ar.shein.com |

❓ = **to confirm by the owner** (default in config: disabled for `ksa`).
Domains above are proposals; confirm them before step 3 is finished.

Draft config file (`server/config/markets.json`):

```json
{
  "countries": {
    "usa": { "name": "United States of America", "currency": "USD",
             "markets": ["amazon", "ebay", "aliexpress", "temu", "shein"] },
    "ksa": { "name": "Kingdom of Saudi Arabia", "currency": "SAR",
             "markets": ["amazon", "aliexpress", "temu", "shein"] },
    "jor": { "name": "Jordan", "currency": "JOD",
             "markets": ["amazon", "aliexpress", "temu", "shein"] }
  },
  "markets": {
    "amazon":     { "name": "Amazon",     "domains": { "usa": "amazon.com", "ksa": "amazon.sa", "jor": "amazon.com" } },
    "ebay":       { "name": "eBay",       "domains": { "usa": "ebay.com" } },
    "aliexpress": { "name": "AliExpress", "domains": { "*": "aliexpress.com" } },
    "temu":       { "name": "Temu",       "domains": { "*": "temu.com" } },
    "shein":      { "name": "SHEIN",      "domains": { "usa": "us.shein.com", "*": "ar.shein.com" } }
  }
}
```

## 3. Search providers (how each marketplace is searched)

Every source sits behind one Go interface, so providers can be swapped
without touching handlers:

```go
type SearchProvider interface {
    Name() string
    Search(ctx context.Context, q SearchQuery) ([]Product, error)
}
```

| Provider | Used for | Cost | When |
|---|---|---|---|
| `searxng` (self-hosted) | site-restricted web search for any market | free | default |
| `serpapi` | same, higher quality | 250 free/month, then paid | optional fallback (env key) |
| `ebay` (Browse API) | eBay, with real prices | free (5,000 calls/day) | when eBay keys arrive |
| `aliexpress` (Affiliate API) | AliExpress, with prices + affiliate links | free | when affiliate keys arrive |

Rules:
- **One web-search call per request**, not one per shop:
  `"<title>" (site:amazon.com OR site:temu.com OR ...)`. Shops with their own
  API (eBay, AliExpress) are called directly, in parallel.
- Results are **cached** (key = country + normalized title) for 6–24 h.
- A failing provider never fails the whole request: the response lists
  which markets succeeded and which failed.

## 4. Users and features

### 4.1 Authentication
- **Firebase Authentication** (free tier): email/password, Google, Apple
  login handled by Firebase; the app sends the Firebase ID token.
- The server only **verifies the token** (Firebase Admin SDK for Go) and
  creates/loads the matching user row. No passwords stored by us.
- Search can stay usable without login (rate-limited); cart, purchases and
  notifications require login.

### 4.2 Saved cart
The user saves a search result with optional notes ("check size", "wait for
sale"), and can list, edit, and delete saved items.

### 4.3 Purchase reports
The user reports: **marketplace, purchase date, price, currency**, optionally
linked to a saved item or a search result.
- When the user opens a product link, the app calls `POST /clicks`. After a
  delay (e.g. 24 h) the server creates a friendly "Did you buy it? Tell us
  the price" notification. Filling it in is optional.
- This data later powers price insights ("users in Jordan paid X on Temu").

### 4.4 Notifications
- Stored in the database (in-app inbox: list, mark read).
- Delivered as push via **Firebase Cloud Messaging** (free). The app
  registers its device token with the server.
- v1 types: `purchase_prompt`, `system`. Later: price drops on saved items.

## 5. API (v1 draft)

All paths under `/api/v1`. JSON in and out. Errors: `{"error": {"code", "message"}}`.

| Method | Path | Auth | Purpose |
|---|---|:-:|---|
| GET  | `/health` | – | liveness |
| GET  | `/countries` | – | countries + their markets |
| POST | `/search` | optional | `{"title","country"}` → merged results |
| GET  | `/me` | ✅ | current user profile (country, language) |
| PATCH| `/me` | ✅ | update profile |
| GET/POST | `/cart` | ✅ | list / add saved items |
| PATCH/DELETE | `/cart/{id}` | ✅ | edit notes / remove |
| POST | `/clicks` | ✅ | user opened a product link |
| GET/POST | `/purchases` | ✅ | list / report a purchase |
| GET  | `/notifications` | ✅ | inbox |
| POST | `/notifications/{id}/read` | ✅ | mark as read |
| POST | `/devices` | ✅ | register FCM push token |

Search response shape:

```json
{
  "query": "samsung s pen",
  "country": "jor",
  "results": [
    { "market": "temu", "title": "...", "link": "...", "snippet": "...",
      "thumbnail": "...", "price": 9.99, "currency": "USD",
      "position": 1, "provider": "searxng" }
  ],
  "markets": { "amazon": "ok", "aliexpress": "ok", "temu": "ok", "shein": "error" },
  "cached": false
}
```

## 6. Data model (PostgreSQL)

```
users          id, firebase_uid (unique), email, country, language, created_at
saved_items    id, user_id, market, title, link, thumbnail, price, currency, notes, created_at, updated_at
link_clicks    id, user_id, market, link, title, clicked_at, prompted_at
purchases      id, user_id, saved_item_id NULL, market, title, price, currency, purchased_at, created_at
notifications  id, user_id, type, title, body, data JSONB, read_at NULL, created_at
devices        id, user_id, fcm_token (unique), platform, created_at
search_cache   key, response JSONB, expires_at        (or in-memory / Redis later)
```

Free hosting options for Postgres: Neon or Supabase free tier, or the same VPS.

## 7. Server layout

```
Owis_Find_Deal_Engine/
├── PLAN.md
├── README.md
├── server/                      # Go service: owis_find_deal_engine
│   ├── cmd/owis_find_deal_engine/main.go
│   ├── config/markets.json
│   ├── internal/
│   │   ├── config/              # env + markets config loading
│   │   ├── api/                 # router, handlers, middleware, errors
│   │   ├── search/              # service, merge, cache
│   │   ├── providers/           # searxng, serpapi, ebay, aliexpress
│   │   ├── auth/                # Firebase token verification
│   │   ├── store/               # Postgres repositories + migrations
│   │   └── notify/              # inbox + FCM push + purchase-prompt job
│   ├── migrations/
│   ├── Dockerfile
│   └── docker-compose.yml       # server + postgres + searxng for local dev
└── app/                         # client apps (later)
```

## 8. Step-by-step

Each step ends with code that builds, has tests, and is pushed.

1. **Folder + module** ✅ — `Owis_Find_Deal_Engine/` created, legacy code
   moved to `server/`, `go.mod` with module `owis_find_deal_engine`.
2. **Restructure + bug fixes** — new layout (section 7); config from env
   (`PORT`, `SERPAPI_KEY`, ...); remove hard-coded key/token; fix the hang
   in the worker pool, the panics on missing fields, swallowed errors;
   consistent JSON errors; graceful shutdown kept.
3. **Countries & markets** — `markets.json`, `GET /countries`, validation of
   `country`, query built only from that country's markets.
4. **Provider interface + SearXNG + SerpApi** — one combined query per
   request, map results to `market` by link domain, partial-failure report.
   `docker-compose` with SearXNG for local dev.
5. **Cache + rate limit** — in-memory TTL cache and per-client limit.
6. **Database** — Postgres, migrations, repositories, `docker-compose`.
7. **Auth** — Firebase token verification middleware, `users` table, `/me`.
8. **Saved cart** — `/cart` endpoints.
9. **Clicks + purchases** — `/clicks`, `/purchases`.
10. **Notifications** — inbox endpoints, `/devices`, FCM push, background
    job that creates purchase prompts from clicks.
11. **eBay provider** — when keys are approved (usa only).
12. **AliExpress provider** — when affiliate keys are approved.
13. **Deploy** — Dockerfile, deploy to a free/cheap host, HTTPS.
14. **AI-agent access** — expose search as an MCP tool / OpenAPI spec.

## 9. Open questions for the owner

1. Does eBay deliver to **KSA** for your users? (default: disabled)
2. Confirm the per-country domains in section 2 (e.g. amazon.sa for KSA).
3. Login methods for v1: email/password, Google, Apple?
4. Should search work without login?
5. Where to host (VPS you already have, or a free tier)?
