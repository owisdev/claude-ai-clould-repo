"""Builds docs/architecture.pdf: the architecture and flows of
owis_find_deal_engine, written for reading before running it.

    pip install reportlab
    python3 docs/build_architecture_pdf.py
"""

import os

from reportlab.graphics.shapes import Drawing, Line, Polygon, Rect, String
from reportlab.lib import colors
from reportlab.lib.enums import TA_CENTER
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle
from reportlab.lib.units import mm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import (
    CondPageBreak, KeepTogether, PageBreak, Paragraph, Preformatted,
    SimpleDocTemplate, Spacer, Table, TableStyle,
)

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "architecture.pdf")

# ---------------------------------------------------------------- fonts
FONT_DIR = "/usr/share/fonts/truetype/dejavu"
pdfmetrics.registerFont(TTFont("Sans", f"{FONT_DIR}/DejaVuSans.ttf"))
pdfmetrics.registerFont(TTFont("Sans-Bold", f"{FONT_DIR}/DejaVuSans-Bold.ttf"))
pdfmetrics.registerFont(TTFont("Mono", f"{FONT_DIR}/DejaVuSansMono.ttf"))
pdfmetrics.registerFontFamily("Sans", normal="Sans", bold="Sans-Bold",
                              italic="Sans", boldItalic="Sans-Bold")

# ---------------------------------------------------------------- palette
NAVY = colors.HexColor("#1F3A5F")
TEAL = colors.HexColor("#2A7F8E")
INK = colors.HexColor("#222222")
MUTED = colors.HexColor("#5A6472")
LINE = colors.HexColor("#8A94A3")
BLUE = colors.HexColor("#E8F0FA")
BLUE_D = colors.HexColor("#5B84B1")
GREEN = colors.HexColor("#E6F4EA")
GREEN_D = colors.HexColor("#4E9A6B")
ORANGE = colors.HexColor("#FDF0E1")
ORANGE_D = colors.HexColor("#D08A3B")
RED = colors.HexColor("#FCE8E8")
RED_D = colors.HexColor("#C0504D")
GREY = colors.HexColor("#F2F3F5")
GREY_D = colors.HexColor("#9AA3AE")
PURPLE = colors.HexColor("#F1ECF8")
PURPLE_D = colors.HexColor("#8064A2")

# ---------------------------------------------------------------- styles
base = dict(fontName="Sans", textColor=INK)
S = {
    "title": ParagraphStyle("title", fontName="Sans-Bold", fontSize=26, leading=32,
                            textColor=NAVY, spaceAfter=6),
    "subtitle": ParagraphStyle("subtitle", fontName="Sans", fontSize=13, leading=18,
                               textColor=MUTED, spaceAfter=18),
    "h1": ParagraphStyle("h1", fontName="Sans-Bold", fontSize=17, leading=22,
                         textColor=NAVY, spaceBefore=4, spaceAfter=8),
    "h2": ParagraphStyle("h2", fontName="Sans-Bold", fontSize=12.5, leading=17,
                         textColor=TEAL, spaceBefore=10, spaceAfter=4),
    "body": ParagraphStyle("body", fontSize=9.6, leading=14, spaceAfter=6, **base),
    "bullet": ParagraphStyle("bullet", fontSize=9.6, leading=14, leftIndent=14,
                             bulletIndent=3, spaceAfter=2, **base),
    "small": ParagraphStyle("small", fontSize=8.2, leading=11, textColor=MUTED,
                            fontName="Sans"),
    "cell": ParagraphStyle("cell", fontSize=8.6, leading=11.5, **base),
    "cellb": ParagraphStyle("cellb", fontSize=8.6, leading=11.5, fontName="Sans-Bold",
                            textColor=INK),
    "head": ParagraphStyle("head", fontSize=8.6, leading=11.5, fontName="Sans-Bold",
                           textColor=colors.white),
    "code": ParagraphStyle("code", fontName="Mono", fontSize=7.8, leading=10.2,
                           textColor=INK, backColor=GREY, borderPadding=6,
                           leftIndent=6, rightIndent=6, spaceBefore=4, spaceAfter=10),
    "caption": ParagraphStyle("caption", fontSize=8.2, leading=11, textColor=MUTED,
                              alignment=TA_CENTER, spaceBefore=3, spaceAfter=10,
                              fontName="Sans"),
    "callout": ParagraphStyle("callout", fontSize=9.4, leading=13.5, **base),
}

PAGE_W, PAGE_H = A4
MARGIN = 18 * mm
CONTENT_W = PAGE_W - 2 * MARGIN


def P(text, style="body"):
    return Paragraph(text, S[style])


def bullets(items, style="bullet"):
    return [Paragraph(t, S[style], bulletText="•") for t in items]


def numbered(items):
    return [Paragraph(t, S["bullet"], bulletText=f"{i}.") for i, t in enumerate(items, 1)]


def table(rows, widths, header=True, zebra=True, first_col_bold=False):
    data = []
    for r, row in enumerate(rows):
        cells = []
        for c, v in enumerate(row):
            if r == 0 and header:
                cells.append(Paragraph(v, S["head"]))
            elif c == 0 and first_col_bold:
                cells.append(Paragraph(v, S["cellb"]))
            else:
                cells.append(Paragraph(v, S["cell"]))
        data.append(cells)
    t = Table(data, colWidths=widths, repeatRows=1 if header else 0)
    style = [
        ("VALIGN", (0, 0), (-1, -1), "TOP"),
        ("LEFTPADDING", (0, 0), (-1, -1), 5),
        ("RIGHTPADDING", (0, 0), (-1, -1), 5),
        ("TOPPADDING", (0, 0), (-1, -1), 4),
        ("BOTTOMPADDING", (0, 0), (-1, -1), 4),
        ("LINEBELOW", (0, 0), (-1, -1), 0.4, colors.HexColor("#D5DAE1")),
    ]
    if header:
        style.append(("BACKGROUND", (0, 0), (-1, 0), NAVY))
    if zebra:
        for r in range(1 if header else 0, len(rows)):
            if r % 2 == 0:
                style.append(("BACKGROUND", (0, r), (-1, r), GREY))
    t.setStyle(TableStyle(style))
    return t


