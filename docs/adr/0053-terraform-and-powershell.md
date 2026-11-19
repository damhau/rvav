# ADR-0053: A Terraform provider and a PowerShell module

- Status: accepted
- Date: 2026-11-19
- Builds on the client and sign-in of [ADR-0051](0051-command-line.md)
  and the inputs rvav fills in of [ADR-0055](0055-inputs-left-out.md).
  O1.2 (#168).

## Context
vRA has a Terraform provider (`vmware/vra`) and PowerCLI's vRA cmdlets.
Platform teams keep projects, catalog items and policies in Git and apply
them with Terraform; Windows administrators script requests and day-2
actions in PowerShell. rvav had its API, a generated client
(`pkg/rvavclient`) and rvavctl ([ADR-0051](0051-command-line.md)), but no
way to hold its configuration as code outside the catalog's own Git sync
([ADR-0029](0029-catalog-in-git.md)), and nothing for PowerShell.

## Decision

### The provider lives in this repository
`cmd/terraform-provider-rvav` and `internal/tfprovider`, in rvav's Go
module. The provider calls the API through `pkg/rvavclient`, so a change
of the contract regenerates the client it uses and breaks its build, not
its users. Its end-to-end tests run in `internal/e2e` against the same
in-process rvav as the API's, which another module couldn't import. It
is released with rvav and has rvav's version: the provider of a release
manages that release's API.

### terraform-plugin-framework
HashiCorp's current framework, on protocol 6 (Terraform 1.0 and later,
and OpenTofu). SDKv2 is in maintenance. The tests use
terraform-plugin-testing's `resource.UnitTest` with the provider served
in the test's process, against the e2e harness: they need a Terraform CLI,
the one on `PATH` or the latest release, which the tests download once
with hc-install.

### Installation without the public registry
The provider's address is `registry.terraform.io/damhau/rvav`, so that a
configuration says `source = "damhau/rvav"`, and nothing changes if it is
published there one day. Until then:
- **A filesystem mirror.** `VERSION=1.4.0 make terraform-dist` builds it
  for Linux, macOS and Windows on amd64 and arm64 into `dist/terraform/`,
  in Terraform's packed layout
  (`registry.terraform.io/damhau/rvav/terraform-provider-rvav_1.4.0_linux_amd64.zip`)
  with a `SHA256SUMS` file. The tree goes to a shared directory named in
  the CLI configuration's `filesystem_mirror`, or into a user's
  `~/.terraform.d/plugins`, which Terraform reads without configuration.
  `terraform init` then installs it as from the registry, and the lock
  file records its checksums.
- **`dev_overrides`** for development: `make build` puts the provider in
  `bin/`, and the CLI configuration points Terraform at it, without
  `terraform init`.

A network mirror served by rvav itself was considered, and left for when
someone needs it: a filesystem mirror covers air-gapped sites and CI.

### Authentication, as rvavctl's
The provider block takes `url`, `token` and `profile`, each optional:
- `token`, else `RVAV_TOKEN`: an API token, sent to `url`, else
  `RVAV_URL`. This is the way for CI: a service user's token, in the
  pipeline's secrets.
- Else an rvavctl profile (`profile`, else `default`): its rvav and its
  sign-in, refreshed and written back to rvavctl's file as rvavctl does.
  This is the way for a person at their desk, after `rvavctl login`. A
  profile's credentials go to its own rvav only.
- Else, with an address alone, no credentials: an rvav in development
  mode.

An API token acts with the roles given to its user in rvav itself, not
those of their groups ([ADR-0051](0051-command-line.md)): a service user
that manages policies needs the admin role assigned in rvav.

### Resources follow the API
One resource per thing the API writes, named as the API names it
(`rvav_project`, `rvav_catalog_item`, …), with the API's field names, and
data sources to find what Terraform doesn't manage. A resource:
- sends only the attributes its configuration gives, and takes rvav's
  defaults for the rest, which the schema states, so that a plan shows
  them;
- reads what rvav holds on every refresh: a change made in the web app
  shows as a diff, and one deleted there is removed from the state, to be
  made again;
- imports by its ID (`terraform import rvav_project.web prj_…`).

