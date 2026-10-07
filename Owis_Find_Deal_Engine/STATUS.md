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
- Cache with price-freshness strategy (fresh 2h, stale-while-revalidate
  up to 24h, pull-to-refresh). **Cached answers are free** for users.
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
| SHEIN | Apify `clearpath/shein-product-scraper` (site `us`) | 🔧 output mapped and tested with the owner's sample (2026-10-07); live retest pending |
| eBay (usa, ksa) | fallback only | not tested yet |

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
APIFY_TEMU_ACTOR=crw/temu-products-scraper
APIFY_TEMU_INPUT={"keyword":"{{query}}","max_items":{{max}},"region":"US","sort":"relevance"}
# SHEIN (added 2026-10-07, not yet tested live): add ",shein" to APIFY_MARKETS
APIFY_SHEIN_ACTOR=clearpath/shein-product-scraper
APIFY_SHEIN_INPUT={"enrichDetails":false,"maxItemsPerSearch":{{max}},"quickShip":false,"searchTerms":["{{query}}"],"site":"us","sortBy":"recommend","category":""}
APIFY_TIMEOUT=60s
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
3. Alternative scraper `shahidirfan/shein-product-scraper` (more users):
   it takes a URL, not a keyword. The service can already fill it with a
   SHEIN search URL (`{{query_path}}`, e.g. `ssd%20usb%20enclosure`):
   `APIFY_SHEIN_INPUT={"startUrl":"https://us.shein.com/pdsearch/{{query_path}}/","results_wanted":{{max}},"proxyConfiguration":{"useApifyProxy":true}}`.
   Its output is not mapped yet: run it once in the console with
   `startUrl` = `https://us.shein.com/pdsearch/ssd%20usb%20enclosure/`
   (and once with `ar.shein.com`) and send one or two output items. If it
   reads `ar.shein.com`, it could give local links / prices for ksa, jor.
4. Check in the Apify console whether the scraper's `site` field offers
   `ar` / `sa` (Arab / Saudi site). If yes, report it: the input can use
   `{{country}}` and links would point to the local site.

**Other open items:** eBay (usa, ksa: only the fallback today); loading
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
