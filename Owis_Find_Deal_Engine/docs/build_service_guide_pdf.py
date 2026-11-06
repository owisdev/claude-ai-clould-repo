"""Builds docs/service_guide.pdf: the complete, current description of
owis_find_deal_engine (October 2026, after phase 3 step 11) - what it is,
architecture, Docker structure, request flow, cache, API clients, cost
measurement, code map, how to run / use / test it locally, and what is
left to do.

    pip install reportlab
    python3 docs/build_service_guide_pdf.py

Styles and drawing helpers come from build_architecture_pdf.py.
"""

import os
import textwrap

from build_architecture_pdf import (
    BLUE, BLUE_D, CONTENT_W, GREEN, GREEN_D, GREY, GREY_D, INK, MARGIN, MUTED, NAVY,
    ORANGE, ORANGE_D, PAGE_H, PAGE_W, PURPLE, PURPLE_D, RED, RED_D, TEAL, A4, P, S,
    arrow, box, bullets, callout, caption, numbered, table, text,
)
from build_architecture_pdf import code as _code
from build_flow_and_plan_pdfs import diagram_cache_48, diagram_sources
from reportlab.graphics.shapes import Drawing, Line, Polygon, Rect, String
from reportlab.lib import colors
from reportlab.lib.units import mm
from reportlab.platypus import (
    CondPageBreak, KeepTogether, PageBreak, Paragraph, Preformatted, SimpleDocTemplate, Spacer,
)
from reportlab.platypus.tableofcontents import TableOfContents

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "service_guide.pdf")
DATE = "16 October 2026"
TITLE = "owis_find_deal_engine — Service Guide"
BRANCH = "claude/cloud-vs-local-7xi988"
REPO = "https://github.com/owisdev/claude-ai-clould-repo.git"
W = CONTENT_W


# ================================================================ helpers
def diamond(d, cx, cy, w, h, label, fill=colors.white, stroke=NAVY, size=7.2):
    d.add(Polygon([cx, cy + h / 2, cx + w / 2, cy, cx, cy - h / 2, cx - w / 2, cy],
                  fillColor=fill, strokeColor=stroke, strokeWidth=1))
    lines = label.split("\n")
    lh = size + 2.2
    top = cy + lh * len(lines) / 2 - size + 0.5
    for i, line in enumerate(lines):
        d.add(String(cx, top - i * lh, line, fontName="Sans-Bold" if i == 0 else "Sans",
                     fontSize=size, fillColor=INK, textAnchor="middle"))


def poly(d, pts, color=GREY_D, width=1.0, dash=None):
    """Polyline without arrow head; pts = [x1, y1, x2, y2, ...]."""
    for i in range(0, len(pts) - 2, 2):
        d.add(Line(pts[i], pts[i + 1], pts[i + 2], pts[i + 3], strokeColor=color,
                   strokeWidth=width, strokeDashArray=dash))


class H1(Paragraph):
    """Heading that is added to the table of contents."""

    def __init__(self, txt, level=0):
        super().__init__(txt, S["h1"] if level == 0 else S["h2"])
        self.toc_level = level
        self.toc_text = txt


def h1(t):
    return [CondPageBreak(220), H1(t, 0)]


def h2(t):
    return [CondPageBreak(150), H1(t, 1)]


class Doc(SimpleDocTemplate):
    def afterFlowable(self, f):
        if isinstance(f, H1):
            key = f"h{id(f)}"
            self.canv.bookmarkPage(key)
            if f.toc_level == 0:
                self.notify("TOCEntry", (f.toc_level, f.toc_text, self.page, key))
            self.canv.addOutlineEntry(f.toc_text, key, level=f.toc_level, closed=f.toc_level > 0)


def on_page(canvas, doc):
    canvas.saveState()
    if doc.page > 1:
        canvas.setFont("Sans", 7.5)
        canvas.setFillColor(MUTED)
        canvas.drawString(MARGIN, PAGE_H - 11 * mm, TITLE)
        canvas.drawRightString(PAGE_W - MARGIN, PAGE_H - 11 * mm, DATE)
        canvas.setStrokeColor(colors.HexColor("#D5DAE1"))
        canvas.setLineWidth(0.5)
        canvas.line(MARGIN, PAGE_H - 12.5 * mm, PAGE_W - MARGIN, PAGE_H - 12.5 * mm)
    canvas.setFont("Sans", 7.5)
    canvas.setFillColor(MUTED)
    canvas.drawCentredString(PAGE_W / 2, 10 * mm, f"{doc.page}")
    canvas.restoreState()


def T(*a, **k):
    return KeepTogether([table(*a, **k)])


def code(txt):
    longest = max(len(line) for line in txt.splitlines())
    if longest > 96:  # shrink the font so the longest line still fits
        style = S["code"].clone("code_small", fontSize=round(455 / (0.6 * longest), 1))
        style.leading = style.fontSize * 1.3
        return KeepTogether([Preformatted(txt, style)])
    return KeepTogether([_code(txt)])


def fig(drawing, cap):
    return KeepTogether([drawing, caption(cap)])


def hfig(title, drawing, cap):
    """Sub-heading kept on the same page as its figure."""
    return [KeepTogether([H1(title, 1), drawing, caption(cap)])]


# ================================================================ diagrams
def diagram_architecture():
    H = 390
    d = Drawing(W, H)
    # Docker host and its two networks
    d.add(Rect(108, 18, 266, 364, rx=8, ry=8, fillColor=None, strokeColor=GREY_D,
               strokeWidth=1, strokeDashArray=[4, 3]))
    text(d, 116, 370, "Docker host — docker-compose.yml (project owis_find_deal_engine)", 7, MUTED, bold=True)
    d.add(Rect(116, 168, 250, 194, rx=6, ry=6, fillColor=BLUE, strokeColor=BLUE_D, strokeWidth=0.6))
    text(d, 362, 352, "network \"edge\" — internet", 6.8, BLUE_D, "end", bold=True)
    d.add(Rect(116, 26, 250, 134, rx=6, ry=6, fillColor=GREEN, strokeColor=GREEN_D, strokeWidth=0.6))
    text(d, 362, 150, "\"data\" — internal, no internet", 6.8, GREEN_D, "end", bold=True)
    # server spans both networks
    box(d, 126, 36, 112, 304,
        "server (Go)\nowis_find_deal_engine\n\n/api/v1  public API\n/admin/v1  admin API\n/admin/  admin web app\n\n"
        "login / API key check\nrate limit\nmetering (quota)\ncache (48 h)\nsearch + fallback\nusage + cost counters\n"
        "settings hot reload\n\nnon-root · read-only\n~19 MB distroless image",
        fill=colors.white, stroke=NAVY, size=7)
    box(d, 256, 262, 100, 62, "SearXNG\nfree metasearch\n(self-hosted)\nlast fallback", fill=colors.white,
        stroke=BLUE_D, size=7)
    box(d, 256, 92, 100, 50, "Redis 7\nsearch cache\nusage counters", fill=colors.white, stroke=GREEN_D, size=7)
    box(d, 256, 34, 100, 50, "Postgres 17\nsettings · admins · audit\nusage · API clients", fill=colors.white,
        stroke=GREEN_D, size=6.8)
    arrow(d, 238, 293, 256, 293)
    arrow(d, 238, 117, 256, 117)
    arrow(d, 238, 59, 256, 59)
    # callers on the left
    box(d, 0, 300, 92, 52, "Mobile / web app\nusers · Firebase\nlogin token (JWT)", fill=PURPLE, stroke=PURPLE_D, size=7)
    box(d, 0, 230, 92, 52, "Own servers &\nAI agents\nAPI key ofd_live_…", fill=PURPLE, stroke=PURPLE_D, size=7)
    box(d, 0, 160, 92, 52, "Admin (browser)\nlocalhost:3002/admin\nFirebase login", fill=PURPLE, stroke=PURPLE_D, size=7)
    box(d, 0, 50, 92, 62, "Firebase Auth\n(Google project)\nemail/password,\nGoogle, Apple…", fill=ORANGE,
        stroke=ORANGE_D, size=7)
    for y in (326, 256, 186):
        arrow(d, 92, y, 126, y)
    text(d, 98, 334, "HTTPS", 6.4, MUTED)
    arrow(d, 30, 112, 30, 160, "sign in", label_dx=4, label_anchor="start", dash=[3, 2])
    text(d, 108, 6, "only 127.0.0.1:3002 is published to the host", 6.6, NAVY)
    # external services on the right
    box(d, 392, 300, 101, 52, "Search engines\nGoogle, Bing,\nDuckDuckGo, Brave…", fill=GREY, stroke=GREY_D, size=7)
    box(d, 392, 232, 101, 56, "SerpApi (paid)\nAmazon engine\neBay engine\nGoogle Shopping", fill=GREY,
        stroke=GREY_D, size=7)
    box(d, 392, 168, 101, 54, "Apify (paid)\nscrapers: AliExpress\nTemu · SHEIN", fill=GREY, stroke=GREY_D, size=7)
    box(d, 392, 104, 101, 50, "Firebase JWKS\npublic keys\n(refreshed hourly)", fill=ORANGE, stroke=ORANGE_D, size=7)
    arrow(d, 356, 300, 392, 320)
    arrow(d, 238, 250, 392, 250, "HTTPS", label_dx=40)
    arrow(d, 238, 196, 392, 196, "HTTPS", label_dx=40)
    poly(d, [238, 178, 378, 178, 378, 129], dash=[3, 2])
    arrow(d, 378, 129, 392, 129, dash=[3, 2])
    text(d, 300, 181, "public keys", 6.4)
    return d


