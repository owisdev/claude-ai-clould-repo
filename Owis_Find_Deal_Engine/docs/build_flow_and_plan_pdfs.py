"""Builds two PDFs for the owner (October 2026):

    docs/current_flow.pdf   how the service works today, with the settings
                            and sources chosen in the local tests
    docs/next_plan.pdf      the new plan (admin panel -> API integration ->
                            deployment -> users) and the owner's decisions

    pip install reportlab
    python3 docs/build_flow_and_plan_pdfs.py

Styles and drawing helpers come from build_architecture_pdf.py.
"""

import os

from build_architecture_pdf import (
    BLUE, BLUE_D, CONTENT_W, GREEN, GREEN_D, GREY, GREY_D, INK, MARGIN, MUTED, NAVY,
    ORANGE, ORANGE_D, PAGE_H, PAGE_W, PURPLE, PURPLE_D, RED, RED_D, A4, P, S,
    arrow, box, bullets, callout, caption, code, diagram_flow, diagram_login, numbered,
    table, text,
)
from reportlab.graphics.shapes import Drawing, Line, Rect
from reportlab.lib import colors
from reportlab.lib.units import mm
from reportlab.platypus import CondPageBreak, PageBreak, SimpleDocTemplate, Spacer

HERE = os.path.dirname(os.path.abspath(__file__))
DATE = "7 October 2026"


def page_deco(title):
    def on_page(canvas, doc):
        canvas.saveState()
        if doc.page > 1:
            canvas.setFont("Sans", 7.5)
            canvas.setFillColor(MUTED)
            canvas.drawString(MARGIN, PAGE_H - 11 * mm, title)
            canvas.drawRightString(PAGE_W - MARGIN, PAGE_H - 11 * mm, DATE)
            canvas.setStrokeColor(colors.HexColor("#D5DAE1"))
            canvas.setLineWidth(0.5)
            canvas.line(MARGIN, PAGE_H - 12.5 * mm, PAGE_W - MARGIN, PAGE_H - 12.5 * mm)
        canvas.setFont("Sans", 7.5)
        canvas.setFillColor(MUTED)
        canvas.drawCentredString(PAGE_W / 2, 10 * mm, f"{doc.page}")
        canvas.restoreState()
    return on_page


def write(name, story, title):
    out = os.path.join(HERE, name)
    doc = SimpleDocTemplate(out, pagesize=A4, leftMargin=MARGIN, rightMargin=MARGIN,
                            topMargin=18 * mm, bottomMargin=16 * mm, title=title,
                            author="owis_find_deal_engine")
    deco = page_deco(title)
    doc.build(story, onFirstPage=deco, onLaterPages=deco)
    print("written", out)


# ================================================================ diagrams
def diagram_sources():
    """Which source answers each shop, and what happens when it fails."""
    W, H = CONTENT_W, 300
    d = Drawing(W, H)
    box(d, 0, 125, 92, 50, "Search service\none search =\nall shops of the\ncountry, in parallel",
        fill=colors.white, stroke=NAVY, size=7.4)
    rows = [
        ("Amazon", "SerpApi Amazon engine", "1 SerpApi search · ~3 s", "usa ksa jor", BLUE, BLUE_D),
        ("eBay", "SerpApi eBay engine", "1 SerpApi search · ~3 s", "usa ksa", BLUE, BLUE_D),
        ("AliExpress", "Apify piotrv1001/\naliexpress-listings-scraper", "≈ $0.02 · 40–90 s", "usa ksa jor", GREEN, GREEN_D),
        ("Temu", "Apify crw/\ntemu-products-scraper", "$0.10 (10 results) · 12–21 s", "usa ksa jor", GREEN, GREEN_D),
        ("SHEIN", "Apify clearpath/\nshein-product-scraper", "≈ $0.033 (5 results) · ~8 s", "usa ksa jor", GREEN, GREEN_D),
    ]
    rh, gap = 46, 9
    top = H - 6
    for i, (shop, src, cost, countries, fill, edge) in enumerate(rows):
        y = top - (i + 1) * rh - i * gap + gap
        box(d, 112, y, 66, rh - 8, shop, fill=PURPLE, stroke=PURPLE_D, size=8)
        box(d, 196, y, 128, rh - 8, src, fill=fill, stroke=edge, size=7.2)
        text(d, 332, y + rh / 2 + 1, cost, 7.2, INK)
        text(d, 332, y + rh / 2 - 9, "countries: " + countries, 6.8, MUTED)
        arrow(d, 92, 150, 112, y + (rh - 8) / 2, width=0.8)
        arrow(d, 178, y + (rh - 8) / 2, 196, y + (rh - 8) / 2, width=0.8)
    box(d, 196, 2, 128, 30, "Fallback if a source fails\nGoogle Shopping → SearXNG",
        fill=ORANGE, stroke=ORANGE_D, size=7.2, dash=[3, 2])
    text(d, 332, 20, "error or timeout only — costs 1 extra", 7, MUTED)
    text(d, 332, 10, "SerpApi search; gives search links", 7, MUTED)
    return d