def callout(text, fill=BLUE, edge=BLUE_D):
    t = Table([[Paragraph(text, S["callout"])]], colWidths=[CONTENT_W])
    t.setStyle(TableStyle([
        ("BACKGROUND", (0, 0), (-1, -1), fill),
        ("LINEBEFORE", (0, 0), (0, -1), 3, edge),
        ("LEFTPADDING", (0, 0), (-1, -1), 10),
        ("RIGHTPADDING", (0, 0), (-1, -1), 10),
        ("TOPPADDING", (0, 0), (-1, -1), 7),
        ("BOTTOMPADDING", (0, 0), (-1, -1), 7),
    ]))
    return t


def code(text):
    return Preformatted(text, S["code"])


# ---------------------------------------------------------------- drawing helpers
def box(d, x, y, w, h, text, fill=BLUE, stroke=BLUE_D, size=8, bold_first=True,
        text_color=INK, dash=None, radius=5):
    d.add(Rect(x, y, w, h, rx=radius, ry=radius, fillColor=fill, strokeColor=stroke,
               strokeWidth=1, strokeDashArray=dash))
    lines = text.split("\n")
    lh = size + 2.4
    total = lh * len(lines)
    top = y + h / 2 + total / 2 - size + 0.5
    for i, line in enumerate(lines):
        font = "Sans-Bold" if (i == 0 and bold_first) else "Sans"
        d.add(String(x + w / 2, top - i * lh, line, fontName=font, fontSize=size,
                     fillColor=text_color, textAnchor="middle"))


def arrow(d, x1, y1, x2, y2, label=None, color=LINE, width=1.1, dash=None,
          label_dx=0, label_dy=4, label_anchor="middle", label_size=7, both=False):
    d.add(Line(x1, y1, x2, y2, strokeColor=color, strokeWidth=width,
               strokeDashArray=dash))

    def head(xa, ya, xb, yb):
        import math
        ang = math.atan2(yb - ya, xb - xa)
        L, Wd = 6.5, 3.2
        bx, by = xb - L * math.cos(ang), yb - L * math.sin(ang)
        px, py = -math.sin(ang) * Wd, math.cos(ang) * Wd
        d.add(Polygon([xb, yb, bx + px, by + py, bx - px, by - py],
                      fillColor=color, strokeColor=color, strokeWidth=0.5))

    head(x1, y1, x2, y2)
    if both:
        head(x2, y2, x1, y1)
    if label:
        mx, my = (x1 + x2) / 2 + label_dx, (y1 + y2) / 2 + label_dy
        d.add(String(mx, my, label, fontName="Sans", fontSize=label_size,
                     fillColor=MUTED, textAnchor=label_anchor))


def text(d, x, y, s, size=7.5, color=MUTED, anchor="start", bold=False):
    d.add(String(x, y, s, fontName="Sans-Bold" if bold else "Sans", fontSize=size,
                 fillColor=color, textAnchor=anchor))


# ---------------------------------------------------------------- diagrams
def diagram_overview():
    W, H = CONTENT_W, 345
    d = Drawing(W, H)
    # Docker host
    d.add(Rect(186, 22, 214, 318, rx=8, ry=8, fillColor=None, strokeColor=GREY_D,
               strokeWidth=1, strokeDashArray=[4, 3]))
    text(d, 194, 328, "Docker host (docker-compose.yml)", 7.5, MUTED, bold=True)
    # networks
    d.add(Rect(196, 118, 194, 200, rx=6, ry=6, fillColor=BLUE, strokeColor=BLUE_D,
               strokeWidth=0.6))
    text(d, 202, 307, "network \"edge\" — has internet", 7, BLUE_D, bold=True)
    d.add(Rect(196, 30, 194, 80, rx=6, ry=6, fillColor=GREEN, strokeColor=GREEN_D,
               strokeWidth=0.6))
    text(d, 297, 100, "network \"data\"", 7, GREEN_D, bold=True)
    text(d, 297, 91, "internal, no internet", 6.6, GREEN_D)
    # server spans both networks
    box(d, 204, 40, 86, 256,
        "API server\n(our Go code)\n\n19 MB image\nnon-root\nread-only\n\n"
        "login check\nrate limit\nmetering\ncache\nsearch",
        fill=colors.white, stroke=NAVY, size=7.4)
    box(d, 302, 228, 80, 58, "SearXNG\nfree metasearch\n(self-hosted)",
        fill=colors.white, stroke=BLUE_D, size=7.4)
    box(d, 302, 36, 80, 50, "Redis\nusage counters\n+ search cache",
        fill=colors.white, stroke=GREEN_D, size=7.2)
    # left: app, auth, proxy
    box(d, 0, 228, 80, 58, "Mobile / Web app\n(AI agent later)", fill=PURPLE,
        stroke=PURPLE_D, size=7.6)
    box(d, 0, 60, 80, 78, "Auth provider\nFirebase / Supabase\n/ Clerk / Auth0\n\nGoogle · Apple\n· Facebook",
        fill=ORANGE, stroke=ORANGE_D, size=7.2)
    box(d, 96, 228, 76, 58, "HTTPS proxy\nor Cloudflare\nTunnel\n(at deployment)",
        fill=GREY, stroke=GREY_D, size=7, dash=[3, 2])
    text(d, 134, 296, "③ request + JWT", 7, MUTED, "middle")
    # right: external services
    box(d, 412, 228, 81, 58, "Search engines\nGoogle, Bing,\nDuckDuckGo,\nBrave, ...",
        fill=GREY, stroke=GREY_D, size=7.2)
    box(d, 412, 160, 81, 46, "Paid backups\nSerpApi (Google\nShopping) · Apify",
        fill=GREY, stroke=GREY_D, size=7.2)
    box(d, 412, 106, 81, 46, "Auth provider\npublic keys\n(JWKS)",
        fill=ORANGE, stroke=ORANGE_D, size=7.2)
    # arrows
    arrow(d, 40, 228, 40, 138, "① sign in", label_dx=4, label_anchor="start")
    arrow(d, 54, 138, 54, 228, "② JWT", label_dx=4, label_dy=-16, label_anchor="start")
    arrow(d, 80, 257, 96, 257)
    arrow(d, 172, 257, 204, 257)
    arrow(d, 290, 257, 302, 257)
    arrow(d, 382, 257, 412, 257, "queries", label_dy=5)
    arrow(d, 290, 183, 412, 183, "④ if SearXNG fails", label_dy=4)
    arrow(d, 290, 129, 412, 129, "public keys, hourly", label_dy=4, dash=[3, 2])
    arrow(d, 290, 61, 302, 61)
    text(d, 0, 6, "Only the API port is published. SearXNG and Redis cannot be reached from outside Docker.",
         7, MUTED)
    return d


