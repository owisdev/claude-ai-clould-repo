"""Builds docs/step12_guide.pdf: what phase 3 step 12 (asynchronous
search, signed webhooks, usage per API client) does, and how the owner
tests it on the Docker stack, step by step with the commands.

    pip install reportlab
    python3 docs/build_step12_guide_pdf.py

Styles and drawing helpers come from build_architecture_pdf.py.
"""

import os

from build_architecture_pdf import (
    BLUE, BLUE_D, CONTENT_W, GREEN, GREEN_D, GREY, GREY_D, MARGIN, MUTED, NAVY, ORANGE, ORANGE_D,
    PAGE_H, PAGE_W, PURPLE, PURPLE_D, A4, P, S, arrow, box, bullets, callout, caption, numbered, table, text,
)
from build_architecture_pdf import code as _code
from reportlab.graphics.shapes import Drawing
from reportlab.lib import colors
from reportlab.lib.units import mm
from reportlab.platypus import CondPageBreak, KeepTogether, PageBreak, Preformatted, SimpleDocTemplate, Spacer

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "step12_guide.pdf")
DATE = "24 October 2026"
TITLE = "owis_find_deal_engine — Step 12 guide"
BRANCH = "claude/cloud-vs-local-7xi988"
W = CONTENT_W
OK = "<font color='#4E9A6B'><b>✓ Expected:</b></font> "


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


def code(txt):
    txt = txt.strip("\n")
    longest = max(len(line) for line in txt.splitlines())
    if longest > 96:  # shrink the font so the longest line still fits
        style = S["code"].clone("code_small", fontSize=round(455 / (0.6 * longest), 1))
        style.leading = style.fontSize * 1.3
        return KeepTogether([Preformatted(txt, style)])
    return KeepTogether([_code(txt)])


def h1(t):
    return [CondPageBreak(200), P(t, "h1")]


def step(title, *parts):
    """A test step: heading, then paragraphs (str), code (('code', s)) or lists."""
    flow = []
    for p in parts:
        if isinstance(p, tuple) and p[0] == "code":
            flow.append(code(p[1]))
        elif isinstance(p, list):
            flow += bullets(p)
        else:
            flow.append(P(p))
    first = flow[:1]
    if first and isinstance(first[0], KeepTogether):
        first = list(first[0]._content)  # no nested KeepTogether
    return [CondPageBreak(120), KeepTogether([P(title, "h2")] + first)] + flow[1:]


def T(*a, **k):
    return KeepTogether([table(*a, **k)])


# ================================================================ diagram
def diagram_flow():
    # columns: caller (x 0-140), service (175-335), right (365-W)
    lx, lw, mx, mw, rx = 0, 140, 175, 160, 365
    rw = W - rx
    d = Drawing(W, 230)
    text(d, lx + lw / 2, 218, "Your server / AI agent", size=8, color=NAVY, bold=True, anchor="middle")
    text(d, mx + mw / 2, 218, "owis_find_deal_engine", size=8, color=NAVY, bold=True, anchor="middle")

    box(d, lx, 160, lw, 44, "POST /api/v1/searches\n{title, country, webhook_url?}", fill=BLUE, stroke=BLUE_D, size=7.2)
    box(d, mx, 160, mw, 44, "Checks at once\ninput · key · country · daily limit", fill=PURPLE, stroke=PURPLE_D, size=7.2)
    arrow(d, lx + lw, 182, mx, 182)
    box(d, rx, 160, rw, 44, "Refused at once\n400 · 402 · 403 · 429", fill=colors.HexColor("#FCE8E8"),
        stroke=colors.HexColor("#C0504D"), size=7.2)
    arrow(d, mx + mw, 182, rx, 182)

    box(d, lx, 95, lw, 44, "202 Accepted\nid srch_… · Location · Retry-After", fill=GREEN, stroke=GREEN_D, size=7.2)
    arrow(d, mx, 168, lx + lw, 130)
    text(d, lx + lw + 6, 152, "accepted", size=7)
    box(d, mx, 95, mw, 44, "Job runs in the background\nlive 25–50 s · cached: done at once",
        fill=ORANGE, stroke=ORANGE_D, size=7.2)
    arrow(d, mx + mw / 2, 160, mx + mw / 2, 139)

    box(d, mx, 25, mw, 44, "Job saved in Redis, 1 hour\ndone (result) or failed (error)", fill=GREY, stroke=GREY_D,
        size=7.2)
    arrow(d, mx + mw / 2, 95, mx + mw / 2, 69)
    box(d, lx, 25, lw, 44, "GET /api/v1/searches/{id}\npoll every 5 s while running", fill=BLUE, stroke=BLUE_D,
        size=7.2)
    arrow(d, lx + lw, 47, mx, 47)
    box(d, rx, 25, rw, 44, "Signed webhook to you\nX-OFD-Signature: t=…,v1=…", fill=GREEN, stroke=GREEN_D, size=7.2)
    arrow(d, mx + mw, 47, rx, 47)
    text(d, rx + rw / 2, 75, "only with webhook_url", size=7, anchor="middle")
    return d


