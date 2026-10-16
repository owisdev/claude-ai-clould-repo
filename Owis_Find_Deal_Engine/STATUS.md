# Project status and handoff

Last updated: 2026-10-16 · Project branch: `claude/cloud-vs-local-7xi988`
(steps 7–11 merged; step 11 via PR #6; nothing is merged into `main` yet).

Read first:
- [docs/current_flow.pdf](docs/current_flow.pdf) — **how the service works
  today** (7 Oct 2026): sources per shop, request flow, 48 h cache, costs,
  configuration, known limits, test results.
- [docs/next_plan.pdf](docs/next_plan.pdf) — **the new plan** (admin panel →
  API integration → deployment → users) and the owner's decisions D1–D8
  with an answer sheet.
- [docs/architecture.pdf](docs/architecture.pdf) — the original full
  architecture (predates the dedicated sources per shop).

Regenerate the PDFs: `python3 docs/build_flow_and_plan_pdfs.py` and
`python3 docs/build_architecture_pdf.py` (needs `reportlab`).

## Where we are

**Phase 3, step 11 — API clients and keys: done (2026-10-15).** Details:
server README section "API clients and keys", PLAN.md step 11.

- Keys for **your own servers and AI agents** (D4), made in the admin
  panel: new page **API clients** (<http://localhost:3002/admin/#/clients>)
  or `POST /admin/v1/clients`. Settings per client: name, owner, scopes
  (`search`, `countries`), live searches per day (default 100), requests
  per second + burst (1/s, 5), allowed countries, allowed addresses
  (IP / CIDR), expiry.
- The key (`ofd_live_…`, 49 characters) is **shown once** with a copy
  button and a `curl` example; the database keeps only its SHA-256 hash
  and the first characters. Never in logs or the audit log.
- `POST /api/v1/search` accepts the user's Firebase token **or** an API
  key (`Authorization: Bearer ofd_live_…`). Each client has its own daily
  counter and burst limit, separate from users; cached answers stay free;
  used up → `429` (`X-Plan: api`). Wrong country → `403
  country_not_allowed`; missing scope or address not allowed → `403`.
- **New key** (rotate): the old key keeps working for a grace period you
  choose (default 24 h, 0 = stop at once); *Stop now* ends it early.
  **Revoke** stops a client and all its keys at once. Last use of each key
  is shown. Every change is in the audit log (`clients.*`).
- Command line for emergencies: `clients list`, `clients revoke ID`.
- New tables `api_clients`, `api_keys` (created automatically at start).
- Tested: unit tests (key format and check, cache, expiry, rotation, scope,
  address, country, own quota and rate limit, admin endpoints), store tests
  against Postgres 16, the race detector, an end-to-end run of the binary
  with Postgres and a fake Firebase (create → search → quota 429 → wrong
  country 403 → rotate → revoke → user token still works; no key in the
  log), and the web page in Chromium (light and dark: create, copy, edit,
  new key, stop old key, revoke, invalid input refused with the server's
  message). Not run here: `govulncheck` (its database is not reachable
  from this environment; CI runs it; CI on PR #6 was green). No new Go or
  npm dependencies.
- **Owner test on the Docker stack (2026-10-16, Windows, Git Bash), steps
  1–3 below:** key created in the admin panel (limit 2 per day, `jor`
  only); first search `usb c hub` / `jor` → `200`, `X-Cache: MISS`,
  `X-Plan: api`, remaining 1, all 4 shops `ok`, 44 s; same search again →
  `200`, `X-Cache: HIT`, 0 ms, remaining still 1 (free); `usa` → `403
  country_not_allowed`; made-up key → `401 invalid API key`. ✅
  **Still to test by the owner** (free, use the cached `usb c hub`
  search): new key with 1 h grace (both keys work) → *Stop now* on the old
  key (`401 API key expired`) → *Revoke* (`401 API key revoked`); a client
  without the `search` scope (`403`); audit log entries `clients.*`;
  "last used" and `clients list`. Optional (one live search): daily limit
  → `429 quota_exceeded`.

**Owner, to try step 11:**

1. `docker compose up -d --build` (nothing to add to `.env`).
2. Admin panel → **API clients** → name e.g. `test agent`, tick `jor`,
   *Create client and key* → copy the key.
3. Search with it (Git Bash; PowerShell: `curl.exe`):
   `curl -i -X POST localhost:3002/api/v1/search -H "Authorization: Bearer ofd_live_…" -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"jor"}'`
   → `200`, `X-Plan: api`. The same with `"country":"usa"` → `403
   country_not_allowed`.
4. *New key* with 1 hour → both keys work; *Stop now* on the old one →
   it answers `401 API key expired`. *Revoke* → `401 API key revoked`.
5. Audit log page: `clients.create`, `clients.rotate`, … (no keys in it).
6. Report: wording, missing fields, the defaults (100 searches per day,
   1/s) — are they right for your agents?

**Phase 2, step 10 — admin web app: done (2026-10-11). Phase 2 is
complete.** Details: server README section "Admin web app", PLAN.md step 10.

- **<http://localhost:3002/admin/>**: React + TypeScript app built into
  the server image (D1), same origin as the admin API.
- Sign-in with Firebase email + password; the session ends when the tab
  closes. A refused account sees why (and can send the email
  verification).
- Pages: **Dashboard** (tiles, searches per day cached vs live, estimated
  cost per day, budget meter, sources per shop, fallbacks, countries, top
  queries), **Settings** (grouped with help text, secrets write-only, all
  changes saved together and checked as a whole, change history),
  **Countries & shops** (overview + JSON editor), **Test search**,
  **Cache & sources** (server status, forget one search, drop all cached
  answers, circuit breakers), **Admins**, **Audit log**.
- Strict Content-Security-Policy; only `@firebase/app` + `@firebase/auth`
  are used (npm audit: 0 vulnerabilities).
- **D5 answered: monthly budget $5** (free plans) — now the default of
  `BUDGET_MONTHLY_USD`, nothing to set.
- Tested: Go tests, typecheck + build, and a real browser (Chromium,
  light and dark mode) against the server with a fake Firebase: sign-in,
  every page, saving a setting, an invalid change refused with the
  server's message, a test search; no script or CSP errors.

**Owner, to try step 10:**

1. In `.env`: `ADMIN_FIREBASE_API_KEY=<WEB_API_KEY>` (the Web API key you
   use for test tokens), then `docker compose up -d --build` (the image
   now also builds the web app; the first build takes a bit longer).
2. Open <http://localhost:3002/admin/> and sign in with the Firebase
   user (email + password; create it in Firebase console → Authentication
   → Users → Add user) whose email is on the admin list (`ADMIN_EMAIL`, or
   `docker compose exec server /owis_find_deal_engine admins add EMAIL`).
   Email verification is off for now, so a made-up email works.
3. Go through the pages. Useful first steps: Settings → add
   `APIFY_TEMU_RUN_COST_USD` = 0.10, `APIFY_SHEIN_RUN_COST_USD` = 0.033,
   `APIFY_ALIEXPRESS_RUN_COST_USD` = 0.02 (Add a setting); then a test
   search, then the Dashboard after 30 s.
4. Report what is unclear or missing — wording, layout, a setting you
   cannot find.

**Phase 2, step 9 — usage and cost dashboard: done (2026-10-11).**
Details: server README section "Usage and cost dashboard", PLAN.md step 9.

- `GET /admin/v1/usage?days=30` — per day, country, source and shop:
  searches (cache hit / stale / live / refused by quota / failed), cache
  hit rate, source calls with ok / no results / errors, average time,
  fallbacks and why, **estimated cost per day**, top queries (no user ids).
- Counted in memory and saved to Postgres every 30 s and at shutdown
  (new tables `usage_daily`, kept 400 days, and `query_daily`, kept 90
  days). Admin test searches are counted too (they cost money).
- **Monthly budget (D5, $5 since step 10):** settings `BUDGET_MONTHLY_USD` (0 = none) and
  `BUDGET_ALERT_PERCENT` (80). At 80 % and at 100 % of the month's
  estimated cost: one `ERROR` log line and one audit entry `budget.alert`
  per level and month; the dashboard shows `ok` / `warning` / `exceeded`.
  Searches are **not** blocked (a hard stop can be added if wanted).
- Costs are estimates from the cost settings of step 8
  (`SERPAPI_SEARCH_COST_USD`, `APIFY_<SHOP>_RUN_COST_USD`): **set them**,
  otherwise every cost shows 0.
- Tested: unit tests (counters, report, budget, store against Postgres 16,
  admin endpoint) and an end-to-end run: searches through the public API,
  dashboard after the flush, budget alert logged and audited once per
  level, counts saved at shutdown.

**Owner, to try step 9** (after step 8 works, see below):

1. Set costs and budget (one PATCH or `settings set`):
   `{"changes":{"SERPAPI_SEARCH_COST_USD":"0","APIFY_TEMU_RUN_COST_USD":"0.10","APIFY_SHEIN_RUN_COST_USD":"0.033","APIFY_ALIEXPRESS_RUN_COST_USD":"0.02","BUDGET_MONTHLY_USD":"5"}}`
   (SerpApi free plan = 0; Developer plan ≈ 0.015 per search.)
   (D5 answered at step 10: $5 is now the default.)
2. Run a few searches (repeat one to see a cache hit), wait 30 s, then
   `curl -H "Authorization: Bearer $ADMIN_TOKEN" "localhost:3002/admin/v1/usage?days=7"`.
   Does the estimated cost match the Apify console (Billing → usage)?

**Phase 2, step 8 — admin API: done (2026-10-11).** Details: server
README section "Admin API", PLAN.md step 8.

- `/admin/v1/...`, separate from the public API (only with a database).
  Every request: valid Firebase token **+ two-factor sign-in + sign-in
  not older than 12 h + active entry in the admin list** (D2). The first
  sign-in with a verified email links the Firebase login to the entry.
- Settings: list (secrets only "set"), change (validated as a whole,
  refused if invalid, applied within 10 s), history. Secrets write-only
  (D3).
- Countries and shops: now the runtime setting `MARKETS` (whole
  `markets.json` format), `GET` / `PUT` / `DELETE /admin/v1/markets`,
  applied without restart. Without it, `MARKETS_FILE` / built-in file.
- Admin list: add, disable, enable, unlink (not yourself, never the last
  active admin).
- Audit log (new table `audit_log`): every change, test search, cache
  tool and refused login, with who / when / IP / request id; never secret
  values. `GET /admin/v1/audit`, CLI `audit`.
- Tools: **test search** (asks each shop's source directly — no cache,
  quota, breaker or fallback — and shows products, time, error and
  estimated cost; `all_sources` also asks the fallbacks), forget one cached
  search, bump the results version (`CACHE_GENERATION`), circuit breaker
  state per source.
- Estimated costs are settings: `SERPAPI_SEARCH_COST_USD`,
  `APIFY_<SHOP>_RUN_COST_USD` (default 0 = unknown; suggested values in
  `.env.example`). Step 9 builds the dashboard on them.
- Rate limits: per IP, per admin, test search ≈ 1 per 6 s.
- Command line also: `admins add|disable|enable|unlink EMAIL`, `audit`
  (for setup and when every admin is locked out).
- Tested: unit tests (admin access rules, every endpoint, probe, breaker
  health, cache forget, store against Postgres 16) and an end-to-end run
  of the binary with a fake Firebase (JWKS) and Postgres: refused logins
  audited, settings change applied in 1 s, secret never in logs / audit /
  database in clear, test search with all sources, cache bump, CLI.

**Two-factor admin login: disabled for now (owner, 2026-10-11).** The
check and its setting `ADMIN_REQUIRE_2FA` stay in the code; the default
is `false`, so admins sign in with Firebase + the admin list only (the
server logs a warning). **To do before production (phase 4, step 14):**
turn on Firebase *Identity Platform* (Authentication → Settings → upgrade;
free tier), let admins enrol a second factor (sign-in page of the admin
web app, step 10) and set `ADMIN_REQUIRE_2FA=true`.

**Admin email verification: disabled for now (owner, 2026-10-11).** The
check and its setting `ADMIN_REQUIRE_VERIFIED_EMAIL` stay in the code; the
default is `false`, so a test admin with a made-up email (that cannot
receive the verification link) can sign in once it is on the admin list.
**To do before production (phase 4, step 14):** use real admin emails,
verify them, and set `ADMIN_REQUIRE_VERIFIED_EMAIL=true` (otherwise
whoever first creates a Firebase login with an admin's email could claim
that admin entry).

**Note (owner, 2026-10-11): fake emails in Firebase during development.**

- Firebase Authentication only checks that an email *looks* valid
  (`user@domain.com`); it does not check that the inbox exists. Fake /
  dummy emails (`test@test.com`, `user@example.com`) are fine for
  development and testing.
- Other sign-in methods (phone, anonymous, custom / third-party SSO) show
  a phone number, a UID or a custom string as the identifier instead of
  an email.
- Caveats: verification and password-reset emails never arrive at a fake
  address; in production fake emails are discouraged (no way to contact
  users, recover accounts or send security notices).
- Good practice while developing: use reserved domains (`@example.com`,
  `@test.com`) so nothing is ever mailed to a stranger; skip the
  verification check (what `ADMIN_REQUIRE_VERIFIED_EMAIL=false` does) or
  mark test users verified with the Firebase Admin SDK; or use the
  **Firebase Local Emulator** (Auth), which accepts any email and shows
  verification / reset links in its local UI without sending mail.

**Owner, to try step 8:**

1. In `.env`: `ADMIN_EMAIL=<your Firebase login email>` (already set at
   step 7); `ADMIN_REQUIRE_2FA` can stay out (default `false`);
   `docker compose up -d --build`.
2. The email must be verified once: sign in (`accounts:signInWithPassword`,
   README step 5), then
   `curl -s -X POST "https://identitytoolkit.googleapis.com/v1/accounts:sendOobCode?key=<WEB_API_KEY>" -H "Content-Type: application/json" -d '{"requestType":"VERIFY_EMAIL","idToken":"<idToken>"}'`
   and open the link in the email. Sign in again → new `idToken` =
   `$ADMIN_TOKEN`.
3. `curl -H "Authorization: Bearer $ADMIN_TOKEN" localhost:3002/admin/v1/me`
   → your email. Then try `GET /admin/v1/settings`, a `PATCH`, a test
   search (examples in the server README, section "Admin API"), and
   `docker compose exec server /owis_find_deal_engine audit`.
4. Optional: set the cost estimates, e.g.
   `{"changes":{"APIFY_TEMU_RUN_COST_USD":"0.10","APIFY_SHEIN_RUN_COST_USD":"0.033","APIFY_ALIEXPRESS_RUN_COST_USD":"0.02"}}`.

**Next: phase 3, step 12 — integration endpoints:** asynchronous search
for slow live searches (`POST /api/v1/searches` → `202` + id,
`GET /api/v1/searches/{id}`, optional signed webhook), per-client usage
counters on the dashboard (base for billing partners later).

**Phase 2, step 7 — database + settings store: done (2026-10-11).**

- Postgres 17 joins the stack (`postgres` service on the internal `data`
  network, `postgres-data` volume, non-root, read-only, no capabilities).
- Tables `settings`, `settings_history`, `admins` (embedded migrations,
  run at start).
- Runtime settings (sources, shops, Apify / SerpApi, timeouts, cache,
  plans, rate limits, CORS) live in the database; `.env` keeps only the
  bootstrap values (database, encryption key, first admin, auth, Redis,
  service addresses). **On the first start the runtime settings in `.env`
  are copied into the database once**; after that `.env` no longer changes
  them (the server logs a warning for keys that differ).
- A change is validated as a whole and refused if invalid; the server sees
  it within 10 s and swaps a new provider chain + cache front + router —
  no restart, searches in flight finish on the old setup.
- Secrets (`SERPAPI_KEY`, `APIFY_TOKEN`): AES-256-GCM with
  `SETTINGS_ENCRYPTION_KEY`, never printed, never in the history.
- First admin from `ADMIN_EMAIL` (used from step 8).
- Until the admin panel exists: command line in the container —
  `docker compose exec server /owis_find_deal_engine settings list`
  (`get`, `set`, `unset`, `history`; `admins list`). See the server README
  section "Settings".
- Tested: unit tests + store tests against Postgres 16 locally (CI runs
  them against Postgres 17), an end-to-end run of the binary (import from
  env, CLI change, hot reload, restart), and **by the owner on the Docker
  stack (2026-10-11, Windows, Git Bash): four containers healthy, `.env`
  imported correctly, a change applied without restart ("settings
  applied", settings version 37)**.

**Owner, to try step 7:**

1. Add to `.env` (each `openssl rand -hex 32`):
   `POSTGRES_PASSWORD=…`, `SETTINGS_ENCRYPTION_KEY=…` (keep a copy: the
   stored secrets cannot be read without it), and
   `ADMIN_EMAIL=<your Firebase login email>`.
2. `docker compose up -d --build` → four containers healthy; the server
   log shows "runtime settings imported from the environment".
3. `docker compose exec server /owis_find_deal_engine settings list` — do
   the values match your `.env`? Secrets show "(set, hidden)".
   (Git Bash: `export MSYS_NO_PATHCONV=1` first, otherwise the `/…` path
   is turned into a Windows path. Owner check 2026-10-11: containers
   healthy, import correct.)
4. Change one, e.g. `… settings set APIFY_SHEIN_MAX_ITEMS 3`, and check
   the log line "settings applied" within ~10 s, then a search.
5. Report problems with `docker compose logs server postgres`.


### Phase 1 (search service)

**Phase 1 — search service: complete and tested locally** (plan steps
1–6, see [PLAN.md](PLAN.md) section 9).

- Countries `usa`, `ksa`, `jor` → only the shops that deliver there
  ([markets.json](server/internal/markets/markets.json)).
- Login required: JWT from the auth provider, verified locally (JWKS).
- Cache with price-freshness strategy (fresh 48h, stale-while-revalidate
  up to 72h, pull-to-refresh; was 2h / 24h until 2026-10-07). **Cached answers are free** for users.
- Daily allowance per plan: free plan used up → 402, paid → 429.
- Docker image + full stack in `docker-compose.yml` on private networks.

### Local test (2026-10-06): sources that work

Lesson: free web search (SearXNG) gets blocked quickly from one IP and
rarely finds product pages; each shop needs its own source.

| Shop | Source | Result |
|---|---|---|
| Amazon | SerpApi Amazon engine (on by default with `SERPAPI_KEY`) | ✅ ~10 products, price, rating, direct `/dp/` links, ~3 s |
| AliExpress | Apify `piotrv1001/aliexpress-listings-scraper` | ✅ 10 products, price, direct `/item/` links, ~40 s |
| Temu | Apify `crw/temu-products-scraper` (US catalogue only) | ✅ 10 products, price, `goods.html?goods_id=` links; empty for some items |
| SHEIN | Apify `clearpath/shein-product-scraper` (site `us`, needs `APIFY_MAX_CHARGE_USD`) | ✅ live 2026-10-07 (`usa`): 8 products, sale price, direct `-p-<id>.html` links |
| eBay (usa, ksa) | SerpApi eBay engine (`serpapi-ebay`, on by default with `SERPAPI_KEY`) | ✅ live 2026-10-07 (`ksa`): 10 products, direct `/itm/` links, price matches the page, condition + shipping |

Example `m.2 enclosure` / `jor`: 31 products from 4 shops with prices,
28.6 s live (both scrapers run in parallel), instant from cache.
Cost per live search: 1–2 SerpApi searches + 2 Apify runs.

Working `.env` (besides auth, secrets and keys):

```ini
SEARCH_PROVIDERS=serpapi,searxng
SERPAPI_ENGINE=google_shopping
APIFY_MARKETS=aliexpress,temu
APIFY_ALIEXPRESS_ACTOR=piotrv1001/aliexpress-listings-scraper
APIFY_ALIEXPRESS_INPUT={"maxResults":{{max}},"searchQueries":["{{query}}"],"proxyConfiguration":{"useApifyProxy":true}}
APIFY_ALIEXPRESS_CURRENCY=USD   # the scraper returns "currency": null
APIFY_TEMU_ACTOR=crw/temu-products-scraper
APIFY_TEMU_INPUT={"keyword":"{{query}}","max_items":10,"region":"US","sort":"relevance"}   # Actor needs >= 10
# SHEIN (added 2026-10-07, not yet tested live): add ",shein" to APIFY_MARKETS
APIFY_SHEIN_ACTOR=clearpath/shein-product-scraper
APIFY_SHEIN_INPUT={"enrichDetails":false,"maxItemsPerSearch":{{max}},"quickShip":false,"searchTerms":["{{query}}"],"site":"us","sortBy":"recommend","category":""}
APIFY_TEMU_MAX_ITEMS=10    # charged for 10 anyway ($0.10 per run)
APIFY_SHEIN_MAX_ITEMS=5    # $0.0066 per result
APIFY_TIMEOUT=90s          # 60s was too short for AliExpress sometimes
APIFY_MAX_CHARGE_USD=0.20   # cost cap per run, needed by per-event scrapers (SHEIN)
APIFY_EMPTY_FALLBACK=false   # nothing relevant from a scraper = no results (no extra time or SerpApi search)
```

**Prices are US prices (known limit, accepted):** the Apify scrapers read
the US sites, so AliExpress / Temu prices often differ from what a user in
Jordan sees on the page (local price, currency, shipping, personal
discounts, variant). It differs per product and cannot be fixed in the
service. In the app: show them as "from $X · approx." — the link shows
the real price.

Latest checks (2026-10-06): duplicates within a shop removed (cheapest
kept); Apify items must match the query; a scraper with nothing relevant
ends as `no_results` (`APIFY_EMPTY_FALLBACK=false`, ~42 s instead of ~52 s
for "EAGET JHL7440"); cache keys carry a results version (`v4`), so a
rebuild drops answers cached by an older build.

## SHEIN (2026-10-07): mapped, waiting for the owner's live test

First live test (`usa`): the SHEIN run was **aborted** ("reached its
maximum cost of $0.00") — `clearpath/shein-product-scraper` is billed per
event and the service sent no cost limit, so SHEIN fell back to Google
Shopping. Fix: new `APIFY_MAX_CHARGE_USD` (default `0.20`, sent as
`maxTotalChargeUsd`; ignored by per-result scrapers).

Retest (`usb c ssd enclosure` / `usa`): SHEIN ✅ 8 products from Apify.
But AliExpress fell back to Google Shopping (search links) and the search
took 67 s (limit 70 s) — probably the AliExpress scraper hit
`APIFY_TIMEOUT=60s`; waiting for the log line. Cost of that live search:
3 SerpApi credits (Amazon, eBay, AliExpress fallback) + Apify runs.

With `APIFY_TIMEOUT=90s` (`iphone 18 max case` / `jor`): all 4 shops OK,
no fallback, 36 s, 1 SerpApi credit (Amazon only). SHEIN links stay on
`us.shein.com`. AliExpress: the scraper returns `"currency": null`
(prices are US prices; the page in Jordan showed JD 1.52 vs $10.42 from
the scraper — new-shopper / deal prices are not returned, accepted) →
new optional `APIFY_<MARKET>_CURRENCY` (`APIFY_ALIEXPRESS_CURRENCY=USD`).
Apify usage so far (2026-10-06/07, all tests incl. console runs): $1.66 of
the $5 monthly credit — Temu $0.80, SHEIN clearpath $0.65, AliExpress
$0.17, SHEIN shahidirfan $0.04. Temu and SHEIN are the cost drivers;
cost per run still to work out (runs per Actor). SHEIN run from the
service: 10 results, 8 s, **$0.066** (≈ $0.0066 per result, billed per
event). SHEIN links in `jor` open on `us.shein.com` with the same price
as returned ($3.00) — accepted. Temu run: 10 results, 12 s, **$0.100**
($0.01 per result). AliExpress ≈ $0.02 per run (estimate from the total).

**Cost of one live search (10 results per shop): ≈ $0.19 Apify + 1 SerpApi
search (Amazon).** $3.34 left this month ≈ 17 live test searches; cached
answers are free.

**Owner decisions (2026-10-07), built:** SHEIN 5 results per search
(`APIFY_SHEIN_MAX_ITEMS=5`; Temu stays at 10, see below — its scraper
charges for at least 10 anyway) and cache fresh for 48 h (`CACHE_FRESH_TTL=48h`,
`CACHE_STALE_TTL=72h`; now the defaults). Expected live search ≈ $0.10
Apify + 1–2 SerpApi searches, then free for 48 h.

**Apify platform prices (owner's account, 2026-10-07) — what they mean
for us.** Two ways an Actor is billed:

- *Per result / per event* (Temu `crw/…` $0.01 per result, SHEIN
  `clearpath/…` ≈ $0.0066 per result): the Actor's own price covers
  everything; the platform prices below are **not** added on top.
- *Per usage* (Actors without their own price): we pay the platform
  prices below for what the run uses.

| Item | Price | Relevant for us? |
|---|---|---|
| Compute units (CU) | $0.20 / CU | Yes, for per-usage Actors. 1 CU = 1 GB memory × 1 hour; a 60 s run with 1 GB ≈ 0.017 CU ≈ $0.003 |
| Datacenter proxies | 5 IPs included | Yes — `"useApifyProxy": true` uses these by default (no extra cost) |
| Residential proxies | $8.00 / GB | Avoid unless a shop blocks datacenter IPs; a scraper page load can be 1–5 MB |
| Google SERP proxies | $2.50 / 1,000 searches | Not used. Possible later: Google Shopping via Apify (~$0.0025 per search) instead of SerpApi |
| Unblocker | $1.50 / 1,000 requests | Not used |
| Dataset storage / reads / writes | $1.00 per 1,000 GB-hours; $0.0004 / 1,000 reads; $0.005 / 1,000 writes | Negligible (10 items per run) |
| Key-value store | $1.00 per 1,000 GB-hours; $0.005 / 1,000 reads; $0.05 / 1,000 writes; $0.05 / 1,000 lists | Negligible |
| Request queue | $4.00 per 1,000 GB-hours; $0.004 / 1,000 reads; $0.01 / 1,000 writes | Negligible |
| Data transfer | $0.20 / GB external, $0.05 / GB internal | Negligible (results are a few KB) |

So the cost per search is driven by the Actors' own per-result prices
(Temu, SHEIN), not by these platform prices. When choosing a new scraper,
compare: per-result price × results per search, or for per-usage Actors
memory × run time × $0.20 / CU (plus residential proxy if it needs one).

**SHEIN scraper: keep `clearpath/shein-product-scraper` for now.** It
supports the US site only (no `sa` / `ar`), so SHEIN links and prices are
US ones in every country. Cost scales with the result count: a console
run with 100 results cost $0.516 (≈ $0.005 per result; the `category`
field does not change that). **Later: check another SHEIN scraper** if one
is cheaper or supports the Saudi / Arab sites (`sa`, `ar`, `ae`); the
service only needs its Actor, input and one sample item.
Console runs use the account balance as their cost limit (the service
sends `APIFY_MAX_CHARGE_USD=0.20`), so keep `maxItemsPerSearch` small
when testing in the console.

Scraper: `clearpath/shein-product-scraper`. Its items carry the sale price
as `price.current` (flat, as the console exports it) or
`{"price": {"current": …, "original": …, "currency": "USD"}}` (nested);
both are read, and the price before discount (`original`) is never used.
Title = `name`, link = `url` (`…-p-<id>.html`, already accepted by
`markets.json`), image = `image`. Test: `TestSheinProductScraperOutput`
(usa, ksa, jor). Cache results version bumped to `v5`.

Links point to `us.shein.com` also for `ksa` / `jor` (prices are US prices,
same accepted limit as AliExpress / Temu); SHEIN usually redirects by
location.

**Owner, next:**

1. In `.env`: `APIFY_MARKETS=aliexpress,temu,shein` and the two
   `APIFY_SHEIN_*` lines above, then `docker compose up -d --build`.
2. Search `usa`, `ksa`, `jor` (e.g. `ssd usb 3.0 enclosure`,
   `phone case`): do SHEIN products appear with price and working links?
   How long does the search take now (3 scrapers run in parallel)?
3. Rejected: `shahidirfan/shein-product-scraper` (tested 2026-10-07 with
   `startUrl` = `https://us.shein.com/pdsearch/ssd%20usb%20enclosure/`):
   it returned 20 women's clothing items and a category link — it reads
   listing / category pages, not search results. Its fields would map
   (`title`, `url` on `m.shein.com/us/…-p-<id>.html`, `sale_price`,
   `image_url`), but the relevance filter would drop everything.
   `{{query_path}}` stays available for URL-input scrapers.
4. Check in the Apify console whether the scraper's `site` field offers
   `ar` / `sa` (Arab / Saudi site). If yes, report it: the input can use
   `{{country}}` and links would point to the local site.

**eBay (2026-10-07):** SerpApi's eBay engine (`engine=ebay`,
`ebay_domain=ebay.com`) replaces Google Shopping for eBay: direct
`/itm/<id>` links, eBay's price (lowest of a range), condition, shipping.
Same cost as before (1 SerpApi search). Live `ksa` test ✅ (Amazon and
eBay links show the same price as returned). eBay Browse API (free) can
replace it if the keys come.

Same test: Temu's Apify run failed — `crw/temu-products-scraper` needs
`max_items >= 10`, so `{{max}}` = 5 was rejected; Temu fell back to Google
Shopping (+1 SerpApi credit) and SearXNG, which gave one bad result
(title "Temu", link on the Slovak site `/sk-en/…`, removed there). Fixed:
Temu input uses a fixed `"max_items":10` (`APIFY_TEMU_MAX_ITEMS=5` still
caps kept / charged items via the run's `maxItems`); Temu links are
rebuilt as `goods.html?goods_id=<id>` (no country prefix); web results
whose title is only the shop name are dropped. Cache version `v6`.

Retest (`fast charger type c` / `jor`): ✅ all 4 shops, no fallback, 26 s,
1 SerpApi credit (Amazon). Temu 4 + SHEIN 5 results (limit 5). Apify
cost of the search ≈ $0.16 ($2.25 → $2.41). Confirmed: the Temu run cost
$0.100 for 10 results although only 5 were kept — `crw/temu-products-scraper`
charges for every result it scrapes (min 10), the run's `maxItems` does not
cap it. → `APIFY_TEMU_MAX_ITEMS=10` (same price, all 10 shown).
AliExpress: many prices ($0.33, $1.09; page in Jordan "JD0.77, save
JD3.77", timed) are new-shopper **welcome deals** — not what most users
pay. Decision: nothing in the service; **the app shows a note** (e.g.
"AliExpress prices may be new-shopper offers").

**Cost of one live search now: ≈ $0.155 Apify** (Temu $0.10 for 10, SHEIN
≈ $0.033 for 5, AliExpress ≈ $0.02) **+ SerpApi** (1 search in `jor`:
Amazon; 2 in `usa` / `ksa`: Amazon + eBay), then free for 48 h (cache).

**Other open items:** app note on AliExpress welcome-deal prices; loading
bar in the app (first live search takes ~30–50 s; cached answers are
instant); label scraper prices as approximate in the app.

**New plan (owner, 2026-10-07)** — see [PLAN.md](PLAN.md) section 9:

1. **Phase 2 — admin panel:** Postgres + settings store (settings move
   from `.env` to the database, applied without restart), admin API with
   admin role and audit log, write-only encrypted secrets, test search,
   usage / cost dashboard, admin web app under `/admin`.
2. **Phase 3 — API integration:** API keys for other applications and AI
   agents (hashed, scoped, quotas, revoke / rotate), async search +
   webhooks, MCP server for agents, OpenAPI spec.
3. **Phase 4 — deployment** of everything (hosting, HTTPS, backups,
   CI/CD, monitoring).
4. **Phase 5 — users** (old phase 2).

**Phase 2 (steps 7–10) is done (2026-10-11); phase 3, step 11 (API clients and keys) is done (2026-10-15); next session starts step 12 (integration endpoints).**
**Owner decisions D1–D4 (answered 2026-10-11):**

- **D1 admin web app:** small React app built into the server image,
  served under `/admin` (one deployment).
- **D2 admin login:** Firebase login + admin list in the database +
  two-factor login required for admins. (Needed at step 8: the first
  admin's email.)
  *Update 2026-10-11:* two-factor is **disabled for now**
  (`ADMIN_REQUIRE_2FA=false`, setting kept) and turned on before
  production.
- **D3 secrets:** changeable in the admin panel — encrypted
  (AES-256-GCM, key from the environment), write-only, never shown again.
- **D4 API clients:** own apps and AI agents first; keys created by the
  owner in the admin panel. API keys are for servers and agents only —
  the mobile / web app keeps the user's Firebase token (a key inside an
  app could be extracted). Keys stored hashed in the database, not in
  `.env`. Partner self-service sign-up and billing later, on the same
  key model.

D5 answered: $5 per month (free plans). Later: D6 hosting and D7 domain
(step 14), D8 paid Apify / SerpApi plans (before real users).

## To resume in a new Claude Code session

Open the repository and say:

> Continue the Owis_Find_Deal_Engine project from
> `Owis_Find_Deal_Engine/STATUS.md` on branch `claude/cloud-vs-local-7xi988`.
> Start phase 3, step 12. (Decisions D1–D5 are recorded in STATUS.md.)

## Run it on your PC

Needs: **Docker Desktop** (Windows/macOS) or Docker Engine (Linux), and
git. Go is not needed for the Docker way.

### 1. Get the code

```sh
git clone https://github.com/owisdev/claude-ai-clould-repo.git
cd claude-ai-clould-repo
git checkout claude/cloud-vs-local-7xi988
cd Owis_Find_Deal_Engine/server
```

### 2. Create a free Firebase project (for login tokens)

1. <https://console.firebase.google.com> → **Add project** (free plan).
2. **Build → Authentication → Get started** → enable **Email/Password**
   (enough for testing; Google/Apple/Facebook come with the app).
3. **Project settings → General**: note the **Project ID** and the **Web
   API key** (if no key is shown, add a Web app there).

### 3. Configure

Copy `.env.example` to `.env` and set at least:

```ini
AUTH_JWKS_URL=https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com
AUTH_ISSUER=https://securetoken.google.com/<PROJECT_ID>
AUTH_AUDIENCE=<PROJECT_ID>

SEARXNG_SECRET=<random 64 hex chars>
REDIS_PASSWORD=<random 64 hex chars>
POSTGRES_PASSWORD=<random 64 hex chars>
SETTINGS_ENCRYPTION_KEY=<random 64 hex chars>   # keep a copy
ADMIN_EMAIL=<your Firebase login email>

# Free only. Add ",serpapi" and SERPAPI_KEY=... to enable the paid fallback.
SEARCH_PROVIDERS=searxng
```

Random values: `openssl rand -hex 32` (macOS/Linux/Git Bash), or in
PowerShell: `-join ((1..32) | % { '{0:x2}' -f (Get-Random -Max 256) })`.

### 3b. Optional: prices from Google Shopping and Apify

- **Google Shopping** (250 free searches/month): create a key at
  <https://serpapi.com>, then set `SERPAPI_KEY=...` and
  `SEARCH_PROVIDERS=serpapi,searxng` (prices first) or
  `searxng,serpapi` (free first, SerpApi only as fallback).
- **Apify for Temu / SHEIN** ($5 free credit/month): create a token at
  <https://console.apify.com/settings/integrations>, choose one scraper per
  shop in the Apify store and fill `APIFY_MARKETS`, `APIFY_TOKEN`,
  `APIFY_TEMU_ACTOR`, `APIFY_TEMU_INPUT`, … — step-by-step comments are in
  `.env.example`. Tip: try the scraper once in the Apify console first to
  see that it returns products for your search.

### 4. Start

```sh
docker compose up -d --build
docker compose ps          # all four "healthy" after ~30 s
```

### 5. Get a test token (valid 1 hour)

macOS/Linux/Git Bash:

```sh
curl -s -X POST "https://identitytoolkit.googleapis.com/v1/accounts:signUp?key=<WEB_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"email":"test1@example.com","password":"Test-pass-123","returnSecureToken":true}'
# copy "idToken" from the answer. Next time use accounts:signInWithPassword
# (same body) instead of accounts:signUp.
```

PowerShell:

```powershell
$r = Invoke-RestMethod -Method Post -ContentType "application/json" `
  -Uri "https://identitytoolkit.googleapis.com/v1/accounts:signUp?key=<WEB_API_KEY>" `
  -Body '{"email":"test1@example.com","password":"Test-pass-123","returnSecureToken":true}'
$TOKEN = $r.idToken
```

### 6. Try it

```sh
curl localhost:3002/api/v1/health
curl localhost:3002/api/v1/countries

curl -i -X POST localhost:3002/api/v1/search \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"title":"samsung s pen","country":"jor"}'
```

(PowerShell: use `curl.exe` and `"Authorization: Bearer $TOKEN"`; put the
JSON body in single quotes.)

Repeat the same search: `X-Cache: HIT` and `X-RateLimit-Remaining` does
not go down. Logs: `docker compose logs -f server`.
SearXNG directly (for debugging): stop, then
`docker compose -f docker-compose.yml -f compose.dev.yml up -d` and open
<http://127.0.0.1:8888>.

Stop: `docker compose down` (add `-v` to also delete Redis data and the
settings database — the next start imports `.env` again).

Changing search settings after the first start: `.env` is no longer read
for them; use `docker compose exec server /owis_find_deal_engine settings
set KEY VALUE` (applied within ~10 s, no restart).

## Feedback checklist

Please report:

1. Did `docker compose up -d --build` work? All four containers healthy?
2. Search results for a few products per country (`usa`, `ksa`, `jor`):
   - number of results, which shops appear, are links correct?
   - `"provider"` in results (`searxng` or `serpapi`)?
   - any shop missing or wrong (e.g. eBay showing for `jor`)?
3. Response time of the first (live) and second (cached) search.
4. Any errors (status code + `docker compose logs server` lines).
5. Anything you want changed in the response format before the app is
   built on it.
6. Status of the eBay and AliExpress developer applications.
7. If you enabled them: Google Shopping and Apify results (prices
   correct? which scrapers you chose, how long the Apify searches took).

## Future features (noted)

- **Search by photo (Google Lens)** — see PLAN.md section 5.5.
- **Reminder for the app (phase 2):** Firebase email links (verify email,
  reset password, sign-in link) must use the Firebase Hosting domain and
  `linkDomain`, not Dynamic Links — see PLAN.md section 5.6. Not needed for
  the local test (the test token comes from `accounts:signUp`, no email).

## Open decisions (owner)

- Hosting (PLAN.md section 10) — not needed until deployment.
- Payment provider for paid plans — phase 2.
