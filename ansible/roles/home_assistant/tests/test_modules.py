"""Self-check for the home_assistant_* modules' logic, against fake registries.

Run: uv run ansible/roles/home_assistant/tests/test_modules.py
"""

import sys
from pathlib import Path

import ansible.module_utils

role = Path(__file__).resolve().parents[1]
ansible.module_utils.__path__.append(str(role / "module_utils"))  # as Ansible does for a role's module_utils
sys.path.insert(0, str(role / "library"))
from home_assistant_areas import reconcile  # noqa: E402
from home_assistant_http import plan  # noqa: E402

# areas: what onboarding creates
areas = {"living_room": "Living Room", "kitchen": "Kitchen", "bedroom": "Bedroom"}


def call(type, **args):
    match type:
        case "config/area_registry/list":
            return [{"area_id": k, "name": v} for k, v in areas.items()]
        case "config/area_registry/create":
            areas[args["name"].lower().replace(" ", "_")] = args["name"]
        case "config/area_registry/delete":
            del areas[args["area_id"]]


want = ["Cloud Office"]
assert reconcile(call, want, check_mode=True) == (["Cloud Office"], ["Bedroom", "Kitchen", "Living Room"])
assert len(areas) == 3, "check mode changed the registry"
assert reconcile(call, want, check_mode=False) == (["Cloud Office"], ["Bedroom", "Kitchen", "Living Room"])
assert list(areas.values()) == ["Cloud Office"], areas
assert reconcile(call, want, check_mode=False) == ([], [])  # idempotent

# http: the default stable config, as http/config returns it
stable = {"server_port": 8123, "cors_allowed_origins": ["https://cast.home-assistant.io"], "ip_ban_enabled": True,
          "created_at": "2026-09-28T12:26:15+00:00", "error": None, "error_message": None}
proxy = {"use_x_forwarded_for": True, "trusted_proxies": ["127.0.0.1", "::1"]}
staged = plan(stable, proxy)
assert staged == {"server_port": 8123, "cors_allowed_origins": ["https://cast.home-assistant.io"], "ip_ban_enabled": True,
                  "use_x_forwarded_for": True, "trusted_proxies": ["127.0.0.1/32", "::1/128"]}, staged
assert plan({**staged, "created_at": "later", "error": None, "error_message": None}, proxy) is None  # promoted
print("ok")
