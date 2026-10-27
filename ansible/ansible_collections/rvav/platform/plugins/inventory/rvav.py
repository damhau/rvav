# rvav.platform.rvav: rvav's VMs as an Ansible inventory (docs/integrations.md).
from __future__ import annotations

DOCUMENTATION = r"""
name: rvav
short_description: rvav's VMs as an inventory
description:
  - Reads the VMs rvav manages from its API (C(/api/v1/ansible/inventory)), with the
    address rvav reserved for each as C(ansible_host), groups by project
    (C(project_<code>)), environment (C(env_<key>)), catalog item (C(item_<slug>)) and
    OS family (C(os_<family>)), and rvav's context as host variables (C(rvav_deployment_id),
    C(rvav_project), C(rvav_owner), C(rvav_fqdn) and others).
  - The configuration file must end in C(rvav.yml) or C(rvav.yaml).
extends_documentation_fragment:
  - constructed
options:
  plugin:
    description: The name of this plugin.
    required: true
    choices: [rvav.platform.rvav]
  url:
    description: rvav's URL, for example U(https://rvav.corp.example).
    required: true
    env:
      - name: RVAV_URL
  token:
    description: An rvav API token of a user with the auditor role.
    required: true
    env:
      - name: RVAV_TOKEN
  validate_certs:
    description: Check rvav's TLS certificate.
    type: bool
    default: true
    env:
      - name: RVAV_VALIDATE_CERTS
  timeout:
    description: Seconds to wait for rvav.
    type: int
    default: 30
"""

EXAMPLES = r"""
# rvav.yml, with RVAV_URL and RVAV_TOKEN set (in AWX, by the "rvav API" credential)
plugin: rvav.platform.rvav
keyed_groups:
  - key: rvav_owner
    prefix: owner
"""

import json

from ansible.errors import AnsibleError
from ansible.module_utils.common.text.converters import to_native
from ansible.module_utils.urls import open_url
from ansible.plugins.inventory import BaseInventoryPlugin, Constructable
from ansible.utils.unsafe_proxy import wrap_var


class InventoryModule(BaseInventoryPlugin, Constructable):
    NAME = "rvav.platform.rvav"

    def verify_file(self, path):
        return super().verify_file(path) and path.endswith(("rvav.yml", "rvav.yaml"))

    def parse(self, inventory, loader, path, cache=True):
        super().parse(inventory, loader, path, cache)
        self._read_config_data(path)
        data = self._fetch()
        for group, members in data.items():
            if group in ("_meta", "all"):
                continue
            self.inventory.add_group(group)
            for host in members.get("hosts") or []:
                self.inventory.add_host(host, group=group)
        strict = self.get_option("strict")
        for host, variables in (data.get("_meta") or {}).get("hostvars", {}).items():
            # rvav's values are data, never templates: they include what users
            # typed in forms. ansible-core 2.19 and later do this themselves.
            variables = wrap_var(variables)
            self.inventory.add_host(host)
            for key, value in variables.items():
                self.inventory.set_variable(host, key, value)
            self._set_composite_vars(self.get_option("compose"), variables, host, strict=strict)
            self._add_host_to_composed_groups(self.get_option("groups"), variables, host, strict=strict)
            self._add_host_to_keyed_groups(self.get_option("keyed_groups"), variables, host, strict=strict)

    def _fetch(self):
        url = self.get_option("url").rstrip("/") + "/api/v1/ansible/inventory"
        try:
            resp = open_url(
                url,
                headers={"Authorization": "Bearer " + self.get_option("token"), "Accept": "application/json"},
                validate_certs=self.get_option("validate_certs"),
                timeout=self.get_option("timeout"),
            )
            return json.loads(resp.read())
        except Exception as e:  # HTTP errors, refused connections, bad JSON
            raise AnsibleError("rvav inventory from %s: %s" % (url, to_native(e)))