JSON-valued attributes (a catalog item's definition) are JSON strings
compared by value, so that a configuration writes them with
`jsonencode()` or `file()`, and rvav's own formatting is no diff.

Where the API doesn't delete, destroying does what the web app does:
- **A project is archived**, as rvav keeps projects for their
  deployments' history. Its deployments go on. Creating a project with an
  archived one's code brings that one back, with its ID, so that
  `terraform destroy` then `apply` gives the same project.
- **A catalog item that was requested is withdrawn**, with a warning:
  rvav keeps it for its requests' history, and nobody can request it
  again. An item never requested is deleted.
- **An environment's lease stays**, with a warning, as every environment
  has one. A project's override is removed.

What the API sets as a whole is one resource: a project's quotas
(`rvav_project_quotas`, its limits and its members'), and rvav's
(`rvav_global_quotas`). Terraform then owns all of them: those its
configuration leaves out are removed. Things the API keys by more than an
ID take a composite one: a project's lease override is `dev/prj_web`, a
member `prj_web/usr_afavre`, a project's secret `prj_web/cmdb-hook`.

The resources cover what rvav governs with: projects and catalog items;
approval, day-2, naming, lease and deployment limit policies, lease
rules and quotas; roles, their assignments, project members and group
mappings; secrets and event subscriptions. And deployments, below.
Infrastructure (vCenters, templates, placement) stays in the web app: it
is set up once, and its objects come from vCenter.

A catalog item's `definition` is the version Terraform writes: a change
makes a new version, published (deprecating the one before) unless
`publish` is false, which keeps it as the item's draft. A version
published in the web app shows as a diff, and the next apply publishes the
configuration's again.

### Deployments are requests
`rvav_deployment` makes requests, as a person does in the web app, and
waits for each until it ends, approvals included:
- **Create** requests the item with the configuration's `inputs` only:
  rvav fills in the others from the form
  ([ADR-0055](0055-inputs-left-out.md)), so a configuration says what it
  cares about. Without `version`, the newest the project may request.
- **A change of `inputs` or `version`** is an update request
  ([ADR-0042](0042-deployment-updates.md)): the deployment's other inputs
  as they are, those given over them, and on a new version its new
  fields' initial values, as the web app's update form starts. One rvav
  says changes nothing is no request. rvav refuses what it can't do in
  place, a smaller disk: Terraform says so, and doesn't replace the
  deployment.
- **A change of `name` or `description`** runs Edit deployment.
- **Destroy** runs Delete. A change of project, environment or item
  replaces the deployment.

A refresh reads the deployment's values of the inputs the configuration
gives, so that one changed in the web app is a diff; the others aren't
Terraform's. A secret input's value never comes back, so its change in
the web app isn't seen. A request that fails leaves the deployment
tainted, to be deleted and requested again by the next apply. Stopping
Terraform while it waits doesn't stop the request.

### The PowerShell module is a script module
`powershell/Rvav`: a manifest and one script module on PowerShell 7.2 and
later (Core only), on Windows, Linux and macOS. It calls the API with
`Invoke-RestMethod`, without the Go client or a compiled assembly, so
that it is read, changed and signed as any script, and installs by
copying a folder. It covers what a person does with rvav: the catalog,
requests and their waits, deployments, their day-2 actions and delete,
and approvals. `Invoke-RvavApi` calls the rest of the API as the
connected user. Configuration is Terraform's.

`Connect-Rvav` takes an API token (a `SecureString`, else `RVAV_TOKEN`)
and rvav's address (else `RVAV_URL`), as rvavctl does. rvavctl's
profiles and their browser sign-in aren't read: a person at their desk
makes a token in the web app. Approvals work as the token's user named
as approver, not through their groups
([ADR-0051](0051-command-line.md)).

The cmdlets follow PowerShell's rules: approved verbs (`Get`, `New`,
`Wait`, `Invoke`, `Remove`, `Approve`, `Deny`), objects with the API's
fields and a type name per kind for their table views, pipeline input by
property name (`Get-RvavDeployment … | Get-RvavDeploymentAction`), and
`-WhatIf` and `-Confirm` on what changes things. Deleting, and an action
that can't be undone, asks first unless `-Force`. A request sends only
the inputs given, as Terraform does. `-Wait` reads the request every two
seconds until it ends; one that doesn't succeed is returned and is an
error too, with rvav's reason. What rvav refuses is a terminating error
with its reasons, field by field.

`VERSION=1.4.0 make powershell-dist` zips the module with its version
set, in the `Rvav/1.4.0/` folder `Install-Module` would make, for a
`PSModulePath` directory or an internal PowerShell repository. The module
isn't on the PowerShell Gallery.

## Consequences
- A change of the API's write schemas needs the provider's schema changed
  with it, by hand: the provider's attributes aren't generated. Its tests
  run with the API's end-to-end tests, so a removed or renamed field fails
  them. `TestResourcesCoverWriteSchemas` fails when a write schema gains
  a field that its resource doesn't have, as a subscription's recovery
  target did ([ADR-0054](0054-subscriptions-that-change-the-request.md)):
  an apply would have sent the subscription without it, and cleared it.
- `make terraform-dist` needs `zip`, as `make cli-dist` does.
- Terraform makes ten changes at once. Changes of who administers rvav or
  a project wait for one lock; its waiters held a pooled connection each,
  so that ten of them left the holder none, and the API hung. They now
  wait in the process, without a connection (`Store.WithLock`).
- A secret's value never comes back from rvav: Terraform can't see it
  changed in the web app, and an import doesn't have it.
- Terraform downloads nothing for the provider, and the provider's
  archives aren't signed: a mirror's integrity is its directory's and the
  lock file's checksums.
- An API token can't approve as a member of an approver group: the
  provider manages configuration, not approvals, and the module's
  approvals are those of a user named as approver.
- The module's end-to-end test (`TestPowerShell`) runs a script with
  `pwsh` against the e2e harness, and is skipped where `pwsh` isn't on
  `PATH`. GitHub's Ubuntu runners have it.
- The module's cmdlets aren't generated either: a renamed field of the
  API breaks their test, not their build.
