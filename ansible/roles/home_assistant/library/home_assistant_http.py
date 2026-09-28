#!/usr/bin/python
"""Ansible module: set Home Assistant's HTTP settings."""

from ipaddress import ip_network

from ansible.module_utils.basic import AnsibleModule, missing_required_lib
from ansible.module_utils.home_assistant import ERRORS, WEBSOCKET_ERR, connect, wait_restart

DOCUMENTATION = r"""
module: home_assistant_http
short_description: Set Home Assistant's HTTP settings
description:
  - Since 2026.x the HTTP settings live in .storage; the http YAML is migrated
    once, on the first start, and ignored afterwards. This goes through the
    websocket API instead. A change is staged as pending, Home Assistant
    restarts with it on trial, and the module promotes it once Home Assistant
    is back. Without the promotion Home Assistant reverts after 5 minutes.
requirements: [websocket-client (Debian python3-websocket)]
options:
  config:
    description:
      - The settings to change, e.g. use_x_forwarded_for and trusted_proxies.
        Settings left out keep their current value.
    type: dict
    required: true
  token:
    description: A long-lived access token of an admin (profile > security).
    type: str
    required: true
  url:
    description: The websocket API.
    type: str
    default: ws://127.0.0.1:8123/api/websocket
"""

EXAMPLES = r"""
- name: Trust the reverse proxy
  home_assistant_http:
    config:
      use_x_forwarded_for: true
      trusted_proxies: [127.0.0.1, "::1"]
    token: "{{ home_assistant_token }}"
"""

RETURN = r"""
config:
  description: The HTTP settings now in effect.
  type: dict
  returned: always
"""

META = ("created_at", "error", "error_message")


def plan(stable, config):
    """The config to stage over stable, normalized as Home Assistant stores it; None when stable has it already."""
    stable = {k: v for k, v in stable.items() if k not in META}
    config = dict(config)
    if "trusted_proxies" in config:
        config["trusted_proxies"] = [str(ip_network(p)) for p in config["trusted_proxies"]]
    want = {**stable, **config}
    return None if want == stable else want


def main():
    module = AnsibleModule(
        argument_spec={
            "config": {"type": "dict", "required": True},
            "token": {"type": "str", "required": True, "no_log": True},
            "url": {"type": "str", "default": "ws://127.0.0.1:8123/api/websocket"},
        },
        supports_check_mode=True,
    )
    if WEBSOCKET_ERR:
        module.fail_json(msg=missing_required_lib("websocket-client"), exception=WEBSOCKET_ERR)
    url, token = module.params["url"], module.params["token"]
    try:
        ws, call = connect(url, token)
        try:
            state = call(type="http/config")
            want = plan(state["stable"], module.params["config"])
            if want is None or module.check_mode:
                module.exit_json(changed=want is not None, config=want or state["stable"])
            pending = {k: v for k, v in (state["pending"] or {}).items() if k not in META}
            # a run cut short after the restart only has the promotion left
            if state["active_config_type"] != "pending" or pending != want:
                if not call(type="http/config/configure", config=want)["restart"]:
                    # already pending, but not what runs: restart to try it
                    call(type="call_service", domain="homeassistant", service="restart")
                ws, call = wait_restart(ws, url, token)
                state = call(type="http/config")
                if state["active_config_type"] != "pending":
                    error = (state["pending"] or {}).get("error_message") or (state["pending"] or {}).get("error")
                    module.fail_json(msg=f"Home Assistant did not start with the new HTTP config: {error}")
            call(type="http/config/promote")
        finally:
            ws.close()
    except ERRORS as err:
        module.fail_json(msg=str(err))
    module.exit_json(changed=True, config=want)


if __name__ == "__main__":
    main()
