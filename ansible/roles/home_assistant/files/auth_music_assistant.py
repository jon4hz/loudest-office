"""Home Assistant command_line auth provider: log in with a Music Assistant user.

Home Assistant runs this with $username and $password; exit 0 accepts the
login. The check is a login to Music Assistant, whose session is closed again
right away. The output names the user and their Home Assistant group: MA
admins become admins, guests read-only, everyone else users.
"""

import json
import os
import sys
import urllib.error
import urllib.request

MA_URL = "http://127.0.0.1:8095"  # host network
GROUPS = {"admin": "system-admin", "guest": "system-read-only"}


def post(url, body=None, token=None):
    req = urllib.request.Request(url, json.dumps(body or {}).encode(), {"Content-Type": "application/json"})
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.load(resp)


def check(base, username, password):
    """The meta lines for a valid Music Assistant login, None for anything else."""
    creds = {"credentials": {"username": username, "password": password}, "device_name": "Home Assistant login"}
    try:
        res = post(f"{base}/auth/login", creds)
    except (urllib.error.URLError, OSError, ValueError):  # HTTPError (401) is a URLError
        return None
    if not res.get("success"):
        return None
    try:
        post(f"{base}/auth/logout", token=res["token"])
    except (urllib.error.URLError, OSError, ValueError):
        pass  # the check is done either way; the session then just stays in MA's token list
    user = res["user"]
    if not user.get("enabled", True):
        return None
    return [f"name = {user.get('display_name') or user['username']}", f"group = {GROUPS.get(user['role'], 'system-users')}"]


if __name__ == "__main__":
    meta = check(MA_URL, os.environ.get("username", ""), os.environ.get("password", ""))
    if meta is None:
        sys.exit(1)
    print("\n".join(meta))