# ================================================================ sections
def cover():
    s = [Spacer(1, 6)]
    s.append(P("Step 12 — Integration endpoints", "title"))
    s.append(P("What it does, and how to test it on your Docker stack step by step with the commands", "subtitle"))
    s.append(T([
        ["Step", "Phase 3, step 12: asynchronous search, signed webhooks, usage per API client"],
        ["Status", "Done 2026-10-23, merged into the project branch (PR #9)"],
        ["Branch", f"<font name='Mono'>{BRANCH}</font>, folder <font name='Mono'>Owis_Find_Deal_Engine/server</font>"],
        ["New endpoints", "<font name='Mono'>POST /api/v1/searches</font> · <font name='Mono'>GET /api/v1/searches/{id}</font> · "
                          "<font name='Mono'>POST /admin/v1/clients/{id}/webhook-secret</font>"],
        ["New setting", "<font name='Mono'>WEBHOOK_ALLOW_PRIVATE</font> (.env, development only, default false)"],
        ["Database", "migration 0005: webhook salt, prefix and time on <font name='Mono'>api_clients</font> (automatic)"],
        ["Date", DATE],
    ], [80, W - 80], header=False, first_col_bold=True))
    s.append(Spacer(1, 14))
    s.append(callout("<b>How to read this guide.</b> Part 1 explains what step 12 does. Part 2 is the test: "
                     "steps 0–17 with the commands to type (Git Bash on Windows) and what you should see. "
                     "Only step 3 and the optional step 17 run a live search (about $0.16 Apify + 1 SerpApi "
                     "search each); everything else uses the cached answer and is free."))
    return s


