# Project status and handoff

Last updated: 2026-10-07 · Branch: `claude/cloud-vs-local-7xi988`
(not merged into `main` yet).

Read first: [docs/architecture.pdf](docs/architecture.pdf) — the full
architecture and flows, illustrated (regenerate with
`python3 docs/build_architecture_pdf.py`). Note: the PDF predates the
local test changes below (dedicated sources per shop).

## Where we are

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
APIFY_TEMU_MAX_ITEMS=5     # $0.01 per result
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

**Owner decisions (2026-10-07), built:** Temu and SHEIN 5 results per
search (`APIFY_TEMU_MAX_ITEMS=5`, `APIFY_SHEIN_MAX_ITEMS=5`; ≈ $0.08
instead of $0.17 for both) and cache fresh for 48 h (`CACHE_FRESH_TTL=48h`,
`CACHE_STALE_TTL=72h`; now the defaults). Expected live search ≈ $0.10
Apify + 1 SerpApi search, then free for 48 h.

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

**Other open items:** loading
bar in the app (first live search takes ~30–50 s; cached answers are
instant); label scraper prices as approximate in the app.

**After that:** phase 2 (users: profile, plans/payments, saved cart +
price tracker, purchase reports, notifications) or phase 3 (eBay /
AliExpress official APIs if the keys were approved).

## To resume in a new Claude Code session

Open the repository and say:

> Continue the Owis_Find_Deal_Engine project from
> `Owis_Find_Deal_Engine/STATUS.md` on branch `claude/cloud-vs-local-7xi988`.
> SHEIN live test results: …

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
docker compose ps          # all three "healthy" after ~30 s
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

Stop: `docker compose down` (add `-v` to also delete Redis data).

## Feedback checklist

Please report:

1. Did `docker compose up -d --build` work? All three containers healthy?
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