def diagram_docker():
    H = 300
    d = Drawing(W, H)
    d.add(Rect(0, 0, W, H, rx=8, ry=8, fillColor=None, strokeColor=GREY_D, strokeWidth=1, strokeDashArray=[4, 3]))
    text(d, 8, H - 13, "docker compose project \"owis_find_deal_engine\"", 7.5, MUTED, bold=True)

    def container(x, y, w, h, title, lines, edge):
        box(d, x, y, w, h, title + "\n" + "\n".join(lines), fill=colors.white, stroke=edge, size=6.9)

    # edge network
    d.add(Rect(10, 150, 300, 126, rx=6, ry=6, fillColor=BLUE, strokeColor=BLUE_D, strokeWidth=0.6))
    text(d, 16, 265, "network edge (internet)", 6.8, BLUE_D, bold=True)
    # data network
    d.add(Rect(10, 12, 474, 128, rx=6, ry=6, fillColor=GREEN, strokeColor=GREEN_D, strokeWidth=0.6))
    text(d, 478, 129, "network data (internal: no internet, no published ports)", 6.8, GREEN_D, "end", bold=True)
    container(20, 22, 150, 236, "server", [
        "image owis_find_deal_engine:dev", "build: ./Dockerfile", "port 127.0.0.1:3002 → 3002",
        "env_file .env + DATABASE_URL,", "REDIS_URL, SEARXNG_URL", "mem 256 MB · pids 256",
        "healthcheck: binary healthcheck", "depends_on: redis, searxng,", "postgres (healthy)",
        "", "networks: edge + data"], NAVY)
    container(185, 160, 115, 96, "searxng", [
        "searxng/searxng:latest", "user 977 · mem 512 MB", "settings.yml read-only",
        "tmpfs cache", "health: /healthz"], BLUE_D)
    container(185, 22, 140, 98, "redis", [
        "redis:7-alpine · user 999", "password · AOF on", "maxmemory 256 MB", "volatile-ttl eviction",
        "volume redis-data → /data"], GREEN_D)
    container(335, 22, 140, 98, "postgres", [
        "postgres:17-alpine · user 70", "db ofd · user ofd", "mem 256 MB · shm 128 MB",
        "health: pg_isready", "volume postgres-data"], GREEN_D)
    # hardening note
    box(d, 320, 160, 164, 96, "Every container\nread_only: true\ncap_drop: ALL\nno-new-privileges\n"
        "restart: unless-stopped\nlogs: json, 10 MB × 3", fill=GREY, stroke=GREY_D, size=6.9)
    arrow(d, 170, 208, 185, 208)
    arrow(d, 170, 71, 185, 71)
    poly(d, [170, 40, 178, 40, 178, 16, 400, 16, 400, 22], color=GREEN_D)
    return d


def diagram_dockerfile():
    H = 112
    d = Drawing(W, H)
    box(d, 0, 26, 140, 76, "Stage 1 · web\nnode:22-alpine\nnpm ci (lockfile)\ntsc --noEmit\nvite build → /dist",
        fill=PURPLE, stroke=PURPLE_D, size=7)
    box(d, 172, 26, 150, 76, "Stage 2 · build\ngolang:1.26-alpine\ngo mod download\ncopy /dist → internal/adminui/dist\n"
        "CGO_ENABLED=0 go build -trimpath", fill=BLUE, stroke=BLUE_D, size=7)
    box(d, 354, 26, 139, 76, "Stage 3 · run\ndistroless/static:nonroot\nonly the binary + CA certs\nno shell · user nonroot\n"
        "EXPOSE 3002 · HEALTHCHECK", fill=GREEN, stroke=GREEN_D, size=7)
    arrow(d, 140, 64, 172, 64, "admin app")
    arrow(d, 322, 64, 354, 64, "binary")
    text(d, 0, 8, "The admin web app is built and embedded in the Go binary, so one image = API + admin panel.", 7, MUTED)
    return d


def diagram_request_flow():
    H = 640
    d = Drawing(W, H)
    cx = 170
    red = dict(fill=RED, stroke=RED_D, size=6.9)
    # 1 request
    box(d, cx - 115, 600, 230, 32, "① Client → POST /api/v1/search\n{\"title\": \"usb c hub\", \"country\": \"jor\"}",
        fill=PURPLE, stroke=PURPLE_D, size=7.2)
    text(d, cx + 125, 612, "middleware: request id, JSON log,", 6.6)
    text(d, cx + 125, 603, "panic recovery, CORS, write deadline", 6.6)
    arrow(d, cx, 600, cx, 579)
    # 2 which credential
    diamond(d, cx, 556, 210, 44, "② Bearer token starts\nwith \"ofd_\" ?")
    box(d, 0, 452, 156, 66, "User login token (JWT)\nsignature checked locally\nwith Firebase JWKS\niss · aud · exp · iat\n→ user id + plan",
        fill=ORANGE, stroke=ORANGE_D, size=6.9)
    box(d, 184, 452, 156, 66, "API key (own servers / agents)\nSHA-256 lookup, 10 s cache\nnot revoked / expired\n"
        "allowed IP · scope \"search\"\nallowed country", fill=ORANGE, stroke=ORANGE_D, size=6.9)
    poly(d, [cx - 105, 556, 78, 556])
    arrow(d, 78, 556, 78, 518, "no", label_dx=-8, label_dy=8)
    poly(d, [cx + 105, 556, 262, 556])
    arrow(d, 262, 556, 262, 518, "yes", label_dx=10, label_dy=8)
    box(d, 360, 452, 133, 66, "401 unauthorized\n(missing / bad token,\nkey revoked / expired)\n403 forbidden,\ncountry_not_allowed",
        **red)
    arrow(d, 340, 485, 360, 485, "fails")
    # 3 rate limit
    box(d, cx - 115, 392, 230, 36, "③ Burst rate limit (token bucket)\nuser 1/s burst 5 · API client: its own",
        fill=GREY, stroke=GREY_D, size=7)
    arrow(d, 78, 452, cx - 50, 428)
    arrow(d, 262, 452, cx + 50, 428)
    box(d, 360, 394, 133, 32, "429 rate_limited\n+ Retry-After", **red)
    arrow(d, cx + 115, 410, 360, 410, "too fast")
    arrow(d, cx, 392, cx, 372)
    # 4 cache
    diamond(d, cx, 350, 210, 44, "④ Answer in cache?\nfresh < 48 h or stale < 72 h")
    box(d, 360, 322, 133, 56, "Return cached answer\nFREE (not counted)\nX-Cache: HIT / STALE\nstale → refreshed in\nthe background",
        fill=GREEN, stroke=GREEN_D, size=6.9)
    arrow(d, cx + 105, 350, 360, 350, "yes")
    arrow(d, cx, 328, cx, 308, "no", label_dx=10, label_dy=-2)
    # 5 quota
    diamond(d, cx, 286, 210, 44, "⑤ Daily allowance left?\nRedis counter per user / client")
    box(d, 360, 256, 133, 60, "402 payment_required\n(free plan used up)\n429 quota_exceeded\n(paid plan / API client)\n"
        "503 if Redis is down", **red)
    arrow(d, cx + 105, 286, 360, 286, "no")
    arrow(d, cx, 264, cx, 244, "yes: take 1", label_dx=26, label_dy=-2)
    # 6 live search
    box(d, cx - 115, 194, 230, 50, "⑥ Live search — only the shops of the country,\nin parallel (worker pool); each shop asks\n"
        "its own source, then its fallback chain;\nidentical searches share one call", fill=BLUE, stroke=BLUE_D, size=6.9)
    arrow(d, cx, 194, cx, 174)
    diamond(d, cx, 152, 210, 44, "⑦ Did every shop fail?")
    box(d, 360, 122, 133, 60, "unit refunded\nolder answer cached?\n→ return it (stale)\nelse 502 upstream_error",
        **red)
    arrow(d, cx + 105, 152, 360, 152, "yes")
    arrow(d, cx, 130, cx, 108, "no", label_dx=10, label_dy=-2)
    # 8 answer
    box(d, cx - 115, 38, 230, 70, "⑧ Merge · remove duplicates (cheapest kept)\nmax 10 per shop · interleave shops\n"
        "save in cache (48 h; 10 min if a shop\nfailed, 30 min if empty) · count usage + cost\n→ 200 OK, X-Cache: MISS",
        fill=GREEN, stroke=GREEN_D, size=6.9)
    text(d, 0, 18, "Every answer carries X-Plan, X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset "
         "and, per shop, markets: ok / no_results / error.", 6.8)
    text(d, 0, 6, "Input check (title length, known country) happens before ④: 400 invalid_request / "
         "unsupported_country.", 6.8)
    return d


def diagram_fallback_chain():
    H = 150
    d = Drawing(W, H)
    box(d, 0, 52, 80, 50, "one shop\nof the search", fill=PURPLE, stroke=PURPLE_D, size=7.2)
    box(d, 104, 52, 104, 50, "1 · own source\nSerpApi engine\nor Apify scraper", fill=GREEN, stroke=GREEN_D, size=7.2)
    box(d, 238, 52, 104, 50, "2 · Google Shopping\n(SerpApi, 1 search)\nshop-search links", fill=ORANGE,
        stroke=ORANGE_D, size=7.2)
    box(d, 372, 52, 121, 50, "3 · SearXNG\nfree web search\nlinks, no prices", fill=BLUE, stroke=BLUE_D, size=7.2)
    arrow(d, 80, 77, 104, 77)
    arrow(d, 208, 77, 238, 77, "error / timeout", label_size=6.2)
    arrow(d, 342, 77, 372, 77, "error", label_size=6.2)
    box(d, 104, 112, 389, 30, "Circuit breaker per source: after PROVIDER_FAILURE_THRESHOLD (3) failures in a row the source "
        "is skipped\nfor PROVIDER_COOLDOWN (1 min) → a broken source adds no delay; state visible in the admin panel",
        fill=GREY, stroke=GREY_D, size=6.8, bold_first=False)
    text(d, 104, 34, "Each attempt has its own timeout: PROVIDER_ATTEMPT_TIMEOUT 8 s, SERPAPI_TIMEOUT 25 s, APIFY_TIMEOUT 60–90 s.",
         6.8)
    text(d, 104, 22, "APIFY_EMPTY_FALLBACK=false: a scraper that finds nothing relevant ends as no_results (no extra cost).",
         6.8)
    text(d, 104, 10, "The first source that answers wins; every product carries \"provider\": serpapi / apify / searxng.",
         6.8)
    return d


def diagram_cache_logic():
    H = 150
    d = Drawing(W, H)
    box(d, 0, 92, 110, 50, "request\ncountry + title\n\"refresh\": true?", fill=PURPLE, stroke=PURPLE_D, size=7)
    box(d, 132, 92, 150, 50, "cache key\nsearch:v6:jor:<hash>\nhash = shops + setup +\nnormalized title", fill=GREY,
        stroke=GREY_D, size=6.9)
    box(d, 304, 92, 189, 50, "Store\nRedis (shared by instances)\nor in-memory LRU (no REDIS_URL)\n10,000 entries max",
        fill=GREEN, stroke=GREEN_D, size=6.9)
    arrow(d, 110, 117, 132, 117)
    arrow(d, 282, 117, 304, 117)
    box(d, 0, 14, 150, 62, "singleflight\nthe same search arriving\ntwice at once → only one\nupstream call, both wait",
        fill=BLUE, stroke=BLUE_D, size=6.9)
    box(d, 168, 14, 150, 62, "stale-if-error\nlive search failed but an\nold answer exists → it is\nreturned (stale: true)",
        fill=BLUE, stroke=BLUE_D, size=6.9)
    box(d, 336, 14, 157, 62, "pull to refresh\n\"refresh\": true forces a live\nsearch, at most once per\n10 min per query",
        fill=BLUE, stroke=BLUE_D, size=6.9)
    return d


