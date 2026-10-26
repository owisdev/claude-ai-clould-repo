"""Builds docs/step13_guide.pdf: what phase 3 step 13 (MCP endpoint for AI
agents, OpenAPI 3.1 description, developer docs page) does, and how the
owner tests it on the Docker stack, step by step with the commands.

    pip install reportlab
    python3 docs/build_step13_guide_pdf.py

Styles and drawing helpers come from build_architecture_pdf.py and
build_step12_guide_pdf.py.
"""

import os

from build_architecture_pdf import (
    BLUE, BLUE_D, GREEN, GREEN_D, GREY, GREY_D, MARGIN, MUTED, NAVY, ORANGE, ORANGE_D, PAGE_H, PAGE_W,
    PURPLE, PURPLE_D, A4, P, arrow, box, bullets, callout, caption, numbered, text,
)
from build_step12_guide_pdf import BRANCH, OK, W, T, code, h1, step
from reportlab.graphics.shapes import Drawing
from reportlab.lib import colors
from reportlab.lib.units import mm
from reportlab.platypus import KeepTogether, PageBreak, SimpleDocTemplate, Spacer

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "step13_guide.pdf")
DATE = "26 October 2026"
TITLE = "owis_find_deal_engine — Step 13 guide"
M = "<font name='Mono'>%s</font>"


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


# ================================================================ diagram
def diagram_mcp():
    lx, lw, mx, mw, rx = 0, 140, 175, 160, 365
    rw = W - rx
    d = Drawing(W, 200)
    text(d, lx + lw / 2, 188, "AI agent (MCP client)", size=8, color=NAVY, bold=True, anchor="middle")
    text(d, mx + mw / 2, 188, "owis_find_deal_engine", size=8, color=NAVY, bold=True, anchor="middle")
    text(d, rx + rw / 2, 188, "Same as the REST API", size=8, color=NAVY, bold=True, anchor="middle")

    box(d, lx, 125, lw, 48, "Claude Code, Cursor, …\nBearer ofd_live_…",
        fill=BLUE, stroke=BLUE_D, size=7.2)
    box(d, mx, 125, mw, 48, "POST /mcp\nkey check · address · 5 msg/s", fill=PURPLE, stroke=PURPLE_D, size=7.2)
    arrow(d, lx + lw, 149, mx, 149)
    box(d, rx, 125, rw, 48, "API client in the admin panel\nscopes · countries · daily limit",
        fill=GREY, stroke=GREY_D, size=7.2)
    arrow(d, mx + mw, 149, rx, 149)

    box(d, mx, 55, mw / 2 - 4, 48, "list_countries\nscope countries\nfree", fill=GREEN, stroke=GREEN_D, size=7)
    box(d, mx + mw / 2 + 4, 55, mw / 2 - 4, 48, "search_products\nscope search\nmetered", fill=ORANGE,
        stroke=ORANGE_D, size=7)
    arrow(d, mx + mw / 4, 125, mx + mw / 4, 103)
    arrow(d, mx + 3 * mw / 4, 125, mx + 3 * mw / 4, 103)
    box(d, rx, 55, rw, 48, "48 h cache (free) or live\nsame daily counter as REST",
        fill=GREY, stroke=GREY_D, size=7.2)
    arrow(d, mx + mw, 79, rx, 79)

    box(d, lx, 55, lw, 48, "Tool result\nresults + quota, or isError\nwith the reason", fill=BLUE, stroke=BLUE_D,
        size=7.2)
    arrow(d, mx, 79, lx + lw, 79)
    text(d, W / 2, 30, "One JSON answer per request · no sessions · versions 2026-07-28 and 2025-03-26 … 2025-11-25",
         size=7, anchor="middle")
    return d


# ================================================================ sections
def cover():
    s = [Spacer(1, 6)]
    s.append(P("Step 13 — AI agents (MCP) and developer docs", "title"))
    s.append(P("What it does, and how to test it on your Docker stack step by step with the commands", "subtitle"))
    s.append(T([
        ["Step", "Phase 3, step 13: MCP endpoint for AI agents, OpenAPI 3.1 description, docs page for developers"],
        ["Status", "Built 2026-10-26; not yet tested by the owner"],
        ["Branch", f"{M % BRANCH} (via a pull request), folder {M % 'Owis_Find_Deal_Engine/server'}"],
        ["New endpoints", f"{M % 'POST /mcp'} (API keys) · {M % 'GET /api/v1/openapi.json'} · {M % 'GET /docs/'} (public)"],
        ["Settings", "none new; nothing to add to .env"],
        ["Database", "no change"],
        ["Date", DATE],
    ], [80, W - 80], header=False, first_col_bold=True))
    s.append(Spacer(1, 14))
    s.append(callout("<b>How to read this guide.</b> Part 1 explains what step 13 does. Part 2 is the test: steps "
                     "0–10 with the commands to type (Git Bash on Windows) and what you should see. Every step uses "
                     f"the cached {M % 'usb c hub'} / {M % 'jor'} search or no search at all; only if that search "
                     "is no longer in the cache (older than 48 h) does step 4 run one live search."))
    return s


