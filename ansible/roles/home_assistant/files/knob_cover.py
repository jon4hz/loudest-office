"""Home Assistant shell_command: the track's cover, small, for the knob.

Music Assistant hands out covers from the internet as they are, often 1200 px,
which the knob needs 8 s to decode, frozen meanwhile. This fetches the URL
(argv[1], empty = no cover) and stores it at 180 px as www/knob_cover.jpg,
which Home Assistant serves as /local/knob_cover.jpg. No cover, or one that
does not load (a radio's SVG logo), is a black one, on the knob's black page:
no file would be a 404 and an error in the knob's log on every track.
"""

import io
import os
import sys
import urllib.request

from PIL import Image

HA_URL = "http://127.0.0.1:8123"  # for the paths of Home Assistant's own image proxy
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "www", "knob_cover.jpg")
SIDE = 180
MAX_BYTES = 20 << 20


def shrink(data):
    """data as a baseline JPEG of at most SIDE x SIDE (the knob decodes no progressive ones); None = black."""
    img = Image.open(io.BytesIO(data)).convert("RGB") if data else Image.new("RGB", (SIDE, SIDE))
    img.thumbnail((SIDE, SIDE))
    out = io.BytesIO()
    img.save(out, "JPEG", quality=85)
    return out.getvalue()


def fetch(url):
    if url.startswith("/"):
        url = HA_URL + url
    if not url.startswith(("http://", "https://")):  # urllib would also open file:
        raise ValueError(f"not an http url: {url[:40]}")
    with urllib.request.urlopen(url, timeout=10) as r:
        return r.read(MAX_BYTES)


def main(url, out=OUT):
    try:
        data = shrink(fetch(url) if url else None)
    except Exception as e:  # noqa: BLE001: whatever it was, there is no cover
        print(f"knob_cover: {e}", file=sys.stderr)
        data = shrink(None)
    with open(out + ".tmp", "wb") as f:
        f.write(data)
    os.replace(out + ".tmp", out)  # never a half-written file for the knob


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "")