def diagram_key_lifecycle():
    H = 190
    d = Drawing(W, H)
    steps = [
        ("1 · Create", "admin panel → API clients\nor POST /admin/v1/clients\nname, owner, scopes,\nquota, rate, countries,\nIPs, expiry", PURPLE, PURPLE_D),
        ("2 · Key shown ONCE", "ofd_live_ + 40 chars\n(200 random bits)\ncopy button + curl\nexample", ORANGE, ORANGE_D),
        ("3 · Use", "Authorization:\nBearer ofd_live_…\non POST /api/v1/search\nown quota, own limit", GREEN, GREEN_D),
        ("4 · Rotate", "\"New key\": new key\nshown once; old key\nworks for the grace\n(default 24 h, 0 = now)", BLUE, BLUE_D),
        ("5 · Revoke", "client + all keys\nstop at once (final)\nor one key only\n→ 401 API key revoked", RED, RED_D),
    ]
    bw, gap = 88, 13
    for i, (t, body, f, e) in enumerate(steps):
        x = i * (bw + gap)
        box(d, x, 82, bw, 98, t + "\n" + body, fill=f, stroke=e, size=6.8)
        if i:
            arrow(d, x - gap, 131, x, 131)
    box(d, 0, 6, W, 62, "What the database keeps (table api_keys)\nonly the SHA-256 hash of the key + its first 8 characters "
        "(ofd_live_abcd1234…) + created / expires / revoked / last used.\nThe key itself is never stored, logged or written to "
        "the audit log. A lost key cannot be shown again → make a new one (rotate).\nChecked keys are cached 10 s; "
        "a change is seen at once on the same server and within 10 s on others.", fill=GREY, stroke=GREY_D, size=6.9)
    return d


def diagram_cost():
    H = 210
    d = Drawing(W, H)
    box(d, 0, 140, 104, 60, "Source call\nSerpApi search or\nApify scraper run\n(user or admin test)",
        fill=PURPLE, stroke=PURPLE_D, size=6.9)
    box(d, 126, 140, 112, 60, "Estimated cost\nfrom settings:\nSERPAPI_SEARCH_COST_USD\nAPIFY_<SHOP>_RUN_COST_USD",
        fill=ORANGE, stroke=ORANGE_D, size=6.6)
    box(d, 260, 140, 104, 60, "Recorder (memory)\nper day · country ·\nsource · shop · outcome\n+ ms + cost", fill=BLUE,
        stroke=BLUE_D, size=6.9)
    box(d, 386, 140, 107, 60, "Postgres\nusage_daily (400 d)\nquery_daily (90 d)\nadded every 30 s\n+ at shutdown",
        fill=GREEN, stroke=GREEN_D, size=6.9)
    arrow(d, 104, 170, 126, 170)
    arrow(d, 238, 170, 260, 170)
    arrow(d, 364, 170, 386, 170, "flush")
    box(d, 260, 50, 233, 62, "Dashboard  ·  GET /admin/v1/usage?days=30\nsearches hit / stale / live / quota / error, cache hit "
        "rate,\ncalls + errors + avg time per source and shop, fallbacks,\nestimated cost per day, top queries, budget meter",
        fill=colors.white, stroke=NAVY, size=6.8)
    arrow(d, 440, 140, 440, 112)
    box(d, 0, 50, 238, 62, "Budget check (every minute)\nthis month's estimated cost vs BUDGET_MONTHLY_USD ($5)\n"
        "≥ 80 % → warning · ≥ 100 % → exceeded\none ERROR log line + audit entry budget.alert per\nlevel and month (searches are NOT blocked)",
        fill=RED, stroke=RED_D, size=6.8)
    arrow(d, 400, 140, 200, 112)
    text(d, 0, 30, "Costs are ESTIMATES (price per call × number of calls). The real bill is in the SerpApi and Apify "
         "consoles —", 6.9)
    text(d, 0, 19, "compare once a month and adjust the cost settings. Cached answers cost nothing and are not counted.", 6.9)
    text(d, 0, 6, "A source call counts once per shop for Apify (one run per shop) and once per call for SerpApi.", 6.9)
    return d


def diagram_settings():
    H = 130
    d = Drawing(W, H)
    box(d, 0, 62, 90, 58, "Admin\npanel / PATCH\n/admin/v1/settings\nor CLI settings set", fill=PURPLE, stroke=PURPLE_D, size=6.8)
    box(d, 106, 62, 92, 58, "Validate the\nWHOLE new config\ninvalid → refused\nwith a message", fill=ORANGE,
        stroke=ORANGE_D, size=6.8)
    box(d, 214, 62, 92, 58, "Postgres\nsettings + history\nsecrets AES-256-GCM\nversion + 1", fill=GREEN,
        stroke=GREEN_D, size=6.8)
    box(d, 322, 62, 80, 58, "Every server\npolls version\nevery 10 s", fill=BLUE, stroke=BLUE_D, size=6.8)
    box(d, 418, 62, 75, 58, "Build new\nchain, cache,\nrouter → swap\natomically", fill=BLUE, stroke=BLUE_D, size=6.8)
    for x1, x2 in ((90, 106), (198, 214), (306, 322), (402, 418)):
        arrow(d, x1, 91, x2, 91)
    text(d, 0, 44, "No restart. Searches already running finish on the old setup. .env is copied into the database "
         "only on the FIRST start;", 6.9)
    text(d, 0, 32, "afterwards change runtime settings in the panel / CLI (the server warns about .env keys that differ). "
         "Secrets are", 6.9)
    text(d, 0, 20, "write-only: never shown again, never in the history. Bootstrap settings (database, encryption key, "
         "auth, ports) stay in", 6.9)
    text(d, 0, 8, ".env and need a restart.", 6.9)
    return d


def diagram_code_map():
    H = 330
    d = Drawing(W, H)
    rows = [
        ("Entry", "cmd/owis_find_deal_engine", "main.go: wiring, settings swap, graceful shutdown · admincmd.go: CLI "
         "(settings, admins, audit, clients)", PURPLE, PURPLE_D),
        ("HTTP", "internal/api · internal/adminui", "router, middleware, access (JWT or key), handlers, admin API, "
         "admin clients · serves /admin/", GREY, GREY_D),
        ("Access", "internal/auth · internal/apikeys", "JWT + JWKS cache · API key format, hash, cached check, "
         "client rules (IP, scope, country)", ORANGE, ORANGE_D),
        ("Limits", "internal/ratelimit · internal/usage", "token bucket per user / client · plans, daily quota "
         "counters in Redis or memory", ORANGE, ORANGE_D),
        ("Cost rule", "internal/metering", "cached answer = free · live search = take 1 unit · refund on failure",
         PURPLE, PURPLE_D),
        ("Cache", "internal/cache", "stale-while-revalidate, singleflight, Redis / LRU store, key with results "
         "version v6", GREEN, GREEN_D),
        ("Search", "internal/search · internal/markets", "worker pool, merge, dedupe, ranking, relevance, fallback "
         "+ breaker, probe · countries → shops", BLUE, BLUE_D),
        ("Sources", "internal/providers/…", "serpapi (Google Shopping, Amazon, eBay) · apify (scrapers) · searxng "
         "(free web search)", BLUE, BLUE_D),
        ("Data", "internal/store · settings · config", "Postgres + migrations · encrypted settings store + watcher · "
         "config keys and validation", GREEN, GREEN_D),
        ("Stats", "internal/stats", "usage counters, flush to Postgres, dashboard report, monthly budget alert",
         GREEN, GREEN_D),
    ]
    bh, gap = 27, 5.5
    y = H - bh
    for layer, pkg, what, f, e in rows:
        box(d, 0, y, 62, bh, layer, fill=f, stroke=e, size=7.2)
        box(d, 68, y, 150, bh, pkg, fill=colors.white, stroke=e, size=6.9)
        lines = textwrap.wrap(what, 66)
        for i, ln in enumerate(lines):
            d.add(String(226, y + bh / 2 + 3.6 * (len(lines) - 1) - 2.5 - 7.4 * i, ln, fontName="Sans",
                         fontSize=6.6, fillColor=INK))
        y -= bh + gap
    return d


def diagram_roadmap():
    H = 126
    d = Drawing(W, H)
    phases = [
        ("Phase 1", "Search service", "steps 1–6", "✓ done", GREEN, GREEN_D),
        ("Phase 2", "Admin panel", "steps 7–10", "✓ done", GREEN, GREEN_D),
        ("Phase 3", "API integration", "11 ✓ · 12 · 13", "in progress", ORANGE, ORANGE_D),
        ("Phase 4", "Deployment", "step 14", "to do", BLUE, BLUE_D),
        ("Phase 5", "Users", "steps 15–19", "to do", GREY, GREY_D),
    ]
    bw, gap = 88, 13
    for i, (p, name, steps, state, f, e) in enumerate(phases):
        x = i * (bw + gap)
        box(d, x, 40, bw, 76, f"{p}\n{name}\n{steps}\n{state}", fill=f, stroke=e, size=7.4)
        if i:
            arrow(d, x - gap, 78, x, 78)
    d.add(Polygon([2 * (bw + gap) + bw / 2 - 6, 30, 2 * (bw + gap) + bw / 2 + 6, 30, 2 * (bw + gap) + bw / 2, 38],
                  fillColor=RED_D, strokeColor=RED_D))
    text(d, 2 * (bw + gap) + bw / 2, 18, "we are here: next is step 12", 7.2, RED_D, "middle", bold=True)
    return d


# ================================================================ content
def cover():
    s = [Spacer(1, 6)]
    s.append(P("owis_find_deal_engine", "title"))
    s.append(P("Service guide — description, architecture, Docker, flow, cache, API clients, cost, code map, "
               "how to run, use and test it, and what comes next", "subtitle"))
    s.append(T([
        ["", ""],
        ["Status", "Phases 1–2 done · phase 3 step 11 (API clients and keys) done · next: step 12"],
        ["Code", f"{REPO}<br/>folder <font name='Mono'>Owis_Find_Deal_Engine/</font>, branch "
                 f"<font name='Mono'>{BRANCH}</font>"],
        ["Stack", "Go 1.26 · Redis 7 · Postgres 17 · SearXNG · React 19 admin app · Docker Compose"],
        ["Sources", "SerpApi (Amazon, eBay, Google Shopping) · Apify (AliExpress, Temu, SHEIN) · SearXNG"],
        ["Countries", "usa · ksa · jor"],
        ["Date", DATE],
    ], [70, W - 70], header=False, first_col_bold=True))
    s.append(Spacer(1, 16))
    s.append(callout("<b>How to read this guide.</b> Sections 1–3 explain <i>what</i> the service is and how it is "
                     "built (architecture, Docker). Sections 4–10 explain <i>how it works</i>: request flow, sources, "
                     "cache, login and API keys, cost, settings, admin panel. Sections 11–12 map the code and the "
                     "database. Sections 13–17 are hands-on: how to use the API, how to run it on your PC step by "
                     "step (git and docker commands), how to test it, git workflow and troubleshooting. Sections "
                     "18–20 list the known limits, what is remaining and what is recommended next."))
    s.append(Spacer(1, 4))
    s.append(P("Contents", "h2"))
    toc = TableOfContents()
    toc.levelStyles = [
        S["cellb"].clone("toc0", fontSize=9, leading=11.6, leftIndent=0),
        S["cell"].clone("toc1", fontSize=8.2, leading=11, leftIndent=14, textColor=MUTED),
    ]
    s.append(toc)
    s.append(PageBreak())
    return s