def diagram_layers():
    W = CONTENT_W
    rows = [
        ("HTTP middleware", "request ID, JSON logs, panic recovery, CORS", GREY, GREY_D),
        ("Login check", "verify JWT signature, issuer, audience, expiry → user id + plan", ORANGE, ORANGE_D),
        ("Burst rate limit", "per user, e.g. 1 request/second with bursts of 5 → else 429", ORANGE, ORANGE_D),
        ("Search handler", "HTTP only: read JSON body, map result to status codes + headers", PURPLE, PURPLE_D),
        ("Metering", "cached answer = free · live search = 1 from daily allowance", PURPLE, PURPLE_D),
        ("Cache", "fresh / stale / expired · pull-to-refresh · merges identical searches", GREEN, GREEN_D),
        ("Search service", "validate, pick the country's shops, worker pool, merge + order results", BLUE, BLUE_D),
        ("Provider chain", "SearXNG first → SerpApi only if it fails · circuit breaker", BLUE, BLUE_D),
    ]
    bh, gap = 30, 9
    H = len(rows) * (bh + gap) + 6
    d = Drawing(W, H)
    bw = 150
    for i, (name, desc, fill, edge) in enumerate(rows):
        y = H - (i + 1) * (bh + gap)
        box(d, 0, y, bw, bh, name, fill=fill, stroke=edge, size=8.4)
        text(d, bw + 14, y + bh / 2 - 3, desc, 8, INK)
        if i < len(rows) - 1:
            arrow(d, bw / 2, y, bw / 2, y - gap + 0.5, width=1)
    # side stores
    return d


def diagram_flow():
    W = CONTENT_W
    steps = [
        ("Valid login token?", "no", "401 unauthorized", RED, RED_D),
        ("Not too fast? (burst limit)", "no", "429 rate_limited\n+ Retry-After", RED, RED_D),
        ("Body is valid JSON?", "no", "400 invalid_request\n(nothing counted)", RED, RED_D),
        ("Answer already in cache?", "yes", "200 from cache — FREE\nX-Cache: HIT or STALE", GREEN, GREEN_D),
        ("Searches left today?", "no", "free plan → 402 upgrade\npaid plan → 429", RED, RED_D),
        ("Count 1 search, search live", "fails", "refund the search;\nold answer? 200 stale : 502", ORANGE, ORANGE_D),
    ]
    bh, gap, start_h = 34, 16, 22
    H = start_h + gap + len(steps) * (bh + gap) + bh + 4
    d = Drawing(W, H)
    bx, bw = 30, 190
    rx, rw = 300, 193
    top = H - start_h
    box(d, bx + 40, top, bw - 80, start_h, "POST /api/v1/search", fill=NAVY, stroke=NAVY,
        size=8.2, text_color=colors.white)
    prev_bottom = top
    for i, (q, branch, outcome, fill, edge) in enumerate(steps):
        y = prev_bottom - gap - bh
        arrow(d, bx + bw / 2, prev_bottom, bx + bw / 2, y + bh)
        box(d, bx, y, bw, bh, q, fill=colors.white, stroke=NAVY, size=8.2)
        arrow(d, bx + bw, y + bh / 2, rx, y + bh / 2, branch, label_dy=4)
        box(d, rx, y, rw, bh, outcome, fill=fill, stroke=edge, size=7.8)
        prev_bottom = y
    y = prev_bottom - gap - bh
    arrow(d, bx + bw / 2, prev_bottom, bx + bw / 2, y + bh, "ok", label_dx=6,
          label_anchor="start")
    box(d, bx, y, bw, bh, "Store in cache\n200 — X-Cache: MISS", fill=GREEN,
        stroke=GREEN_D, size=8.2)
    return d


def diagram_login():
    W, H = CONTENT_W, 200
    d = Drawing(W, H)
    cols = [(60, "Mobile / Web app", PURPLE, PURPLE_D),
            (245, "Auth provider (Firebase)", ORANGE, ORANGE_D),
            (430, "API server", BLUE, BLUE_D)]
    for x, name, fill, edge in cols:
        box(d, x - 58, H - 30, 116, 24, name, fill=fill, stroke=edge, size=8)
        d.add(Line(x, H - 30, x, 8, strokeColor=GREY_D, strokeWidth=0.8,
                   strokeDashArray=[3, 3]))
    msgs = [
        (60, 245, 150, "1. sign in with Google / Apple / Facebook"),
        (245, 60, 122, "2. signed JWT (sub = user id, plan, expires 1 h)"),
        (60, 430, 94, "3. POST /search   Authorization: Bearer <JWT>"),
        (430, 245, 66, "4. public keys (JWKS) — fetched once, cached, refreshed hourly"),
    ]
    for x1, x2, y, label in msgs:
        arrow(d, x1, y, x2, y, label, label_dy=5,
              label_dx=40 if label.startswith("4") else 0,
              dash=[3, 2] if label.startswith("4") else None)
    box(d, 330, 14, 160, 36,
        "5. verify locally\nsignature · issuer · audience · expiry",
        fill=colors.white, stroke=BLUE_D, size=7.4)
    return d


def diagram_fallback():
    W, H = CONTENT_W, 178
    d = Drawing(W, H)
    box(d, 0, 66, 88, 50, "Search service\nshops grouped\nby source", fill=BLUE, stroke=BLUE_D, size=7.6)
    # chain A: shops without a scraper
    text(d, 112, 166, "amazon, aliexpress, ebay (and temu / shein when Apify is off)", 7.2, MUTED)
    box(d, 112, 112, 104, 46, "SearXNG (free)\nmax 8 s", fill=GREEN, stroke=GREEN_D, size=7.6)
    box(d, 252, 112, 124, 46, "SerpApi (paid)\nGoogle Shopping:\nprices · seller · image",
        fill=ORANGE, stroke=ORANGE_D, size=7.4)
    arrow(d, 216, 135, 252, 135, "fails", label_dy=4)
    # chain B: shops with an Apify scraper
    text(d, 112, 72, "temu, shein — when APIFY_MARKETS is set", 7.2, MUTED)
    box(d, 112, 18, 104, 46, "Apify scraper\nreal prices\nmax 25 s", fill=PURPLE, stroke=PURPLE_D, size=7.6)
    box(d, 252, 18, 124, 46, "same chain as above\nSearXNG → SerpApi\n(for this shop only)",
        fill=colors.white, stroke=GREY_D, size=7.4, dash=[3, 2])
    arrow(d, 216, 41, 252, 41, "fails/slow", label_dy=5, label_size=6.4)
    arrow(d, 88, 100, 112, 128)
    arrow(d, 88, 82, 112, 48)
    # results
    box(d, 410, 66, 83, 50, "Merged results\nprovider: searxng\n/ serpapi / apify",
        fill=colors.white, stroke=NAVY, size=7.2)
    arrow(d, 376, 135, 410, 108)
    arrow(d, 376, 41, 410, 74)
    text(d, 0, 4, "The first source that succeeds answers. Circuit breaker: 3 failures in a row → that "
         "source is skipped for 1 minute.", 7, MUTED)
    return d


