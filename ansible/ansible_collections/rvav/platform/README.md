# rvav.platform

Ansible content for [rvav](https://github.com/damhau/rvav).

## Inventory plugin `rvav.platform.rvav`

The VMs rvav manages, read from `GET /api/v1/ansible/inventory`:
`ansible_host` is the address rvav reserved, groups are `rvav`,
`project_<code>`, `env_<key>`, `item_<slug>` and `os_<family>`, and host
variables carry rvav's context (`rvav_deployment_id`, `rvav_project`,
`rvav_owner`, `rvav_fqdn`, ...). It supports `compose`, `groups` and
`keyed_groups`.

```yaml
# rvav.yml
plugin: rvav.platform.rvav
```

with `RVAV_URL` and `RVAV_TOKEN` (an API token of a user with the auditor
role: `rvav api-token -user usr_svc-awx -name awx-inventory`).

## In AWX

`rvav awx-bootstrap` (deploy/awx) sets up a credential type "rvav API" that
injects `RVAV_URL` and `RVAV_TOKEN`, and an inventory whose source is
`deploy/awx/inventory/rvav.yml` in this repository; AWX installs this
collection from `collections/requirements.yml` when it syncs the project.

## Tests

`make test-ansible` runs `ansible-inventory` with the plugin against a fake
rvav (uv, ansible-core, pytest).

The collection is not published to Ansible Galaxy or an Automation Hub yet.
