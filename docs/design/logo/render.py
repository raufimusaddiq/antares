"""Render the chosen mark at 16 / 32 / 180 px on light and dark plus a circle crop: python3 render.py

Writes selected.png (needs the playwright Python package and a chromium; not an app dependency).

gallery.png is NOT written by this script. It is the frozen round-1 record: all three concepts (A · Core, B · Rival,
C · Tail) side by side with the round-0 drafts, rendered before the rejected SVGs were removed. Keep it as the record
of why B was chosen; the concept SVGs are in git history at commit 2e55ea2 if it ever needs redrawing.
"""
import functools, http.server, pathlib, threading
from playwright.sync_api import sync_playwright

here = pathlib.Path(__file__).resolve().parent


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Quiet, directory=str(here)))
threading.Thread(target=srv.serve_forever, daemon=True).start()
with sync_playwright() as pw:
    browser = pw.chromium.launch()
    page = browser.new_page(viewport={"width": 560, "height": 480})
    page.goto(f"http://127.0.0.1:{srv.server_address[1]}/gallery.html")
    page.wait_for_function("document.title === 'ready'", timeout=10000)
    page.screenshot(path=str(here / "selected.png"), full_page=True)
    browser.close()
