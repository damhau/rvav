"""The inventory plugin through ansible-inventory, against a fake rvav API.

Run with: make test-ansible (uv, ansible-core and pytest).
"""

import json
import os
import pathlib
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

COLLECTIONS = pathlib.Path(__file__).resolve().parents[4]  # the directory holding ansible_collections
TOKEN = "rvav_pat_test"
INVENTORY = {
    "_meta": {
        "hostvars": {
            "web-dev-app001": {
                "ansible_host": "10.31.10.20",
                "rvav_project": "web",
                "rvav_environment": "dev",
                "rvav_owner": "thomas.nguyen@corp.example",
                "rvav_fqdn": "web-dev-app001.corp.example",
                "rvav_os_family": "linux",
            },
            "fin-prd-db001": {
                "ansible_host": "10.22.30.40",
                "rvav_project": "fin",
                "rvav_environment": "prd",
                "rvav_owner": "sophie.brunner@corp.example",
                "rvav_os_family": "linux",
            },
        }
    },
    "all": {"children": ["env_dev", "env_prd", "project_fin", "project_web", "rvav"]},
    "rvav": {"hosts": ["fin-prd-db001", "web-dev-app001"]},
    "project_web": {"hosts": ["web-dev-app001"]},
    "project_fin": {"hosts": ["fin-prd-db001"]},
    "env_dev": {"hosts": ["web-dev-app001"]},
    "env_prd": {"hosts": ["fin-prd-db001"]},
}


class FakeRvav(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.headers.get("Authorization") != "Bearer " + TOKEN:
            self.send_response(401)
            self.end_headers()
            return
        if self.path != "/api/v1/ansible/inventory":
            self.send_response(404)
            self.end_headers()
            return
        body = json.dumps(INVENTORY).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


@pytest.fixture
def rvav():
    server = HTTPServer(("127.0.0.1", 0), FakeRvav)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    yield "http://127.0.0.1:%d" % server.server_port
    server.shutdown()


def ansible_env(tmp_path, url, token):
    return dict(os.environ, ANSIBLE_COLLECTIONS_PATH=str(COLLECTIONS), ANSIBLE_HOME=str(tmp_path / "ansible"),
                RVAV_URL=url, RVAV_TOKEN=token, ANSIBLE_INVENTORY_UNPARSED_FAILED="true",
                ANSIBLE_INVENTORY_ENABLED="rvav.platform.rvav")


def inventory(tmp_path, url, token=TOKEN, extra=""):
    config = tmp_path / "rvav.yml"
    config.write_text("plugin: rvav.platform.rvav\n" + extra)
    env = ansible_env(tmp_path, url, token)
    return subprocess.run(["ansible-inventory", "-i", str(config), "--list"], capture_output=True, text=True, env=env)


def plain(value):
    """A value as ansible-inventory prints it: since ansible-core 2.19, data
    from an inventory plugin is never a template, and shows as
    {"__ansible_unsafe": value}."""
    if isinstance(value, dict) and set(value) == {"__ansible_unsafe"}:
        return value["__ansible_unsafe"]
    if isinstance(value, dict):
        return {k: plain(v) for k, v in value.items()}
    return value


def test_hosts_groups_and_vars(tmp_path, rvav):
    out = inventory(tmp_path, rvav, extra="keyed_groups:\n  - key: rvav_owner.split('@')[0]\n    prefix: owner\n")
    assert out.returncode == 0, out.stderr
    inv = json.loads(out.stdout)
    hostvars = plain(inv["_meta"]["hostvars"])
    assert hostvars["web-dev-app001"]["ansible_host"] == "10.31.10.20"
    assert hostvars["fin-prd-db001"]["rvav_owner"] == "sophie.brunner@corp.example"
    assert inv["project_web"]["hosts"] == ["web-dev-app001"]
    assert inv["env_prd"]["hosts"] == ["fin-prd-db001"]
    assert sorted(inv["rvav"]["hosts"]) == ["fin-prd-db001", "web-dev-app001"]
    assert inv["owner_thomas_nguyen"]["hosts"] == ["web-dev-app001"]


def test_wrong_token(tmp_path, rvav):
    out = inventory(tmp_path, rvav, token="rvav_pat_wrong")
    assert out.returncode != 0
    assert "401" in out.stderr


def test_only_rvav_files(tmp_path, rvav):
    other = tmp_path / "hosts.yml"
    other.write_text("plugin: rvav.platform.rvav\n")
    env = ansible_env(tmp_path, rvav, TOKEN)
    out = subprocess.run(["ansible-inventory", "-i", str(other), "--list"], capture_output=True, text=True, env=env)
    assert out.returncode != 0