def diagram_cache_timeline():
    W, H = CONTENT_W, 110
    d = Drawing(W, H)
    x0, x1, x2, x3 = 10, 150, 400, 483
    y, h = 52, 26
    d.add(Rect(x0, y, x1 - x0, h, fillColor=GREEN, strokeColor=GREEN_D, strokeWidth=0.8))
    d.add(Rect(x1, y, x2 - x1, h, fillColor=ORANGE, strokeColor=ORANGE_D, strokeWidth=0.8))
    d.add(Rect(x2, y, x3 - x2, h, fillColor=GREY, strokeColor=GREY_D, strokeWidth=0.8))
    text(d, (x0 + x1) / 2, y + 10, "FRESH", 8.4, GREEN_D, "middle", True)
    text(d, (x1 + x2) / 2, y + 10, "STALE", 8.4, ORANGE_D, "middle", True)
    text(d, (x2 + x3) / 2, y + 10, "EXPIRED", 8.4, MUTED, "middle", True)
    for x, lbl in [(x0, "fetched"), (x1, "2 hours"), (x2, "24 hours")]:
        d.add(Line(x, y - 4, x, y + h + 4, strokeColor=INK, strokeWidth=0.8))
        text(d, x, y + h + 8, lbl, 7.6, INK, "middle")
    text(d, (x0 + x1) / 2, y - 14, "served from cache", 7.6, INK, "middle")
    text(d, (x0 + x1) / 2, y - 24, "(no cost)", 7.6, MUTED, "middle")
    text(d, (x1 + x2) / 2, y - 14, "served instantly (stale: true) AND refreshed", 7.6, INK, "middle")
    text(d, (x1 + x2) / 2, y - 24, "in the background, so the next user gets new prices", 7.6, MUTED, "middle")
    text(d, (x2 + x3) / 2, y - 14, "fetched live", 7.6, INK, "middle")
    text(d, (x2 + x3) / 2, y - 24, "(counts 1 search)", 7.6, MUTED, "middle")
    text(d, x0, 6, "Age of the cached answer  →", 7.4, MUTED)
    return d


def caption(s):
    return P(s, "caption")


# ---------------------------------------------------------------- page decorations
def on_page(canvas, doc):
    canvas.saveState()
    if doc.page > 1:
        canvas.setFont("Sans", 7.5)
        canvas.setFillColor(MUTED)
        canvas.drawString(MARGIN, PAGE_H - 11 * mm, "owis_find_deal_engine — architecture and flows")
        canvas.drawRightString(PAGE_W - MARGIN, PAGE_H - 11 * mm, "October 2026")
        canvas.setStrokeColor(colors.HexColor("#D5DAE1"))
        canvas.setLineWidth(0.5)
        canvas.line(MARGIN, PAGE_H - 12.5 * mm, PAGE_W - MARGIN, PAGE_H - 12.5 * mm)
    canvas.setFont("Sans", 7.5)
    canvas.setFillColor(MUTED)
    canvas.drawCentredString(PAGE_W / 2, 10 * mm, f"{doc.page}")
    canvas.restoreState()