def diagram_cache_48():
    W, H = CONTENT_W, 92
    d = Drawing(W, H)
    x0, x1 = 10, W - 10
    span = 72.0
    px = lambda h: x0 + (x1 - x0) * h / span  # noqa: E731
    y = 46
    d.add(Rect(px(0), y, px(48) - px(0), 22, fillColor=GREEN, strokeColor=GREEN_D, strokeWidth=0.8))
    d.add(Rect(px(48), y, px(72) - px(48), 22, fillColor=ORANGE, strokeColor=ORANGE_D, strokeWidth=0.8))
    text(d, (px(0) + px(48)) / 2, y + 8, "FRESH 0–48 h: answer from cache, free, instant (X-Cache: HIT)",
         7.6, INK, "middle")
    text(d, (px(48) + px(72)) / 2, y + 13, "STALE 48–72 h: instant +", 7.2, INK, "middle")
    text(d, (px(48) + px(72)) / 2, y + 4, "new prices fetched in background", 7.2, INK, "middle")
    for h in (0, 24, 48, 72):
        d.add(Line(px(h), y - 4, px(h), y, strokeColor=GREY_D))
        text(d, px(h), y - 13, f"{h} h", 7, MUTED, "middle")
    text(d, x1, y + 30, "after 72 h: live search again (X-Cache: MISS)", 7.2, MUTED, "end")
    text(d, x0, 8, "A shop failed → that answer is fresh only 10 min · no results → 30 min · "
         "pull-to-refresh at most once per 10 min per search.", 7, MUTED)
    return d


def diagram_phases():
    W, H = CONTENT_W, 118
    d = Drawing(W, H)
    phases = [
        ("Phase 1", "Search service", "✓ done", GREEN, GREEN_D),
        ("Phase 2", "Admin panel", "next · steps 7–10", BLUE, BLUE_D),
        ("Phase 3", "API integration", "steps 11–13", BLUE, BLUE_D),
        ("Phase 4", "Deployment", "step 14", BLUE, BLUE_D),
        ("Phase 5", "Users", "steps 15–19", GREY, GREY_D),
    ]
    bw, gap = 86, 11
    for i, (ph, name, note, fill, edge) in enumerate(phases):
        x = i * (bw + gap)
        box(d, x, 40, bw, 62, f"{ph}\n{name}\n\n{note}", fill=fill, stroke=edge, size=7.8)
        if i < len(phases) - 1:
            arrow(d, x + bw, 71, x + bw + gap, 71, width=1)
    text(d, 0, 18, "Owner's new order (7 Oct 2026): admin panel first, then other applications / AI agents, "
         "then deployment; user features after.", 7.4, MUTED)
    return d