def sec_about():
    s = h1("1. About the service")
    s += [P("<b>owis_find_deal_engine</b> is a backend service that receives a <b>product name</b> and a "
            "<b>country</b>, searches the online shops that <b>deliver to that country</b>, and returns <b>one "
            "merged list</b> of products with title, price, image and a direct link to the shop's product page."),
          P("It is the engine behind a future mobile / web app (\"find the best deal\") and can also be called by "
            "your own servers and AI agents with an API key. The goal is to keep the running cost as close to "
            "<b>$0</b> as possible: answers are cached for 48 hours, so a popular search is paid only once.")]
    s += h2("1.1 Countries and shops")
    s.append(T([
        ["Shop", "usa", "ksa", "jor", "Source (today)"],
        ["Amazon", "amazon.com", "amazon.sa", "amazon.com", "SerpApi Amazon engine"],
        ["eBay", "ebay.com", "ebay.com", "— (no delivery)", "SerpApi eBay engine"],
        ["AliExpress", "aliexpress.com", "aliexpress.com", "aliexpress.com", "Apify scraper"],
        ["Temu", "temu.com", "temu.com", "temu.com", "Apify scraper"],
        ["SHEIN", "us.shein.com", "us.shein.com*", "us.shein.com*", "Apify scraper"],
    ], [64, 82, 82, 82, W - 310], first_col_bold=True))
    s.append(P("* the SHEIN scraper reads the US site only. The country → shop list is configuration "
               "(<font name='Mono'>markets.json</font> or the admin panel page <i>Countries &amp; shops</i>), not "
               "code.", "small"))
    s += h2("1.2 Who calls it")
    s.append(T([
        ["Caller", "Credential", "Limits", "Status"],
        ["Mobile / web app users", "Firebase login token (JWT)", "plan quota per day (free 20, pro 500), 1 req/s "
         "burst 5", "service ready; app not built yet"],
        ["Own servers and AI agents", "API key <font name='Mono'>ofd_live_…</font>", "own quota (default 100 live "
         "searches/day), own rate", "✓ step 11"],
        ["Admins", "Firebase login + admin list", "per-admin rate limit, audit log", "✓ steps 8–10"],
        ["Partners (later)", "API key, self-service", "billing per client", "later"],
    ], [110, 120, 150, W - 380], first_col_bold=True))
    s += h2("1.3 Main features today")
    s += bullets([
        "One search = all shops of the country, in parallel; each shop has its own best source with fallbacks.",
        "Only real product pages are returned (store / category / search pages are filtered out), duplicates "
        "removed, results must match the query, at most 10 per shop.",
        "48 h cache with stale-while-revalidate (72 h), pull-to-refresh, stale-if-error; <b>cached answers are "
        "free</b> for the caller.",
        "Login with any JWT provider (Firebase now); daily quota per plan in Redis (402 / 429); burst rate limit.",
        "API clients and keys for servers and agents (hashed keys, scopes, quotas, IP / country limits, rotate, "
        "revoke).",
        "Admin panel at <font name='Mono'>/admin/</font>: dashboard (usage + estimated cost + $5 budget), all "
        "settings changed without restart, test search, cache tools, admins, API clients, audit log.",
        "Hardened Docker stack: 4 containers, private networks, read-only, non-root, only the API port published "
        "on 127.0.0.1.",
    ])
    s += hfig("1.4 Status at a glance", diagram_roadmap(),
              "Figure 1 — Project phases. Phase 3 (API integration) is in progress.")
    return s


def sec_architecture():
    s = h1("2. Architecture")
    s.append(P("The whole service is <b>one Go binary</b> (API + admin API + admin web app) plus three helper "
               "containers. Everything runs with Docker Compose on one host today; the same image can be deployed "
               "anywhere later (phase 4)."))
    s.append(fig(diagram_architecture(), "Figure 2 — Architecture. Callers on the left, the Docker stack in the "
                                         "middle, paid / external services on the right."))
    s += h2("2.1 Components")
    s.append(T([
        ["Component", "Role", "Why"],
        ["server (Go)", "public API, admin API, admin web app, all business logic", "one small, fast, static "
         "binary; easy to deploy"],
        ["Redis", "search cache (shared) + daily usage counters", "atomic counters, TTL, survives restarts (AOF)"],
        ["Postgres", "runtime settings + history, admins, audit log, usage / cost per day, API clients + keys",
         "changes without restart, history, durable"],
        ["SearXNG", "free self-hosted metasearch (last fallback)", "$0 links when paid sources fail"],
        ["Firebase Auth", "user and admin login; the server only checks tokens with public keys (JWKS)",
         "no passwords stored by us"],
        ["SerpApi", "Amazon and eBay engines (prices, direct links), Google Shopping fallback", "250 free "
         "searches / month"],
        ["Apify", "store scrapers for AliExpress, Temu, SHEIN (no official API)", "$5 free credit / month"],
    ], [82, 230, W - 312], first_col_bold=True))
    s += h2("2.2 Inside the server: layers")
    s.append(P("A request passes these layers in order. Each layer is its own Go package, so it can be tested and "
               "replaced alone:"))
    s += numbered([
        "<b>HTTP middleware</b> — request id, JSON log line, panic recovery, CORS allow-list, write deadline.",
        "<b>Access</b> — JWT (user) or API key (client); scopes, IP and country rules for keys.",
        "<b>Rate limit</b> — token bucket per user / client.",
        "<b>Handler</b> — strict JSON decoding (body limit), maps results and errors to status codes + headers.",
        "<b>Metering</b> — the single place that decides: cached = free, live = 1 unit of the daily quota.",
        "<b>Cache</b> — fresh / stale / miss, merges identical searches, background refresh.",
        "<b>Search service</b> — picks the country's shops, runs them on a bounded worker pool, merges and ranks.",
        "<b>Provider chains</b> — per shop: own source → Google Shopping → SearXNG, with circuit breakers.",
    ])
    return s


def sec_docker():
    s = h1("3. Docker structure")
    s.append(P("Files in <font name='Mono'>Owis_Find_Deal_Engine/server/</font>: "
               "<font name='Mono'>Dockerfile</font> (the image), <font name='Mono'>docker-compose.yml</font> (the "
               "full stack), <font name='Mono'>compose.dev.yml</font> (override for running Go on your PC), "
               "<font name='Mono'>.dockerignore</font> (keeps <font name='Mono'>.env</font> and tests out of the "
               "image), <font name='Mono'>deploy/searxng/settings.yml</font>."))
    s.append(fig(diagram_docker(), "Figure 3 — Containers, networks and volumes of docker-compose.yml."))
    s += hfig("3.1 The image (multi-stage Dockerfile)", diagram_dockerfile(),
              "Figure 4 — Three build stages; the final image is ~19 MB.")
    s += h2("3.2 Services")
    s.append(T([
        ["Service", "Image", "Networks", "Ports", "Data", "Limits"],
        ["server", "built here", "edge + data", "127.0.0.1:3002", "—", "256 MB, 256 pids"],
        ["searxng", "searxng/searxng:latest", "edge", "none (dev: 127.0.0.1:8888)", "tmpfs", "512 MB"],
        ["redis", "redis:7-alpine", "data", "none (dev: 127.0.0.1:6379)", "redis-data", "320 MB (256 MB data)"],
        ["postgres", "postgres:17-alpine", "data", "none (dev: 127.0.0.1:5432)", "postgres-data", "256 MB"],
    ], [52, 96, 60, 112, 64, W - 384], first_col_bold=True))
    s += h2("3.3 Security choices")
    s += bullets([
        "<b>Network <font name='Mono'>data</font> is internal</b>: Redis and Postgres have no internet and no "
        "published port. Only the server talks to them.",
        "<b>Only the API is published</b>, and only on <font name='Mono'>127.0.0.1</font> "
        "(<font name='Mono'>API_BIND</font>/<font name='Mono'>API_PORT</font>). In production put Caddy / Nginx "
        "or a Cloudflare Tunnel in front for HTTPS.",
        "Every container: read-only file system, all Linux capabilities dropped, no-new-privileges, non-root user, "
        "memory and process limits, log rotation.",
        "Passwords come from <font name='Mono'>.env</font>; compose refuses to start if "
        "<font name='Mono'>REDIS_PASSWORD</font>, <font name='Mono'>POSTGRES_PASSWORD</font>, "
        "<font name='Mono'>SEARXNG_SECRET</font> or <font name='Mono'>SETTINGS_ENCRYPTION_KEY</font> is missing.",
        "Health checks: the server image has its own <font name='Mono'>healthcheck</font> sub-command (distroless "
        "has no curl); the server starts only after Redis, SearXNG and Postgres are healthy.",
    ])
    return s


