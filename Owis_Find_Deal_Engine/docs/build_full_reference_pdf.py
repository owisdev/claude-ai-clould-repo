"""Builds docs/full_service_reference.pdf from docs/full_service_reference.md.

    pip install markdown                 # once
    python3 docs/reference/make_diagrams.py   # if a diagram changed
    python3 docs/build_full_reference_pdf.py

Needs Node.js with Playwright (`npm i -g playwright` + `npx playwright
install chromium`) to print the page. Without it, the script still writes
docs/full_service_reference.html: open it in Chrome / Edge and use
Print -> Save as PDF (A4, background graphics on).
"""
from __future__ import annotations

import base64
import os
import posixpath
import re
import shutil
import subprocess
import sys
import tempfile

import markdown

DOCS = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(DOCS, "full_service_reference.md")
HTML_OUT = os.path.join(DOCS, "full_service_reference.html")
PDF_OUT = os.path.join(DOCS, "full_service_reference.pdf")
GITHUB = "https://github.com/owisdev/claude-ai-clould-repo/blob/main/"
DOCS_IN_REPO = "Owis_Find_Deal_Engine/docs/"

CSS = """
@page { size: A4; margin: 18mm 16mm 18mm 16mm; }
:root { --ink:#1d2330; --muted:#5b6475; --line:#d9dde4; --accent:#1f5fbf; --soft:#f5f7fa; }
* { box-sizing: border-box; }
html { font-size: 10.2pt; }
body { font-family: Inter, "Segoe UI", Helvetica, Arial, sans-serif; color: var(--ink); line-height: 1.5; margin: 0; }
h1, h2, h3, h4 { line-height: 1.25; color: #0f1b33; }
h1 { font-size: 22pt; margin: 0 0 8pt; }
h2 { font-size: 16pt; margin: 0 0 10pt; padding-bottom: 5pt; border-bottom: 2.5px solid var(--accent); page-break-before: always; }
h3 { font-size: 12.5pt; margin: 16pt 0 6pt; color: #16305e; }
p, li { orphans: 3; widows: 3; }
a { color: var(--accent); text-decoration: none; }
code { font-family: "JetBrains Mono", Consolas, "Courier New", monospace; font-size: 8.6pt; background: #eef1f6;
       padding: 1px 4px; border-radius: 3px; word-break: break-word; }
pre { background: #0f1b2d; color: #e6edf7; padding: 9pt 11pt; border-radius: 6px; font-size: 8.2pt; line-height: 1.45;
      white-space: pre-wrap; word-break: break-all; page-break-inside: avoid; }
pre code { background: none; color: inherit; padding: 0; font-size: inherit; }
table { border-collapse: collapse; width: 100%; margin: 6pt 0 12pt; font-size: 8.8pt; page-break-inside: auto; }
tr { page-break-inside: avoid; }
th { background: #e9eef7; text-align: left; font-weight: 600; }
th, td { border: 1px solid var(--line); padding: 4pt 6pt; vertical-align: top; }
td code { font-size: 8pt; word-break: normal; overflow-wrap: break-word; }
tbody tr:nth-child(even) td { background: #fafbfd; }
blockquote { margin: 10pt 0; padding: 8pt 12pt; background: #fff8e6; border-left: 4px solid #e0a31e; border-radius: 4px; }
img { max-width: 100%; height: auto; display: block; margin: 8pt auto 12pt; page-break-inside: avoid; }
hr { display: none; } /* every section starts on a new page */
.cover { height: 255mm; display: flex; flex-direction: column; justify-content: space-between; page-break-after: always; }
.cover .band { background: linear-gradient(135deg, #0f2c5c, #1f5fbf); color: #fff; padding: 26mm 14mm 20mm; border-radius: 10px; }
.cover .band h1 { color: #fff; font-size: 30pt; margin: 0 0 6pt; }
.cover .band .sub { font-size: 14pt; opacity: .92; }
.cover .band .tag { margin-top: 14pt; font-size: 10.5pt; opacity: .85; }
.cover img { margin: 6mm auto; max-height: 120mm; }
.cover .meta { font-size: 9.5pt; color: var(--muted); border-top: 1px solid var(--line); padding-top: 6pt; }
.cover .meta b { color: var(--ink); }
.toc-page h2 { page-break-before: avoid; }
/* The document's own first heading and info table are on the cover. */
body > h1:first-of-type { display: none; }
"""


def strip_front_matter(text: str) -> str:
    if text.startswith("---"):
        end = text.find("\n---", 3)
        if end != -1:
            return text[end + 4:].lstrip("\n")
    return text