def diagram_target():
    """Target architecture after phases 2–4."""
    W, H = CONTENT_W, 250
    d = Drawing(W, H)
    d.add(Rect(150, 8, 220, 236, rx=8, ry=8, fillColor=None, strokeColor=GREY_D,
               strokeWidth=1, strokeDashArray=[4, 3]))
    text(d, 158, 233, "Server (one Docker deployment)", 7.5, MUTED, bold=True)
    box(d, 0, 186, 120, 40, "Mobile / web app\nusers · Firebase JWT", fill=PURPLE, stroke=PURPLE_D, size=7.4)
    box(d, 0, 132, 120, 40, "Other applications\nAPI key (step 11)", fill=PURPLE, stroke=PURPLE_D, size=7.4)
    box(d, 0, 78, 120, 40, "AI agents\nMCP + API key (step 13)", fill=PURPLE, stroke=PURPLE_D, size=7.4)
    box(d, 0, 20, 120, 44, "Admin (you)\nbrowser · Firebase\n+ admin role + 2FA", fill=ORANGE, stroke=ORANGE_D, size=7.2)
    box(d, 162, 150, 196, 70, "API server\n/api/v1 · /mcp · /admin/v1\n\nlogin / key check · quotas · cache\n"
        "search · settings hot-reload · audit", fill=colors.white, stroke=NAVY, size=7.2)
    box(d, 162, 96, 92, 40, "Admin web app\n/admin (step 10)", fill=BLUE, stroke=BLUE_D, size=7.2)
    box(d, 266, 96, 92, 40, "SearXNG\nfree fallback", fill=BLUE, stroke=BLUE_D, size=7.2)
    box(d, 162, 22, 92, 60, "Postgres (step 7)\nsettings · history\nadmins · API keys\nusage", fill=GREEN, stroke=GREEN_D, size=7)
    box(d, 266, 22, 92, 60, "Redis\ncache · counters\nrate limits\nasync search jobs", fill=GREEN, stroke=GREEN_D, size=7)
    box(d, 392, 160, 101, 50, "SerpApi\nAmazon · eBay\nGoogle Shopping", fill=GREY, stroke=GREY_D, size=7.2)
    box(d, 392, 96, 101, 50, "Apify scrapers\nAliExpress · Temu\nSHEIN", fill=GREY, stroke=GREY_D, size=7.2)
    box(d, 392, 32, 101, 50, "Firebase\nlogin keys (JWKS)", fill=ORANGE, stroke=ORANGE_D, size=7.2)
    for y in (206, 152, 98):
        arrow(d, 120, y, 162, 185, width=0.8)
    arrow(d, 120, 42, 162, 116, width=0.8)
    arrow(d, 358, 185, 392, 185, width=0.8)
    arrow(d, 358, 170, 392, 121, width=0.8)
    arrow(d, 358, 160, 392, 57, width=0.8, dash=[3, 2])
    return d