def sec_flow():
    s = [PageBreak()] + h1("4. Request flow (search)")
    s.append(P("What happens when a client calls <font name='Mono'>POST /api/v1/search</font>. Green = answer, "
               "red = refused, orange = credential checks."))
    s.append(fig(diagram_request_flow(), "Figure 5 — Flowchart of one search request."))
    s += h2("4.1 Step by step")
    s += numbered([
        "<b>Request arrives</b> with <font name='Mono'>Authorization: Bearer …</font> and a JSON body "
        "<font name='Mono'>{\"title\", \"country\", \"refresh\"?}</font>.",
        "<b>Credential</b>: a token starting with <font name='Mono'>ofd_</font> is an API key, anything else a "
        "Firebase JWT. JWT: signature checked locally with Firebase public keys (refreshed hourly), issuer, "
        "audience, expiry; the plan comes from a custom claim (default <font name='Mono'>free</font>). Key: looked "
        "up by its SHA-256 hash, must be active, allowed from this IP, have scope "
        "<font name='Mono'>search</font> and allow the country.",
        "<b>Burst rate limit</b>: users 1 request/s with bursts of 5; each API client its own rate.",
        "<b>Cache lookup</b>: a fresh or stale answer is returned immediately and is <b>free</b> — even when the "
        "daily allowance is used up.",
        "<b>Quota</b>: otherwise one unit is taken from the caller's daily counter in Redis. Used up → 402 (free "
        "plan, show upgrade) or 429 (paid plan / API client). Redis down → 503 (fail closed, protects paid "
        "sources).",
        "<b>Live search</b>: only the shops of the country, in parallel; each shop through its source chain "
        "(section 5). Identical concurrent searches share one upstream call.",
        "<b>Failure handling</b>: if every shop fails, the unit is refunded and an older cached answer is returned "
        "if one exists (<font name='Mono'>stale: true</font>), else 502. If only some shops fail, the request "
        "still succeeds and <font name='Mono'>markets</font> shows which.",
        "<b>Answer</b>: merged, deduplicated (cheapest kept), max 10 per shop, shops interleaved; saved in the "
        "cache; usage and estimated cost recorded; 200 with <font name='Mono'>X-Cache: MISS</font>.",
    ])
    s += h2("4.2 Response")
    s.append(code("""HTTP/1.1 200 OK
X-Cache: MISS            X-Plan: free          X-RateLimit-Limit: 20
X-RateLimit-Remaining: 19                      X-RateLimit-Reset: 1792195200

{
  "query": "usb c hub", "country": "jor",
  "results": [
    {"market": "amazon", "title": "Anker USB C Hub 7-in-1 ...",
     "link": "https://www.amazon.com/dp/B0...", "price": 29.99, "currency": "$",
     "thumbnail": "https://...", "position": 1, "provider": "serpapi"},
    {"market": "temu", "title": "...", "link": "https://www.temu.com/goods.html?goods_id=...",
     "price": 6.49, "currency": "$", "position": 1, "provider": "apify"}
  ],
  "markets": {"amazon": "ok", "aliexpress": "ok", "temu": "ok", "shein": "no_results"},
  "took_ms": 28650, "fetched_at": "2026-10-16T09:12:00Z", "cached": false, "stale": false
}"""))
    s.append(P("<font name='Mono'>link_type: \"search\"</font> marks a link that opens the shop's search for the "
               "product (Google Shopping gives no direct link). The app should show prices as approximate and "
               "\"prices updated X ago\" from <font name='Mono'>fetched_at</font>.", "small"))
    s += h2("4.3 Errors")
    s.append(P("All errors look like <font name='Mono'>{\"error\": {\"code\": \"…\", \"message\": \"…\"}}</font>."))
    s.append(T([
        ["Status", "code", "Meaning / what to do"],
        ["400", "invalid_request, unsupported_country", "bad JSON, title too short / long, unknown country"],
        ["401", "unauthorized", "missing / invalid / expired JWT; unknown, revoked or expired API key"],
        ["402", "payment_required", "free plan's daily searches used up → show upgrade"],
        ["403", "forbidden, country_not_allowed", "API key lacks scope, wrong IP, or country not allowed"],
        ["429", "rate_limited, quota_exceeded", "too fast (see Retry-After) / paid plan or API client used up"],
        ["502", "upstream_error", "every shop failed and nothing cached"],
        ["503", "service_unavailable", "Redis (usage counters) unreachable — fails closed"],
        ["500", "internal_error", "a bug — check the server log with the request id"],
    ], [46, 160, W - 206]))
    return s


def sec_sources():
    s = h1("5. Search sources per shop")
    s.append(P("Free web search alone was not enough (blocked quickly from one IP, finds few product pages, no "
               "prices). So each shop has its own best source, chosen in the October 2026 local tests:"))
    s.append(fig(diagram_sources(), "Figure 6 — Source, cost and speed per shop (October 2026 measurements)."))
    s += hfig("5.1 Fallback chain and circuit breaker", diagram_fallback_chain(),
              "Figure 7 — What happens when a shop's own source fails.")
    s += h2("5.2 Result quality rules")
    s += bullets([
        "<b>Product pages only</b>: each shop has <font name='Mono'>product_pages</font> patterns (Amazon "
        "<font name='Mono'>/dp/&lt;ASIN&gt;</font>, eBay <font name='Mono'>/itm/&lt;id&gt;</font>, AliExpress "
        "<font name='Mono'>/item/&lt;id&gt;.html</font>, Temu <font name='Mono'>goods.html?goods_id=</font>, SHEIN "
        "<font name='Mono'>-p-&lt;id&gt;.html</font>).",
        "<b>Relevance</b>: at least two thirds of the query words must appear in the title, snippet or link.",
        "<b>Clean links</b>: tracking parameters removed, the same product listed once (cheapest kept).",
        "<b>Per shop limit</b>: <font name='Mono'>RESULTS_PER_MARKET</font> = 10, best first, shops interleaved.",
    ])
    s += h2("5.3 Typical timings")
    s.append(T([
        ["Case", "Time", "Notes"],
        ["Cached answer (HIT / STALE)", "&lt; 50 ms", "free"],
        ["Live, SerpApi shops only (Amazon, eBay)", "~3 s", "1–2 SerpApi searches"],
        ["Live, all shops incl. Apify scrapers", "25–50 s", "scrapers start a browser; run in parallel"],
    ], [200, 80, W - 280]))
    return s


def sec_cache():
    s = h1("6. Cache — how it works")
    s.append(P("The cache is the main cost saver: a live search may cost ~$0.16–0.19, a cached answer costs "
               "nothing. It sits in front of the search service (<font name='Mono'>internal/cache</font>) and is "
               "stored in Redis (or in memory when <font name='Mono'>REDIS_URL</font> is empty)."))
    s.append(fig(diagram_cache_48(), "Figure 8 — Life of a cached answer (default settings)."))
    s.append(fig(diagram_cache_logic(), "Figure 9 — Cache key, storage and the three safety features."))
    s += h2("6.1 Rules")
    s.append(T([
        ["Age / situation", "What happens", "X-Cache", "Setting"],
        ["&lt; 48 h", "served from cache, free", "HIT", "CACHE_FRESH_TTL"],
        ["48 – 72 h", "served instantly (stale: true) and refreshed in the background", "STALE", "CACHE_STALE_TTL"],
        ["&gt; 72 h or not cached", "live search", "MISS", "—"],
        ["a shop failed", "answer fresh only 10 min (retried sooner)", "", "CACHE_PARTIAL_TTL"],
        ["no results at all", "fresh only 30 min", "", "CACHE_EMPTY_TTL"],
        ["\"refresh\": true", "forces live search, at most once per 10 min per query", "MISS", "CACHE_MIN_REFRESH"],
        ["background refreshes", "at most 4 in parallel", "", "CACHE_MAX_BACKGROUND_REFRESH"],
    ], [92, 206, 58, W - 356]))
    s += h2("6.2 The cache key")
    s.append(code("search:<results version>:<country>:<first 16 bytes of SHA-256(\n"
                  "        shop-list version ; search setup ; shop=domain;... ; normalized title )>\n"
                  "example: search:v6:jor:3f9a0c...    (title: lower case, spaces collapsed)"))
    s += bullets([
        "<b>Same search, any spelling of spaces / case</b> → same key, so users share answers.",
        "<b>Shop list or search setup changed</b> (markets, provider order, results per shop) → new key; old "
        "answers are never served for a changed setup.",
        "<b>Results version</b> (<font name='Mono'>v6</font> in code) is raised when result parsing changes, so a "
        "new build drops answers from an older one.",
        "<b>Admin tools</b>: <i>forget one search</i> (next search is live) and <i>drop all cached answers</i> "
        "(raises <font name='Mono'>CACHE_GENERATION</font>).",
        "<b>Redis memory</b>: max 256 MB, policy <font name='Mono'>volatile-ttl</font> — cache entries (closest to "
        "expiry) are evicted before usage counters.",
    ])
    return s


def sec_auth_and_clients():
    s = h1("7. Login, quotas and API clients")
    s += h2("7.1 App users (Firebase JWT)")
    s += bullets([
        "The app signs the user in with Firebase (email, Google, Apple…) and sends the ID token as "
        "<font name='Mono'>Authorization: Bearer &lt;JWT&gt;</font>.",
        "The server checks it <b>locally</b> with Firebase's public keys (JWKS, cached, refreshed hourly; RS256 / "
        "ES256 only, <font name='Mono'>alg: none</font> and HMAC refused) — no call to Firebase per request.",
        "Plan from the custom claim <font name='Mono'>plan</font> (<font name='Mono'>AUTH_PLAN_CLAIM</font>); "
        "plans in <font name='Mono'>PLANS=free:20,pro:500</font> (live searches per UTC day).",
    ])
    s += h2("7.2 API clients (own servers and AI agents)")
    s.append(P("Owner decision D4: keys are for <b>servers and agents only</b>. The mobile / web app keeps the "
               "user's login token, because a key inside an app could be extracted."))
    s.append(fig(diagram_key_lifecycle(), "Figure 10 — Life of an API key."))
    s.append(T([
        ["Client setting", "Default", "Effect"],
        ["name, owner", "—", "who uses it (shown in panel and audit log)"],
        ["scopes", "search, countries", "search = may call /api/v1/search; countries = for agent tools (step 13)"],
        ["daily_quota", "100", "live searches per UTC day (cached answers free); used up → 429 quota_exceeded"],
        ["rate_rps / rate_burst", "1 / 5", "burst limit of this client only"],
        ["countries", "all", "other country → 403 country_not_allowed"],
        ["ip_allow", "any", "list of IPs / CIDRs; other address → 403 forbidden"],
        ["expires_at", "never", "the client and all its keys stop at that time"],
    ], [100, 90, W - 190], first_col_bold=True))
    s.append(Spacer(1, 6))
    s.append(callout("Counters never mix: a user's counter is the user id, a client's is "
                     "<font name='Mono'>client:&lt;id&gt;</font>, and a login token whose subject starts with "
                     "<font name='Mono'>client:</font> is refused. Answers to API clients carry "
                     "<font name='Mono'>X-Plan: api</font>.", fill=GREEN, edge=GREEN_D))
    s += h2("7.3 Admins")
    s += bullets([
        "Admin API <font name='Mono'>/admin/v1/…</font> and panel <font name='Mono'>/admin/</font>: valid Firebase "
        "token + active entry in the <font name='Mono'>admins</font> table + sign-in not older than 12 h.",
        "The first admin comes from <font name='Mono'>ADMIN_EMAIL</font>; more with the panel or "
        "<font name='Mono'>admins add EMAIL</font>.",
        "<b>Disabled for development (must be turned on before production)</b>: two-factor login "
        "(<font name='Mono'>ADMIN_REQUIRE_2FA</font>) and email verification "
        "(<font name='Mono'>ADMIN_REQUIRE_VERIFIED_EMAIL</font>).",
        "Every change, test search, cache tool and refused login goes to the audit log (never secret values).",
    ])
    return s