def part1():
    s = h1("Part 1 — What step 13 does")
    s.append(P("Step 13 lets AI agents use the search directly, and gives developers the description and "
               "documentation they need to build on it."))
    s.append(P("1. MCP endpoint for AI agents", "h2"))
    s.append(P("The <b>Model Context Protocol</b> (MCP) is the standard way AI agents (Claude, Cursor, VS Code, "
               "your own agents) discover and call tools. The service now has an MCP endpoint at "
               f"{M % 'POST /mcp'}. An agent connects with the <b>same API key</b> as the REST API and sees two tools:"))
    s.append(T([
        ["Tool", "Needs scope", "What it does"],
        [M % "list_countries", M % "countries",
         "The countries this key may search in, with currency and marketplaces. Free."],
        [M % "search_products", M % "search",
         f"Searches one country for a product ({M % 'title'}, {M % 'country'}, optional {M % 'refresh'}). "
         f"Same results as {M % '/api/v1/search'}, plus the allowance left. Cached answers free, a live search counts once."],
    ], [95, 70, W - 165]))
    s.append(Spacer(1, 6))
    s.append(KeepTogether([diagram_mcp(), caption("An agent calls the tools with its API key; the key's scopes, "
                                                   "countries and daily limit apply as on the REST API.")]))
    s += bullets([
        "<b>API keys only.</b> A user's login token is refused (401): agents are servers, not app users.",
        f"<b>Problems come back to the agent as readable tool errors</b> ({M % 'isError'}), e.g. "
        f"{M % 'quota_exceeded'} with the seconds to wait, {M % 'country_not_allowed'}, or a missing scope, so the "
        "agent can explain or adjust instead of failing.",
        "<b>The same limits as the REST API:</b> the key's daily allowance (one counter for REST and MCP), its "
        "searches per second, and at most 5 MCP messages per second.",
        "<b>Both protocol generations are spoken:</b> the new stateless version 2026-07-28 and the older "
        "2025-03-26 … 2025-11-25 (with a start handshake). Tested with the official TypeScript and Go MCP "
        "libraries.",
        "<b>A new search takes 20–60 s</b>: the tool waits for it, so agents need a timeout of about 90 s.",
    ])

    s.append(P("2. Developer docs and OpenAPI", "h2"))
    s += bullets([
        f"<b>{M % 'http://localhost:3002/docs/'}</b> — one page for developers: quick start, keys, search, "
        "asynchronous search, webhooks (with code to check the signature in Python and Node.js), how to connect "
        "MCP clients, limits and all error codes. Public, no scripts.",
        f"<b>{M % 'http://localhost:3002/api/v1/openapi.json'}</b> — the machine-readable description (OpenAPI "
        "3.1) of every public endpoint, including the webhook. Developers load it into Swagger UI, Postman or "
        "Insomnia, or generate client code from it.",
    ])
    return s


def mcp_call(id_, method, params, version="2025-06-18"):
    return f"""curl -s localhost:3002/mcp -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" -H "MCP-Protocol-Version: {version}" \\
  -d '{{"jsonrpc":"2.0","id":{id_},"method":"{method}",
       "params":{params}}}'"""