def part1():
    s = h1("Part 1 — What step 12 does")
    s.append(P("Step 12 gives your own servers and AI agents three things they need to build on the search "
               "service."))
    s.append(P("1. Asynchronous search (start now, collect later)", "h2"))
    s.append(P("A live search takes 25–50 seconds, and many programs and AI agents do not want to hold a "
               "connection open that long. Step 12 adds a second way to search:"))
    s.append(T([
        ["Call", "What happens"],
        ["<font name='Mono'>POST /api/v1/searches</font><br/>{\"title\", \"country\"}",
         "The search starts in the background. The answer comes at once: <b>202 Accepted</b> with a job id "
         "(<font name='Mono'>srch_…</font>) and a <font name='Mono'>Location</font> header pointing to the job."],
        ["<font name='Mono'>GET /api/v1/searches/{id}</font>",
         "Shows the job: <b>running</b>, <b>done</b> (with the same results as /search) or <b>failed</b> "
         "(with the error /search would have given)."],
    ], [150, W - 150]))
    s.append(Spacer(1, 6))
    s.append(fig_flow())
    s.append(P("The rules and limits are the same as for /search:"))
    s += bullets([
        "<b>It works with an API key or a user's login token.</b>",
        "<b>Cached answers are free.</b> A live search counts once against the daily limit.",
        "<b>Problems are refused immediately, before a job starts:</b> a bad title (400), an unknown country (400), "
        "a country the key is not allowed (403), a missing scope (403), or a used-up daily limit (402 for app "
        "users, 429 for keys).",
        "<b>Cached searches are already done</b> in the 202 answer, with the results included.",
        "<b>Only the caller who started a job can read it.</b> Anyone else gets 404.",
        "<b>Finished jobs are kept for 1 hour</b> in Redis, so they survive a server restart.",
        "<b>Each caller can have at most 10 jobs running at once.</b> The 11th gets <font name='Mono'>429 too_many_jobs</font>.",
        "<b>Reading a job has its own limit</b> (5 per second), so polling does not use up your search limit. "
        "While a job is running, the answer includes <font name='Mono'>Retry-After: 5</font> (\"ask again in 5 seconds\").",
        "<b>If the server stops during a job</b>, the job ends as failed (503) and you start it again.",
    ])

    s.append(P("2. Signed webhook (the service calls you when the search is done)", "h2"))
    s.append(P("This is for API keys only. Instead of polling, add <font name='Mono'>\"webhook_url\": "
               "\"https://your-server/…\"</font> to the request. When the job ends, the service sends the "
               "finished job to that URL with these headers:"))
    s.append(code("""
X-OFD-Event:     search.completed   (or search.failed)
X-OFD-Delivery:  srch_...           (the job id: use it to ignore repeats)
X-OFD-Signature: t=<unix time>,v1=<HMAC-SHA256 of "<t>.<body>" with the client's secret>
"""))
    s += bullets([
        "<b>The signature lets your receiver check</b> that the call really came from your service and was not "
        "changed on the way.",
        "<b>Any 2xx answer counts as delivered.</b> Network errors, 408, 429 and 5xx answers are retried after "
        "5 s and again after 30 s, so 3 attempts in all. Redirects are not followed.",
        "<b>The job shows how delivery went:</b> <font name='Mono'>webhook.status</font> is pending, delivered or "
        "failed, with the number of attempts and the last answer.",
        "<b>Only https URLs to public addresses are allowed.</b> URLs pointing at localhost, 10.x, 192.168.x, "
        "169.254.x and similar are refused, so nobody can make your server call its own Redis or Postgres. "
        "For a test receiver on your PC, set <font name='Mono'>WEBHOOK_ALLOW_PRIVATE=true</font> in .env "
        "(development only).",
    ])
    s.append(P("<b>The signing secret is made per API client</b> with the admin panel's <i>API clients → Webhook "
               "secret</i> button:"))
    s += bullets([
        "It looks like <font name='Mono'>whsec_…</font> and is shown once.",
        "It is not stored anywhere; the server recalculates it from <font name='Mono'>SETTINGS_ENCRYPTION_KEY</font>.",
        "Making a new one stops the old one immediately.",
        "Each new secret is recorded in the audit log as <font name='Mono'>clients.webhook_secret</font>, without "
        "the secret itself.",
    ])

    s.append(P("3. Usage per API client on the dashboard", "h2"))
    s.append(P("The dashboard has a new <b>API clients</b> table. For each client it shows:"))
    s += bullets([
        "searches, cached answers, live searches, refused searches and failed searches",
        "<b>estimated cost</b>: the client's live searches × the average cost of a live search in the period",
        "the last day it searched",
    ])
    s.append(P("The same data is in <font name='Mono'>GET /admin/v1/usage</font> under <font name='Mono'>clients</font>. "
               "This is the base for billing partners later."))

    s.append(P("Error codes added by step 12", "h2"))
    s.append(T([
        ["Status", "code", "Meaning"],
        ["202", "—", "job started (running) or already done (cached answer)"],
        ["400", "webhook_not_configured", "webhook_url given, but the API client has no webhook secret yet"],
        ["400", "invalid_request", "bad input; also an unsafe webhook URL, or webhook_url with a user token"],
        ["404", "not_found", "no such job, someone else's job, or finished more than 1 hour ago"],
        ["429", "too_many_jobs", "10 asynchronous searches already running for this caller"],
        ["job error 504", "timeout", "the search took too long"],
        ["job error 503", "job_lost / service_unavailable", "the server stopped during the search: start it again"],
    ], [70, 140, W - 210]))
    return s


def fig_flow():
    return KeepTogether([diagram_flow(), caption("Asynchronous search: the request is checked at once, the "
                                                  "search runs in the background, the result is polled or pushed.")])