def sec_cost():
    s = h1("8. Cost — how it is measured")
    s.append(P("The service pays only for <b>live</b> searches that reach a paid source. It cannot read the "
               "providers' bills, so it <b>estimates</b> the cost of every call from price settings and adds it up "
               "per day."))
    s.append(fig(diagram_cost(), "Figure 11 — From a source call to the dashboard and the budget alert."))
    s += h2("8.1 The formula")
    s.append(code("cost of one live search = Σ over shops that called a paid source:\n"
                  "     SerpApi call  → SERPAPI_SEARCH_COST_USD            (per search, any number of shops)\n"
                  "     Apify run     → APIFY_<SHOP>_RUN_COST_USD          (per scraper run = per shop)\n"
                  "     SearXNG       → 0\n"
                  "cost of a cached answer = 0\n"
                  "month cost = Σ usage_daily.cost_usd for this month (all servers add up)"))
    s += h2("8.2 Current prices (owner's accounts, October 2026)")
    s.append(T([
        ["Source", "Measured price", "Setting to enter"],
        ["Temu (Apify crw/temu-products-scraper)", "$0.10 per run (charged for 10 results)", "APIFY_TEMU_RUN_COST_USD = 0.10"],
        ["SHEIN (Apify clearpath/shein-product-scraper)", "≈ $0.0066 per result, 5 results", "APIFY_SHEIN_RUN_COST_USD = 0.033"],
        ["AliExpress (Apify piotrv1001/…)", "≈ $0.02 per run", "APIFY_ALIEXPRESS_RUN_COST_USD = 0.02"],
        ["SerpApi (Amazon, eBay, Shopping)", "free plan: 250 / month · Developer ≈ $0.015", "SERPAPI_SEARCH_COST_USD = 0 (free) or 0.015"],
    ], [170, 150, W - 320]))
    s.append(Spacer(1, 6))
    s.append(callout("<b>Worked example</b> — live search in <font name='Mono'>jor</font>: Temu 0.10 + SHEIN 0.033 + "
                     "AliExpress 0.02 = <b>$0.153 Apify</b> + 1 SerpApi search (Amazon; eBay does not deliver to "
                     "Jordan). In <font name='Mono'>usa</font> / <font name='Mono'>ksa</font>: same Apify + 2 SerpApi "
                     "searches (Amazon + eBay). Repeating it within 48 h costs <b>$0</b>. With the $5 Apify free "
                     "credit that is ≈ 32 different live searches per month.", fill=ORANGE, edge=ORANGE_D))
    s += h2("8.3 Budget")
    s += bullets([
        "<font name='Mono'>BUDGET_MONTHLY_USD</font> = <b>5</b> (owner decision D5: free plans), "
        "<font name='Mono'>BUDGET_ALERT_PERCENT</font> = 80.",
        "Status on the dashboard: <font name='Mono'>none / ok / warning / exceeded</font>. At 80 % and 100 %: one "
        "ERROR log line and one audit entry <font name='Mono'>budget.alert</font> per level and month.",
        "Searches are <b>not</b> blocked when the budget is exceeded (a hard stop can be added — see section 20).",
        "<b>Important:</b> if the cost settings are not entered, every cost shows 0. Enter them once in "
        "<i>Settings</i> (or with the CLI) and compare with the Apify / SerpApi consoles monthly.",
    ])
    s += h2("8.4 What is counted (table usage_daily)")
    s.append(T([
        ["kind", "keys", "values"],
        ["search", "country, outcome = hit / stale / live / quota / error", "count"],
        ["source", "provider, shop, outcome = ok / no_results / error", "count, cost_usd, total_ms"],
        ["fallback", "chain, from, to : reason (error / partial / circuit_open)", "count"],
        ["query_daily", "country, normalized query (no user ids)", "searches, live"],
    ], [70, 260, W - 330]))
    return s


def sec_settings_admin():
    s = h1("9. Settings")
    s.append(P("Two kinds of settings (<font name='Mono'>internal/config/keys.go</font>):"))
    s.append(T([
        ["Kind", "Where", "Examples", "Change"],
        ["bootstrap", ".env only", "DATABASE_URL, SETTINGS_ENCRYPTION_KEY, AUTH_*, ADMIN_EMAIL, REDIS_URL, PORT, "
         "LOG_LEVEL", "edit .env + restart"],
        ["runtime", "Postgres (copied from .env on first start)", "MARKETS, SEARCH_PROVIDERS, APIFY_*, SERPAPI_*, "
         "CACHE_*, PLANS, RATE_LIMIT_*, BUDGET_*, costs", "admin panel / CLI, no restart"],
    ], [60, 110, 210, W - 380], first_col_bold=True))
    s.append(Spacer(1, 6))
    s.append(fig(diagram_settings(), "Figure 12 — A settings change is applied on every server within ~10 s."))
    s += h2("9.1 Command line inside the container")
    s.append(code("""export MSYS_NO_PATHCONV=1                 # Git Bash on Windows only
S="docker compose exec server /owis_find_deal_engine"
$S settings list                          # value or (default), who changed it
$S settings set APIFY_SHEIN_MAX_ITEMS 5 CACHE_FRESH_TTL 48h
docker compose exec -T server /owis_find_deal_engine settings set SERPAPI_KEY - < serpapi.key
$S settings unset CACHE_FRESH_TTL         # back to the default
$S settings history [KEY]
$S admins list | add EMAIL | disable EMAIL | enable EMAIL | unlink EMAIL
$S audit [ACTION-PREFIX]                  # latest admin actions
$S clients list                           # API clients, key prefixes, last use
$S clients revoke ID                      # emergency stop of a client and all its keys"""))
    s += h1("10. Admin panel")
    s.append(P("<b>http://localhost:3002/admin/</b> — React + TypeScript app built into the server image, same "
               "origin as the admin API, strict Content-Security-Policy, only "
               "<font name='Mono'>@firebase/app</font> + <font name='Mono'>@firebase/auth</font>. Sign in with the "
               "Firebase email + password of an admin; the session ends when the tab closes."))
    s.append(T([
        ["Page", "What you do there"],
        ["Dashboard", "tiles, searches per day (cached vs live), estimated cost per day, budget meter, sources per "
         "shop, fallbacks, countries, top queries"],
        ["Settings", "every runtime setting grouped with help text; secrets write-only; changes saved together and "
         "checked as a whole; change history"],
        ["Countries & shops", "overview + JSON editor of markets (validated, applied without restart)"],
        ["Test search", "asks each shop's source directly (no cache / quota / fallback): products, time, error, "
         "estimated cost; optional all sources"],
        ["Cache & sources", "server status, forget one search, drop all cached answers, circuit breaker state"],
        ["API clients", "create, edit, new key with grace period, stop old key, revoke; key shown once + curl "
         "example"],
        ["Admins", "add, disable, enable, unlink (never yourself or the last admin)"],
        ["Audit log", "who did what, when, from which IP, with request id"],
    ], [90, W - 90], first_col_bold=True))
    return s


def sec_code():
    s = h1("11. Main code sections")
    s.append(P("Go module <font name='Mono'>owis_find_deal_engine</font> in "
               "<font name='Mono'>Owis_Find_Deal_Engine/server/</font>. About 16,000 lines of Go incl. tests, "
               "188 test functions. Each row is a package and what it is related to:"))
    s.append(fig(diagram_code_map(), "Figure 13 — Code map, from the entry point down to the data layer."))
    s.append(PageBreak())
    s.append(H1("11.1 Key files and what they relate to", 1))
    s.append(T([
        ["File", "Related to", "What to know"],
        ["cmd/…/main.go", "everything", "run(): config → Postgres + migrations → JWKS → Redis → buildApp() → "
         "settings watcher (atomic swap) → HTTP server → graceful shutdown. sourceCost()/callCost(): cost "
         "estimate per call"],
        ["cmd/…/admincmd.go", "settings, admins, audit, clients", "the CLI sub-commands"],
        ["internal/api/router.go", "public API", "routes + middleware order; access.go: JWT or key; handlers.go: "
         "search handler"],
        ["internal/api/admin*.go", "admin API", "admin access rules, settings, markets, tools, usage, clients"],
        ["internal/metering/metering.go", "quota + cost rule", "cached = free; take / refund units; reports outcome"],
        ["internal/cache/cache.go", "cache", "Lookup / Search, key(), resultsVersion = v6, background refresh"],
        ["internal/search/search.go", "search", "worker pool, merge, dedupe, limitPerMarket"],
        ["internal/search/fallback.go", "fallbacks", "chain + circuit breaker; probe.go = admin test search"],
        ["internal/providers/*", "sources", "serpapi.go, amazon.go, ebay.go, apify.go, searxng.go"],
        ["internal/markets/markets.json", "countries / shops", "domains, product page patterns, shopping region"],
        ["internal/apikeys/*.go", "API clients", "Generate(), Hash(), Authenticate() with 10 s cache, client rules"],
        ["internal/stats/*.go", "cost dashboard", "Recorder (memory) → Flush (30 s) → BuildReport, CheckBudget"],
        ["internal/store/migrations", "database", "0001 settings + admins, 0002 audit, 0003 usage, 0004 API "
         "clients"],
        ["web/admin/src", "admin web app", "App.tsx, api.ts, auth.ts, pages/*.tsx"],
    ], [128, 92, W - 220]))
    s += h1("12. Database (Postgres)")
    s.append(T([
        ["Table", "Contents", "Since"],
        ["settings / settings_history", "runtime settings (secrets encrypted) and every change: who, when, old → new", "step 7"],
        ["admins", "email, linked Firebase uid, disabled", "step 7"],
        ["audit_log", "admin actions, test searches, refused logins, budget alerts", "step 8"],
        ["usage_daily / query_daily", "counters per day and kind (400 days) / top queries (90 days)", "step 9"],
        ["api_clients / api_keys", "client settings / key hash, prefix, expiry, revoked, last used", "step 11"],
        ["schema_migrations", "applied migrations (run automatically at start, advisory lock)", "step 7"],
    ], [140, W - 190, 50], first_col_bold=True))
    return s


