"""Self-check for the Music Assistant login check, against a fake Music Assistant.

Run: uv run ansible/roles/home_assistant/tests/test_auth_music_assistant.py
"""

import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "files"))
from auth_music_assistant import check  # noqa: E402

USERS = {
    "anna": ("pw", {"username": "anna", "display_name": "Anna", "role": "admin", "enabled": True}),
    "bob": ("pw", {"username": "bob", "display_name": None, "role": "user", "enabled": True}),
    "gast": ("pw", {"username": "gast", "role": "guest", "enabled": True}),
    "old": ("pw", {"username": "old", "role": "user", "enabled": False}),
}
logouts = []


class MA(BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if self.path == "/auth/logout":
            logouts.append(self.headers["Authorization"])
            return self.reply(200, {"success": True})
        c = body["credentials"]
        pw, user = USERS.get(c["username"], (None, None))
        if pw is None or pw != c["password"]:
            return self.reply(401, {"success": False, "error": "Invalid username or password"})
        self.reply(200, {"success": True, "token": "tok-" + c["username"], "user": user})

    def reply(self, status, body):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(body).encode())

    def log_message(self, *args):
        pass


server = ThreadingHTTPServer(("127.0.0.1", 0), MA)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = f"http://127.0.0.1:{server.server_port}"

assert check(base, "anna", "pw") == ["name = Anna", "group = system-admin"]
assert check(base, "bob", "pw") == ["name = bob", "group = system-users"]
assert check(base, "gast", "pw") == ["name = gast", "group = system-read-only"]
assert check(base, "anna", "wrong") is None
assert check(base, "nobody", "pw") is None
assert check(base, "old", "pw") is None  # disabled in MA
assert logouts == ["Bearer tok-anna", "Bearer tok-bob", "Bearer tok-gast", "Bearer tok-old"], logouts
server.shutdown()
assert check(base, "anna", "pw") is None  # MA down
print("ok")
