# ADR-0031: On awxnext, the inventory source reads rvav itself

## Status
Accepted (2026-11-01). Revises the AWX side of
[ADR-0017](0017-awx-inventory.md) for awxnext; on AWX it is unchanged.

## Context
ADR-0017's inventory source is from a project. AWX checks out rvav's
repository, installs the `rvav.platform` collection and runs its inventory
plugin in an execution environment, with a custom credential type "rvav
API" that injects `RVAV_URL` and `RVAV_TOKEN`. That is a project, its
collection requirements and an execution environment that reaches rvav, for
one `GET /api/v1/ansible/inventory`.

awxnext, the AWX-compatible controller rvav also runs against, has an
inventory source type of its own, `rvav` (its roadmap task M4-11,
[damhau/awxnext#122](https://github.com/damhau/awxnext/issues/122)). Its
server reads that endpoint itself and imports the answer, with a managed
credential type `rvav`: `url`, `token`, `verify_ssl` and `ssl_ca_cert`.

## Decision
- rvav supports both routes. It tells them apart by what the API offers, not
  by the product: an AWX whose `OPTIONS /inventory_sources/` lists the
  source type `rvav` has rvav sources.
- `rvav awx-bootstrap` makes the rvav inventory's source an rvav source
  there, with a credential "rvav API" of the managed type and no project or
  path. Elsewhere it is ADR-0017's source from the project.
- The Inventory token ([ADR-0030](0030-settings-and-local-administrator.md))
  fills the credential that matches the source's type: the managed rvav type
  for an rvav source, "rvav API" for one from a project. It keeps the
  credential's other inputs, such as an rvav credential's TLS settings. A
  source without a credential of that type gets the organisation's "rvav
  API" credential of that type, made when missing.
- Credentials are looked up by name and type: AWX names them per type.
- Administration's AWX test says which kind of source the source is.
- The endpoint, its token and the inventory's format are unchanged. The
  plugin and awxnext read the same answer.

## Consequences
- On awxnext the inventory needs no project, collection or execution
  environment, and a sync is one request. awxnext's server must reach rvav,
  rather than an execution node. It refuses loopback, link-local and cloud
  metadata addresses unless its setting
  `INVENTORY_SOURCE_ALLOWED_LOCAL_ADDRESSES` lists them.
- The `rvav.platform` collection stays, for AWX.
- An rvav source takes no plugin options. The credential's `verify_ssl` and
  `ssl_ca_cert` replace the plugin's `validate_certs`, and the source's
  timeout replaces the plugin's.
