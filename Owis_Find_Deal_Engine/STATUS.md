# Project status and handoff

Last updated: 2026-11-08 · Project branch: **`main`** from step 14,
step 2 on (owner, 2026-11-06, Q8: the old project branch
`claude/cloud-vs-local-7xi988` goes into `main` with PR #8 and stays
frozen; new step PRs target `main`). Steps 7–12: PRs #9–#11, step 13:
#12, step 14 step 1: #17, step 2: #18, step 3: see the PR from
`claude/peaceful-meitner-wml0f0`.

Read first:
- [docs/step13_guide.pdf](docs/step13_guide.pdf) — **step 13**: what the
  MCP endpoint for AI agents, the OpenAPI description and the developer
  docs page do, and the test steps 0–10 with the Git Bash commands
  (26 Oct 2026).
- [docs/step12_guide.pdf](docs/step12_guide.pdf) — **step 12**: what the
  asynchronous search, signed webhooks and per-client usage do, and the
  test steps 0–17 with the Git Bash commands (updated after the owner's
  test, 24 Oct 2026).
- [docs/service_guide.pdf](docs/service_guide.pdf) — **complete guide of the
  service as it is now** (16 Oct 2026, after step 11): architecture, Docker,
  request flow, cache, API clients, cost measurement, code map, how to run /
  use / test locally (git + docker commands), remaining work and
  recommendations. Step 12 (async search, webhooks, per-client usage) is
  not in it yet: see below and the server README.
- [docs/current_flow.pdf](docs/current_flow.pdf) — **how the service works
  today** (7 Oct 2026): sources per shop, request flow, 48 h cache, costs,
  configuration, known limits, test results.
- [docs/next_plan.pdf](docs/next_plan.pdf) — **the new plan** (admin panel →
  API integration → deployment → users) and the owner's decisions D1–D8
  with an answer sheet.
- [docs/architecture.pdf](docs/architecture.pdf) — the original full
  architecture (predates the dedicated sources per shop).

Regenerate the PDFs: `python3 docs/build_step13_guide_pdf.py`,
`python3 docs/build_service_guide_pdf.py`,
`python3 docs/build_flow_and_plan_pdfs.py` and
`python3 docs/build_architecture_pdf.py` (needs `reportlab`).

## Where we are

**Phase 4, step 14 — deployment, steps 1–4 done (2026-11-08):
production runs on the Oracle VM, public at
`https://globalprodsearch.dawirli.com` through the Cloudflare Tunnel.**
**Step 4 — Cloudflare Tunnel and `globalprodsearch.dawirli.com`: done
2026-11-08. ✅** GitHub `main`, the owner's PC and the VM in sync at
`04da381` (VM server folder = `main`, image `666ab78`).

**Start of the next session — owner picks from this list (2026-11-08):**
1. **MCP test with real agents.** Connect Claude Code on the owner's PC
   to `https://globalprodsearch.dawirli.com/mcp` with the `owner-test`
   key, then a search from a chat (PLAN step 14 note).
2. **Cost settings and scraper time limit** (admin panel through SSH):
   - per-search costs (`SERPAPI_SEARCH_COST_USD`,
     `APIFY_*_RUN_COST_USD`), so cost and budget figures stop showing
     $0;
   - `APIFY_TIMEOUT` to 75 s or less, so slow searches always finish
     within Cloudflare's 100 s limit (see the step 4 entry).
3. **Re-engineer the service's business rules (owner, 2026-11-08):**
   which exact online shops to use, and for each the source, cost, time
   and sync / async handling — "all this stuff" reviewed together
   (builds on 2 and 4; to explain, decide with the owner, then build).
4. **SHEIN.** No results three times now. Run its scraper by hand in
   Apify to see whether it is broken; until then take SHEIN out of the
   search (`APIFY_MARKETS`) to stop paying for empty runs.
5. **Step 5:** admin two-factor login and verified email.

Suggested order: **2 first** (a few minutes; removes the 100 s risk and
makes cost numbers real before more test searches), then 3.

**Step 14 — worked through one step at a time with the owner
(2026-11-06): each step is explained, approved, built, then the next.**

- **Step 1 — built 2026-11-06 (PR into the project branch):** real
  client IP + E1. New bootstrap settings `TRUSTED_PROXIES` (compose:
  `10.203.20.10`, cloudflared's fixed address on the new `tunnel`
  network `10.203.20.0/28`; others get `.0–.7`) and
  `AUTH_ALLOWED_SIGN_IN` (default `google.com,apple.com,password`, `*` =
  any). `api.WithClientIP` wraps the whole server; `remoteIP` now gives
  the caller's address (admin per-IP limit, API key `ip_allow`, audit
  log, and the request log's new `"ip"` field). Refused sign-in method →
  `401 unauthorized` with a clear message; API keys and admin login not
  affected. Tested: unit tests (header only from the trusted address,
  faked / bad / missing header, IPv6, allow-list through the tunnel,
  each sign-in method, settings parsing), race detector, and the stack
  in Docker with a fake Firebase, a fake cloudflared at `10.203.20.10`
  and an "intruder" on the tunnel network: tunnel → caller IP in log
  and audit (`203.0.113.7`), intruder's and host port's fake header
  ignored, anonymous `401`, password / Google accepted. Not run here:
  `govulncheck`, store tests (CI runs them). Owner: review and merge the
  PR; optional check on the PC (`docker compose up -d --build`, normal
  login and test search still work).
  **Owner test on the PC (2026-11-06): passed. ✅** Start line shows
  `trusted_proxies [10.203.20.10/32]` and the sign-in list; admin login
  and test search work (audit IP `172.19.0.1`); every request line has
  `"ip"`; a faked `CF-Connecting-IP: 1.2.3.4` from the PC is ignored
  (`172.19.0.1`); email/password user's search works; same search again
  `cached:true` in 0.22 s, remaining 19 unchanged (the first one was live:
  the step 13 cache entry was older than 72 h; the admin test search
  never uses the cache).