# ================================================================ PDF 1
def build_flow():
    st = [Spacer(1, 30 * mm),
          P("How the service works today", "title"),
          P("owis_find_deal_engine — search service after the local tests of 6–7 October 2026: "
            "sources per shop, settings, costs and limits.", "subtitle"),
          callout("<b>In one sentence:</b> an app sends a product title and a country; the server checks "
                  "the login, answers from its cache when it can (free), otherwise asks each shop of that "
                  "country in parallel — Amazon and eBay through SerpApi, AliExpress, Temu and SHEIN "
                  "through Apify scrapers — and returns one list with prices and direct product links.",
                  fill=BLUE, edge=BLUE_D),
          Spacer(1, 8),
          P("Contents", "h2")]
    st += numbered(["Countries and shops", "Where each shop's results come from",
                    "The request flow (login, limits, cache, live search)",
                    "Cache: 48 hours fresh", "Costs and limits", "The answer format",
                    "Current configuration (.env)", "Known limits (accepted)",
                    "Test results (6–7 Oct)"])
    st.append(PageBreak())

    # 1 countries
    st += [P("1. Countries and shops", "h1"),
           P("Only the shops that deliver to the country are searched (file "
             "<font name='Mono'>internal/markets/markets.json</font>)."),
           table([["Country", "Code", "Amazon", "eBay", "AliExpress", "Temu", "SHEIN"],
                  ["United States", "usa", "amazon.com", "ebay.com", "✓", "✓", "us.shein.com"],
                  ["Saudi Arabia", "ksa", "amazon.sa", "ebay.com", "✓", "✓", "ar.shein.com *"],
                  ["Jordan", "jor", "amazon.com", "—", "✓", "✓", "ar.shein.com *"]],
                 [30 * mm, 14 * mm, 25 * mm, 22 * mm, 24 * mm, 18 * mm, CONTENT_W - 133 * mm]),
           P("* The SHEIN scraper reads the US site only, so links and prices are from us.shein.com "
             "in every country (accepted).", "small"),
           Spacer(1, 8)]

    # 2 sources
    st += [P("2. Where each shop's results come from", "h1"),
           diagram_sources(),
           caption("Each shop has its own source; all shops of a country are searched at the same time."),
           table([["Shop", "Source", "What we get", "Link format"],
                  ["Amazon", "SerpApi Amazon engine", "price, rating, delivery, sponsored mark",
                   "amazon.&lt;tld&gt;/dp/&lt;ASIN&gt;"],
                  ["eBay", "SerpApi eBay engine (new 7 Oct)", "price (lowest of a range), condition, shipping, rating",
                   "ebay.com/itm/&lt;id&gt;"],
                  ["AliExpress", "Apify piotrv1001/aliexpress-listings-scraper",
                   "price (US, no currency → set to USD), image", "aliexpress.com/item/&lt;id&gt;.html"],
                  ["Temu", "Apify crw/temu-products-scraper", "price, image",
                   "temu.com/goods.html?goods_id=&lt;id&gt;"],
                  ["SHEIN", "Apify clearpath/shein-product-scraper", "sale price (not the old price), image",
                   "us.shein.com/…-p-&lt;id&gt;.html"]],
                 [20 * mm, 50 * mm, 55 * mm, CONTENT_W - 125 * mm], first_col_bold=True),
           Spacer(1, 6),
           P("Rules applied to every result", "h2")]
    st += bullets([
        "<b>Relevant only:</b> at least two thirds of the search words must appear in the title "
        "(shops also return merely related items).",
        "<b>Direct product pages only;</b> links are cleaned (tracking parameters and country prefixes removed).",
        "<b>No duplicates</b> within a shop (the cheapest copy is kept).",
        "Results whose title is only the shop's name (e.g. \"Temu\") are dropped.",
        "<b>Fallback</b> only on error or timeout: Google Shopping (SerpApi) → SearXNG (free). "
        "A scraper that finds nothing relevant means \"no results\" for that shop "
        "(<font name='Mono'>APIFY_EMPTY_FALLBACK=false</font>) — no extra time or cost.",
        "A source that keeps failing is skipped for a minute (circuit breaker).",
    ])

    # 3 flow
    st += [CondPageBreak(90 * mm), P("3. The request flow", "h1"),
           P("Login: the app signs the user in with Firebase and sends the token with every search; "
             "the server checks it locally with Firebase's public keys."),
           diagram_login(), caption("Login check — no call to Firebase per request."),
           diagram_flow(),
           caption("POST /api/v1/search — every check happens before any paid search is made.")]
    st += bullets([
        "<b>Burst limit:</b> 1 request per second per user, bursts of 5 (<font name='Mono'>RATE_LIMIT_RPS</font>, "
        "<font name='Mono'>RATE_LIMIT_BURST</font>).",
        "<b>Daily allowance:</b> free plan 20 live searches, pro 500 (<font name='Mono'>PLANS=free:20,pro:500</font>). "
        "Cached answers are not counted. Failed searches are refunded.",
        "<b>Time:</b> a live search waits for the slowest shop: usually 25–50 s (AliExpress is the slowest). "
        "Each scraper gets up to 90 s; the whole search up to 100 s.",
    ])

    # 4 cache
    st += [CondPageBreak(70 * mm), P("4. Cache: 48 hours fresh", "h1"),
           P("Each answer is stored per country + title (case and spaces ignored), in Redis."),
           diagram_cache_48(),
           caption("New defaults since 7 Oct: CACHE_FRESH_TTL=48h, CACHE_STALE_TTL=72h."),
           callout("Effect: a search is paid for at most once every 48 hours, whoever searches it. "
                   "Prices can be up to 2 days old — the app shows them as \"from $X · approx.\" and "
                   "the link opens the real price.", fill=GREEN, edge=GREEN_D)]

    # 5 costs
    st += [CondPageBreak(90 * mm), P("5. Costs and limits", "h1"),
           table([["Item", "Cost per live search", "Notes"],
                  ["Temu (Apify)", "$0.100", "charges for 10 results even if fewer are kept → keep 10"],
                  ["SHEIN (Apify)", "≈ $0.033", "5 results × ≈ $0.0066"],
                  ["AliExpress (Apify)", "≈ $0.02", "estimate from the monthly total"],
                  ["<b>Apify total</b>", "<b>≈ $0.155</b>", "free plan: $5 credit / month"],
                  ["SerpApi", "jor: 1 search · usa / ksa: 2", "Amazon (+ eBay); free plan 250 searches / month"],
                  ["Fallback (rare)", "+1 SerpApi search", "only when a source fails or times out"],
                  ["Cached answer", "$0", "for 48 h"]],
                 [38 * mm, 42 * mm, CONTENT_W - 80 * mm], first_col_bold=True),
           Spacer(1, 8),
           P("Safety limits on spending", "h2")]
    st += bullets([
        "<font name='Mono'>APIFY_MAX_CHARGE_USD=0.20</font> — the most one scraper run may cost "
        "(needed by the SHEIN scraper, which is billed per event).",
        "<font name='Mono'>APIFY_&lt;SHOP&gt;_MAX_ITEMS</font> — results per shop (SHEIN 5; Temu 10).",
        "Daily allowance per user (section 3) and the cache (section 4).",
        "Console test runs in Apify have <b>no</b> limit unless set — keep the result count small there.",
    ])
    st += [P("Apify platform prices (for comparing scrapers later)", "h2"),
           P("Scrapers with their own price per result (Temu, SHEIN) include everything. Only scrapers billed "
             "\"per usage\" add platform costs: $0.20 per compute unit (1 GB × 1 hour; a 60 s run with 1 GB "
             "≈ $0.003), residential proxy $8 / GB (avoid), datacenter proxy included. Storage and data "
             "transfer are negligible for us.")]

    # 6 answer
    st += [CondPageBreak(80 * mm), P("6. The answer format", "h1"),
           code('''{
  "query": "usb c hub", "country": "ksa",
  "results": [
    {"market": "ebay", "title": "USB C Hub 4K HDMI Adapter ...",
     "link": "https://www.ebay.com/itm/266881403243",
     "thumbnail": "https://i.ebayimg.com/...", "price": 13.99, "currency": "$",
     "position": 1, "provider": "serpapi-ebay",
     "extensions": ["Brand New", "Free delivery in 2-4 days"]},
    ...
  ],
  "markets": {"amazon": "ok", "ebay": "ok", "aliexpress": "ok", "temu": "ok", "shein": "ok"},
  "took_ms": 48742, "fetched_at": "2026-10-07T11:29:13Z", "cached": false, "stale": false
}''')]
    st += bullets([
        "Results are interleaved by position: the first result of every shop, then the second, …",
        "<font name='Mono'>provider</font>: serpapi-amazon, serpapi-ebay, apify, serpapi (Google Shopping "
        "fallback), searxng (free fallback). Fallback results may have <font name='Mono'>\"link_type\": \"search\"</font>.",
        "<font name='Mono'>markets</font>: per shop ok / no_results / failed.",
        "Headers: <font name='Mono'>X-Cache</font> (HIT / STALE / MISS), <font name='Mono'>X-Plan</font>, "
        "<font name='Mono'>X-RateLimit-Limit / -Remaining / -Reset</font>.",
    ])

    # 7 config
    st += [CondPageBreak(150 * mm), P("7. Current configuration (.env)", "h1"),
           P("Values used in the local tests, besides login, secrets and keys (never in the repository)."),
           code('''SEARCH_PROVIDERS=serpapi,searxng        # fallback chain
SERPAPI_ENGINE=google_shopping          # fallback engine
# SERPAPI_MARKETS defaults to amazon,ebay when SERPAPI_KEY is set

APIFY_MARKETS=aliexpress,temu,shein
APIFY_ALIEXPRESS_ACTOR=piotrv1001/aliexpress-listings-scraper
APIFY_ALIEXPRESS_INPUT={"maxResults":{{max}},"searchQueries":["{{query}}"],
                        "proxyConfiguration":{"useApifyProxy":true}}
APIFY_ALIEXPRESS_CURRENCY=USD           # scraper returns "currency": null
APIFY_TEMU_ACTOR=crw/temu-products-scraper
APIFY_TEMU_INPUT={"keyword":"{{query}}","max_items":10,"region":"US",
                  "sort":"relevance"}   # scraper needs >= 10
APIFY_TEMU_MAX_ITEMS=10
APIFY_SHEIN_ACTOR=clearpath/shein-product-scraper
APIFY_SHEIN_INPUT={"enrichDetails":false,"maxItemsPerSearch":{{max}},
                   "quickShip":false,"searchTerms":["{{query}}"],"site":"us",
                   "sortBy":"recommend","category":""}
APIFY_SHEIN_MAX_ITEMS=5
APIFY_TIMEOUT=90s
APIFY_MAX_CHARGE_USD=0.20
APIFY_EMPTY_FALLBACK=false

CACHE_FRESH_TTL=48h
CACHE_STALE_TTL=72h
PLANS=free:20,pro:500'''),
           P("In phase 2 these settings move into the admin panel (applied without restart); "
             "<font name='Mono'>.env</font> keeps only start-up values and secrets.", "small")]

    # 8 limits
    st += [Spacer(1, 6), P("8. Known limits (accepted)", "h1"),
           table([["Limit", "Why", "In the app"],
                  ["Prices are US prices", "the scrapers read the US sites; the page in Jordan / Saudi may show "
                   "another price, currency or discount", "show \"from $X · approx.\"; the link shows the real price"],
                  ["AliExpress welcome deals", "many prices ($0.33, $1.09) are new-shopper offers",
                   "note: \"AliExpress prices may be new-shopper offers\""],
                  ["SHEIN on the US site", "the scraper supports only us", "check another SHEIN scraper later"],
                  ["First live search is slow", "25–50 s (scrapers start a browser)",
                   "loading bar; repeat searches are instant"],
                  ["Up to 48 h old prices", "cache keeps costs low", "pull-to-refresh for a new price"]],
                 [38 * mm, 64 * mm, CONTENT_W - 102 * mm], first_col_bold=True)]

    # 9 tests
    st += [CondPageBreak(60 * mm), P("9. Test results (6–7 Oct 2026)", "h1"),
           table([["Search", "Country", "Result", "Time", "Cost"],
                  ["usb c ssd enclosure", "usa", "5 shops ok; SHEIN 8 via Apify; AliExpress fell back (60 s timeout)",
                   "67 s", "3 SerpApi + Apify"],
                  ["iphone 18 max case", "jor", "4 shops ok, no fallback (timeout 90 s)", "36 s", "1 SerpApi + Apify"],
                  ["usb c hub", "ksa", "eBay 10 direct links ✓; Temu failed (max_items 5) → fixed", "49 s",
                   "3 SerpApi + Apify"],
                  ["fast charger type c", "jor", "4 shops ok, no fallback", "26 s", "1 SerpApi + ≈ $0.16"]],
                 [34 * mm, 15 * mm, 72 * mm, 13 * mm, CONTENT_W - 134 * mm]),
           Spacer(1, 6),
           P("Links opened by the owner: Amazon, eBay, SHEIN and Temu show the same price as returned "
             "(Temu / AliExpress pages in Jordan may show local offers).", "small")]

    write("current_flow.pdf", st, "owis_find_deal_engine — how the service works today")