def part2():
    s = [PageBreak()] + h1("Part 2 — Test steps")
    s.append(P(f"The commands are for <b>Git Bash</b> on Windows, in the {M % 'Owis_Find_Deal_Engine/server'} folder. "
               f"In PowerShell, type {M % 'curl.exe'} instead of {M % 'curl'}. Replace every "
               f"{M % '&lt;…&gt;'} with your own value. The Git Bash tips from the step 12 guide apply "
               f"({M % 'export NAME=value'} without spaces, variables live in one window only)."))

    s += step("Step 0 — Update and start", ("code", f"""
cd claude-ai-clould-repo
git checkout {BRANCH}
git pull
cd Owis_Find_Deal_Engine/server
docker compose up -d --build
docker compose ps                     # all four containers "healthy" after ~30 s
curl localhost:3002/api/v1/health     # {{"status":"ok"}}
"""), "Until the step 13 pull request is merged, use " + (M % "git checkout claude/optimistic-brown-tqclh3") +
       " instead (the branch with step 13).",
       OK + "the four containers are healthy; nothing to add to .env, no database change.")

    s += step("Step 1 — The docs page and the OpenAPI file (free)",
              f"Open <b>{M % 'http://localhost:3002/docs/'}</b> in your browser. Then:", ("code", """
curl -s localhost:3002/api/v1/openapi.json | head -c 300; echo
curl -s -o /dev/null -w "%{http_code}\\n" localhost:3002/docs     # 301 -> /docs/
"""), OK + f"the page shows <i>Owis Find Deal API</i> with a menu (Quick start … OpenAPI) and follows your "
           f"light / dark setting; the JSON begins with {M % '\"openapi\": \"3.1.0\"'}. Optional: paste the "
           f"JSON into <font name='Mono'>https://editor.swagger.io</font> (File → Import) to browse it.")

    s += step("Step 2 — Make a client for the agent",
              f"<b>Admin panel → API clients → New client</b>: Name {M % 'step13 agent'}, Live searches per day "
              f"{M % '20'}, scopes <b>search</b> and <b>countries</b> both ticked, country <b>jor</b> → <b>Create "
              "client and key</b> → <b>Copy</b> → <b>I have saved it</b>. Then:", ("code", """
export KEY=ofd_live_<...>
echo "${KEY:0:12}"        # ofd_live_xxx
"""), "The step 12 client also works if it has both scopes.")

    s += step("Step 3 — The handshake and the tool list (free)", ("code", mcp_call(
        1, "initialize", '{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gitbash","version":"1"}}')
        + "\n\n" + mcp_call(2, "tools/list", "{}")),
        OK + f"the first answer has {M % '\"protocolVersion\":\"2025-06-18\"'}, {M % '\"serverInfo\":{\"name\":\"owis_find_deal_engine\"'} "
             f"and {M % '\"instructions\"'}; the second lists two tools, {M % 'list_countries'} and "
             f"{M % 'search_products'}, each with an inputSchema and outputSchema.")

    s += step("Step 4 — Call the tools", ("code", mcp_call(3, "tools/call", '{"name":"list_countries","arguments":{}}')
                                          + "\n\n" + mcp_call(4, "tools/call",
                                                              '{"name":"search_products","arguments":{"title":"usb c hub","country":"jor"}}')),
              OK + f"list_countries shows only <b>Jordan</b> (the key is limited to jor). search_products answers "
                   f"with {M % '\"structuredContent\":{\"query\":\"usb c hub\",…,\"results\":[…]'} and "
                   f"{M % '\"quota\":{\"plan\":\"api\",\"limit\":20,…}'}. If the search was cached (step 12 test "
                   f"less than 48 h ago, or run it twice) {M % '\"cached\":true'} and the remaining count does not "
                   "change; otherwise the first call takes 30–50 s and counts once.")

    s += step("Step 5 — Errors the agent can read (free)", ("code", mcp_call(
        5, "tools/call", '{"name":"search_products","arguments":{"title":"usb c hub","country":"usa"}}') + "\n\n" + mcp_call(
        6, "tools/call", '{"name":"search_products","arguments":{"title":"x","country":"jor"}}') + "\n\n" + mcp_call(
        7, "tools/call", '{"name":"buy_everything","arguments":{}}')),
        OK + f"usa → a normal answer with {M % '\"isError\":true'} and the text "
             f"{M % 'country_not_allowed'}; title {M % 'x'} → {M % 'isError'} with {M % 'invalid_request'} (title "
             f"must be 2-200 characters); the made-up tool → a JSON-RPC {M % '\"error\":{\"code\":-32602'} "
             f"(unknown tool).")

    s += step("Step 6 — The new protocol version 2026-07-28 (free)",
              "The newest MCP version has no handshake: each request names its version in the body and in three "
              "headers, which must match.", ("code", """
curl -s localhost:3002/mcp -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" \\
  -H "MCP-Protocol-Version: 2026-07-28" -H "Mcp-Method: server/discover" \\
  -d '{"jsonrpc":"2.0","id":8,"method":"server/discover","params":{"_meta":{
       "io.modelcontextprotocol/protocolVersion":"2026-07-28",
       "io.modelcontextprotocol/clientCapabilities":{}}}}'

# the same with a wrong header -> 400
curl -s -w "\\n%{http_code}\\n" localhost:3002/mcp -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" \\
  -H "MCP-Protocol-Version: 2026-07-28" -H "Mcp-Method: tools/list" \\
  -d '{"jsonrpc":"2.0","id":9,"method":"server/discover","params":{"_meta":{
       "io.modelcontextprotocol/protocolVersion":"2026-07-28",
       "io.modelcontextprotocol/clientCapabilities":{}}}}'
"""), OK + f"the first lists {M % '\"supportedVersions\":[\"2026-07-28\",\"2025-11-25\",\"2025-06-18\",\"2025-03-26\"]'} "
           f"with {M % '\"resultType\":\"complete\"'}; the second answers {M % '400'} with "
           f"{M % '\"code\":-32020'} (<i>Header mismatch</i>).")

    s += step("Step 7 — Who is refused (free)", ("code", """
# no key, and a login token: both 401
curl -s -X POST localhost:3002/mcp -d '{}'; echo
curl -s -X POST localhost:3002/mcp -H "Authorization: Bearer $ADMIN_TOKEN" -d '{}'; echo
# GET is not used by this server: 405
curl -s -o /dev/null -w "%{http_code}\\n" localhost:3002/mcp
"""), OK + f"{M % 'missing bearer token'}; {M % 'this endpoint takes an API key …, not a login token'} "
           f"(with a fresh admin token from step 1 of the step 12 guide; an expired one gives "
           f"{M % 'invalid or expired token'}, also fine); {M % '405'}.",
              f"<b>A key without the search scope:</b> make a client {M % 'step13 lister'} with only the scope "
              "<b>countries</b>, export its key as LKEY, and call search_products with it "
              f"(step 4, {M % '$LKEY'} instead of {M % '$KEY'}).",
              OK + f"{M % 'isError'} with {M % 'forbidden'}: <i>this API key lacks the scope \"search\" needed for "
                   f"search_products</i>; list_countries works with it.")

    s += step("Step 8 — Connect a real agent (optional, free with the cached search)",
              "<b>Claude Code</b> (if installed):", ("code", """
claude mcp add --transport http owis-find-deal http://localhost:3002/mcp \\
  --header "Authorization: Bearer $KEY"
claude mcp list            # owis-find-deal: ... - ✓ Connected
claude                     # then ask: "Use owis-find-deal to find a usb c hub in Jordan"
"""), "<b>Or the MCP Inspector</b> (a test tool in the browser; needs Node.js):", ("code", """
npx @modelcontextprotocol/inspector
"""), "In the page that opens: Transport <b>Streamable HTTP</b>, URL "
      f"{M % 'http://localhost:3002/mcp'}, under Authentication add the header {M % 'Authorization'} = "
      f"{M % 'Bearer ofd_live_…'} → <b>Connect</b> → <b>Tools</b> → <b>List Tools</b> → run "
      f"{M % 'search_products'} with {M % 'usb c hub'} / {M % 'jor'}.",
              OK + "the agent (or Inspector) lists both tools and shows the offers per marketplace. Remove the "
                   f"Claude Code entry afterwards with {M % 'claude mcp remove owis-find-deal'} if you like.")

    s += step("Step 9 — Dashboard and logs (free)", ("code", """
docker compose logs server | grep '"/mcp"' | tail -3
docker compose logs server | grep -c ofd_live_      # 0
"""), OK + f"one log line per MCP request with {M % '\"user\":\"client:&lt;id&gt;\"'}; the key never appears in "
           "the log. After ~30 s the dashboard's <b>API clients</b> table shows <i>step13 agent</i> with its "
           "cached (and, if any, live) searches.")

    s += step("Step 10 (optional) — Daily limit through MCP (costs one live search)",
              f"Make a client with Live searches per day <b>1</b>, run search_products once with a <b>new</b> "
              "title, then again with another new title.",
              OK + f"the second call answers {M % 'isError'} with {M % 'quota_exceeded'} and "
                   f"{M % 'retry_after_seconds'} (the seconds until midnight UTC); no second live search runs.")

    s += h1("What to report back")
    s += numbered([
        f"Which steps passed or failed, with the answer and {M % 'docker compose logs server'} lines for any failure.",
        "Which agents you plan to connect (Claude Code, Cursor, your own code), and whether 90 s for a new search "
        f"is acceptable for them, or they should use the asynchronous search instead (a third tool, e.g. "
        f"{M % 'start_search'} + {M % 'get_search'}, can be added).",
        "Whether the docs page is clear for an outside developer: wording, missing examples, order.",
        "Whether the docs page and OpenAPI file should stay public in production (now: yes, they hold no "
        "secrets), or be behind a login.",
    ])
    return s


def build():
    story = cover() + part1() + part2()
    doc = SimpleDocTemplate(OUT, pagesize=A4, leftMargin=MARGIN, rightMargin=MARGIN, topMargin=18 * mm,
                            bottomMargin=16 * mm, title=TITLE, author="owis_find_deal_engine")
    doc.build(story, onFirstPage=on_page, onLaterPages=on_page)
    print("written", OUT)


if __name__ == "__main__":
    build()