def part2():
    s = [PageBreak()] + h1("Part 2 — Test steps")
    s.append(P("The commands are for <b>Git Bash</b> on Windows. In PowerShell, type <font name='Mono'>curl.exe</font> "
               "instead of <font name='Mono'>curl</font> and put the JSON body in single quotes. Replace every "
               "<font name='Mono'>&lt;…&gt;</font> with your own value."))
    s.append(callout("<b>Cost:</b> step 3 runs one live search (about $0.16 Apify plus one SerpApi search). After "
                     "that, the same search is cached for 48 h and every other step is free, except the optional "
                     "step 17.", fill=ORANGE, edge=ORANGE_D))

    s += step("Step 0 — Update and start", ("code", """
cd claude-ai-clould-repo
git checkout claude/cloud-vs-local-7xi988
git pull
cd Owis_Find_Deal_Engine/server
docker compose up -d --build
docker compose ps                     # all four containers "healthy" after ~30 s
docker compose logs server | grep -i -E "migrat|error"
curl localhost:3002/api/v1/health     # {"status":"ok"}
"""), OK + "no ERROR lines; the new database migration (0005_webhooks.sql) runs automatically at start; "
       "health answers {\"status\":\"ok\"}.")

    s += step("Step 1 — Get tokens",
              "<b>Admin token</b> (your admin email from ADMIN_EMAIL):", ("code", """
curl -s -X POST "https://identitytoolkit.googleapis.com/v1/accounts:signInWithPassword?key=<WEB_API_KEY>" \\
  -H "Content-Type: application/json" \\
  -d '{"email":"<admin email>","password":"<password>","returnSecureToken":true}'
export ADMIN_TOKEN=<idToken from the answer>
"""), "<b>User token</b> (a normal app user, for example test1@example.com):", ("code", """
curl -s -X POST "https://identitytoolkit.googleapis.com/v1/accounts:signInWithPassword?key=<WEB_API_KEY>" \\
  -H "Content-Type: application/json" \\
  -d '{"email":"test1@example.com","password":"Test-pass-123","returnSecureToken":true}'
export TOKEN=<idToken>
"""), "Both tokens are valid for 1 hour. Repeat this step if you later get "
      "<font name='Mono'>401 invalid or expired token</font>.")

    s += step("Step 2 — Make a test client for step 12",
              "Your step-11 client only allows 2 searches per day, so make a new one. Use the admin panel "
              "(<b>API clients</b> → name <font name='Mono'>step12 agent</font>, tick jor, 20 per day) or:",
              ("code", """
curl -s -X POST localhost:3002/admin/v1/clients \\
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \\
  -d '{"name":"step12 agent","daily_quota":20,"countries":["jor"]}'
"""), OK + "status 201; the answer has <font name='Mono'>\"key\":\"ofd_live_…\"</font> and "
           "<font name='Mono'>\"client\":{\"id\":&lt;N&gt;,…,\"webhook_secret_prefix\":\"\"}</font>.",
              ("code", """
export KEY=ofd_live_<...>
export CID=<N>          # the client id from the answer
"""))

    s += step("Step 3 — A slow live search, started asynchronously (costs one live search)", ("code", """
curl -i -X POST localhost:3002/api/v1/searches \\
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \\
  -d '{"title":"usb c hub","country":"jor"}'
"""), OK + "the answer comes <b>at once</b>:", [
        "<font name='Mono'>HTTP/1.1 202 Accepted</font>",
        "<font name='Mono'>Location: /api/v1/searches/srch_…</font>",
        "<font name='Mono'>Retry-After: 5</font> and <font name='Mono'>X-Plan: api</font>",
        "body <font name='Mono'>{\"id\":\"srch_…\",\"status\":\"running\",…,\"finished_at\":null}</font>",
    ], "Save the id, then poll every 5 seconds until the job is no longer running:", ("code", """
export ID=srch_<...>
while true; do
  S=$(curl -s localhost:3002/api/v1/searches/$ID -H "Authorization: Bearer $KEY")
  echo "$S" | grep -o '"status":"[a-z]*"' | head -1
  echo "$S" | grep -q '"status":"running"' || break
  sleep 5
done
echo "$S"
"""), OK + "\"status\":\"running\" several times (about 30–50 s), then \"status\":\"done\". The final answer has:", [
        "<font name='Mono'>\"result\":{…}</font> with the products and <font name='Mono'>\"cached\":false</font>",
        "<font name='Mono'>\"finished_at\"</font> with a time",
        "<font name='Mono'>\"quota\":{\"plan\":\"api\",\"limit\":20,\"remaining\":19,…}</font>",
    ])

    s += step("Step 4 — A cached search is done immediately (free)", ("code", """
curl -i -X POST localhost:3002/api/v1/searches \\
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \\
  -d '{"title":"usb c hub","country":"jor"}'
"""), OK + "202 with \"status\":\"done\" and \"cached\":true in the body. There is <b>no</b> Retry-After, and "
           "X-RateLimit-Remaining is still 19, because cached answers are free.")

    s += step("Step 5 — A user login token works too", ("code", """
curl -i -X POST localhost:3002/api/v1/searches \\
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \\
  -d '{"title":"usb c hub","country":"jor"}'
"""), OK + "202, \"status\":\"done\", X-Plan: free.")

    s += step("Step 6 — Refusals happen immediately (free)", ("code", """
# title too short -> 400 invalid_request
curl -s -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" -d '{"title":"x","country":"jor"}'
# unknown country -> 400 unsupported_country
curl -s -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"xyz"}'
# country not allowed for this key -> 403 country_not_allowed
curl -s -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"usa"}'
# no token -> 401
curl -s -X POST localhost:3002/api/v1/searches \\
  -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"jor"}'
# made-up field -> 400 invalid_request ("unknown field")
curl -s -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"jor","x":1}'
"""), OK + "each answers right away with <font name='Mono'>{\"error\":{\"code\":\"…\",\"message\":\"…\"}}</font> "
           "and the code shown in the comment.")

    s += step("Step 7 — Nobody else can read the job", ("code", """
# the user token reading the key's job -> 404
curl -s localhost:3002/api/v1/searches/$ID -H "Authorization: Bearer $TOKEN"
# a made-up id -> 404
curl -s localhost:3002/api/v1/searches/srch_aaaaaaaaaaaaaaaaaaaaaaaaaa \\
  -H "Authorization: Bearer $KEY"
"""), OK + "both answer 404 not_found (\"no search job with this id…\").")

    s += step("Step 8 — The job survives a server restart (stored in Redis)", ("code", """
docker compose restart server
sleep 15
curl -s localhost:3002/api/v1/searches/$ID -H "Authorization: Bearer $KEY" | head -c 300
"""), OK + "the job from step 3 is still there with \"status\":\"done\", as long as you are within 1 hour of "
           "when it finished.")

    s += step("Step 9 — Webhook without a secret is refused",
              "Go to <b>https://webhook.site</b>, which gives you a free https URL such as "
              "<font name='Mono'>https://webhook.site/1234-…</font>.", ("code", """
export HOOK=https://webhook.site/<your id>
curl -s -X POST localhost:3002/api/v1/searches \\
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \\
  -d "{\\"title\\":\\"usb c hub\\",\\"country\\":\\"jor\\",\\"webhook_url\\":\\"$HOOK\\"}"
"""), OK + "400 with \"code\":\"webhook_not_configured\".")

    s += step("Step 10 — Make the webhook secret",
              "Use the admin panel (<b>API clients</b> → <b>Webhook secret</b> on step12 agent → copy it) or:",
              ("code", """
curl -s -X POST localhost:3002/admin/v1/clients/$CID/webhook-secret \\
  -H "Authorization: Bearer $ADMIN_TOKEN"
"""), OK + "201 with <font name='Mono'>\"webhook_secret\":\"whsec_…\"</font>. The client list now shows "
           "<font name='Mono'>whsec_xxxxxxxx…</font> in the <b>Webhook</b> column.", ("code", """
export SECRET=whsec_<...>
"""), "The server may take up to 10 s to see the new secret, so wait about 10 s before step 11.")

    s += step("Step 11 — Webhook delivered and signed (free, uses the cached search)", ("code", """
curl -s -X POST localhost:3002/api/v1/searches \\
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \\
  -d "{\\"title\\":\\"usb c hub\\",\\"country\\":\\"jor\\",\\"webhook_url\\":\\"$HOOK\\"}"
"""), OK + "202 with \"status\":\"done\" and "
           "<font name='Mono'>\"webhook\":{\"url\":\"…\",\"status\":\"pending\",\"attempts\":0}</font>.",
              OK + "within a few seconds webhook.site shows a <b>POST</b> with the headers "
                   "<font name='Mono'>x-ofd-event: search.completed</font>, <font name='Mono'>x-ofd-delivery: srch_…</font> "
                   "and <font name='Mono'>x-ofd-signature: t=…,v1=…</font>. The body is the job JSON with the results.",
              "Read the job again:", ("code", """
export ID2=<srch_... from the answer above>
curl -s localhost:3002/api/v1/searches/$ID2 -H "Authorization: Bearer $KEY" \\
  | grep -o '"webhook":{[^}]*}'
"""), OK + "<font name='Mono'>\"webhook\":{…\"status\":\"delivered\",\"attempts\":1,\"http_status\":200}</font>",
              "<b>Check the signature.</b> Save this as <font name='Mono'>verify.py</font>:", ("code", """
import hashlib, hmac, sys, time
secret, header, body_file = sys.argv[1], sys.argv[2], sys.argv[3]
body = open(body_file, "rb").read()
parts = dict(p.split("=", 1) for p in header.split(","))
want = hmac.new(secret.encode(), parts["t"].encode() + b"." + body, hashlib.sha256).hexdigest()
print("signature OK" if hmac.compare_digest(want, parts["v1"]) else "BAD signature")
print("age:", int(time.time()) - int(parts["t"]), "s")
"""), "In webhook.site, copy the <b>raw content</b> into <font name='Mono'>body.json</font> exactly as shown, and "
      "copy the x-ofd-signature value. Then run:", ("code", """
python verify.py "$SECRET" "t=...,v1=..." body.json
"""), OK + "<font name='Mono'>signature OK</font>. If you change one character in body.json and run it again, it "
           "prints <font name='Mono'>BAD signature</font>.")

    s += step("Step 12 — Unsafe webhook URLs are refused (free)", ("code", """
for U in http://example.com/hook https://127.0.0.1/hook \\
         https://192.168.1.10/hook https://localhost/hook; do
  curl -s -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer $KEY" \\
    -H "Content-Type: application/json" \\
    -d "{\\"title\\":\\"usb c hub\\",\\"country\\":\\"jor\\",\\"webhook_url\\":\\"$U\\"}"; echo
done
# webhooks are for API keys only -> 400
curl -s -X POST localhost:3002/api/v1/searches -H "Authorization: Bearer $TOKEN" \\
  -H "Content-Type: application/json" \\
  -d "{\\"title\\":\\"usb c hub\\",\\"country\\":\\"jor\\",\\"webhook_url\\":\\"$HOOK\\"}"
"""), OK + "all answer 400 invalid_request:", [
        "the http URL gets \"must use https\";",
        "the three internal addresses get \"must not point to an internal address\";",
        "the user-token request gets \"webhook_url is only for API keys\".",
    ])

    s += step("Step 13 — A new secret replaces the old one (free)")
    s += numbered([
        "Click <b>Webhook secret</b> again (confirm the dialog) and copy the new secret.",
        "Run step 11 again.",
        "Run verify.py on the new call with the <b>old</b> secret: " + OK + "<font name='Mono'>BAD signature</font>.",
        "Run it with the <b>new</b> secret: " + OK + "<font name='Mono'>signature OK</font>.",
    ])

    s += step("Step 14 (optional) — Receiver on your PC: failures and retries (free)",
              "This step uses a receiver script that checks every webhook it receives.")
    s += numbered([
        "Add <font name='Mono'>WEBHOOK_ALLOW_PRIVATE=true</font> to .env, then run "
        "<font name='Mono'>docker compose up -d</font> again.",
        "Save this as <font name='Mono'>receiver.py</font>:",
    ])
    s.append(code("""
import hashlib, hmac, sys, time
from http.server import BaseHTTPRequestHandler, HTTPServer
SECRET = sys.argv[1]
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        parts = dict(p.split("=", 1) for p in self.headers["X-OFD-Signature"].split(","))
        want = hmac.new(SECRET.encode(), parts["t"].encode() + b"." + body, hashlib.sha256).hexdigest()
        ok = hmac.compare_digest(want, parts["v1"]) and abs(time.time() - int(parts["t"])) < 300
        print(self.headers["X-OFD-Event"], self.headers["X-OFD-Delivery"],
              "OK" if ok else "BAD SIGNATURE", flush=True)
        self.send_response(204 if ok else 401)
        self.end_headers()
HTTPServer(("0.0.0.0", 9000), H).serve_forever()
"""))
    s.append(P("3. Run the three cases below. Each time, start the search with "
               "<font name='Mono'>\"webhook_url\":\"http://host.docker.internal:9000/hook\"</font> and read the job "
               "afterwards.", "bullet"))
    s.append(T([
        ["Case", "How", "Expected"],
        ["Correct secret", "<font name='Mono'>python receiver.py \"$SECRET\"</font>",
         "The receiver prints <font name='Mono'>search.completed srch_… OK</font>. The job shows delivered, "
         "http_status 204."],
        ["Wrong secret", "<font name='Mono'>python receiver.py whsec_wrong</font>",
         "The receiver prints BAD SIGNATURE and answers 401. The job shows "
         "<font name='Mono'>\"status\":\"failed\",\"attempts\":1,\"http_status\":401</font>. A 4xx answer is not retried."],
        ["Receiver stopped", "Stop the script (Ctrl+C)",
         "After about 35 s the job shows <font name='Mono'>\"status\":\"failed\",\"attempts\":3</font> with "
         "\"connection refused\". The server log has a <i>webhook not delivered</i> warning."],
    ], [80, 140, W - 220]))
    s.append(Spacer(1, 6))
    s.append(P("4. When you are done, remove WEBHOOK_ALLOW_PRIVATE from .env and run "
               "<font name='Mono'>docker compose up -d</font> again.", "bullet"))

    s += step("Step 15 — Dashboard per client (free)",
              "Wait about 30 s, because the counts are saved every 30 s. Then:", [
                  "<b>Admin panel → Dashboard → API clients table:</b> step12 agent shows searches ≥ 5, live 1, "
                  "cached ≥ 4, plus an estimated cost and today's date.",
                  "Or with a command:",
              ], ("code", """
curl -s "localhost:3002/admin/v1/usage?days=1" -H "Authorization: Bearer $ADMIN_TOKEN" \\
  | grep -o '"clients":\\[[^]]*\\]'
"""), OK + "a row with \"name\":\"step12 agent\", \"live\":1, \"cost_usd_estimate\":….")

    s += step("Step 16 — Audit log and logs never contain the secret (free)", ("code", """
curl -s "localhost:3002/admin/v1/audit?action=clients.webhook_secret" \\
  -H "Authorization: Bearer $ADMIN_TOKEN"
docker compose logs server | grep -c "whsec_"      # must be 0
docker compose logs server | grep -c "ofd_live_"   # must be 0
"""), OK + "the audit entries show only <font name='Mono'>\"prefix\":\"whsec_xxxxxxxx\"</font> (and \"replaced\" "
           "after step 13). Both counts are 0.")

    s += step("Step 17 (optional) — Daily limit refused before a job starts (costs one live search)")
    s += numbered([
        "Make a client with <font name='Mono'>\"daily_quota\":1</font>.",
        "Run one live search with a <b>new</b> title through it.",
        "Start a second <b>new</b>, uncached title with POST /api/v1/searches.",
    ])
    s.append(P(OK + "the second request answers <font name='Mono'>429 quota_exceeded</font> immediately, with "
                    "Retry-After and X-RateLimit-Remaining: 0. No job is created."))

    s += h1("What to report back")
    s += numbered([
        "Which steps passed or failed, with the status code and <font name='Mono'>docker compose logs server</font> "
        "lines for any failure.",
        "Whether polling every 5 s suits your agents.",
        "Whether one webhook secret per client is enough, or you want a fixed webhook URL per client set in the "
        "admin panel.",
    ])
    return s


class Doc(SimpleDocTemplate):
    pass


def build():
    story = cover() + part1() + part2()
    doc = Doc(OUT, pagesize=A4, leftMargin=MARGIN, rightMargin=MARGIN, topMargin=18 * mm,
              bottomMargin=16 * mm, title=TITLE, author="owis_find_deal_engine")
    doc.build(story, onFirstPage=on_page, onLaterPages=on_page)
    print("written", OUT)


if __name__ == "__main__":
    build()
