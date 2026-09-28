"""The Home Assistant websocket API, shared by this role's modules."""

import itertools
import json
import time
import traceback

try:
    import websocket
except ImportError:
    websocket = None
    WEBSOCKET_ERR = traceback.format_exc()
else:
    WEBSOCKET_ERR = None

# what connect and its call raise
ERRORS = (OSError, RuntimeError) + ((websocket.WebSocketException,) if websocket else ())


def connect(url, token):
    """Log in to the websocket API; returns the connection and a function that runs one command."""
    ws = websocket.create_connection(url, timeout=30)
    ws.recv()  # auth_required
    ws.send(json.dumps({"type": "auth", "access_token": token}))
    msg = json.loads(ws.recv())
    if msg["type"] != "auth_ok":
        ws.close()
        raise RuntimeError(f"auth: {msg.get('message', msg['type'])}")
    ids = itertools.count(1)

    def call(**cmd):
        ws.send(json.dumps({"id": next(ids), **cmd}))
        msg = json.loads(ws.recv())
        if not msg["success"]:
            raise RuntimeError(f"{cmd['type']}: {msg['error']['message']}")
        return msg["result"]

    return ws, call


def wait_restart(ws, url, token, timeout=240):
    """Wait for a restart the last command started: the old instance closes ws, then reconnect."""
    deadline = time.monotonic() + timeout
    while True:  # until the old instance closes the connection
        try:
            ws.recv()
        except websocket.WebSocketTimeoutException:
            if time.monotonic() > deadline:
                raise RuntimeError("Home Assistant did not restart") from None
        except ERRORS:
            break
    while True:
        try:
            return connect(url, token)
        except ERRORS as err:
            if str(err).startswith("auth:") or time.monotonic() > deadline:
                raise
            time.sleep(5)
