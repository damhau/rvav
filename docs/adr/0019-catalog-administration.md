# ADR-0019: Catalog administration and item versions

- Status: accepted
- Date: 2026-10-27

## Context
S8 ([implementation plan](../implementation-plan.md#s8-catalog-administration))
lets catalog admins build and publish items without touching the database:
items, versions, entitlements, categories, icons, YAML import and export, a
form designer, and the `rest` and `cel` value sources. Until now items came
from the seed and, since S6, from AWX templates
([ADR-0016](0016-awx-catalog.md)). Every request and deployment records the
version it was made with.

## Decision

### Who
Administrators and users with the global `catalog-admin` role build,
publish and open items to projects. Project admins don't author items in
this slice (decided with the product owner); project-scoped authoring can
come later. Every change is in the audit log (`catalog.*`).

### Versions
An item has versions in three states:
- **draft:** the one version that can change. An item has at most one
  draft. It starts as a copy of a version (the published one by default)
  or from a definition, and is saved as a whole.
- **published:** what users request. At most one per item, and it never
  changes; to change it, a draft is made from it.
- **deprecated:** an earlier published version. It never changes either.

Publishing a draft makes it the published version and deprecates the one
before. Publishing a deprecated version again is how an admin **rolls
back**. Deprecating the published version withdraws the item: no version
is published, and it leaves the catalog.

Deployments and requests keep the version they were made with: their day-2
actions, retries and approvals use it. A form opened on a version that is
no longer published is refused on submit as out of date, as it already is
when a new version is published.

Versions are numbered `major.minor`: a new draft takes the next minor
(`3.2` gives `3.3`); a number that doesn't parse gets `.1` added; an admin
may set another. Items made from AWX keep their `AWX #n` numbers and get a
draft numbered the same way.

Only catalog admins see drafts and deprecated versions:
`GET /catalog/items/{id}/form?version_id=` serves anyone else the
published version only.

### What a definition is
A version's definition is its JSON Schema (`form_schema`), UI schema,
initial values and blueprint, as before. Saving a draft checks it as a
whole:
- the JSON Schema compiles;
- every UI field is an input of the schema, and sections name only
  existing fields, each once;
- CEL rules and value sources compile, and the value sources are known;
- the blueprint parses for the item's type.

A draft may be incomplete: the template it clones or launches can still be
empty. Publishing checks again, and also that the draft is complete.
Problems come back as 422 with pointers under `/definition`, so the
designer can show each one on its field.

### Items from AWX
Items made by the AWX sync are versions like any other: a catalog admin
can draft from them to extend the form, for example with a value source on
a survey answer. The sync goes on flagging a changed survey against the
survey the item was made from.

### Entitlements
An item is open to every project (those made later too: the new column
`catalog_items.all_projects`), or to chosen projects (`catalog_entitlements`
as before). Items made from AWX are open to every project.

### Categories and icons
Categories can be added, renamed, ordered, and removed once empty. Icons
come from rvav's own set (`GET /admin/catalog/icons`), and the server
refuses any other. There is no upload, decided with the product owner: an
uploaded SVG can carry script.

### YAML
An item exports to YAML: its metadata and one or all versions, each with
its state, number, changelog and definition. Import makes a new item, or a
new draft of an existing item from the YAML's last version. A new item's
versions are never published by an import; an admin publishes after
checking. Problems name the YAML line. Exporting and importing again
changes nothing.

### Value sources `rest` and `cel`
- **`rest`** calls a URL template (`${inputs.x}`), with GET or POST, and
  maps the answer with JSONPath (`items`, `value`, `label`, `meta`).
  - Only hosts listed in `RVAV_REST_SOURCE_HOSTS` (`*.corp.example`
    style) are called, so a form can't make rvav reach arbitrary or
    internal addresses.
  - It follows no redirects, waits 5 seconds at most and reads 1 MB at
    most. Answers are cached for `cache_seconds`, keyed on the resolved
    request.
  - Authentication comes from a stored **secret**, named in the source.
- **`cel`** gives options or a value from an expression over the inputs,
  the project, the user and the environment.

### Secrets
Secrets are stored encrypted in PostgreSQL. This is the accepted default
(AES-256-GCM, Vault later), brought forward from S9 because `rest`
sources need it.
- **Key:** `RVAV_SECRETS_KEY` (32 bytes, base64). Each value has its own
  nonce, and the secret's name is bound as additional data.
- **Kinds:** `token` (a bearer token), `basic` (a user name and password)
  and `header` (a named header).
- **Access:** catalog admins and administrators manage them. Values are
  never returned or logged, and a secret a version uses can't be deleted.

S9 builds the integration settings on the same store.

### Rule results on the server
The preview answers each input's rule results (`visible`, `required`,
`read_only`, the value a `value` rule computed) in `fields`, and the web
app shows those instead of evaluating CEL itself. The designer's preview
does the same for any version, as a chosen user in a chosen project
(`as_user_id`, catalog admins only), and also returns rules that fail to
evaluate.

## Consequences
- Catalog admins change the catalog without a deployment or SQL. A
  published version is immutable, so what a deployment was made with stays
  known.
- One draft per item keeps editing simple; two admins editing the same item
  save over each other's changes on that draft. The designer warns when the
  draft changed since it was opened.
- The seed still creates its items, and updates one only while it has
  nothing but the seed's own version: once an admin has made a version of
  a seeded item, the seed leaves that item alone.