- **Step 2 — built 2026-11-06 (PR into `main`):** deployment plumbing.
  Owner decisions: **Q1** deploys by hand (each step explained, the owner
  runs the command; no automatic deploy for now), **Q8** `main`.
  - Dockerfile cross-compiles (`--platform=$BUILDPLATFORM`, `TARGETARCH`):
    arm64 without emulation; OCI source label.
  - CI (`server.yml`): after the tests, builds amd64 + arm64; on pushes
    to `main` (and *Run workflow*) publishes
    `ghcr.io/owisdev/owis_find_deal_engine:<commit>` and `:main` (labels
    `revision`, `source`); PRs build only. Checks `compose.prod.yml` and
    the scripts' syntax.
  - `compose.prod.yml`: server from ghcr.io (`IMAGE_TAG`), `cloudflared`
    `2026.10.7` at `10.203.20.10` (profile `tunnel`, `TUNNEL_TOKEN` from
    `.env`, read-only, health check `tunnel ready`). SearXNG pinned to
    `2026.11.2-cccbc9e15` in `docker-compose.yml`.
  - `deploy/bootstrap-vm.sh` (once, `sudo`): updates, Docker from
    Docker's apt repo, unattended-upgrades, SSH keys only (drop-in
    `99-ofd.conf`), Docker log rotation, docker group, clone to
    `/opt/ofd/repo`, `.env` from `.env.example` (600), listening ports.
  - `deploy/deploy.sh [commit]`: newest `:main` image or a given commit of
    `main` (rollback = older commit); moves `/opt/ofd/repo` to it,
    refuses hand-edited files / unknown / non-main / image-less commits,
    turns the tunnel on when `TUNNEL_TOKEN` is set, waits for health,
    warns if the tunnel does not connect, logs to `/opt/ofd/deploys.log`.
  - Tested here: compose configs; `deploy.sh` against a fake VM (local
    registry, bare repo as origin, real Postgres / Redis / SearXNG, fake
    Firebase): first deploy, newer, rollback by short id, unknown,
    other-branch and image-less commits, hand-edited file, tunnel on with
    a bad token (warning, server keeps running); `bootstrap-vm.sh` in an
    Ubuntu 24.04 container (systemd stubbed): Docker + compose
    installed, SSH settings applied, second run changes nothing, clear
    stop while `main` has no server, `.env` 600. Not tested here: the
    real Dockerfile build in Docker (CI builds both platforms on the
    PR), the real VM (step 3).
  - **Done by the owner 2026-11-06:** PR #8 (project branch → `main`)
    and PR #18 merged; CI published the image; package made public
    (checked: anonymous pull of `:main` lists `amd64` and `arm64`).
- **Admin email — done by the owner 2026-11-07. ✅** Email Routing on
  for `dawirli.com`; Cloudflare added MX `route1/2/3.mx.cloudflare.net`
  (priorities chosen by Cloudflare), DKIM TXT `cf2024-1._domainkey` and
  SPF `v=spf1 include:_spf.mx.cloudflare.net ~all`. Rule
  `admin@dawirli.com` → the owner's Gmail; test mail from another
  account arrived. No catch-all (spam), no sending from `@dawirli.com`
  (Gmail "Send mail as" would need an outside SMTP provider; not
  needed: we only receive Firebase and Cloudflare Access mails).
  `admin@dawirli.com` is the production admin (new Firebase user,
  `ADMIN_EMAIL` in the production `.env`, made during step 3).
  Two-factor login on the Cloudflare account and the Gmail: on
  (owner, 2026-11-07). **Q3 decided (owner, 2026-11-07): new empty
  database** in production (API clients made again in the panel;
  search sources copied once from the `.env` at the first start).
  Step 3 started 2026-11-07.
  **3.2 `bootstrap-vm.sh` run by the owner 2026-11-07: done. ✅**
  Docker 29.9.3, Compose v5.7.0 (arm64); unattended-upgrades active;
  SSH keys only (password, keyboard-interactive and root login off);
  repo at `8c05f70`; `.env` made (600). Listening on all addresses:
  22 and **111 (`rpcbind`, part of Oracle's Ubuntu image, not needed:
  owner to turn it off with `sudo systemctl disable --now rpcbind.socket
  rpcbind.service`; to add to `bootstrap-vm.sh`)**. Owner turned it
  off: only 22 left on all addresses. `docker ps` works after a new
  login.
  **3.3 production `.env`: done 2026-11-07. ✅** Local `.env` copied
  with `scp`, mode 600; new `POSTGRES_PASSWORD`, `REDIS_PASSWORD`,
  `SEARXNG_SECRET`, `SETTINGS_ENCRYPTION_KEY` (`openssl rand -hex 32`;
  encryption key saved offline by the owner); `ADMIN_EMAIL=admin@dawirli.com`,
  2FA and verified email `false`, `API_BIND=127.0.0.1`, CORS empty, no
  `WEBHOOK_ALLOW_PRIVATE`, no `TUNNEL_TOKEN`; Firebase `AUTH_*` and
  `ADMIN_FIREBASE_API_KEY`, SerpApi key, Apify token and
  `APIFY_MARKETS=aliexpress,temu,shein` from the local `.env`.
  `SEARCH_PROVIDERS=serpapi,searxng` (owner's local choice: SerpApi
  first, so a live search uses one SerpApi search when it answers).
  `AUTH_ALLOWED_SIGN_IN` not in the file: default
  `google.com,apple.com,password`. Firebase user `admin@dawirli.com`
  made by the owner (Authentication → Users → Add user).
  **3.4 first deploy: done 2026-11-07. ✅** `deploy.sh` (no argument)
  → image `b1647fc` (PR #18, the newest commit that changed the
  server; later `main` commits are docs only), tunnel off; postgres,
  redis, searxng, server all healthy; health `{"status":"ok"}`.
  **3.5 check through `ssh -L 3012:127.0.0.1:3002` (owner,
  2026-11-07):** log shows the runtime settings imported (all Apify,
  SerpApi, cache, plan keys) and `first admin added admin@dawirli.com`,
  only the two expected warnings. Admin login as `admin@dawirli.com`,
  dashboard and settings fine. Test search `USB-C power banks`: 4
  source calls, 24 s — Amazon (SerpApi) 13 results 1.7 s, AliExpress
  (Apify) 3 in 24 s, Temu (Apify) 10 in 21 s, **SHEIN (Apify) no
  results in 5.7 s** (to look at in its log line). Estimated cost
  shows $0.00 everywhere: `SERPAPI_SEARCH_COST_USD=0` and no
  `APIFY_*_RUN_COST_USD` in the `.env`, so the budget figures stay 0
  until these cost settings are set in the panel. `/docs/` opens.
  SHEIN: the log has only the start line (`apify scraper enabled`,
  `clearpath/shein-product-scraper`, 5 items, timeout 1m30s), no line
  for the empty result — to look at later (Apify console → Runs shows
  whether the run returned nothing or failed); not blocking.
  **3.6 restart test: passed. ✅** After `sudo reboot` all four
  containers `Up … (healthy)` by themselves.
  **3.7 Oracle security list: ✅** ingress only TCP 22 from
  `0.0.0.0/0` plus Oracle's default ICMP 3/4 rules.
  `bootstrap-vm.sh` now also turns off `rpcbind` (part 6).