# ================================================================ PDF 2
def build_plan():
    st = [Spacer(1, 6 * mm),
          P("Next plan and your decisions", "title"),
          P("owis_find_deal_engine — admin panel, API integration for other applications and AI agents, "
            "deployment. What will be built, in which order, and what is needed from you.", "subtitle"),
          diagram_phases(), Spacer(1, 6),
          callout("<b>Needed from you before step 7 starts:</b> answers to decisions D1–D4 (page 4). "
                  "D5–D8 are needed later, at the step named next to them. Each decision has a "
                  "recommendation — answering \"recommended\" is enough.",
                  fill=ORANGE, edge=ORANGE_D),
          Spacer(1, 10)]

    st += [P("Where we are going", "h2"), diagram_target(),
           caption("After phases 2–4: three kinds of callers, one server, settings in a database.")]

    # phase 2
    st += [PageBreak(), P("Phase 2 — Admin panel", "h1"),
           P("Goal: control every setting of the service from a browser, safely, without editing files or "
             "restarting the server.")]
    st += [table([["Step", "What is built", "Result for you"],
                  ["7. Database + settings store",
                   "Postgres joins the stack. Tables: settings, settings history (who, when, old → new), "
                   "admins. Runtime settings move from .env to the database; the server applies a change "
                   "by rebuilding its search sources and switching over without a restart.",
                   "change a setting → active in seconds; every change can be traced and undone"],
                  ["8. Admin API (/admin/v1)",
                   "Login with Firebase + admin role, checked on every call; audit log; rate limit. "
                   "Settings: countries and shops, source per shop (scraper, input, currency, result limit, "
                   "timeout, max cost), SerpApi engines, cache times, plans, quotas, rate limits, CORS. "
                   "Secrets write-only and encrypted. Tools: test search (each source's answer, time, cost), "
                   "clear cache, source health.",
                   "everything in today's .env controllable, safely"],
                  ["9. Usage and cost dashboard",
                   "Live searches, cache hit rate, fallbacks and errors per source, estimated cost per day "
                   "(Apify runs × price, SerpApi searches), top searches; alert at a monthly budget.",
                   "see what the service costs before the bill arrives"],
                  ["10. Admin web app",
                   "Browser app: settings forms with validation, dashboard, audit log, API clients. "
                   "Served by the same server under /admin.",
                   "one address, nothing extra to host"]],
                 [36 * mm, 88 * mm, CONTENT_W - 124 * mm], first_col_bold=True)]

    # phase 3
    st += [Spacer(1, 10), P("Phase 3 — API integration (other applications and AI agents)", "h1"),
           table([["Step", "What is built", "Result for you"],
                  ["11. API clients and keys",
                   "Created in the admin panel: name, scopes (search, countries), daily quota, rate limit, "
                   "allowed countries, optional IP list, expiry. Key shown once (ofd_live_…); only a hash is "
                   "stored. Revoke and rotate. Usage counted per client.",
                   "give a partner or agent access in a minute, take it back at once"],
                  ["12. Async search + webhooks",
                   "POST /api/v1/searches → 202 + id; GET /api/v1/searches/{id}; optional signed webhook "
                   "when done (live searches take 25–50 s, too long for many clients).",
                   "integrations do not time out"],
                  ["13. AI agents",
                   "MCP server endpoint (/mcp) with tools search_products and list_countries, same API keys; "
                   "OpenAPI 3.1 spec + docs page.",
                   "Claude and other agents can use the service directly"]],
                 [36 * mm, 88 * mm, CONTENT_W - 124 * mm], first_col_bold=True)]

    # phase 4 + 5
    st += [PageBreak(), P("Phase 4 — Deployment of everything", "h1"),
           table([["Step", "What is done"],
                  ["14. Deployment",
                   "Hosting, domain, HTTPS (Caddy or Cloudflare), private networks for databases, secrets in "
                   "the host's secret store, Postgres backups, staging + production, automatic deploy from "
                   "main, monitoring and alerts (searches, cache, provider errors, costs)."]],
                 [36 * mm, CONTENT_W - 36 * mm], first_col_bold=True),
           Spacer(1, 10),
           P("Phase 5 — Users (after deployment)", "h1"),
           table([["Step", "What is built"],
                  ["15", "Users table, profile (/me), email sign-in links"],
                  ["16", "Plans in the database + payment webhook"],
                  ["17", "Saved cart + price tracker (alerts when a saved item gets cheaper)"],
                  ["18", "Clicks + purchase reports"],
                  ["19", "Notifications: inbox, push (FCM), background jobs"]],
                 [36 * mm, CONTENT_W - 36 * mm], first_col_bold=True),
           Spacer(1, 10),
           P("Later (when possible)", "h2")]
    st += bullets(["eBay Browse API (free) and AliExpress affiliate API, if the developer keys are approved.",
                   "Another SHEIN scraper: cheaper, or supporting the Saudi / Arab sites (sa, ar, ae).",
                   "App work: loading bar, \"approx.\" price label, AliExpress welcome-deal note."])

    # decisions
    st += [PageBreak(), P("Your decisions", "h1"),
           P("Recommended options are marked ★. D1–D4 are needed <b>before step 7</b>.")]
    decisions = [
        ("D1", "Admin web app technology", "before step 7",
         "★ A: small React web app built into the server image (one deploy, modern forms)\n"
         "B: simple server-rendered pages (no JavaScript build, plainer look)"),
        ("D2", "Who can be admin, and how they log in", "before step 7",
         "★ A: your Firebase account + admin list in the database + two-factor login required\n"
         "B: Firebase account + admin list, without two-factor"),
        ("D3", "Secrets (SerpApi key, Apify token)", "before step 7",
         "★ A: changeable in the panel — encrypted, write-only, never shown again\n"
         "B: only in .env on the server (panel shows \"set / not set\")"),
        ("D4", "Who will use the API (phase 3)", "before step 7 (shapes the data model)",
         "★ A: your own apps and agents first; keys created by you in the panel\n"
         "B: also outside partners / developers → later self-service sign-up and billing"),
        ("D5", "Monthly budget alert", "step 9",
         "an amount for Apify + SerpApi per month (e.g. $5 Apify free credit + 250 SerpApi searches)"),
        ("D6", "Hosting", "step 14",
         "★ A: one small VPS (Hetzner / DigitalOcean, ~$5–8 / month) with Docker\n"
         "B: Fly.io / Render / Railway + managed Postgres (Neon / Supabase)\n"
         "C: Oracle Cloud Always Free (free, strict sign-up)"),
        ("D7", "Domain name", "step 14",
         "the domain for the API and admin panel, e.g. api.yourdomain.com / admin.yourdomain.com"),
        ("D8", "Paid plans for Apify / SerpApi", "before real users",
         "free credit covers testing only (≈ 20 live searches / month for Apify)"),
    ]
    rows = [["#", "Decision", "When", "Options"]]
    for n, q, when, opts in decisions:
        rows.append([n, q, when, opts.replace("\n", "<br/>")])
    st.append(table(rows, [10 * mm, 42 * mm, 30 * mm, CONTENT_W - 82 * mm], first_col_bold=True))

    st += [Spacer(1, 10), P("Answer sheet (copy into the next session)", "h2"),
           code('''D1 admin web app:   A / B
D2 admin login:     A / B        admin email(s): ...
D3 secrets:         A / B
D4 API users:       A / B
D5 budget alert:    Apify $... / month, SerpApi ... searches / month   (can wait)
D6 hosting:         A / B / C                                         (can wait)
D7 domain:          ...                                               (can wait)
D8 paid plans:      ...                                               (can wait)'''),
           callout("<b>Next session:</b> open the repository and say <i>\"Continue the "
                   "Owis_Find_Deal_Engine project from Owis_Find_Deal_Engine/STATUS.md on branch "
                   "claude/cloud-vs-local-7xi988. Start phase 2, step 7. My answers: D1 …, D2 …, D3 …, "
                   "D4 …\"</i>", fill=BLUE, edge=BLUE_D)]

    write("next_plan.pdf", st, "owis_find_deal_engine — next plan and decisions")


if __name__ == "__main__":
    build_flow()
    build_plan()