def sec_use():
    s = h1("13. How to use the API")
    s.append(T([
        ["Method", "Path", "Auth", "Purpose"],
        ["GET", "/api/v1/health", "—", "liveness"],
        ["GET", "/api/v1/countries", "—", "countries and their shops"],
        ["POST", "/api/v1/search", "JWT or API key", "search; body {\"title\", \"country\", \"refresh\"?}"],
        ["*", "/admin/v1/…", "admin JWT", "settings, markets, admins, audit, tools, usage, clients"],
        ["GET", "/admin/", "browser", "admin web app"],
    ], [50, 110, 90, W - 250]))
    s += h2("13.1 Examples (Git Bash / macOS / Linux)")
    s.append(code("""curl localhost:3002/api/v1/health
curl localhost:3002/api/v1/countries

# as an app user (Firebase ID token, valid 1 hour)
curl -i -X POST localhost:3002/api/v1/search \\
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \\
  -d '{"title":"usb c hub","country":"jor"}'

# pull to refresh (live again, at most once per 10 min)
  -d '{"title":"usb c hub","country":"jor","refresh":true}'

# as an API client (own server / AI agent)
curl -i -X POST localhost:3002/api/v1/search \\
  -H "Authorization: Bearer ofd_live_xxxxxxxx..." -H "Content-Type: application/json" \\
  -d '{"title":"usb c hub","country":"jor"}'

# admin: usage and cost of the last 7 days
curl -H "Authorization: Bearer $ADMIN_TOKEN" "localhost:3002/admin/v1/usage?days=7\""""))
    s += h2("13.2 PowerShell")
    s.append(code("""curl.exe -i -X POST localhost:3002/api/v1/search `
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" `
  -d '{\\"title\\":\\"usb c hub\\",\\"country\\":\\"jor\\"}'

# or natively
Invoke-RestMethod -Method Post -Uri http://localhost:3002/api/v1/search `
  -Headers @{Authorization="Bearer $TOKEN"} -ContentType "application/json" `
  -Body '{"title":"usb c hub","country":"jor"}'"""))
    return s


def sec_run():
    s = h1("14. Run it locally — step by step")
    s.append(callout("<b>You need:</b> git, Docker Desktop (Windows / macOS) or Docker Engine + compose plugin "
                     "(Linux), a free Firebase project. Optional: SerpApi key (prices for Amazon / eBay), Apify "
                     "token (AliExpress / Temu / SHEIN). Go 1.26 and Node 22 are only needed for development "
                     "without Docker."))
    s += h2("Step 1 — Get the code (git)")
    s.append(code(f"""git clone {REPO}
cd claude-ai-clould-repo
git fetch origin
git checkout {BRANCH}
git pull origin {BRANCH}          # later: get the newest version
git log --oneline -5              # latest: "Phase 3 step 11: API clients and keys"
cd Owis_Find_Deal_Engine/server"""))
    s += h2("Step 2 — Firebase project (login tokens)")
    s += numbered([
        "<font name='Mono'>https://console.firebase.google.com</font> → <b>Add project</b> (free plan).",
        "<b>Build → Authentication → Get started</b> → enable <b>Email/Password</b>.",
        "<b>Project settings → General</b>: note the <b>Project ID</b> and the <b>Web API key</b> (add a Web app "
        "if no key is shown).",
        "<b>Authentication → Users → Add user</b>: your admin login (e.g. <font name='Mono'>admin@example.com</font>; "
        "a made-up email works while verification is off).",
    ])
    s += h2("Step 3 — Configure .env")
    s.append(code("""cp .env.example .env
openssl rand -hex 32     # run 4 times: for SEARXNG_SECRET, REDIS_PASSWORD, POSTGRES_PASSWORD,
                         # SETTINGS_ENCRYPTION_KEY  (PowerShell:
                         # -join ((1..32) | % { '{0:x2}' -f (Get-Random -Max 256) }) )"""))
    s.append(P("Then set at least these lines in <font name='Mono'>.env</font>:"))
    s.append(code("""AUTH_JWKS_URL=https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com
AUTH_ISSUER=https://securetoken.google.com/<PROJECT_ID>
AUTH_AUDIENCE=<PROJECT_ID>
SEARXNG_SECRET=<hex>
REDIS_PASSWORD=<hex>
POSTGRES_PASSWORD=<hex>
SETTINGS_ENCRYPTION_KEY=<hex>          # keep a copy! DB secrets unreadable without it
ADMIN_EMAIL=admin@example.com          # your Firebase admin login
ADMIN_FIREBASE_API_KEY=<WEB_API_KEY>   # for the admin panel sign-in

# optional paid sources (prices):
SERPAPI_KEY=<key>                      # https://serpapi.com (Amazon + eBay engines turn on)
SEARCH_PROVIDERS=serpapi,searxng
APIFY_TOKEN=<token>                    # https://console.apify.com/settings/integrations
APIFY_MARKETS=aliexpress,temu,shein
# + the APIFY_<SHOP>_ACTOR / _INPUT lines from .env.example (tested values are there)"""))
    s.append(callout("<b>Note:</b> runtime settings (sources, cache, plans…) are copied from "
                     "<font name='Mono'>.env</font> into Postgres <b>only on the first start</b>. To change them "
                     "later use the admin panel or <font name='Mono'>settings set</font>; or reset everything with "
                     "<font name='Mono'>docker compose down -v</font> (deletes the database and cache).",
                     fill=ORANGE, edge=ORANGE_D))
    s += h2("Step 4 — Start the stack (docker)")
    s.append(code("""docker compose up -d --build     # first build: a few minutes (Go + admin web app)
docker compose ps                # wait until all four are "healthy" (~30 s)
docker compose logs -f server    # look for "runtime settings imported from the environment" """))
    s += h2("Step 5 — Get a test token (valid 1 hour)")
    s.append(code("""curl -s -X POST "https://identitytoolkit.googleapis.com/v1/accounts:signUp?key=<WEB_API_KEY>" \\
  -H "Content-Type: application/json" \\
  -d '{"email":"test1@example.com","password":"Test-pass-123","returnSecureToken":true}'
# copy "idToken" → export TOKEN=...   (next time: accounts:signInWithPassword, same body)
# admin token: accounts:signInWithPassword with the ADMIN_EMAIL user → export ADMIN_TOKEN=..."""))
    s += h2("Step 6 — Try it")
    s.append(code("""curl localhost:3002/api/v1/health
curl -i -X POST localhost:3002/api/v1/search -H "Authorization: Bearer $TOKEN" \\
  -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"jor"}'
# run the same command again → X-Cache: HIT, instant, X-RateLimit-Remaining does not go down"""))
    s += h2("Step 7 — Admin panel and an API key")
    s += numbered([
        "Open <b>http://localhost:3002/admin/</b> and sign in with the admin email + password.",
        "<b>Settings</b> → add the cost settings: <font name='Mono'>APIFY_TEMU_RUN_COST_USD</font> 0.10, "
        "<font name='Mono'>APIFY_SHEIN_RUN_COST_USD</font> 0.033, <font name='Mono'>APIFY_ALIEXPRESS_RUN_COST_USD</font> "
        "0.02 → Save.",
        "<b>API clients</b> → name <font name='Mono'>test agent</font>, tick <font name='Mono'>jor</font> → "
        "<i>Create client and key</i> → copy the key (shown once).",
        "Search with the key (section 13) → 200 with <font name='Mono'>X-Plan: api</font>; with "
        "<font name='Mono'>\"country\":\"usa\"</font> → 403 country_not_allowed.",
        "<b>Dashboard</b> after ~30 s: searches, cache hits, estimated cost, budget.",
    ])
    s += h2("Step 8 — Stop / reset")
    s.append(code("""docker compose stop              # stop (graceful), keep data
docker compose down              # remove containers, keep volumes (settings, cache, counters)
docker compose down -v           # ALSO delete Redis + Postgres data (next start imports .env)"""))
    s += h2("Option — run the Go server on your PC (development)")
    s.append(code("""docker compose -f docker-compose.yml -f compose.dev.yml up -d searxng redis postgres
# in .env:  SEARXNG_URL=http://127.0.0.1:8888
#           REDIS_URL=redis://:<REDIS_PASSWORD>@127.0.0.1:6379/0
#           DATABASE_URL=postgres://ofd:<POSTGRES_PASSWORD>@127.0.0.1:5432/ofd?sslmode=disable
set -a; . ./.env; set +a
go run ./cmd/owis_find_deal_engine

# admin web app with hot reload (another terminal)
cd web/admin && npm ci && npm run dev      # http://localhost:5173/admin/ (proxies to :3002)"""))
    return s


def sec_test():
    s = h1("15. How to test")
    s += h2("15.1 Automated tests (same as CI)")
    s.append(code("""cd Owis_Find_Deal_Engine/server
gofmt -l .                        # must print nothing
go vet ./...
go test -race ./...               # all packages (store tests skip without a database)

# store tests against a throwaway Postgres (its tables are dropped!)
docker run -d --name ofd-test-db -e POSTGRES_USER=ofd -e POSTGRES_PASSWORD=ofd \\
  -e POSTGRES_DB=ofd_test -p 127.0.0.1:55432:5432 postgres:17-alpine
TEST_DATABASE_URL=postgres://ofd:ofd@127.0.0.1:55432/ofd_test?sslmode=disable \\
  go test -race -count=1 ./internal/store
docker rm -f ofd-test-db

go run golang.org/x/vuln/cmd/govulncheck@latest ./...     # known vulnerabilities

cd web/admin && npm ci && npm run build && npm audit --omit=dev   # admin app"""))
    s.append(P("Result on this branch (16 Oct 2026): <b>gofmt clean, go vet clean, all 17 test packages pass</b>. "
               "GitHub Actions (<font name='Mono'>.github/workflows/server.yml</font>) runs the same on every push: "
               "format, mod tidy, vet, race tests with Postgres 17, govulncheck, admin app build + audit, compose "
               "validation, image build and smoke test."))
    s += h2("15.2 What the tests cover")
    s.append(T([
        ["Area", "Tests"],
        ["auth", "JWT signature / issuer / audience / expiry, alg none and HMAC refused, JWKS refresh and key rotation"],
        ["api", "every route and error code, JWT vs API key, scopes, IP, country, quotas, admin access rules, "
         "every admin endpoint"],
        ["cache", "fresh / stale / miss, partial and empty TTL, refresh limit, singleflight, stale-if-error, keys"],
        ["search", "worker pool, merge, dedupe, relevance, fallback chain, circuit breaker, probe"],
        ["providers", "SerpApi (Shopping, Amazon, eBay), Apify (AliExpress, Temu, SHEIN sample outputs), SearXNG"],
        ["metering / usage / ratelimit", "free cached answers, take / refund, 402 vs 429, token bucket"],
        ["settings / store / stats", "validation, encryption, import, watcher; Postgres store; counters, report, budget"],
        ["apikeys", "format, hashing, cache, expiry, rotation grace, scope / IP / country rules"],
    ], [110, W - 110], first_col_bold=True))
    s += h2("15.3 Testing the Docker stack")
    s.append(code("""docker compose config -q                       # compose file valid (needs .env)
docker compose build --no-cache server         # clean image build
docker images owis_find_deal_engine            # size ~19 MB
docker compose up -d && docker compose ps      # 4 × healthy
docker inspect --format '{{.State.Health.Status}}' owis_find_deal_engine-server-1

# smoke test of the image alone (what CI does):
docker run --rm --entrypoint /owis_find_deal_engine owis_find_deal_engine:dev healthcheck
                                               # ↑ must fail: no server is running
docker run --rm owis_find_deal_engine:dev      # must refuse to start without configuration

# security checks
docker compose exec server id                  # fails as expected: distroless has no tools
docker inspect owis_find_deal_engine-server-1 \\
  --format '{{.HostConfig.ReadonlyRootfs}} {{.HostConfig.CapDrop}}'      # true [ALL]
docker network inspect owis_find_deal_engine_data --format '{{.Internal}}'      # true

# logs, resources, inside services
docker compose logs --tail=100 server
docker stats --no-stream
docker compose exec redis redis-cli --scan --pattern 'search:*' | head    # cached answers
docker compose exec postgres psql -U ofd -d ofd -c \\
  'select day,kind,k1,k2,count,cost_usd from usage_daily order by day desc limit 20;'
docker compose exec server /owis_find_deal_engine settings list"""))
    s += h2("15.4 Manual test checklist")
    s += numbered([
        "<b>Health</b>: <font name='Mono'>/api/v1/health</font> 200; 4 containers healthy.",
        "<b>Login</b>: no token → 401; expired token → 401; valid token → 200.",
        "<b>Search</b> in usa, ksa, jor: shops correct (no eBay in jor), links open product pages, prices shown.",
        "<b>Cache</b>: same search again → <font name='Mono'>X-Cache: HIT</font>, remaining quota unchanged; "
        "<font name='Mono'>\"refresh\":true</font> → MISS (second time within 10 min → HIT).",
        "<b>Quota</b>: set <font name='Mono'>PLANS=free:2</font> in Settings, 3 different searches → third gives 402.",
        "<b>API key</b>: create, search (200, X-Plan api), wrong country 403, rotate with 1 h grace (both work), "
        "stop old (401 expired), revoke (401 revoked), audit log shows clients.* entries without keys.",
        "<b>Settings hot reload</b>: change <font name='Mono'>APIFY_SHEIN_MAX_ITEMS</font> → log \"settings "
        "applied\" within 10 s, no restart.",
        "<b>Cost</b>: after live searches, Dashboard cost ≈ Apify console (Billing → usage).",
        "<b>Test search</b> (admin): each source answers with time and cost; breakers closed.",
    ])
    return s


