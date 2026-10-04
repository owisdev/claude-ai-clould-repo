# Project status and handoff

Last updated: 2026-10-02 · Branch: `claude/cloud-vs-local-7xi988`
(not merged into `main` yet).

Read first: [docs/architecture.pdf](docs/architecture.pdf) — the full
architecture and flows, illustrated (regenerate with
`python3 docs/build_architecture_pdf.py`).

## Where we are

**Phase 1 — search service: complete** (plan steps 1–6, see
[PLAN.md](PLAN.md) section 9). CI is green on GitHub.

- Countries `usa`, `ksa`, `jor` → only the shops that deliver there
  ([markets.json](server/internal/markets/markets.json)).
- Login required: JWT from the auth provider, verified locally (JWKS).
- Free SearXNG first, SerpApi only as fallback, with circuit breaker.
  SerpApi uses **Google Shopping** (prices, seller, image) by default.
- Optional **Apify** scrapers for Temu / SHEIN (real prices), each with an
  automatic fallback to the web search.
- Cache with price-freshness strategy (fresh 2h, stale-while-revalidate
  up to 24h, pull-to-refresh). **Cached answers are free** for users.
- Daily allowance per plan: free plan used up → 402, paid → 429.
- Docker image + full stack in `docker-compose.yml` on private networks.

**Now waiting for:** the owner runs the stack locally and gives feedback
(checklist at the end). No new work until then.

**Next after feedback:** phase 2 (users: profile, plans/payments, saved
cart + price tracker, purchase reports, notifications) or phase 3 first
(eBay / AliExpress APIs if the keys were approved).

## To resume in a new Claude Code session

Open the repository and say:

> Continue the Owis_Find_Deal_Engine project from
> `Owis_Find_Deal_Engine/STATUS.md` on branch `claude/cloud-vs-local-7xi988`.
> Here is my feedback from running it locally: …

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

## Open decisions (owner)

- Hosting (PLAN.md section 10) — not needed until deployment.
- Payment provider for paid plans — phase 2.