- **Step 3 — done 2026-11-07. ✅** Production runs on the VM, reachable
  only through SSH. **Next: step 4 — Cloudflare Tunnel and
  `globalprodsearch.dawirli.com`** (tunnel in Zero Trust → Networks →
  Tunnels, service `http://server:3002`, `TUNNEL_TOKEN` in the VM
  `.env`, `deploy.sh`; Cloudflare bot blocking off for `/api` and
  `/mcp`). Also open: cost settings (`SERPAPI_SEARCH_COST_USD`,
  `APIFY_*_RUN_COST_USD`) in the panel; SHEIN (below).
- **Step 4 — Cloudflare Tunnel and `globalprodsearch.dawirli.com`:
  started 2026-11-08**, one step at a time (the owner runs each step,
  Claude explains it). Nothing to build: `compose.prod.yml` and
  `deploy.sh` already have the tunnel (step 2). Plan:
  1. *Dashboard:* Zero Trust → Networks → Tunnels → Create a tunnel →
     Cloudflared, name `ofd-prod`; copy only the token (do not run the
     install command shown; the token is a secret).
  2. *Dashboard:* public hostname `globalprodsearch` . `dawirli.com`,
     path empty, service `HTTP` `server:3002` (Cloudflare adds the DNS
     record).
  3. *Before the tunnel is up:* WAF custom rule that blocks paths
     starting with `/admin` (panel and `/admin/v1/`) until Cloudflare
     Access (step 6); until then the panel only through SSH
     (`ssh -L 3012:127.0.0.1:3002`). Bot Fight Mode off (on the Free
     plan it cannot be skipped per path and would block API / MCP
     clients). SSL/TLS: Always Use HTTPS on, minimum TLS 1.2.
  4. *VM:* add `TUNNEL_TOKEN=` to the production `.env` (without
     showing it on screen), `deploy/deploy.sh` → `tunnel: connected`.
  5. *Checks from the PC:* `/api/v1/health` over HTTPS, `/docs/`,
     `/admin/` blocked (403), tunnel *Healthy* in the dashboard, the
     server log shows the PC's real IP (`"ip"`), a test search with an
     API key (made in the panel through SSH).
  6. Then: MCP test with real agents (PLAN step 14 note), step 5.
  **Progress (owner, 2026-11-08):** 4.1 tunnel `ofd-prod` made (ID
  `95782bdb-a236-4e1f-8949-04eb9f6bcfa8`; the dashboard's Continue
  button stays disabled until the tunnel connects, so the route was
  added from the tunnel's Routes tab → *Published application*). 4.2
  route `globalprodsearch.dawirli.com` → `http://server:3002` (one
  "Service URL" field), CNAME to `<id>.cfargotunnel.com` added by
  Cloudflare. 4.3 WAF custom rule `Block admin until Access`
  (`http.host eq "globalprodsearch.dawirli.com" and
  starts_with(http.request.uri.path, "/admin")` → Block) deployed; Bot
  Fight Mode was already off; Always Use HTTPS on, minimum TLS 1.2;
  Universal certificate active (the "No access … CSRs" alert on that
  page is about a paid add-on, harmless). 4.4 `TUNNEL_TOKEN` added with
  `read -rs` (line length 197); `deploy.sh` → image `666ab78` (PR #22
  merge), `tunnel: connected`, health ok.
  4.5 checks from the owner's PC: HTTPS health ok (TLS 1.3), HTTP →
  `301` to HTTPS, `/admin/` and `/admin/v1/` → `403`, Cloudflare sees
  `176.28.142.103` (edge `AMM`) and the server log has the same `"ip"`.
  API client `owner-test` (id 1, defaults) made in the panel through
  SSH; live search `USB-C power bank` / `jor` through the public address
  → `200` with Amazon (SerpApi) and AliExpress (Apify) results, 45.5 s,
  log `"user":"client:1"`, no key in the log. Same search again:
  `cached:true`, `took_ms` 0; markets amazon / aliexpress / temu `ok`,
  shein `no_results` (open SHEIN item).
  **Step 4 — done 2026-11-08. ✅** `https://globalprodsearch.dawirli.com`
  is public; the panel stays reachable only through SSH until step 6.
  **Open (seen in step 4): Cloudflare's 100 s limit.** On the Free plan
  Cloudflare waits at most 100 s for an answer (then `524`). On the VM
  the search timeout is `APIFY_TIMEOUT` (1m30s there) + 8 s + 2 s =
  100 s, so a very slow live search could be cut by Cloudflare first.
  To decide: `APIFY_TIMEOUT` ≤ 75s in the panel, and/or slow clients use
  the asynchronous `POST /api/v1/searches` (step 12), which answers at
  once.
- **SHEIN — to check before production (owner, 2026-11-07).** In the
  VM test search the SHEIN scraper (`clearpath/shein-product-scraper`,
  now build 0.0.84) ran 4 s and returned 0 results for `USB-C power
  banks`, charged $0.016; its log shows only "Initializing Actor" →
  "Exiting Actor", no search activity. The input we send is still the
  scraper's current format (Apify form shows the same fields:
  `searchTerms`, `site`, `maxItemsPerSearch`, `sortBy`, `enrichDetails`,
  `category`, `quickShip`, plus `productUrls: []`), so the input is not
  the cause. Open: run it by hand in Apify (`summer dress`, 5 items)
  to see whether the scraper is broken or SHEIN just has nothing for
  electronics; then look for a better SHEIN scraper. Seen in the store:
  `native_emblem/shein-product-scraper` ($5 / 1,000 search results,
  charged only per product returned, start fee ~$0; fields
  `productUrls`, `maxProductsPerTerm`, `country`, `sort`,
  `proxyConfiguration`; keyword field not confirmed yet). Until
  decided, SHEIN can be taken out of `APIFY_MARKETS` in the panel to
  avoid paying for empty runs.