def inline_images(html: str) -> str:
    def repl(m: re.Match) -> str:
        src = m.group(1)
        path = os.path.normpath(os.path.join(DOCS, src))
        if not os.path.isfile(path):
            return m.group(0)
        with open(path, "rb") as f:
            data = base64.b64encode(f.read()).decode()
        mime = "image/svg+xml" if path.endswith(".svg") else "image/png"
        return f'src="data:{mime};base64,{data}"'

    return re.sub(r'src="([^"]+\.(?:svg|png))"', repl, html)


def github_links(html: str) -> str:
    def repl(m: re.Match) -> str:
        href = m.group(1)
        if href.startswith(("http", "#", "mailto:")):
            return m.group(0)
        target = posixpath.normpath(posixpath.join(DOCS_IN_REPO, href))
        kind = "tree/main/" if href.endswith("/") or "." not in posixpath.basename(href) else "blob/main/"
        return f'href="{GITHUB.replace("blob/main/", kind)}{target}"'

    return re.sub(r'href="([^"]+)"', repl, html)


def table_pipes(html: str) -> str:
    """In tables, Markdown needs "|" written as "\\|"; GitHub shows "|", so do the same."""
    return re.sub(r"<td>.*?</td>", lambda m: m.group(0).replace("\\|", "|"), html, flags=re.S)


def cover(arch_img: str) -> str:
    return f"""
<section class="cover">
  <div class="band">
    <h1>Global Product Search</h1>
    <div class="sub">Full Service Reference</div>
    <div class="tag">Owis Find Deal Engine · design, code, deployment and operations</div>
  </div>
  <img src="{arch_img}" alt="Architecture overview">
  <div class="meta">
    <b>Edition:</b> 11 November 2026 — state after step 14b parts 3 + 4 (deployed and tested)<br>
    <b>Service:</b> https://globalprodsearch.dawirli.com &nbsp;·&nbsp;
    <b>Code:</b> github.com/owisdev/claude-ai-clould-repo → Owis_Find_Deal_Engine/server<br>
    <b>Production:</b> Oracle Cloud VM (Dubai) · Docker Compose · Cloudflare Tunnel<br>
    <b>For:</b> the service owner
  </div>
</section>
"""


PRINT_JS = r"""
const { chromium } = require('playwright');
(async () => {
  const [html, pdf] = process.argv.slice(2);
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.goto('file://' + html, { waitUntil: 'load' });
  await page.pdf({
    path: pdf, format: 'A4', printBackground: true, preferCSSPageSize: true,
    displayHeaderFooter: true,
    headerTemplate: '<div style="font-size:7.5px;color:#8a92a3;width:100%;padding:0 16mm;text-align:right;font-family:Helvetica,Arial,sans-serif">Global Product Search — Full Service Reference</div>',
    footerTemplate: '<div style="font-size:7.5px;color:#8a92a3;width:100%;padding:0 16mm;display:flex;justify-content:space-between;font-family:Helvetica,Arial,sans-serif"><span>Edition 11 November 2026</span><span>Page <span class="pageNumber"></span> / <span class="totalPages"></span></span></div>',
  });
  await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
"""


def main() -> int:
    with open(SRC, encoding="utf-8") as f:
        text = strip_front_matter(f.read())
    body = markdown.markdown(text, extensions=["tables", "fenced_code", "toc", "sane_lists"],
                             extension_configs={"toc": {"permalink": False}})
    body = table_pipes(github_links(inline_images(body)))
    arch = inline_images('src="reference/img/01_architecture.svg"')[5:-1]
    page = (f'<!doctype html><html lang="en"><head><meta charset="utf-8">'
            f'<title>Global Product Search — Full Service Reference</title><style>{CSS}</style></head>'
            f'<body>{cover(arch)}{body}</body></html>')
    with open(HTML_OUT, "w", encoding="utf-8") as f:
        f.write(page)
    print("wrote", HTML_OUT)

    node = shutil.which("node")
    if not node:
        print("node not found: open the HTML in a browser and print it to PDF")
        return 0
    with tempfile.TemporaryDirectory() as tmp:
        js = os.path.join(tmp, "print.js")
        with open(js, "w") as f:
            f.write(PRINT_JS)
        env = dict(os.environ)
        r = subprocess.run([node, js, HTML_OUT, PDF_OUT], cwd=tmp, env=env)
        if r.returncode != 0:
            print("printing failed: open the HTML in a browser and print it to PDF")
            return r.returncode
    print("wrote", PDF_OUT)
    return 0


if __name__ == "__main__":
    sys.exit(main())
