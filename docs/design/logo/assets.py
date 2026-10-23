"""Regenerate the favicon set and the OG card into web/public/ from b-rival.svg.

    python3 docs/design/logo/assets.py

One-shot build-time script, not an app dependency: needs the playwright Python package and a chromium.
Fonts come from web/public/fonts, so the card uses the same faces as the app.
"""
import functools, http.server, pathlib, re, threading
from playwright.sync_api import sync_playwright

here = pathlib.Path(__file__).resolve().parent
public = here.parents[2] / "web" / "public"
svg = (here / "b-rival.svg").read_text()
inner = re.search(r"<svg[^>]*>(.*)</svg>", svg, re.S).group(1)

BASE, LIGHT, MID = "#08090b", "#f4f6f8", "#7d8794"
INK, SUB = "#f4f6f8", "#aab2bd"


def mark(colour: str) -> str:
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="{colour}">{inner}</svg>'


# Favicon: one flat colour that holds on either tab bar. Browsers that honour the media query get the
# exact ink for their scheme; the rest get the mid grey default.
(public / "favicon.svg").write_text(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">'
    f"<style>path,circle{{fill:{MID}}}@media(prefers-color-scheme:light){{path,circle{{fill:#10141a}}}}"
    f"@media(prefers-color-scheme:dark){{path,circle{{fill:{LIGHT}}}}}</style>{inner}</svg>\n"
)

OG = f"""<!doctype html><meta charset=utf-8><style>
@font-face{{font-family:SG;src:url(/fonts/space-grotesk-latin-wght-normal.woff2);font-weight:300 700}}
@font-face{{font-family:IN;src:url(/fonts/inter-latin-wght-normal.woff2);font-weight:100 900}}
html,body{{margin:0}}body{{width:1200px;height:630px;background:{BASE};color:{INK};display:flex;align-items:center;padding:0 96px;box-sizing:border-box;gap:64px}}
.t b{{display:block;font:600 164px/1 SG;letter-spacing:-.045em}}
.t span{{display:block;margin-top:28px;font:400 46px/1.2 IN;color:{SUB};letter-spacing:-.01em}}
</style><body>{mark(INK).replace("<svg ", '<svg width="264" height="264" ')}<div class=t><b>Antares</b><span>A private chat, by invitation</span></div>
<script>document.fonts.ready.then(()=>document.title='ready')</script>"""

ICON = lambda px, colour, bg, pad: (
    f'<!doctype html><body style="margin:0;width:{px}px;height:{px}px;background:{bg};display:grid;place-items:center">'
    + mark(colour).replace("<svg ", f'<svg width="{round(px * (1 - 2 * pad))}" height="{round(px * (1 - 2 * pad))}" ')
)


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass


srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Quiet, directory=str(public)))
threading.Thread(target=srv.serve_forever, daemon=True).start()
base = f"http://127.0.0.1:{srv.server_address[1]}"

with sync_playwright() as pw:
    b = pw.chromium.launch()
    for px in (16, 32, 48):
        pg = b.new_page(viewport={"width": px, "height": px})
        pg.set_content(ICON(px, MID, "transparent", 0.04))
        pg.screenshot(path=str(public / f"favicon-{px}.png"), omit_background=True)
    for px, filename in ((180, "apple-touch-icon.png"), (192, "icon-192.png"), (512, "icon-512.png")):
        pg = b.new_page(viewport={"width": px, "height": px})
        pg.set_content(ICON(px, LIGHT, BASE, 0.2))
        pg.screenshot(path=str(public / filename))
    pg = b.new_page(viewport={"width": 1200, "height": 630})
    (public / "_og.html").write_text(OG)
    pg.goto(f"{base}/_og.html")
    pg.wait_for_function("document.title === 'ready'")
    pg.screenshot(path=str(public / "og.png"))
    (public / "_og.html").unlink()
    b.close()
