"""Self-check for the knob's cover script, against a local web server.

Run: uv run ansible/roles/home_assistant/tests/test_knob_cover.py
"""

import io
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from PIL import Image

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "files"))
import knob_cover  # noqa: E402

big = io.BytesIO()
Image.new("RGB", (1200, 900), "red").save(big, "JPEG", progressive=True)


class Web(BaseHTTPRequestHandler):
    def do_GET(self):
        body = big.getvalue() if self.path == "/cover.jpg" else b"<svg/>"
        self.send_response(200)
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


srv = ThreadingHTTPServer(("127.0.0.1", 0), Web)
threading.Thread(target=srv.serve_forever, daemon=True).start()
base = f"http://127.0.0.1:{srv.server_port}"

with tempfile.TemporaryDirectory() as d:
    out = Path(d) / "knob_cover.jpg"

    knob_cover.main(base + "/cover.jpg", str(out))
    img = Image.open(out)
    assert img.format == "JPEG" and img.size == (180, 135), (img.format, img.size)
    assert not img.info.get("progressive"), "the knob decodes baseline JPEGs only"

    # Home Assistant's own proxy paths are fetched from Home Assistant
    knob_cover.HA_URL = base
    out.unlink()
    knob_cover.main("/cover.jpg", str(out))
    assert out.exists()

    # not an image, not http, no cover: a black one, never the old cover or no file
    for url in (base + "/logo.svg", "file:///etc/passwd", ""):
        out.write_bytes(b"old")
        knob_cover.main(url, str(out))
        img = Image.open(out)
        assert img.size == (180, 180) and img.getextrema() == ((0, 0),) * 3, url

print("ok")
