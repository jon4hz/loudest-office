#!/usr/bin/python
"""Ansible module: make Home Assistant's areas exactly the given list."""

from ansible.module_utils.basic import AnsibleModule, missing_required_lib
from ansible.module_utils.home_assistant import ERRORS, WEBSOCKET_ERR, connect

DOCUMENTATION = r"""
module: home_assistant_areas
short_description: Make Home Assistant's areas exactly the given list
description:
  - Areas live in Home Assistant's registry, not in YAML, so this goes through
    the websocket API. Missing areas are created, all others deleted; their
    devices and entities lose the area.
requirements: [websocket-client (Debian python3-websocket)]
options:
  areas:
    description: The area names.
    type: list
    elements: str
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
- name: Set the areas
  home_assistant_areas:
    areas: [Cloud Office]
    token: "{{ home_assistant_token }}"
"""

RETURN = r"""
created:
  description: The names of the areas created.
  type: list
  elements: str
  returned: always
deleted:
  description: The names of the areas deleted.
  type: list
  elements: str
  returned: always
"""


def reconcile(call, want, check_mode):
    """Create the areas in want that are missing and delete the others; call runs one API command."""
    have = {a["name"]: a["area_id"] for a in call(type="config/area_registry/list")}
    created, deleted = sorted(set(want) - have.keys()), sorted(have.keys() - set(want))
    if not check_mode:
        for name in created:
            call(type="config/area_registry/create", name=name)
        for name in deleted:
            call(type="config/area_registry/delete", area_id=have[name])
    return created, deleted


def main():
    module = AnsibleModule(
        argument_spec={
            "areas": {"type": "list", "elements": "str", "required": True},
            "token": {"type": "str", "required": True, "no_log": True},
            "url": {"type": "str", "default": "ws://127.0.0.1:8123/api/websocket"},
        },
        supports_check_mode=True,
    )
    if WEBSOCKET_ERR:
        module.fail_json(msg=missing_required_lib("websocket-client"), exception=WEBSOCKET_ERR)
    try:
        ws, call = connect(module.params["url"], module.params["token"])
        try:
            created, deleted = reconcile(call, module.params["areas"], module.check_mode)
        finally:
            ws.close()
    except ERRORS as err:
        module.fail_json(msg=str(err))
    module.exit_json(changed=bool(created or deleted), created=created, deleted=deleted)


if __name__ == "__main__":
    main()
