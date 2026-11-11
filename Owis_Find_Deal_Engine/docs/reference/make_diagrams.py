"""Draws the diagrams of the Full Service Reference as SVG files.

    python3 docs/reference/make_diagrams.py      # writes docs/reference/img/*.svg

No dependencies. Each diagram is a function below; edit the boxes and
arrows there and run the script again.
"""
from __future__ import annotations

import html
import os

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "img")

# Colours per role: (fill, stroke, text)
ROLE = {
    "client": ("#e8f1fd", "#2f6fd6", "#123a73"),
    "edge": ("#fff2e2", "#e08a1e", "#6b3d06"),
    "server": ("#e6f6ee", "#2a9a62", "#0f4a2d"),
    "store": ("#f1eafb", "#7b52c7", "#3c2266"),
    "ext": ("#f1f2f4", "#7a8090", "#2d323c"),
    "warn": ("#fdecec", "#d0453f", "#6e1512"),
    "note": ("#fffbe6", "#c9a21a", "#5a4706"),
    "plain": ("#ffffff", "#9aa1ad", "#1f2430"),
}
FONT = "Inter, 'Segoe UI', Helvetica, Arial, sans-serif"
MONO = "'JetBrains Mono', Consolas, 'Courier New', monospace"


class SVG:
    def __init__(self, w: int, h: int, title: str):
        self.w, self.h, self.title = w, h, title
        self.parts: list[str] = []

    # --- primitives -------------------------------------------------------
    def rect(self, x, y, w, h, role="plain", rx=10, dash=False, width=1.6, fill=None):
        f, s, _ = ROLE[role]
        d = ' stroke-dasharray="6 5"' if dash else ""
        self.parts.append(
            f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" fill="{fill or f}" '
            f'stroke="{s}" stroke-width="{width}"{d}/>'
        )

    def text(self, x, y, s, size=13, weight=400, color="#1f2430", anchor="middle", mono=False, italic=False):
        fam = MONO if mono else FONT
        st = ' font-style="italic"' if italic else ""
        self.parts.append(
            f'<text x="{x}" y="{y}" font-family="{fam}" font-size="{size}" font-weight="{weight}" '
            f'fill="{color}" text-anchor="{anchor}"{st}>{html.escape(s)}</text>'
        )

    def box(self, x, y, w, h, title, lines=(), role="server", mono_lines=False, size=14, dash=False):
        self.rect(x, y, w, h, role, dash=dash)
        _, _, tc = ROLE[role]
        n = len(lines)
        top = y + h / 2 - (n * 15) / 2 + (4 if n == 0 else -2)
        self.text(x + w / 2, top + (0 if n else 0), title, size=size, weight=700, color=tc)
        for i, line in enumerate(lines):
            self.text(x + w / 2, top + 18 + i * 15, line, size=11.5, color=tc, mono=mono_lines)

    def group(self, x, y, w, h, label, role="plain", dash=True, pos="tl"):
        self.rect(x, y, w, h, role, rx=14, dash=dash, width=1.4, fill="none")
        _, s, _ = ROLE[role]
        if pos == "tr":
            self.text(x + w - 14, y + 20, label, size=12.5, weight=700, color=s, anchor="end")
        elif pos == "bl":
            self.text(x + 14, y + h - 10, label, size=12.5, weight=700, color=s, anchor="start")
        else:
            self.text(x + 14, y + 20, label, size=12.5, weight=700, color=s, anchor="start")

    def arrow(self, pts, label="", color="#4a5262", dash=False, both=False, lx=None, ly=None, lanchor="middle",
              lsize=11.5):
        d = "M " + " L ".join(f"{px} {py}" for px, py in pts)
        da = ' stroke-dasharray="6 5"' if dash else ""
        ms = ' marker-start="url(#arrS)"' if both else ""
        self.parts.append(
            f'<path d="{d}" fill="none" stroke="{color}" stroke-width="1.6"{da} marker-end="url(#arr)"{ms}/>'
        )
        if label:
            if lx is None:
                (x1, y1), (x2, y2) = pts[len(pts) // 2 - 1], pts[len(pts) // 2]
                lx, ly = (x1 + x2) / 2, (y1 + y2) / 2 - 6
            self.label(lx, ly, label, lanchor, lsize)

    def label(self, x, y, s, anchor="middle", size=11.5):
        wdt = len(s) * size * 0.55 + 8
        if anchor == "middle" and hasattr(self, "w") and self.w:
            x = min(max(x, wdt / 2 + 8), self.w - wdt / 2 - 8)
        bx = x - wdt / 2 if anchor == "middle" else (x - 4 if anchor == "start" else x - wdt + 4)
        self.parts.append(f'<rect x="{bx}" y="{y - size}" width="{wdt}" height="{size + 5}" rx="3" fill="#ffffff" opacity="0.92"/>')
        self.text(x, y, s, size=size, color="#3a4150", anchor=anchor)

    def save(self, name):
        os.makedirs(OUT, exist_ok=True)
        head = (
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {self.w} {self.h}" width="{self.w}" height="{self.h}" '
            f'role="img" aria-label="{html.escape(self.title)}">\n<title>{html.escape(self.title)}</title>\n'
            '<defs><marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">'
            '<path d="M0 0 L10 5 L0 10 z" fill="#4a5262"/></marker>'
            '<marker id="arrS" viewBox="0 0 10 10" refX="1" refY="5" markerWidth="7" markerHeight="7" orient="auto">'
            '<path d="M10 0 L0 5 L10 10 z" fill="#4a5262"/></marker></defs>\n'
            f'<rect width="{self.w}" height="{self.h}" fill="#ffffff"/>\n'
        )
        with open(os.path.join(OUT, name), "w", encoding="utf-8") as f:
            f.write(head + "\n".join(self.parts) + "\n</svg>\n")


class Seq(SVG):
    """A sequence diagram: lifelines and messages, top to bottom."""

    def __init__(self, w, title, actors, top=20, gap=None):
        self.actors = actors  # list of (name, role)
        self.margin = 95
        self.gap = gap or (w - 2 * self.margin) / max(len(actors) - 1, 1)
        self.y = top + 70
        self.top = top
        self.rows: list = []
        super().__init__(w, 0, title)

    def x(self, i):
        return self.margin + i * self.gap

    def msg(self, a, b, text, dash=False, note=None, step=34):
        self.rows.append(("msg", a, b, text, dash, self.y))
        self.y += step
        if note:
            self.rows.append(("note", a, b, note, False, self.y - 14))
            self.y += 18

    def self_note(self, a, text, role="note", step=30, width=None):
        self.rows.append(("self", a, a, text, role, self.y, width))
        self.y += step

    def divider(self, text, step=34):
        self.rows.append(("div", 0, 0, text, False, self.y))
        self.y += step

    def finish(self, name):
        self.h = int(self.y + 30)
        for i, (n, role) in enumerate(self.actors):
            x = self.x(i)
            self.parts.append(
                f'<line x1="{x}" y1="{self.top + 46}" x2="{x}" y2="{self.h - 16}" stroke="#b9bfca" stroke-width="1.3" stroke-dasharray="4 4"/>'
            )
            bw = min(170, self.gap - 12)
            self.box(x - bw / 2, self.top, bw, 44, n, role=role, size=12.5)
        for r in self.rows:
            kind = r[0]
            if kind == "msg":
                _, a, b, t, dash, y = r
                x1, x2 = self.x(a), self.x(b)
                pad = 4 if x2 > x1 else -4
                self.arrow([(x1 + pad, y), (x2 - pad, y)], dash=dash)
                lx = (x1 + x2) / 2
                self.label(lx, y - 6, t)
            elif kind == "note":
                _, a, b, t, _, y = r
                lx = (self.x(a) + self.x(b)) / 2
                self.text(lx, y + 8, t, size=10.5, color="#6a7180", italic=True)
            elif kind == "self":
                _, a, _, t, role, y, width = r
                w = width or max(len(t) * 6.4 + 20, 120)
                x = min(max(self.x(a), w / 2 + 8), self.w - w / 2 - 8)
                self.rect(x - w / 2, y - 16, w, 24, role, rx=6)
                self.text(x, y + 1, t, size=11, color=ROLE[role][2])
            elif kind == "div":
                _, _, _, t, _, y = r
                self.parts.append(f'<line x1="20" y1="{y}" x2="{self.w - 20}" y2="{y}" stroke="#d6dae1" stroke-width="1"/>')
                self.label(self.w / 2, y + 4, t, size=11.5)
        self.save(name)


# ---------------------------------------------------------------------------
# 1. The big picture
def arch_overview():
    s = SVG(1100, 690, "Architecture overview")
    s.text(550, 30, "Global Product Search — who talks to whom", size=18, weight=700)
    callers = [
        ("Dawirli app users", ["Firebase login token", "(JWT)"]),
        ("API clients", ["own servers", "API key ofd_live_…"]),
        ("AI agents", ["MCP: POST /mcp", "API key"]),
        ("Owner / admin", ["browser, /admin/", "via SSH tunnel"]),
    ]
    for i, (t, l) in enumerate(callers):
        s.box(40 + i * 260, 55, 220, 70, t, l, role="client")
    s.group(40, 150, 900, 90, "Cloudflare (dawirli.com)", role="edge", pos="tr")
    s.box(60, 178, 260, 50, "DNS + HTTPS", ["globalprodsearch.dawirli.com"], role="edge", size=13)
    s.box(340, 178, 260, 50, "WAF rule", ["/admin blocked (403)"], role="edge", size=13)
    s.box(620, 178, 300, 50, "Tunnel ofd-prod", ["no open web port on the VM"], role="edge", size=13)
    for i in range(3):
        s.arrow([(150 + i * 260, 125), (150 + i * 260, 176)])
    # VM
    s.group(40, 265, 1020, 290, "Oracle Cloud VM (Dubai) — Docker Compose stack", role="server", pos="bl")
    s.box(700, 290, 220, 55, "cloudflared", ["10.203.20.10"], role="edge", size=13)
    s.arrow([(810, 230), (810, 288)])
    s.box(330, 290, 320, 125, "server (Go)", ["API · async jobs · queue workers", "cache · metering · webhooks",
                                               "MCP · admin API + admin web app", "port 3002"], role="server")
    s.arrow([(698, 318), (652, 318)], "HTTP :3002", lx=675, ly=310)
    s.arrow([(930, 127), (930, 140), (990, 140), (990, 385), (652, 385)], "SSH -L 3012 → :3002 (admin)", dash=True,
            lx=820, ly=378)
    s.box(70, 450, 190, 60, "Redis 7", ["cache · usage · jobs · queue"], role="store", size=13)
    s.box(290, 450, 190, 60, "Postgres 17", ["settings · admins · clients"], role="store", size=13)
    s.box(510, 450, 190, 60, "SearXNG", ["free web search"], role="store", size=13)
    s.arrow([(380, 417), (165, 448)])
    s.arrow([(420, 417), (385, 448)])
    s.arrow([(500, 417), (605, 448)])
    # outbound bus
    s.arrow([(640, 417), (640, 432), (740, 432), (740, 572)], "outbound HTTPS", dash=True, lx=800, ly=470,
            lanchor="middle")
    s.group(40, 580, 1020, 95, "Outside services (internet)", role="ext", pos="tr")
    ext = [("Firebase", "login keys (JWKS)"), ("SerpApi", "Amazon · eBay"), ("Apify", "AliExpress · Temu"),
           ("GitHub", "code · CI · ghcr.io image")]
    for i, (t, l) in enumerate(ext):
        s.box(60 + i * 250, 610, 220, 50, t, [l], role="ext", size=13)
    s.parts.append('<path d="M 170 592 L 740 592" stroke="#4a5262" stroke-width="1.6" stroke-dasharray="6 5" fill="none"/>')
    for x in (170, 420, 670):
        s.arrow([(x, 592), (x, 608)], dash=True)
    s.arrow([(920, 608), (920, 540)], "deploy.sh pulls the image", dash=True, lx=920, ly=572)
    s.save("01_architecture.svg")


# 2. Deployment and networks
def deploy_network():
    s = SVG(1100, 700, "Production deployment and Docker networks")
    s.text(550, 30, "Production: one Oracle VM, four containers (+ cloudflared), three private networks", size=17,
           weight=700)
    s.box(40, 60, 250, 70, "Internet callers", ["https://globalprodsearch.dawirli.com"], role="client", size=13)
    s.box(420, 60, 260, 70, "Cloudflare edge", ["TLS, WAF (/admin → 403)", "timeout 125 s per request"], role="edge",
          size=13)
    s.box(810, 60, 250, 70, "Owner PC (Git Bash)", ["ssh -i owis_oracle.key ubuntu@IP", "port 22 only"],
          role="client", size=13)
    s.arrow([(290, 95), (418, 95)], "HTTPS")
    s.group(30, 160, 1040, 520, "VM.Standard.A1.Flex · Ubuntu 24.04 arm64 · 2 OCPU / 12 GB · /opt/ofd/repo", role="server",
            dash=False)
    s.arrow([(550, 130), (550, 160), (180, 160), (180, 228)], "outbound tunnel (QUIC/HTTP2)", lx=360, ly=154)
    # tunnel net
    s.group(60, 200, 360, 160, "network: tunnel  10.203.20.0/28", role="edge", pos="bl")
    s.box(90, 232, 170, 60, "cloudflared", ["10.203.20.10", "128 MB"], role="edge", size=13)
    s.text(240, 312, "only address whose CF-Connecting-IP", size=11, color="#6b3d06")
    s.text(240, 327, "header is trusted (TRUSTED_PROXIES)", size=11, color="#6b3d06")
    # server
    s.box(470, 230, 220, 110, "server", ["ghcr.io/owisdev/", "owis_find_deal_engine:<commit>",
                                            "256 MB · read-only · non-root", "networks: edge, data, tunnel"],
          role="server", size=14)
    s.arrow([(262, 262), (468, 262)], "http://server:3002")
    s.box(760, 230, 270, 60, "127.0.0.1:3002 (VM only)", ["admin & API via SSH -L 3012:…:3002"], role="plain",
          size=12.5)
    s.arrow([(692, 262), (758, 262)])
    s.arrow([(935, 130), (935, 228)], "SSH tunnel", dash=True)
    # edge net
    s.group(450, 375, 590, 120, "network: edge (internet access)", role="ext", pos="bl")
    s.box(480, 410, 200, 65, "SearXNG", ["512 MB · settings.yml"], role="store", size=13)
    s.box(730, 410, 280, 65, "outbound HTTPS", ["SerpApi · Apify · Firebase JWKS", "webhook receivers"], role="ext",
          size=12.5)
    s.arrow([(560, 342), (560, 408)])
    s.arrow([(640, 342), (640, 360), (870, 360), (870, 408)])
    # data net
    s.group(60, 520, 700, 145, "network: data (internal — no internet, nothing published)", role="store", pos="bl")
    s.box(90, 540, 300, 85, "Redis 7", ["256 MB, AOF, volatile-ttl", "cache · usage · jobs · queue", "volume redis-data"],
          role="store", size=13)
    s.box(430, 540, 300, 85, "Postgres 17", ["settings · admins · audit", "usage · API clients",
                                             "volume postgres-data"], role="store", size=13)
    s.arrow([(530, 342), (530, 370), (240, 370), (240, 538)])
    s.arrow([(600, 342), (600, 365), (580, 365), (580, 538)])
    s.box(800, 525, 240, 140, "Secrets", [".env (mode 600)", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "SETTINGS_ENCRYPTION_KEY",
                                          "SEARXNG_SECRET · TUNNEL_TOKEN"], role="warn", size=13, mono_lines=True)
    s.save("02_deployment.svg")


# 3. Release pipeline
def cicd():
    s = SVG(1100, 360, "From code change to production")
    s.text(550, 30, "Release flow: branch → pull request → CI → main → image → deploy.sh", size=17, weight=700)
    steps = [
        ("Work branch", ["claude/… (Claude Code)", "commits + tests"], "client"),
        ("Pull request", ["into main", "owner reviews"], "client"),
        ("GitHub Actions", ["gofmt · vet · test -race", "admin web build · audit", "docker build amd64+arm64"],
         "ext"),
        ("Merge to main", ["CI again on main", "publishes the image"], "client"),
        ("ghcr.io", ["owis_find_deal_engine", ":<commit>  and  :main"], "store"),
    ]
    for i, (t, l, r) in enumerate(steps):
        s.box(30 + i * 215, 70, 190, 95, t, l, role=r, size=13.5)
        if i:
            s.arrow([(30 + i * 215 - 25, 117), (30 + i * 215 - 2, 117)])
    s.box(250, 220, 600, 110, "On the VM:  deploy/deploy.sh [commit]", [
        "1. find the commit  ·  2. move /opt/ofd/repo to it  ·  3. docker compose pull",
        "4. compose up -d  ·  5. wait until healthy (+ tunnel connected)",
        "logged in /opt/ofd/deploys.log · .env never touched",
        "roll back: deploy/deploy.sh <older commit>"], role="server", size=14)
    s.arrow([(955, 167), (955, 275), (852, 275)], "docker pull", lx=955, ly=225)
    s.arrow([(250, 300), (120, 300), (120, 170)], "next change", dash=True, lx=150, ly=292)
    s.save("03_release.svg")


# 4. Code map
def code_map():
    s = SVG(1100, 760, "Go packages and how they call each other")
    s.text(550, 30, "Code map — server/ (module owis_find_deal_engine, Go 1.26)", size=17, weight=700)
    s.box(380, 50, 340, 60, "cmd/owis_find_deal_engine", ["main.go: wiring, settings hot swap, shutdown",
                                                          "admincmd.go: settings / admins / clients CLI"],
          role="client", size=13)
    s.group(30, 130, 1040, 150, "internal/api — HTTP layer", role="server")
    api = [("router.go", ["public routes", "Deps, NewRouter"]), ("middleware.go", ["request id, log,", "CORS, recover"]),
           ("access.go", ["requireCaller:", "JWT or API key"]), ("handlers.go", ["POST /search", "(waiting)"]),
           ("searches.go", ["POST /searches", "GET /searches/{id}"]), ("jobwork.go", ["job work, webhooks,", "ResumeJobs"]),
           ("mcp.go", ["POST /mcp tools"]), ("admin*.go", ["/admin/v1/*", "clients, tools"])]
    for i, (t, l) in enumerate(api):
        s.box(45 + i * 128, 160, 118, 100, t, l, role="server", size=12)
    s.arrow([(550, 112), (550, 158)])
    # middle
    s.box(60, 320, 210, 70, "internal/metering", ["counted once · cached free", "refund · Continue"], role="server",
          size=12.5)
    s.box(310, 320, 230, 70, "internal/jobs", ["Job, Runner, Store", "progress saves · Resume"], role="server",
          size=12.5)
    s.box(580, 320, 210, 70, "internal/webhook", ["secrets · HMAC sign", "Sender (retries)"], role="server", size=12.5)
    s.box(830, 320, 220, 70, "internal/auth · apikeys", ["JWT via JWKS", "API keys (SHA-256)"], role="server",
          size=12.5)
    s.arrow([(165, 262), (165, 318)])
    s.arrow([(420, 262), (420, 318)])
    s.arrow([(680, 262), (680, 318)])
    s.arrow([(940, 262), (940, 318)])
    s.box(60, 420, 210, 75, "internal/cache", ["per shop · fresh/stale", "last_known · SearchProgress",
                                               "RunTask"], role="server", size=12.5)
    s.arrow([(165, 392), (165, 418)])
    s.box(310, 420, 230, 75, "internal/queue", ["one task per shop", "caps · join · heartbeats",
                                                "Redis Lua / memory"], role="server", size=12.5)
    s.arrow([(272, 455), (308, 455)], "Do")
    s.arrow([(425, 497), (425, 520), (165, 520), (165, 497)], "RunTask", dash=True, lx=300, ly=515)
    s.box(60, 545, 480, 75, "internal/search", ["Service.Search: per-shop time limit, merge, dedupe, sort",
                                                "fallback.go (web chain + circuit breaker) · probe.go (admin test)",
                                                "instrument.go (cost / calls → stats)"], role="server", size=12.5)
    s.arrow([(120, 497), (120, 543)])
    s.group(60, 640, 480, 105, "internal/providers", role="ext")
    for i, (t, l) in enumerate([("serpapi", "Amazon · eBay · Google"), ("apify", "AliExpress · Temu"),
                                ("searxng", "web (free)")]):
        s.box(75 + i * 155, 670, 145, 60, t, [l], role="ext", size=12.5)
    s.arrow([(300, 622), (300, 668)])
    # side column
    side = [("internal/config", "settings: env or DB, validation"), ("internal/settings", "encrypt, Set, Watch (hot swap)"),
            ("internal/store", "Postgres + migrations"), ("internal/markets", "countries → shops (markets.json)"),
            ("internal/usage", "plans, daily quotas"), ("internal/ratelimit", "token bucket"),
            ("internal/stats", "usage, cost, budget"), ("internal/devdocs · adminui", "/docs, OpenAPI, /admin/ app")]
    for i, (t, l) in enumerate(side):
        s.box(600, 420 + i * 41, 450, 35, t + "  —  " + l, role="store", size=12)
    s.save("04_code_map.svg")


# 5. Request pipeline
def pipeline():
    s = SVG(1100, 330, "What every public request goes through")
    s.text(550, 30, "Request pipeline (internal/api) — outside to inside", size=17, weight=700)
    items = [("WithClientIP", "CF-Connecting-IP", "from cloudflared only"), ("writeDeadline", "per-request write", "timeout"),
             ("requestID", "X-Request-ID", ""), ("logRequests", "one JSON log line", "(no secrets)"),
             ("recoverPanics", "500, never crash", ""), ("cors", "allowed browser", "origins"),
             ("requireCaller", "JWT (Firebase) or", "API key + scope"), ("rateLimit", "burst per user /", "client → 429")]
    for i, (t, a, b) in enumerate(items):
        x = 25 + i * 134
        s.box(x, 60, 122, 85, t, [a, b] if b else [a], role="server" if i < 6 else "warn", size=12)
        if i:
            s.arrow([(x - 10, 102), (x - 1, 102)])
    s.box(150, 190, 260, 110, "handler", ["POST /search · POST /searches", "GET /searches/{id} · POST /mcp",
                                          "country allowed? title 2–200?"], role="client", size=13.5)
    s.arrow([(1050, 147), (1050, 170), (280, 170), (280, 188)])
    s.box(470, 190, 270, 110, "metering", ["cached answer → free", "else Take 1 from daily allowance",
                                           "→ 402 / 429 when used up", "failed / all cached → given back"],
          role="server", size=13.5)
    s.arrow([(412, 245), (468, 245)])
    s.box(800, 190, 260, 110, "cache → queue → shops", ["only shops without an answer", "are searched live",
                                                       "results stored per shop"], role="store", size=13.5)
    s.arrow([(742, 245), (798, 245)])
    s.save("05_pipeline.svg")


# 6. Cache freshness
def cache_timeline():
    s = SVG(1100, 300, "Cache freshness of one shop's results")
    s.text(550, 30, "One shop's results in the cache (key shop:v7:<shop>:<hash>) — age since fetched", size=17,
           weight=700)
    segs = [(60, 380, "fresh (≤ 48 h)", "served from cache, free", "client"),
            (380, 560, "stale (48–72 h)", "served + refreshed in background", "edge"),
            (560, 920, "kept as backup (≤ 7 days)", "searched live; shown as last_known", "store"),
            (920, 1040, "gone", "", "ext")]
    for x1, x2, t, sub, r in segs:
        s.rect(x1, 70, x2 - x1, 60, r, rx=4)
        s.text((x1 + x2) / 2, 96, t, size=13, weight=700, color=ROLE[r][2])
        if sub:
            s.text((x1 + x2) / 2, 115, sub, size=11, color=ROLE[r][2])
    for x, l in [(60, "0"), (380, "CACHE_FRESH_TTL 48h"), (560, "CACHE_STALE_TTL 72h"), (920, "CACHE_LAST_KNOWN_TTL 168h")]:
        s.parts.append(f'<line x1="{x}" y1="130" x2="{x}" y2="150" stroke="#4a5262" stroke-width="1.5"/>')
        s.text(x, 166, l, size=11.5, mono=True)
    notes = ["• A shop that found nothing is fresh only CACHE_EMPTY_TTL (30 min). Failures are never stored.",
             "• \"refresh\": true forces a live search at most once per CACHE_MIN_REFRESH (10 min) per shop.",
             "• Countries share entries when the shop request is the same (AliExpress, Temu everywhere; amazon.com for usa + jor).",
             "• Key = shop + site + source setup + normalized title (+ region for web shops). CACHE_GENERATION drops everything."]
    for i, n in enumerate(notes):
        s.text(60, 200 + i * 22, n, size=12.5, anchor="start")
    s.save("06_cache.svg")


# 7. Queue lifecycle
def queue_lifecycle():
    s = SVG(1100, 560, "Life of a queue task")
    s.text(550, 30, "Queue task (internal/queue) — one per shop + title", size=17, weight=700)
    s.box(40, 70, 230, 80, "Enqueue (claim)", ["id = the shop's cache key", "same id queued/running → join",
                                               "done well < 10 s ago → join"], role="client", size=13)
    s.box(330, 60, 260, 100, "queued", ["Redis lists owis:q:hi:<class>", "(searches) and owis:q:lo:<class>",
                                        "(background refreshes)", "a search joining a refresh → hi"], role="edge", size=14)
    s.arrow([(272, 110), (328, 110)], "new")
    s.box(650, 60, 400, 100, "running", ["worker slot free? apify 4 · serpapi 10 · web 10",
                                         "heartbeat owis:q:beat:<id> every 5 s (TTL 20 s)",
                                         "shop's time limit starts now",
                                         "cache.RunTask → search one shop → store"], role="server", size=14)
    s.arrow([(592, 110), (648, 110)], "pop (Lua)")
    s.box(650, 230, 400, 80, "done", ["outcome ok / no_results / error", "kept 10 min for waiters; good ones joinable 10 s",
                                      "waiters woken (same server) or poll every 1 s"], role="store", size=14)
    s.arrow([(850, 162), (850, 228)], "finish")
    s.box(330, 230, 260, 80, "error: waited too long", ["queued > QUEUE_MAX_WAIT (60 s)", "→ shop shows last_known / error"],
          role="warn", size=13)
    s.arrow([(460, 162), (460, 228)])
    s.arrow([(592, 270), (648, 270)])
    s.box(40, 380, 470, 150, "Server stops or crashes while running", [
        "clean stop (SIGTERM): task put back in front, attempt + 1",
        "crash: heartbeat expires → another start finds the orphan",
        "attempt 2 allowed if the task is < 3 min old (RetryMaxAge)",
        "otherwise → error \"the server stopped during this shop's search\"",
        "(a re-run Apify search is paid twice)"], role="warn", size=13.5)
    s.arrow([(680, 162), (680, 200), (620, 200), (620, 350), (275, 350), (275, 378)], "server stops", dash=True,
            lx=450, ly=345)
    s.arrow([(300, 378), (300, 185), (360, 185), (360, 162)], "attempt 2", dash=True, lx=300, ly=290)
    s.box(560, 380, 490, 150, "Who waits for a task", [
        "waiting search (POST /search): up to search timeout + 5 s",
        "async job / MCP: until the job deadline (slowest shop + 60 s + 15 s)",
        "background refresh: up to search timeout + 5 s",
        "a waiter that gives up does NOT stop the task:",
        "it finishes and fills the cache for the next search"], role="note", size=13.5)
    s.save("07_queue.svg")


# 8. Sync search sequence
def sync_seq():
    q = Seq(1100, "Waiting (synchronous) search", [("App / client", "client"), ("server API", "server"),
                                                 ("metering", "server"), ("cache", "store"), ("queue + workers", "server"),
                                                 ("shops (SerpApi / Apify)", "ext")])
    q.msg(0, 1, "POST /api/v1/search {title, country}")
    q.msg(1, 2, "Search(user, req)")
    q.msg(2, 3, "Lookup: every shop cached?")
    q.self_note(3, "yes → answer at once, free (X-Cache: HIT)", width=290)
    q.msg(2, 2 if False else 3, "no → Take 1 from the allowance, then Search")
    q.msg(3, 4, "one task per shop without an answer (Do)")
    q.msg(4, 5, "amazon (≤ 15 s), aliexpress (≤ 90 s), temu (≤ 45 s)")
    q.msg(5, 4, "products", dash=True)
    q.msg(4, 3, "outcome per shop · stored in the cache", dash=True)
    q.self_note(3, "not done in time → last_known (≤ 7 days) or error", width=330)
    q.msg(3, 2, "merged result", dash=True)
    q.self_note(2, "all failed or all cached → allowance given back", width=320)
    q.msg(2, 1, "outcome + quota", dash=True)
    q.msg(1, 0, "200 {results, markets, shops, fetched_at} + X-RateLimit-*", dash=True)
    q.divider("waits for the slowest shop: up to 92 s in production (Cloudflare cuts at 125 s)")
    q.finish("08_sync.svg")


# 9. Async sequence
def async_seq():
    q = Seq(1100, "Asynchronous search with progress and webhooks", [("App / API client", "client"),
                                                                   ("server API", "server"), ("job runner", "server"),
                                                                   ("cache + queue", "store"), ("shops", "ext"),
                                                                   ("webhook receiver", "client")])
    q.msg(0, 1, "POST /api/v1/searches {title, country, webhook_url?, webhook_events?}")
    q.self_note(1, "checks at once: input, scope, country, allowance (no job if refused)", width=430)
    q.msg(1, 2, "Start job (Redis owis:job:<id>, set owis:jobs:running)")
    q.msg(1, 0, "202 {id: srch_…, status: running}  Location, Retry-After: 2", dash=True)
    q.msg(2, 3, "SearchProgress: cached shops now, live shops → queue")
    q.self_note(2, "save: cached shops ok, the rest pending", width=300)
    q.msg(0, 1, "GET /api/v1/searches/{id}  (every ~2 s)")
    q.msg(1, 0, "200 {status: running, result: {amazon ok, temu pending, …}}", dash=True)
    q.msg(3, 4, "live searches, each within its time limit")
    q.msg(4, 3, "amazon done (~3 s)", dash=True)
    q.msg(3, 2, "progress", dash=True)
    q.self_note(2, "save · fast shops done → first_results", width=300)
    q.msg(2, 5, "POST search.first_results (signed, if asked)")
    q.msg(4, 3, "temu (~25 s), aliexpress (~85 s)", dash=True)
    q.msg(3, 2, "final result", dash=True)
    q.self_note(2, "job done (counted once) · removed from running list", width=360)
    q.msg(2, 5, "POST search.completed / search.failed (after first_results)")
    q.msg(0, 1, "GET /api/v1/searches/{id}")
    q.msg(1, 0, "200 {status: done, result, quota, webhook: {…}}", dash=True)
    q.finish("09_async.svg")


# 10. Webhook
def webhook_seq():
    q = Seq(1100, "Webhook delivery and signature check", [("admin panel", "client"), ("server", "server"),
                                                          ("Postgres", "store"), ("client's receiver", "client")])
    q.msg(0, 1, "API clients → Webhook secret")
    q.msg(1, 2, "store random salt (32 bytes), prefix")
    q.msg(1, 0, "whsec_… shown once", dash=True)
    q.self_note(1, "secret = HMAC(key from SETTINGS_ENCRYPTION_KEY, salt) — never stored", width=470)
    q.divider("later: a job with webhook_url ends (or its fast shops are done)")
    q.msg(1, 3, "POST body = job JSON   X-OFD-Event · X-OFD-Delivery · X-OFD-Signature")
    q.self_note(3, "v1 = HMAC-SHA256(secret, t + \".\" + raw body); refuse t older than 5 min", width=470)
    q.msg(3, 1, "2xx within 10 s → delivered", dash=True)
    q.msg(1, 3, "no 2xx / network error / 408 / 429 / 5xx → retry after 5 s, then 30 s")
    q.self_note(1, "job shows webhook.status and webhook.first_results.status", width=380)
    q.finish("10_webhook.svg")


# 11. Restart and resume
def restart_seq():
    q = Seq(1100, "Server restart during an asynchronous search", [("caller", "client"), ("server (old)", "server"),
                                                                  ("Redis", "store"), ("server (new)", "server"),
                                                                  ("Apify", "ext")])
    q.msg(0, 1, "POST /searches → job running")
    q.msg(1, 4, "aliexpress run starts (attempt 1)")
    q.msg(1, 2, "job saved: amazon ok, aliexpress pending")
    q.self_note(1, "SIGTERM (deploy / docker restart)", role="warn", width=260)
    q.msg(1, 2, "job left running (cause ErrResumeLater: stays counted)")
    q.msg(1, 2, "task put back in front, attempt 2")
    q.divider("new container starts")
    q.msg(3, 2, "queue: pick up the task (attempt 2, < 3 min old)")
    q.msg(3, 4, "aliexpress run again (paid twice)")
    q.msg(3, 2, "ResumeJobs: read owis:jobs:running")
    q.self_note(3, "job work again: only pending shops, joins the task, not counted again", width=460)
    q.msg(4, 3, "products", dash=True)
    q.msg(3, 2, "job done (merged with amazon from before)")
    q.msg(0, 3, "GET /searches/{id}")
    q.msg(3, 0, "200 status done", dash=True)
    q.finish("11_restart.svg")


# 12. Admin
def admin_access():
    s = SVG(1100, 330, "How the owner reaches the admin panel")
    s.text(550, 30, "Admin panel access (until Cloudflare Access, step 6)", size=17, weight=700)
    s.box(40, 70, 230, 90, "Owner PC", ["ssh -i ~/.ssh/owis_oracle.key", "-L 3012:127.0.0.1:3002",
                                        "ubuntu@<public IP>"], role="client", size=13, mono_lines=True)
    s.box(330, 70, 230, 90, "Browser", ["http://localhost:3012/admin/", "React app (embedded)"], role="client",
          size=13)
    s.box(620, 70, 220, 90, "VM :22 → 127.0.0.1:3002", ["server container"], role="server", size=13)
    s.box(880, 70, 190, 90, "Firebase", ["email + password", "sign-in (ID token)"], role="ext", size=13)
    s.arrow([(272, 115), (328, 115)])
    s.arrow([(562, 115), (618, 115)], "SSH tunnel")
    s.arrow([(445, 162), (445, 200), (975, 200), (975, 162)], "sign in", dash=True, lx=700, ly=195)
    s.box(40, 230, 1030, 80, "Server checks on every /admin/v1 call", [
        "valid Firebase ID token · signed in < ADMIN_SESSION_MAX_AGE (12 h) · active entry in the admin list (linked to the login)",
        "(off for now, step 5: ADMIN_REQUIRE_2FA, ADMIN_REQUIRE_VERIFIED_EMAIL) · rate limits · every change in the audit log"],
          role="warn", size=13.5)
    s.save("12_admin.svg")


if __name__ == "__main__":
    for fn in (arch_overview, deploy_network, cicd, code_map, pipeline, cache_timeline, queue_lifecycle, sync_seq,
               async_seq, webhook_seq, restart_seq, admin_access):
        fn()
    print("written to", OUT)
