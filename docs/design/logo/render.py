"""Regenerate gallery.png: python3 render.py (needs the playwright Python package and a chromium; not an app dependency)."""
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
    page = browser.new_page(viewport={"width": 1300, "height": 710})
    page.goto(f"http://127.0.0.1:{srv.server_address[1]}/gallery.html")
    page.wait_for_function("document.title === 'ready'")
    page.screenshot(path=str(here / "gallery.png"), full_page=True)
    browser.close()