def sec_git_trouble():
    s = h1("16. Git workflow")
    s.append(code(f"""git status                                   # what changed
git switch {BRANCH}             # the project branch
git pull origin {BRANCH}        # update
git switch -c my-change                      # own branch for a change
git add -A && git commit -m "Describe the change"
git push -u origin my-change                 # then open a pull request into the project branch
git log --oneline --graph -15                # history
git diff origin/{BRANCH}         # compare with the project branch
git stash / git stash pop                    # park local edits

# never commit .env (it is in .gitignore). Check:  git check-ignore -v server/.env"""))
    s.append(P("Branches: work happens on <font name='Mono'>" + BRANCH + "</font> (steps 7–11 merged there through "
               "pull requests #2–#6). <font name='Mono'>main</font> does not have the service yet — merging it into "
               "<font name='Mono'>main</font> is part of the next steps (section 20)."))
    s += h1("17. Troubleshooting")
    s.append(T([
        ["Problem", "Cause / fix"],
        ["compose: \"set REDIS_PASSWORD in .env\"", "a required secret is empty in .env — fill all four hex values"],
        ["container unhealthy", "<font name='Mono'>docker compose logs server postgres redis searxng</font>; "
         "often a wrong AUTH_* or DATABASE_URL"],
        ["401 on every search", "token expired (1 h) or AUTH_ISSUER / AUTH_AUDIENCE not the Firebase project id"],
        ["Git Bash: \"no such file\" with exec", "<font name='Mono'>export MSYS_NO_PATHCONV=1</font> or use "
         "<font name='Mono'>//owis_find_deal_engine</font>"],
        [".env change has no effect", "runtime settings are in Postgres after the first start — use Settings / "
         "CLI, or <font name='Mono'>down -v</font>"],
        ["admin panel: \"not an admin\"", "email not in the admin list → <font name='Mono'>admins add EMAIL</font>; "
         "ADMIN_FIREBASE_API_KEY missing → no sign-in form"],
        ["a shop always error / slow", "Admin → Test search; check APIFY_TIMEOUT (90 s for AliExpress), Apify "
         "credit, breaker state"],
        ["Apify \"maximum cost of $0.00\"", "set APIFY_MAX_CHARGE_USD (default 0.20) for per-event scrapers (SHEIN)"],
        ["costs all 0 on the dashboard", "cost settings not entered (section 8.2)"],
        ["503 service_unavailable", "Redis down or wrong password — quota counters fail closed"],
        ["lost SETTINGS_ENCRYPTION_KEY", "stored secrets unreadable → set SERPAPI_KEY / APIFY_TOKEN again"],
    ], [150, W - 150]))
    return s


def sec_next():
    s = h1("18. Known limits (accepted for now)")
    s += bullets([
        "<b>Scraper prices are US prices</b> (AliExpress, Temu, SHEIN read the US sites); they can differ from what "
        "a user in Jordan / KSA sees. The app should show \"from $X · approx.\" — the link shows the real price.",
        "<b>AliExpress welcome deals</b>: some prices are new-shopper offers → app note.",
        "<b>SHEIN</b> links are <font name='Mono'>us.shein.com</font> in every country (scraper supports US only).",
        "<b>First live search takes 25–50 s</b> (scrapers start a browser) → loading bar in the app; async search "
        "is step 12.",
        "<b>Budget alert does not block</b> searches; costs are estimates.",
        "<b>2FA and email verification for admins are off</b> (development only).",
        "<b>One host, no HTTPS, no backups</b> yet — local development stack.",
        "Behind a reverse proxy the API-key IP allow-list would see the proxy address until the proxy header is "
        "trusted (step 14) — leave the IP list empty until then.",
    ])
    s += h1("19. What is remaining")
    s.append(T([
        ["Step", "What", "Status"],
        ["12", "Integration endpoints: async search <font name='Mono'>POST /api/v1/searches</font> → 202 + id, "
         "<font name='Mono'>GET /api/v1/searches/{id}</font>, optional signed (HMAC) webhook; per-client usage "
         "and cost on the dashboard", "<b>next</b>"],
        ["13", "AI agents: MCP server <font name='Mono'>/mcp</font> (tools search_products, list_countries) with "
         "API keys; OpenAPI 3.1 spec + developer docs page", "to do"],
        ["14", "Deployment: 2FA + email verification on; hosting (D6), domain (D7), HTTPS, secrets store, Postgres "
         "backups, staging + production, CI/CD from main, monitoring + alerts, gobreaker", "to do"],
        ["15–19", "Users: users table + /me, plans in DB + payment webhook, saved cart + price tracker, clicks + "
         "purchase reports, notifications (inbox, FCM push, background jobs)", "to do"],
        ["later", "eBay Browse API / AliExpress affiliate API if keys are approved; cheaper or Saudi SHEIN scraper; "
         "search by photo (Google Lens)", "ideas"],
    ], [40, W - 110, 70]))
    s.append(Spacer(1, 6))
    s.append(P("Open owner decisions: <b>D6</b> hosting, <b>D7</b> domain (step 14), <b>D8</b> paid Apify / SerpApi "
               "plans (before real users), payment provider (phase 5)."))
    s += h1("20. Recommended next")
    s.append(P("In priority order — the first items are cheap and remove risk before more features are added:"))
    s += numbered([
        "<b>Merge into main.</b> Open a pull request <font name='Mono'>" + BRANCH + " → main</font> so the default "
        "branch holds the real service (today it does not); protect main and require the CI checks.",
        "<b>Owner test of step 11</b> on the Docker stack (section 14, step 7) and enter the cost settings, so the "
        "dashboard shows real estimates.",
        "<b>Step 12 — async search + webhook</b>: removes the 25–50 s wait for agents and servers; add per-client "
        "usage to the dashboard (base for billing).",
        "<b>Optional hard budget stop</b>: a setting such as <font name='Mono'>BUDGET_HARD_STOP=true</font> that "
        "serves cached answers only once the month's budget is exceeded — protects the $5 free credit.",
        "<b>Pre-warm popular queries</b> before they expire (top queries per country are already counted) — more "
        "cache hits at almost no cost.",
        "<b>Step 13 — MCP server + OpenAPI</b> so AI agents (Claude, ChatGPT…) can use the service as a tool.",
        "<b>Before any public deployment (step 14)</b>: turn on admin 2FA and email verification, HTTPS via Caddy "
        "or Cloudflare Tunnel, Postgres backups (daily dump + restore test), secrets out of .env files, pin the "
        "SearXNG image version (today <font name='Mono'>latest</font>), metrics + alerts.",
        "<b>Choose hosting (D6)</b>: a ~$5/month VPS (Hetzner / DigitalOcean) runs the whole compose stack as is.",
        "<b>Then the app and users (phase 5)</b>, with the UI notes: approximate prices, AliExpress welcome-deal "
        "note, loading bar, \"prices updated X ago\".",
    ])
    s.append(Spacer(1, 10))
    s.append(callout("<b>To resume in a new Claude Code session:</b> \"Continue the Owis_Find_Deal_Engine project from "
                     "<font name='Mono'>Owis_Find_Deal_Engine/STATUS.md</font> on branch <font name='Mono'>"
                     + BRANCH + "</font>. Start phase 3, step 12.\"", fill=GREEN, edge=GREEN_D))
    s.append(Spacer(1, 8))
    s.append(P("Other documents: <font name='Mono'>STATUS.md</font> (handoff + owner test steps), "
               "<font name='Mono'>PLAN.md</font> (design + all steps), <font name='Mono'>server/README.md</font> "
               "(reference), <font name='Mono'>docs/current_flow.pdf</font>, <font name='Mono'>next_plan.pdf</font>, "
               "<font name='Mono'>architecture.pdf</font> (older snapshots). Regenerate this guide with "
               "<font name='Mono'>python3 docs/build_service_guide_pdf.py</font>.", "small"))
    return s


def build():
    story = []
    for part in (cover, sec_about, sec_architecture, sec_docker, sec_flow, sec_sources, sec_cache,
                 sec_auth_and_clients, sec_cost, sec_settings_admin, sec_code, sec_use, sec_run, sec_test,
                 sec_git_trouble, sec_next):
        story += part()
    doc = Doc(OUT, pagesize=A4, leftMargin=MARGIN, rightMargin=MARGIN, topMargin=18 * mm,
              bottomMargin=16 * mm, title=TITLE, author="owis_find_deal_engine")
    doc.multiBuild(story, onFirstPage=on_page, onLaterPages=on_page)
    print("written", OUT)


if __name__ == "__main__":
    build()
