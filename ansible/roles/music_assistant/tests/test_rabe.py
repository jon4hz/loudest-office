"""Self-check for the RaBe songticker parser.

Run: uv run ansible/roles/music_assistant/tests/test_rabe.py
"""

import sys
import types
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "files" / "plugins"))
# importing the real MusicProvider boots half the server; stub the one base class the plugin needs
mp = types.ModuleType("music_assistant.models.music_provider")
mp.MusicProvider = type("MusicProvider", (), {})
sys.modules["music_assistant.models.music_provider"] = mp

from rabe import parse_ticker  # noqa: E402

SHOW = """<?xml version='1.0' encoding='UTF-8'?>
<ticker xmlns="http://rabe.ch/schema/ticker.xsd" xmlns:xlink="http://www.w3.org/1999/xlink">
  <show id="1b2cb33d">
    <name>Klangbecken</name>
    <link xlink:type="simple" xlink:href="https://rabe.ch/klangbecken">https://rabe.ch/klangbecken</link>
    <startTime>2026-09-10T11:30:00+02:00</startTime>
    <endTime>2026-09-10T15:00:00+02:00</endTime>
  </show>
"""
TRACK = """  <track id="76829ebf">
    <show ref="1b2cb33d">Klangbecken</show>
    <artist>Jack White</artist>
    <title>Dollar Bill</title>
    <startTime>2026-09-10T14:41:39+02:00</startTime>
    <endTime>2026-09-10T14:43:19+02:00</endTime>
  </track>
"""
NOW = datetime.fromisoformat("2026-09-10T14:42:09+02:00").timestamp()

md = parse_ticker(SHOW + TRACK + "</ticker>", NOW)
assert md.title == "Dollar Bill", md
assert md.artist == "Jack White"
assert md.album == "Klangbecken"
assert md.uri == "https://rabe.ch/klangbecken"
assert md.duration == 100
assert md.elapsed_time == 30
assert md.elapsed_time_last_updated == NOW

# track overran its declared end: keep title/artist, drop progress
md = parse_ticker(SHOW + TRACK + "</ticker>", NOW + 600)
assert md.title == "Dollar Bill" and md.duration is None and md.elapsed_time is None, md

md = parse_ticker(SHOW + "</ticker>", NOW)
assert md.title == "Klangbecken", md
assert md.artist is None and md.album is None
assert md.uri == "https://rabe.ch/klangbecken"
assert md.duration is None and md.elapsed_time is None

print("ok")