# ---------------------------------------------------------------- content
def build():
    st = []

    # ---- cover
    st += [Spacer(1, 40 * mm),
           P("owis_find_deal_engine", "title"),
           P("Architecture, processes and request flow — explained step by step", "subtitle"),
           Spacer(1, 4 * mm),
           table([
               ["Item", "Value"],
               ["What", "Product search service: one product name + one country → matching "
                        "offers from the online shops that deliver to that country"],
               ["Status", "Phase 1 (search service) complete · CI green · ready for local test"],
               ["Code", "github.com/owisdev/claude-ai-clould-repo, folder "
                        "<b>Owis_Find_Deal_Engine/</b>, branch <b>claude/cloud-vs-local-7xi988</b>"],
               ["Language / runtime", "Go 1.26 · Docker · Redis · SearXNG"],
               ["Date", "4 October 2026"],
           ], [38 * mm, CONTENT_W - 38 * mm], first_col_bold=True),
           Spacer(1, 10 * mm),
           P("<b>How to read this document</b>", "h2")]
    st += bullets([
        "Sections 1–2 give the big picture: what the service does and which parts it has.",
        "Sections 3–8 follow one search request through the system, step by step.",
        "Sections 9–11 cover running it: the API, configuration and Docker.",
        "Section 12 lists what comes next; section 13 is a short glossary of the technical words.",
    ])
    st.append(PageBreak())

    # ---- 1 what it does
    st += [P("1. What the service does", "h1"),
           P("A user types a product name (for example <i>“samsung s pen”</i>) and picks their "
             "country. The service searches the online shops that <b>deliver to that country</b> "
             "and returns one merged list of offers: title, link, short description, image and, "
             "when available, the price."),
           P("Key ideas", "h2")]
    st += bullets([
        "<b>Country-aware:</b> only shops that deliver to the user's country are searched "
        "(eBay is skipped for Jordan, Amazon Saudi Arabia is used for KSA).",
        "<b>Login required:</b> every search comes from a signed-in user (Google, Apple or "
        "Facebook through an auth provider such as Firebase).",
        "<b>As cheap as possible:</b> a free self-hosted search engine (SearXNG) is used first; "
        "paid sources only when needed: SerpApi with Google Shopping (prices) as backup, and "
        "optional Apify scrapers for Temu and SHEIN (real prices).",
        "<b>Cache:</b> answers are remembered for up to 24 hours and refreshed automatically, "
        "so popular searches cost nothing and prices stay reasonably current.",
        "<b>Fair use:</b> each user has a daily number of searches by plan (free / pro). "
        "Answers from the cache are free and never reduce that number.",
        "<b>Robust:</b> if one shop or search source fails, the user still gets the rest; "
        "timeouts, fallbacks and safe defaults everywhere.",
    ])
    st += [P("Supported countries and shops", "h2"),
           table([
               ["Shop", "USA (usa)", "Saudi Arabia (ksa)", "Jordan (jor)"],
               ["Amazon", "amazon.com", "amazon.sa", "amazon.com"],
               ["eBay", "ebay.com", "ebay.com", "— (no delivery)"],
               ["AliExpress", "aliexpress.com", "aliexpress.com", "aliexpress.com"],
               ["Temu", "temu.com", "temu.com", "temu.com"],
               ["SHEIN", "us.shein.com", "ar.shein.com", "ar.shein.com"],
           ], [32 * mm, (CONTENT_W - 32 * mm) / 3, (CONTENT_W - 32 * mm) / 3,
               (CONTENT_W - 32 * mm) / 3], first_col_bold=True),
           Spacer(1, 4),
           P("This table lives in one configuration file "
             "(<font name='Mono'>server/internal/markets/markets.json</font>). Adding a country or a "
             "shop means editing that file — no code change.", "small"),
           PageBreak()]

    # ---- 2 architecture
    st += [P("2. The big picture", "h1"),
           P("Three programs run together in Docker: the <b>API server</b> (our Go code), "
             "<b>SearXNG</b> (free search) and <b>Redis</b> (fast storage for counters and the "
             "cache). Around them are outside services: the auth provider that logs users in, the "
             "public search engines, and SerpApi as a paid backup."),
           diagram_overview(),
           caption("Figure 1 — System overview. Numbers show the order of a first search."),
           P("The parts", "h2"),
           table([
               ["Part", "Role", "Why it is there"],
               ["Mobile / Web app", "Shows the search box and results", "Built later (phase 2+)"],
               ["Auth provider", "Signs users in with Google / Apple / Facebook and issues a "
                                 "signed token (JWT)", "We never store passwords; free tier"],
               ["API server", "Our Go service: checks the user, applies limits, searches, "
                              "merges and caches results", "The product itself"],
               ["SearXNG", "Self-hosted metasearch: asks Google, Bing, DuckDuckGo, Brave… "
                           "in one go", "Free search results"],
               ["SerpApi", "Paid Google API; uses Google Shopping (prices, seller, image)",
                "Backup when SearXNG is blocked"],
               ["Apify (optional)", "Store of ready-made scrapers; one per shop for Temu and SHEIN",
                "Real prices where shops have no official API"],
               ["Redis", "In-memory database: daily search counters + cached answers",
                "Fast, shared by all server copies"],
               ["HTTPS proxy / Tunnel", "Adds HTTPS in front of the API in production",
                "Only public entry point (deployment step)"],
           ], [33 * mm, 78 * mm, CONTENT_W - 111 * mm], first_col_bold=True),
           PageBreak()]

    # ---- 3 inside server
    st += [P("3. Inside the API server", "h1"),
           P("A search request passes through a stack of small layers. Each layer does one job "
             "and hands the request to the next one. This keeps every piece simple to test and to "
             "change."),
           diagram_layers(),
           caption("Figure 2 — Layers a search request passes through, top to bottom."),
           P("Code map (folder <font name='Mono'>server/</font>)", "h2"),
           table([
               ["Folder", "What it contains"],
               ["cmd/owis_find_deal_engine", "Program start: reads settings, connects everything, "
                                             "graceful shutdown, health check command"],
               ["internal/api", "HTTP routes, handlers, middleware (login, rate limit, logs, CORS)"],
               ["internal/auth", "JWT verification and cache of the provider's public keys"],
               ["internal/metering", "The rule “cached = free, live = counted”"],
               ["internal/usage", "Plans (free / pro) and daily counters (Redis or memory)"],
               ["internal/cache", "Search cache with freshness rules (Redis or memory)"],
               ["internal/search", "Search service, worker pool, provider fallback chain"],
               ["internal/providers", "SearXNG and SerpApi clients"],
               ["internal/markets", "Countries → shops table (markets.json)"],
               ["internal/ratelimit", "Per-user burst limiter"],
               ["internal/config", "All settings from environment variables, with validation"],
               ["deploy/searxng", "SearXNG settings (Google/Bing enabled, JSON output on)"],
           ], [52 * mm, CONTENT_W - 52 * mm], first_col_bold=True),
           PageBreak()]

    # ---- 4 request flow
    st += [P("4. A search request, step by step", "h1"),
           P("This is the complete decision path of <font name='Mono'>POST /api/v1/search</font>. "
             "Every possible answer the app can receive is on this page."),
           diagram_flow(),
           caption("Figure 3 — Decision flow of one search. Left: the checks in order. "
                   "Right: what the app receives when a check stops the request."),
           P("The same steps in words", "h2")]
    st += numbered([
        "<b>Login check.</b> The token in the <font name='Mono'>Authorization</font> header must be "
        "valid and not expired. Otherwise: <b>401</b>.",
        "<b>Speed check.</b> A user cannot fire requests faster than the burst limit "
        "(default 1 per second, bursts of 5). Otherwise: <b>429</b> with a wait time.",
        "<b>Input check.</b> The body must be JSON with a title (2–200 characters) and a known "
        "country code. Otherwise: <b>400</b>. Nothing is counted.",
        "<b>Cache check.</b> If the answer is cached, it is returned immediately and is "
        "<b>free</b> — even if the user has no searches left today.",
        "<b>Allowance check.</b> Only for live searches: one search is taken from today's "
        "allowance. Free plan used up: <b>402</b> (show an upgrade screen). Paid plan used up: "
        "<b>429</b> until midnight UTC.",
        "<b>Live search.</b> The shops of the country are searched (section 6). The answer is "
        "stored in the cache and returned: <b>200</b>, <font name='Mono'>X-Cache: MISS</font>.",
        "<b>If the live search fails,</b> the search is given back to the user. If an older "
        "cached answer exists it is returned (marked stale); otherwise <b>502</b>.",
    ])
    st.append(PageBreak())

    # ---- 5 login
    st += [P("5. Login and security", "h1"),
           P("The server never sees passwords. Users sign in with an <b>auth provider</b>; the "
             "provider gives the app a <b>JWT</b> — a small signed text that says “this is user X, "
             "valid until time T”. The app sends it with every request."),
           diagram_login(),
           caption("Figure 4 — Login sequence. Step 4 happens once at start and then hourly, "
                   "not on every request."),
           P("What the server checks on every request", "h2")]
    st += bullets([
        "<b>Signature</b> — made with the provider's private key; checked with its public keys "
        "(JWKS). A changed token fails. Only strong key types are accepted (RS256 / ES256); "
        "unsigned tokens and shared-secret tokens are rejected.",
        "<b>Issuer and audience</b> — the token must come from <i>our</i> provider project.",
        "<b>Expiry</b> — expired tokens are refused (Firebase tokens live 1 hour; the app "
        "refreshes them automatically).",
        "<b>User id and plan</b> — taken from the token (<font name='Mono'>sub</font> and a "
        "<font name='Mono'>plan</font> claim; missing plan = free).",
    ])
    st += [P("Other protections", "h2")]
    st += bullets([
        "Per-user burst limit and daily allowance (section 8).",
        "Strict input: unknown JSON fields, oversized bodies and bad titles are rejected.",
        "Secrets (SerpApi key, Redis password) only in environment variables, never in code "
        "or logs; the SerpApi key is removed even from error messages.",
        "If the counter store (Redis) is down, live searches are refused (<b>503</b>) rather "
        "than allowed without limits — protects the paid search budget.",
        "Docker: every container runs as a normal (non-root) user, with a read-only file system, "
        "no special Linux permissions and memory limits. Redis has no internet and a password.",
    ])
    st.append(PageBreak())

    # ---- 6 search
    st += [P("6. How the search itself works", "h1"),
           P("Step 1 — pick the shops", "h2"),
           P("The country code selects the shops from the table in section 1. Jordan → Amazon, "
             "AliExpress, Temu, SHEIN."),
           P("Step 2 — build one query", "h2"),
           P("All shops go into a single web search using the <font name='Mono'>site:</font> "
             "operator, so one search covers every shop (cheapest option):"),
           code('samsung s pen (site:amazon.com OR site:aliexpress.com OR site:temu.com OR site:ar.shein.com)'),
           P("(Setting <font name='Mono'>SEARCH_COMBINED=false</font> sends one query per shop "
             "instead, in parallel: better coverage, more cost.) Google Shopping does not support "
             "<font name='Mono'>site:</font>; there the query is just the product name and offers "
             "are matched to our shops by seller name (“Amazon.com”, “SHEIN”…).", "small"),
           P("Step 3 — ask the providers, free first", "h2"),
           diagram_fallback(),
           caption("Figure 5 — Search sources. Paid sources run only when the free one errors, "
                   "times out or is blocked; Apify (optional) gives Temu and SHEIN real prices."),
           P("Step 4 — clean and merge the results", "h2")]
    st += bullets([
        "Each result link is matched to its shop by domain (regional sites such as "
        "sa.shein.com count as SHEIN); results from other sites are dropped.",
        "Duplicates are removed, and results are numbered per shop (1st Amazon, 1st Temu, …).",
        "The list is ordered so the <b>best result of every shop comes first</b>, then the "
        "second of each, and so on.",
        "Missing fields never crash the service; a price is included when the search engine "
        "provides one.",
    ])
    st += [P("Step 5 — report per shop", "h2"),
           P("The answer includes <font name='Mono'>markets</font>: <i>ok</i> or <i>error</i> for "
             "each shop. One failing shop does not fail the search; the app can show “SHEIN "
             "temporarily unavailable”. Only if every shop fails does the user get 502."),
           callout("<b>Concurrency, in short:</b> provider calls run on a small pool of goroutines "
                   "(Go's lightweight threads) with one overall time limit (15 s; raised automatically "
                   "when Apify is on). Channels are "
                   "sized so no goroutine can get stuck, and a crash inside a provider is caught "
                   "and reported as an error instead of stopping the server."),
           PageBreak()]

    # ---- 7 cache
    st += [P("7. Cache and keeping prices current", "h1"),
           P("Every answer is stored under <b>country + product name</b> (upper/lower case and extra "
             "spaces do not matter, so “Samsung  S PEN” and “samsung s pen” share one entry). "
             "What happens next depends on the answer's age:"),
           diagram_cache_timeline(),
           caption("Figure 6 — Life of a cached answer (default times; all configurable)."),
           P("Rules that keep prices honest", "h2"),
           table([
               ["Rule", "What it does"],
               ["Background refresh", "Stale answers are served instantly and refreshed behind "
                                      "the scenes. At most 4 refreshes run at once."],
               ["Shorter life for doubtful answers", "If a shop failed, the answer is fresh for "
                                                     "10 min only; an empty answer for 30 min."],
               ["Pull to refresh", "<font name='Mono'>\"refresh\": true</font> forces a live search, "
                                   "at most once per 10 min per product (prevents abuse)."],
               ["Old answer beats an error", "If a live search fails but an older answer exists, "
                                             "the older one is returned with stale: true."],
               ["One search for many users", "If 20 users search the same product at the same "
                                             "moment, only one live search runs."],
               ["Shop list changes", "Editing markets.json automatically stops old answers from "
                                     "being used."],
               ["Shown to the user", "Every answer has fetched_at, cached and stale, so the app "
                                     "can show “prices updated 3 h ago”. The shop link always "
                                     "opens the live price."],
           ], [48 * mm, CONTENT_W - 48 * mm], first_col_bold=True),
           Spacer(1, 6),
           callout("<b>Later (phase 2):</b> a scheduled job re-checks items saved in users' carts "
                   "and sends price-drop notifications; eBay and AliExpress results (exact prices "
                   "from their APIs) get their own shorter cache time.",
                   fill=GREEN, edge=GREEN_D),
           PageBreak()]

    # ---- 8 plans
    st += [P("8. Plans and daily allowance", "h1"),
           P("Each user has a plan; each plan has a number of <b>live</b> searches per day "
             "(UTC). Defaults: <b>free = 20</b>, <b>pro = 500</b> "
             "(<font name='Mono'>PLANS=free:20,pro:500</font>)."),
           table([
               ["Situation", "Counted?", "Answer to the app"],
               ["Answer found in cache", "No — free", "200, X-Cache: HIT / STALE"],
               ["Live search, allowance left", "Yes, 1", "200, X-Cache: MISS"],
               ["Live search fails", "No — given back", "502 (or older answer, stale)"],
               ["Invalid input", "No", "400"],
               ["Free plan used up, not cached", "—", "402 payment_required → show upgrade"],
               ["Paid plan used up, not cached", "—", "429 quota_exceeded + Retry-After"],
               ["Counter store down, not cached", "—", "503 (refused to protect the budget)"],
           ], [62 * mm, 32 * mm, CONTENT_W - 94 * mm]),
           Spacer(1, 6),
           P("Every search answer carries these headers so the app can show “N searches left "
             "today” without spending one:"),
           table([
               ["Header", "Meaning"],
               ["X-Plan", "plan name, e.g. free"],
               ["X-RateLimit-Limit", "searches per day for this plan"],
               ["X-RateLimit-Remaining", "live searches left today"],
               ["X-RateLimit-Reset", "when the counter resets (Unix time, midnight UTC)"],
               ["X-Cache", "MISS (live) · HIT (fresh cache) · STALE (cache, being refreshed)"],
               ["X-Request-ID", "id of this request, also in the server logs (for support)"],
           ], [48 * mm, CONTENT_W - 48 * mm], first_col_bold=True),
           Spacer(1, 6),
           P("Where the plan comes from: a <font name='Mono'>plan</font> field in the login "
             "token (for example a Firebase custom claim set after payment). Unknown or missing = "
             "free. The counting rule lives in one place (the metering layer), so a future AI-agent "
             "endpoint will follow the same rule automatically.", "small"),
           PageBreak()]

    # ---- 9 API
    st += [P("9. API reference", "h1"),
           table([
               ["Method", "Path", "Login", "Purpose"],
               ["GET", "/api/v1/health", "no", "Is the server up?"],
               ["GET", "/api/v1/countries", "no", "Countries and their shops"],
               ["POST", "/api/v1/search", "yes", "Search a product in a country"],
           ], [18 * mm, 45 * mm, 16 * mm, CONTENT_W - 79 * mm]),
           P("Search request", "h2"),
           code('POST /api/v1/search\n'
                'Authorization: Bearer <JWT from the auth provider>\n'
                'Content-Type: application/json\n\n'
                '{ "title": "samsung s pen", "country": "jor", "refresh": false }'),
           P("Search answer (200)", "h2"),
           code('{\n'
                '  "query": "samsung s pen",\n'
                '  "country": "jor",\n'
                '  "results": [\n'
                '    { "market": "amazon", "title": "Samsung Galaxy S Pen ...",\n'
                '      "link": "https://www.amazon.com/...", "snippet": "...",\n'
                '      "thumbnail": "https://...", "price": 29.99, "currency": "$",\n'
                '      "position": 1, "provider": "searxng" },\n'
                '    { "market": "temu", "title": "...", "position": 1, "provider": "searxng" }\n'
                '  ],\n'
                '  "markets": { "amazon": "ok", "aliexpress": "ok", "temu": "ok", "shein": "error" },\n'
                '  "took_ms": 812,\n'
                '  "fetched_at": "2026-10-02T12:00:00Z",\n'
                '  "cached": false,\n'
                '  "stale": false\n'
                '}'),
           P("Errors", "h2"),
           P("Always the same shape: <font name='Mono'>{\"error\": {\"code\": \"...\", "
             "\"message\": \"...\"}}</font>"),
           table([
               ["Status", "code", "Meaning / what the app should do"],
               ["400", "invalid_request, unsupported_country", "Fix the input"],
               ["401", "unauthorized", "Sign in again / refresh the token"],
               ["402", "payment_required", "Free searches used up → upgrade screen"],
               ["429", "rate_limited", "Too fast → wait Retry-After seconds"],
               ["429", "quota_exceeded", "Paid plan's daily searches used up"],
               ["502", "upstream_error", "All shops failed → “try again”"],
               ["503", "service_unavailable", "Temporary problem → retry later"],
               ["500", "internal_error", "Bug → report with X-Request-ID"],
           ], [16 * mm, 62 * mm, CONTENT_W - 78 * mm]),
           PageBreak()]

    # ---- 10 config
    st += [P("10. Configuration", "h1"),
           P("All settings are environment variables, read at start and checked: the server "
             "refuses to start with a clear message if something required is missing or wrong. "
             "The full list with comments is in <font name='Mono'>server/.env.example</font>."),
           table([
               ["Setting", "Default", "Purpose"],
               ["AUTH_JWKS_URL / AUTH_ISSUER / AUTH_AUDIENCE", "— (required)",
                "Which auth provider project to trust"],
               ["AUTH_PLAN_CLAIM", "plan", "Token field holding the user's plan"],
               ["PLANS / DEFAULT_PLAN", "free:20,pro:500 / free", "Daily live searches per plan"],
               ["SEARCH_PROVIDERS", "searxng,serpapi", "Order of search sources"],
               ["SEARXNG_URL", "—", "Address of SearXNG"],
               ["SERPAPI_KEY", "—", "Needed only if serpapi is listed"],
               ["SERPAPI_ENGINE", "google_shopping", "Prices from Google Shopping, or google (web)"],
               ["APIFY_MARKETS / APIFY_TOKEN", "empty", "Shops that use an Apify scraper (temu,shein)"],
               ["APIFY_&lt;SHOP&gt;_ACTOR / _INPUT", "—", "Which scraper, and its input with {{query}}"],
               ["APIFY_TIMEOUT / APIFY_MAX_ITEMS", "25s / 10", "Scraper time limit / results per run"],
               ["SEARCH_COMBINED", "true", "One query for all shops (cheapest)"],
               ["SEARCH_TIMEOUT", "15s", "Max time for a whole search"],
               ["PROVIDER_ATTEMPT_TIMEOUT", "8s", "Time SearXNG gets before falling back"],
               ["PROVIDER_FAILURE_THRESHOLD / COOLDOWN", "3 / 1m", "Circuit breaker"],
               ["CACHE_FRESH_TTL / CACHE_STALE_TTL", "2h / 24h", "Cache life (section 7)"],
               ["CACHE_PARTIAL_TTL / CACHE_EMPTY_TTL", "10m / 30m", "Shorter life for doubtful answers"],
               ["CACHE_MIN_REFRESH", "10m", "Min time between pull-to-refresh"],
               ["RATE_LIMIT_RPS / RATE_LIMIT_BURST", "1 / 5", "Per-user speed limit"],
               ["REDIS_URL / REDIS_PASSWORD", "—", "Counters and cache (memory if empty)"],
               ["SEARXNG_SECRET", "—", "SearXNG's own secret (Docker)"],
               ["API_BIND / API_PORT", "127.0.0.1 / 3002", "Where Docker publishes the API"],
               ["CORS_ALLOWED_ORIGINS", "empty", "Websites allowed to call the API"],
           ], [66 * mm, 36 * mm, CONTENT_W - 102 * mm]),
           PageBreak()]

    # ---- 11 run & deploy
    st += [P("11. Running it: Docker, tests and CI", "h1"),
           P("Full stack with one command", "h2"),
           code('cd Owis_Find_Deal_Engine/server\n'
                'cp .env.example .env        # fill in: AUTH_*, SEARXNG_SECRET, REDIS_PASSWORD\n'
                'docker compose up -d --build\n'
                'docker compose ps           # 3 containers "healthy"\n'
                'curl localhost:3002/api/v1/health'),
           P("Step-by-step instructions for your PC (including how to get a free test login "
             "token from Firebase, and PowerShell commands) are in "
             "<font name='Mono'>Owis_Find_Deal_Engine/STATUS.md</font>.", "small"),
           P("What Docker sets up", "h2"),
           table([
               ["Container", "Networks", "Published?", "Protection"],
               ["server", "edge + data", "API port only, on 127.0.0.1",
                "non-root, read-only, no capabilities, 256 MB, health check"],
               ["searxng", "edge", "no", "runs as its own user (image default is root), read-only"],
               ["redis", "data (no internet)", "no", "password, non-root, read-only, keeps counters "
                                                     "on disk, evicts cache before counters"],
           ], [22 * mm, 30 * mm, 38 * mm, CONTENT_W - 90 * mm]),
           P("Startup and shutdown", "h2")]
    st += bullets([
        "At start the server checks all settings, connects to Redis and downloads the auth "
        "provider's public keys; any problem stops it with a clear message.",
        "On stop (<font name='Mono'>docker compose stop</font>) it stops accepting requests, "
        "finishes running ones and background cache refreshes, then exits (max 10 s).",
        "Logs are one JSON line per event (request, user id, status, duration, request id).",
    ])
    st += [P("Quality checks", "h2")]
    st += bullets([
        "97 automated test functions (128 test cases) across all packages, run with Go's race detector "
        "(finds unsafe concurrent code).",
        "GitHub Actions on every push: formatting, dependency check, vet, tests, "
        "known-vulnerability scan, Docker build and smoke test. First run: all green.",
        "Manually verified end to end with real Redis and SearXNG containers: login, cache "
        "hit/miss/stale, refresh, free cache answers, 402, network isolation, graceful stop.",
    ])
    st.append(PageBreak())

    # ---- 12 next
    st += [P("12. What comes next", "h1"),
           table([
               ["Phase", "Steps", "Content"],
               ["1 — search service", "1–6 ✓", "Everything in this document"],
               ["2 — users", "7–11", "User profile (/me); plans + payment provider; saved cart "
                                    "with notes and price-drop tracking; purchase reports "
                                    "(shop, date, price) with a friendly “did you buy it?” "
                                    "reminder; notifications (in-app + push). Adds Postgres and "
                                    "a Redis job queue."],
               ["3 — more sources, launch", "12–15", "eBay and AliExpress APIs (real prices, "
                                                    "affiliate income) when keys are approved; "
                                                    "deployment with HTTPS; access for AI agents "
                                                    "(OpenAPI / MCP tool)."],
               ["Future feature", "noted", "<b>Search by photo (Google Lens):</b> the user takes a "
                                           "photo, the service finds the product and its offers in "
                                           "the shops of their country (same cache, limits and "
                                           "answer format). With the mobile app."],
           ], [38 * mm, 18 * mm, CONTENT_W - 56 * mm], first_col_bold=True),
           Spacer(1, 6),
           callout("<b>Right now:</b> run the stack locally (STATUS.md), then report back with "
                   "the feedback checklist in STATUS.md — results per country, response times, "
                   "any errors, and anything to change in the response format before the app is "
                   "built on it.", fill=ORANGE, edge=ORANGE_D),
           Spacer(1, 10)]

    # ---- 13 glossary
    st += [CondPageBreak(90 * mm), P("13. Glossary", "h1"),
           table([
               ["Word", "Meaning"],
               ["API", "The set of web addresses the app calls (e.g. /api/v1/search)."],
               ["JWT", "Signed token proving who the user is; issued by the auth provider."],
               ["JWKS", "The auth provider's public keys, used to check a JWT's signature."],
               ["Cache hit / miss", "Answer found in / not in the stored answers."],
               ["Stale", "A cached answer older than the fresh period, still usable while being refreshed."],
               ["Provider", "A search source: SearXNG (free), SerpApi or Apify (paid)."],
               ["Scraper (Apify Actor)", "A ready-made program that reads a shop's website and returns products."],
               ["Fallback", "Using the next provider when the first one fails."],
               ["Circuit breaker", "Temporarily skipping a source that keeps failing, so it does not slow every request."],
               ["Rate limit / allowance", "Speed limit per second / number of live searches per day."],
               ["Goroutine", "A lightweight thread in Go; used to call providers in parallel."],
               ["Redis", "Very fast in-memory database; holds counters and cached answers."],
               ["SearXNG", "Open-source search engine that combines results from many search engines."],
               ["Docker / compose", "Packages each program in a container; compose starts them all together."],
               ["CI", "Automatic checks on GitHub after every push."],
               ["Distroless", "A minimal container image without a shell — less to attack."],
           ], [38 * mm, CONTENT_W - 38 * mm], first_col_bold=True)]

    doc = SimpleDocTemplate(OUT, pagesize=A4, leftMargin=MARGIN, rightMargin=MARGIN,
                            topMargin=18 * mm, bottomMargin=16 * mm,
                            title="owis_find_deal_engine — architecture and flows",
                            author="owis_find_deal_engine", subject="Architecture documentation")
    doc.build(st, onFirstPage=on_page, onLaterPages=on_page)
    print("written", OUT)


if __name__ == "__main__":
    build()