- **Admin email steps as planned (owner's to-do, 2026-11-06).** Cloudflare checked: `dawirli.com` is active in the
  account and has **no MX records**, so Email Routing breaks nothing.
  Owner, in this order:
  1. Two-factor login on the Cloudflare account (profile → My Profile →
     Authentication → Two-Factor Authentication; authenticator app;
     keep the backup codes).
  2. Two-factor on the destination mailbox (Gmail: Google Account →
     Security → 2-Step Verification).
  3. Turn on Email Routing for `dawirli.com` (dashboard → `dawirli.com`
     → Email → Email Routing; Cloudflare adds the MX / TXT records).
  4. Add and verify the personal mailbox as the destination address.
  5. Rule `admin@dawirli.com` → that mailbox. No catch-all for now
     (spam); more addresses (e.g. `support@`) only when needed.
  6. Test: a mail from another account to `admin@dawirli.com` arrives
     (also look in spam).
  Then tell Claude the result; `admin@dawirli.com` becomes the
  production admin (new Firebase user, `ADMIN_EMAIL` in the production
  `.env`) and step 3 starts.
- **Next: step 3 — VM setup and first start. Explained to the owner
  2026-11-06; to do in a new session**, command by command (Q1: Claude
  explains each command, the owner runs it over SSH). Nothing is open to
  the internet yet: no tunnel, no domain (step 4).
  1. *Owner's PC:* stop the local stack or use another local port for
     the SSH tunnel (both use 3002). Have the local `.env` at hand.
  2. *VM:* `ssh -i ~/.ssh/owis_oracle.key ubuntu@<IP>`; download
     `bootstrap-vm.sh` from `main` (README, Production), read it, `sudo
     bash bootstrap-vm.sh`; log out and in (docker group). Check its
     last part: only port 22 listens on all addresses.
  3. *Production `.env`* (`/opt/ofd/repo/Owis_Find_Deal_Engine/server/.env`,
     mode 600): start from the local `.env` (copied with `scp`), then
     **new** `POSTGRES_PASSWORD`, `REDIS_PASSWORD`, `SEARXNG_SECRET`,
     `SETTINGS_ENCRYPTION_KEY` (`openssl rand -hex 32` each; keep the
     encryption key also offline, e.g. password manager); `ADMIN_EMAIL` =
     the real admin email; same Firebase values (`AUTH_*`,
     `ADMIN_FIREBASE_API_KEY`); `ADMIN_REQUIRE_2FA` and
     `ADMIN_REQUIRE_VERIFIED_EMAIL` stay `false` until step 5;
     `WEBHOOK_ALLOW_PRIVATE` not true; `CORS_ALLOWED_ORIGINS` empty for
     now; no `TUNNEL_TOKEN` yet. Search sources (SerpApi key, Apify
     actors / token, markets) from the local `.env` are copied into the
     new database once at the first start.
  4. `deploy/deploy.sh` (no argument: newest image of `main`) → all
     services healthy, version = the commit.
  5. *Check from the PC through SSH:* `ssh -i … -L 3012:127.0.0.1:3002
     ubuntu@<IP>` → `http://localhost:3012/admin/`: admin login,
     dashboard, settings (sources, keys present), one test search,
     `/docs/`; `docker logs owis_find_deal_engine-server-1` on the VM.
  6. *Restart test:* `sudo reboot`, log in again → the stack is back by
     itself (`restart: unless-stopped`).
  7. *Oracle console:* the subnet's security list allows only TCP 22 in.
  To decide at the start of step 3: **Q3** new empty database
  (recommended; API clients made again in the panel) or a copy of the
  local one; which real email is the production admin.
  **Admin email idea (owner, 2026-11-06): an address on the own domain,
  e.g. `admin@dawirli.com`, through Cloudflare Email Routing** (free on
  the Free plan; forwards incoming mail to the owner's existing mailbox,
  no inbox on Cloudflare; up to 200 rules per domain, catch-all and
  `user+tag@` supported; Cloudflare adds the MX / TXT records itself).
  Receiving is enough for us: the Firebase verification link and
  password-reset mails (step 5) and Cloudflare Access one-time codes
  (step 6) only need to arrive. Sending from `@dawirli.com` would need
  Email Sending (Workers Paid) — not needed. Setup (owner): Cloudflare
  dashboard → `dawirli.com` → Email → Email Routing → add and verify
  the destination (personal mailbox) → rule `admin@dawirli.com` → it.
  Check first: (1) whether `dawirli.com` already receives mail
  elsewhere (existing MX records, e.g. for the Dawirli app): Email
  Routing replaces them; (2) the admin account then depends on the
  Cloudflare account and the personal mailbox (whoever controls them
  can reset the admin's Firebase password), so two-factor login on both
  (Cloudflare to do, see above) and on the admin (step 5). Then a new
  Firebase user with that email becomes `ADMIN_EMAIL` in the production
  `.env`.

Agreed in the plan review:
- **Real client IP behind the tunnel:** behind `cloudflared` every
  request comes from the tunnel's Docker address, which breaks the
  admin per-IP limit, API key IP allow-lists and the audit log. Fix:
  trust `CF-Connecting-IP` only from the `cloudflared` address (new
  setting), else the connection address; one function replaces
  `remoteIP` (`internal/api/admin.go`).
- **Identity on every search** (owner's point): already required (app
  users: Firebase login token, e.g. Google / Apple sign-in; agents and
  partners: API key; else `401`). **E1, in step 14:** refuse Firebase
  anonymous accounts (allowed sign-in methods setting, default Google,
  Apple, email/password). **E2, in phase 5:** daily free-search limit
  per IP address on top of the per-account limit.
- **Two-factor admin login:** the server check exists; the panel needs
  a second-factor code screen and an authenticator-app (TOTP)
  enrollment screen, QR code with a small QR library + key as text
  (option a). Order: build screens → verify the admin email → enroll →
  `ADMIN_REQUIRE_2FA=true` + `ADMIN_REQUIRE_VERIFIED_EMAIL=true`.
  Lost phone: set `ADMIN_REQUIRE_2FA=false` on the VM over SSH, restart.
- Still to review: the stages (plumbing, VM setup, tunnel, `/admin`
  lock, backups, monitoring, production test, docs) and questions
  Q1–Q8 (deploy trigger, repo private, new or copied database,
  two-factor method, backup days, monitoring size, Access login,
  deploy branch).

- **D6 hosting: Oracle Cloud Always Free.** Home region **UAE East
  (Dubai)** (cannot change). VM `VM.Standard.A1.Flex` (Ampere Arm,
  aarch64), **2 OCPUs, 12 GB**, Canonical Ubuntu 24.04, default boot
  volume (~47 GB), on-demand capacity, own VCN `owis-vcn` (made with
  the VCN wizard "Create VCN with Internet Connectivity"; the instance
  wizard would not give a public IP with a new VCN) and its public
  subnet, public IPv4. Created 2026-11-01, running; the owner logs in
  with `ssh -i ~/.ssh/owis_oracle.key ubuntu@<public IP>` (IP in the
  Oracle console; key only on the owner's PC, never in the repo);
  packages updated. Half of the free allowance (4 OCPUs / 24 GB,
  200 GB disk) is left.
  - Budget alert: `free-tier-guard`, €1 / month, email at 1 % of actual
    spend (any real charge).
  - **To do (owner): upgrade to Pay-As-You-Go before the 30-day trial
    ends** — still $0 within Always Free, and idle Always Free VMs are
    then no longer reclaimed (ours is idle most of the time).
  - The image must be built for **linux/arm64** (GitHub Actions,
    multi-arch); SearXNG, Redis and Postgres have arm64 images.
  - Why: free; 12 GB is far more than the stack needs (~1.5 GB).
    Hetzner (€6–8/month, PayPal) stays the fallback; the compose stack
    moves in about an hour. Oracle sign-up needs a physical bank card
    (virtual / prepaid refused).
- **D7 domain: `dawirli.com`** (changed by the owner 2026-11-06;
  replaces `dealcoon.com`, bought 2026-11-01 with the brand DealCoon,
  now dropped). Bought at **Cloudflare Registrar** (DNS already on
  Cloudflare, as the tunnel needs). `dawirli.com` / `www.dawirli.com`
  are used by the owner's **Dawirli app**, which will later call this
  service. This service is **"Global Product Search"** (the external
  product search behind Dawirli) at its own subdomain
  **`globalprodsearch.dawirli.com`**: API, `/docs`, `/admin` and the
  agents (`https://globalprodsearch.dawirli.com/mcp`); name final
  (owner, 2026-11-06). DNS not changed yet. When the Dawirli app calls the API from a browser:
  `CORS_ALLOWED_ORIGINS=https://dawirli.com,https://www.dawirli.com`.
  `dealcoon.com`: left to expire (owner, 2026-11-06); owner to turn
  off its auto-renew in Cloudflare. Owner to do: verify the registrant email, two-factor login on the
  Cloudflare account.
- **HTTPS: permanent Cloudflare Tunnel** (`cloudflared` next to the
  stack) for `globalprodsearch.dawirli.com`: no web port open on the VM, only SSH.
  Turn off Cloudflare bot blocking for `/api` and `/mcp` (agents and
  apps must get through); Cloudflare's 100 s limit is above the 60 s
  live search.
- **`/admin` extra lock: Cloudflare Access** (free, ≤ 50 users): only
  the owner's email (one-time code or Google) reaches the admin login
  page; then Firebase login + two-factor as before. API, `/mcp` and
  `/docs` stay public.
- **Backups: Oracle Object Storage** (Always Free, 20 GB): nightly
  `pg_dump`, kept for a limited number of days.
- **Staging: none for now** (only the owner tests). Added before real
  users, with fuller monitoring.
- Until the tunnel exists: admin panel and API through an SSH tunnel
  (`ssh -L 3002:127.0.0.1:3002 …` → `http://localhost:3002`); a
  temporary Cloudflare quick tunnel (`*.trycloudflare.com`) can serve
  a one-off agent test.

**Phase 3, step 13 — AI agents (MCP) and developer docs: done
(2026-10-26). Phase 3 is complete.** Details: server README section "AI
agents (MCP) and developer docs", PLAN.md step 13. Merged via PR #12.

- **Owner test on the Docker stack (2026-10-26, Windows, Git Bash),
  guide steps 0–7: all passed. ✅** Docs page and OpenAPI file (`3.1.0`,
  `/docs` → `301`); client `step13 agent` (20 per day, `jor`, both
  scopes); `initialize` → `2025-06-18` with server info and
  instructions; `tools/list` → `list_countries`, `search_products`;
  `list_countries` → Jordan only; `search_products` `usb c hub` / `jor`
  → cached, free (remaining 20; AliExpress, Temu, SHEIN 5 each, Amazon
  none — the open SerpApi point below); `usa` → `country_not_allowed`,
  title `x` → `invalid_request` (both `isError`), unknown tool → `-32602`;
  2026-07-28 `server/discover` → four versions, wrong `Mcp-Method`
  header → `400 -32020`; no key / login token → `401`, `GET` → `405`;
  client `step13 lister` (scope `countries` only) → `search_products`
  `forbidden`, `list_countries` works.
- **Steps 8–10 moved to the production test (owner, 2026-10-26)**, see
  PLAN.md step 14: real agent, logs and dashboard, daily limit through
  MCP. On the owner's PC Claude Code 2.1.310 (Windows) could not connect
  to the local stack: `InvalidHTTPResponse fetching
  "http://127.0.0.1:3002"` (debug log: "version negotiation probe failed
  … reconnecting pinned legacy", then the same error). Found so far: not
  a proxy setting (none set), not IPv6 (`127.0.0.1` fails too), not the
  server (both requests logged `200`; the raw reply is valid HTTP; the
  same Claude Code version connects on Linux, also under Bun and through
  a relay), not Docker Desktop's port alone (it also fails through a
  Python relay on port 3003). Left: something on the PC catching Claude
  Code's connections (antivirus web protection, VPN client) or a Claude
  Code bug on Windows. Next check, if it also fails against production:
  the logging relay ([`docs/relay_log.py`](docs/relay_log.py), prints every request and reply,
  key hidden) shows whether Claude Code's request arrives and what it
  gets back.

- **MCP endpoint `POST /mcp`** (Model Context Protocol, Streamable HTTP)
  for AI agents, with the **same API keys** (login tokens refused: `401`).
  Tools: `list_countries` (scope `countries`; the countries the key may
  use, currency, marketplaces; free) and `search_products` (scope
  `search`; `title`, `country`, optional `refresh`; same result as
  `/api/v1/search` + `quota`; cached free, live counted once — the same
  daily counter and burst limit as the REST API). Every MCP message is
  also limited to 5/s (burst 20) per client.
- Problems come back to the agent as readable tool errors (`isError`,
  `{"error":{"code","message"}}`): `quota_exceeded` with
  `retry_after_seconds`, `country_not_allowed`, `forbidden` (scope
  missing), `invalid_request`, `upstream_error`, …
- **Both MCP generations**: 2026-07-28 (the newest spec, stateless:
  `server/discover`, version in each request's `_meta` and in the
  `MCP-Protocol-Version` / `Mcp-Method` / `Mcp-Name` headers, which must
  match → `400` `-32020`) and 2025-03-26 / 2025-06-18 / 2025-11-25
  (`initialize` handshake). One JSON answer per request: no sessions, no
  SSE, no batches; `GET /mcp` → `405`. A browser `Origin` must be the
  server or in `CORS_ORIGINS` (DNS rebinding protection).
- **OpenAPI 3.1** at `GET /api/v1/openapi.json` (every public endpoint,
  the webhook as `webhooks` + `callbacks`, `/mcp`) and a **developer docs
  page** at `GET /docs/` (quick start, keys, search, async, webhooks with
  signature checks in Python and Node.js, MCP client setup for Claude
  Code / JSON-configured clients / `mcp-remote`, limits, errors). Both
  public, embedded in the binary (`internal/devdocs`), strict CSP, no
  scripts. The OpenAPI file is hand-written: update it with every API
  change (a test checks routes, error codes and `$ref`s).
- No new settings, no database change, no new Go or npm dependencies.
- **Fixed on the way (step 12):** a data race found by the race detector
  in `TestAsyncSearchWebhook`: after a webhook delivery the job runner
  wrote the delivery result into the webhook record that the `202`
  answer could still be writing out (cached search + webhook). It now
  updates its own copy (`internal/jobs/jobs.go`).
- Tested: unit tests (both protocol eras, header checks, version
  negotiation, tool errors, scopes, countries filter, rate limit and
  shared daily counter, transport errors, Origin, docs and spec), the
  race detector, `@redocly/cli lint` (valid; only style warnings), and an
  end-to-end run of the binary with Postgres 16, Redis and a fake search
  source, driven by the **official MCP SDKs**: TypeScript 1.32.2 and
  1.12.0 (2025-11-25 / 2025-03-26 handshake) and Go 1.8.0 (2026-07-28 via
  `server/discover`, and forced 2025-06-18) — tools listed, both tools
  called, cached second call free, errors as tool results, key never in
  the log. Every command in the step 13 guide was run against it. Docs
  page checked in Chromium (light, dark, phone width). Not run here:
  `govulncheck` (CI runs it).
- **Open question for the owner:** a live search through
  `search_products` waits 20–60 s. If an agent cannot wait that long, a
  third tool pair (`start_search` + `get_search`, on top of the
  asynchronous search of step 12) can be added.

**Owner, to try step 13** (done 2026-10-26, steps 0–7, see above; full steps in
[docs/step13_guide.pdf](docs/step13_guide.pdf); free with the cached
`usb c hub` / `jor` search):

1. `docker compose up -d --build` (nothing to add to `.env`).
2. Open <http://localhost:3002/docs/>; `curl localhost:3002/api/v1/openapi.json`.
3. Admin panel → **API clients** → new client with scopes `search` and
   `countries`, country `jor` → copy the key → `export KEY=ofd_live_…`.
4. MCP by hand (Git Bash):
   `curl -s localhost:3002/mcp -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -H "MCP-Protocol-Version: 2025-06-18" -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_products","arguments":{"title":"usb c hub","country":"jor"}}}'`
   → `"structuredContent":{…"results":[…]…,"quota":{…}}`.
5. A real agent: `claude mcp add --transport http owis-find-deal http://localhost:3002/mcp --header "Authorization: Bearer $KEY"`,
   then ask Claude Code to find a usb c hub in Jordan.
6. Report: which agents you will connect; is waiting up to 60 s per new
   search fine for them; is the docs page clear; should `/docs/` and the
   OpenAPI file stay public in production.

**Phase 3, step 12 — integration endpoints: done (2026-10-23).** Details:
server README section "Asynchronous search and webhooks", PLAN.md step 12.

- **Asynchronous search** for slow live searches:
  `POST /api/v1/searches` (same body as `/search`) → `202` + job id
  (`srch_…`) and `Location`; `GET /api/v1/searches/{id}` → `running`,
  `done` (with the result) or `failed` (with the error `/search` would
  have given). Works with an API key **or** the user's login token.
  Bad input, used-up allowance (`402` / `429`), wrong country or scope are
  refused at once; a cached answer is already `done` in the `202`.
  Metering as before: cached free, a live search counts once.
- Only the caller sees its job (others `404`); finished jobs kept 1 hour
  (Redis); at most 10 running per caller (`429 too_many_jobs`); polling
  has its own limit (5/s) and does not use up the search limit.
- **Signed webhook** (API keys only): add `"webhook_url":"https://…"`; when
  done the job is POSTed there with `X-OFD-Signature: t=…,v1=<HMAC-SHA256>`,
  `X-OFD-Event: search.completed|search.failed`, `X-OFD-Delivery: <job id>`;
  3 attempts (now, 5 s, 30 s). The job shows `webhook.status`
  (`delivered` / `failed`). Only https to public addresses (the server
  cannot be made to call Redis, Postgres, etc.); for a receiver on your
  PC: `WEBHOOK_ALLOW_PRIVATE=true` in `.env` (development only).
- **Webhook secret per client**: admin panel → API clients → *Webhook
  secret* (shown once, `whsec_…`; a new one replaces the old at once;
  audit `clients.webhook_secret`, never the secret). It is not stored:
  derived from `SETTINGS_ENCRYPTION_KEY` + a random salt (new columns on
  `api_clients`, migration 0005, applied automatically).
- **Per-client usage on the dashboard**: new table *API clients* —
  searches, cached, live, refused, failed, estimated cost (live searches ×
  average cost of a live search), last day. Also in
  `GET /admin/v1/usage` (`clients`). Base for billing partners later.
- Tested: unit tests (job store in memory + Redis, runner limits /
  shutdown / panic, signing and checking, retries, no redirects,
  internal addresses refused also through a host name, metering pre-check,
  per-client counters and report, admin endpoint, the whole async API with
  and without webhook), store tests against Postgres 16, the race
  detector, and an end-to-end run of the binary with Postgres, a fake
  Firebase and a slow fake search (user job → poll → done; cached job done
  at once; another user `404`; client without secret `400`; secret made →
  webhook delivered and its signature checked; wrong country `403`;
  dashboard row with the client's name after 30 s; secret and key never in
  the log or audit log). Admin web app: typecheck + build, then checked in
  Chromium on 2026-10-24 (see the fix below). No new Go or npm
  dependencies. Not run here: `govulncheck` (CI runs it).
- **Owner test on the Docker stack (2026-10-24, Windows, Git Bash),
  guide steps 0–13, 15, 16: all passed. ✅** New client `step12 agent`
  (#4, 20 per day, `jor`). Live async search `usb c hub` / `jor` → `202`
  at once, `running` → `done` after 30 s (AliExpress, Temu, SHEIN 5 each),
  quota 20 → 19 once; same search again → `done` in the `202`, cached,
  free; user token → `202`, `X-Plan: free`; bad title / other country /
  unknown field / no token refused at once; another caller and a made-up
  id → `404`; job still there after `docker compose restart server`;
  webhook refused without a secret (`webhook_not_configured`), secret
  made → webhook delivered to webhook.site on the first attempt
  (`http_status 200`), signature checked with `verify.py` (`signature
  OK`; one changed character → `BAD signature`); http / internal webhook
  URLs and a webhook with a user token refused; a new secret → the old
  one gives `BAD signature`, the new one `OK`; dashboard row *step12
  agent* (1 live, 3 cached); audit entry shows only the secret's prefix;
  `whsec_` and `ofd_live_` never in the server log.
- **Found and fixed during the test:** the box with a new key (or webhook
  secret) could appear out of view when the page was scrolled down, so it
  looked as if no key was made → it now scrolls into view and selects the
  key (PR #10). Guide corrections from the test (Git Bash tips, Firebase
  test user, country check order, rebuilding the signed body with `GET` +
  `sed` because the terminal cuts long pasted lines, …): PR #11.
- **Not tested yet (optional):** guide step 14 (receiver on your PC:
  failed delivery, retries; needs `WEBHOOK_ALLOW_PRIVATE=true`) and step
  17 (daily limit refused before a job starts; one live search).
- **Open points seen during the test (not step 12):**
  - Amazon answered `no_results` for `usb c hub` / `jor` (the admin test
    search too, 49 ms: SerpApi's own saved answer). Next check, in the
    `server` folder (uses 1 SerpApi search):
    `SK=$(grep '^SERPAPI_KEY=' .env | cut -d= -f2- | tr -d '\r"'); curl -s "https://serpapi.com/search.json?engine=amazon&k=usb+c+hub&amazon_domain=amazon.com&no_cache=true&api_key=$SK" | head -c 600`
    — an `"error"` (allowance used up, bad key), no `organic_results`, or
    products our relevance filter dropped. The SerpApi dashboard shows the
    searches left this month.
  - `cost_usd_estimate` is 0: set `APIFY_ALIEXPRESS_RUN_COST_USD` 0.02,
    `APIFY_TEMU_RUN_COST_USD` 0.10, `APIFY_SHEIN_RUN_COST_USD` 0.033 in
    Settings so the dashboard estimates costs.
  - Extra clients from the test (#3 "step12 agent" and others): revoke
    what you do not need.

**Owner, to try step 12** (done 2026-10-24, see above; the full steps are
in [docs/step12_guide.pdf](docs/step12_guide.pdf). Short version, free with
a cached search, e.g. `usb c hub` / `jor`, or one live search):

1. `docker compose up -d --build` (nothing to add to `.env`).
2. Async with your key from step 11 (Git Bash; PowerShell: `curl.exe`):
   `curl -i -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer ofd_live_…" -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"jor"}'`
   → `202`, `Location: /api/v1/searches/srch_…`. Then
   `curl localhost:3002/api/v1/searches/srch_… -H "Authorization: Bearer ofd_live_…"`
   → `"status":"done"` with the results (a new search: `running` for
   ~30–50 s first).
3. Webhook: admin panel → **API clients** → *Webhook secret* → copy it.
   Open <https://webhook.site> (free, gives you an https URL), then the
   same POST with `"webhook_url":"https://webhook.site/<your id>"` in the
   body. webhook.site shows the call with `X-Ofd-Signature`; the job shows
   `"webhook":{"status":"delivered"}`.
4. Dashboard → new table **API clients** (after ~30 s): your client with
   its searches.
5. Report: is polling every 5 s fine for your agents? Is one webhook
   secret per client enough, or do you want a fixed webhook URL per client
   (set in the admin panel instead of per request)?

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

**Next: phase 4, step 14 — deployment** (step 13 tested by the owner,
steps 0–7; steps 8–10 are part of the production test): turn on two-factor admin login and admin email verification,
hosting choice (D6), domain (D7), HTTPS, hardening, secrets, Postgres
backups, staging + production, CI/CD, monitoring (PLAN.md step 14).

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

**Phase 2 (steps 7–10) is done (2026-10-11); phase 3 is done: step 11 (API clients and keys) 2026-10-15, step 12 (integration endpoints) 2026-10-23, step 13 (AI agents: MCP + OpenAPI + docs) 2026-10-26. Step 13 tested by the owner (steps 0–7; real-agent test moved to production). Next: phase 4, step 14 (deployment; D6 hosting and D7 domain decided 2026-11-01, see "Where we are").**
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

D5 answered: $5 per month (free plans). D6 and D7 answered 2026-11-01:
**D6** Oracle Cloud Always Free (Dubai, Arm A1, 2 OCPUs / 12 GB),
**D7** `dawirli.com` (service at `globalprodsearch.dawirli.com`; changed from
`dealcoon.com` 2026-11-06) — details in "Where
we are". Later: D8 paid Apify / SerpApi plans (before real users).

## To resume in a new Claude Code session

Open the repository and say:

> Continue the Owis_Find_Deal_Engine project from
> `Owis_Find_Deal_Engine/STATUS.md` on branch `claude/cloud-vs-local-7xi988`.
> Continue phase 4, step 14: lay out the detailed build plan for approval.
> (Decisions D1–D7 are recorded in STATUS.md.)

(Step 13 is merged (PR #12) and tested up to step 7; its steps 8–10 are
part of the production test, PLAN.md step 14.)

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
- **Scraping API instead of Apify scrapers (e.g. scrape.do; noted
  2026-11-09, not checked now — owner).** It fetches any page through
  rotating proxies with anti-bot bypass and optional browser rendering,
  and returns raw HTML; we would write and maintain our own parser per
  shop (Temu, SHEIN). Pricing seen: Hobby $29 / month (250k credits,
  $0.11 / 1k), Pro $99, Business $249, Advanced $699. A hard page
  (residential proxy + rendering) costs several credits (≈ 10–25, to
  confirm), so ≈ $0.003 per search page vs $0.10 for Temu on Apify; but
  "successful" means the page loaded, an empty page is still charged.
  Worth it only above ≈ 200 live searches / month ($29 ÷ ≈ $0.155 Apify
  per search), or when a shop's Apify scrapers keep breaking with no
  replacement (SHEIN first). Risks: our parser breaks when the shop
  changes its page; Temu may load results through protected background
  calls, so the page may not contain them. First test when the time
  comes: free trial, one Temu and one SHEIN search page — are the
  products in the HTML?

## Open decisions (owner)

- Hosting and domain: decided 2026-11-01 (D6 Oracle, D7 `dawirli.com`,
  changed from `dealcoon.com` 2026-11-06).
- Oracle Pay-As-You-Go upgrade — before the 30-day trial ends.
- D8 paid Apify / SerpApi plans — before real users.
- Payment provider for paid plans — phase 2.
