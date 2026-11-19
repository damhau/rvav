# Terraform: the rvav provider

The rvav provider manages rvav's configuration from Terraform: projects,
catalog items, policies, quotas, roles, secrets and event subscriptions.
It also requests deployments and keeps them as your configuration says,
through the same requests and approvals as the web app
([ADR-0053](../adr/0053-terraform-and-powershell.md)).

You need Terraform 1.0 or later, or OpenTofu.

## Install the provider

The provider isn't on the public Terraform registry. You install it from a
filesystem mirror, a directory of archives that Terraform reads as it reads
the registry.

1. Build the mirror from rvav's repository, at the version of the rvav you
   manage:

   ```sh
   VERSION=1.4.0 make terraform-dist
   ```

   This writes `dist/terraform/registry.terraform.io/damhau/rvav/`, with
   an archive for Linux, macOS and Windows on amd64 and arm64, and a
   `SHA256SUMS` file. The build needs Go and `zip`.

2. Copy the `registry.terraform.io` directory to one of these places:

   - For yourself: your plugins directory, which Terraform reads without
     configuration: `~/.terraform.d/plugins` on Linux and macOS,
     `%APPDATA%\terraform.d\plugins` on Windows.
   - For a team or CI: a shared directory, for example
     `/opt/terraform/mirror`. Name it in Terraform's CLI configuration
     (`~/.terraformrc`, or the file `TF_CLI_CONFIG_FILE` names):

     ```hcl
     provider_installation {
       filesystem_mirror {
         path    = "/opt/terraform/mirror"
         include = ["registry.terraform.io/damhau/rvav"]
       }
       direct {
         exclude = ["registry.terraform.io/damhau/rvav"]
       }
     }
     ```

3. In your configuration, require the provider:

   ```hcl
   terraform {
     required_providers {
       rvav = {
         source  = "damhau/rvav"
         version = "~> 1.4"
       }
     }
   }
   ```

   With OpenTofu, write the full address:
   `source = "registry.terraform.io/damhau/rvav"`.

4. Run `terraform init`. Terraform installs the provider from the mirror
   and records its checksum in `.terraform.lock.hcl`.

The lock file then has the checksum of your platform only. If your
configuration also runs on other platforms, add theirs:

```sh
terraform providers lock -fs-mirror=/opt/terraform/mirror \
  -platform=linux_amd64 -platform=darwin_arm64 -platform=windows_amd64
```

### Use a provider you built

To try a change to the provider without a mirror, build it with
`make build`, which writes `bin/terraform-provider-rvav`, and point
Terraform at that directory in its CLI configuration:

```hcl
provider_installation {
  dev_overrides {
    "damhau/rvav" = "/home/you/rvav/bin"
  }
  direct {}
}
```

Terraform then uses that build without `terraform init`, and warns that an
override is in effect.

## Configure the provider

The provider signs in to rvav the way rvavctl does
([the command line](cli.md)). Each setting is optional:

```hcl
provider "rvav" {
  url = "https://rvav.corp.example"
}
```

| Setting | Default | What it is |
|---|---|---|
| `url` | `RVAV_URL`, else the rvavctl profile's | rvav's address, the one you open in the browser. |
| `token` | `RVAV_TOKEN` | An API token, made in the web app (your menu, **API tokens**). |
| `profile` | `default` | The rvavctl profile to sign in with when there's no token. |

The provider signs in with the first of these that it finds:

- **A token.** In CI, set `RVAV_URL` and `RVAV_TOKEN` from the pipeline's
  secrets, and leave them out of the configuration. Use a service user's
  token.
- **An rvavctl profile.** At your desk, run `rvavctl login`. If `url` is
  left out or is the profile's rvav, the provider uses that sign-in and
  renews it as rvavctl does.
- **Neither:** the provider calls `url` without credentials, which only an
  rvav in development mode accepts.

The provider acts with the roles of the user it signs in as. An API token
has the roles given to its user in rvav itself, not those of their groups.
To manage projects, policies, quotas, roles and subscriptions, that user
needs the `admin` role assigned in rvav (**Administration › Roles**, or
`rvav_role_assignment`). To manage catalog items, `catalog-admin` is
enough. To request deployments, the user needs a project role that may
request.

## Manage projects and catalog items

```hcl
resource "rvav_project" "web" {
  code         = "web"
  name         = "Web platform"
  description  = "The web team's servers."
  environments = ["dev", "tst", "prd"]
  properties   = { owner = "web-team" }
}

resource "rvav_catalog_item" "app" {
  slug        = "app-server"
  name        = "App server"
  summary     = "A RHEL 9 server for the web team."
  category    = "compute"
  type        = "vm_blueprint"
  tags        = ["linux", "web"]
  definition  = jsonencode(yamldecode(file("app-server.yaml")))
  changelog   = "Adds the monitoring agent."
  project_ids = [rvav_project.web.id]
}
```

- A project's `code` and an item's `type` can't change: a change replaces
  them.
- Destroying a project archives it: it can't request anything new, and its
  deployments go on. Creating a project with an archived one's code brings
  that project back, with its ID.
- An item's `definition` holds its form and blueprint as JSON, in the
  shape of the item's YAML export (`form_schema`, `ui_schema`,
  `initial_values`, `blueprint`). A change to it makes a new version and
  publishes it, which deprecates the version before. To keep the new
  version as the item's draft instead, set `publish = false`.
- Without `project_ids` or `all_projects`, Terraform leaves the item's
  sharing as it is.
- Destroying an item that someone requested withdraws it, with a warning:
  rvav keeps it for its requests' history. An item nobody requested is
  deleted.

To refer to a project or an item that Terraform doesn't manage, use the
data sources:

```hcl
data "rvav_project" "core" {
  code = "core"
}

data "rvav_catalog_item" "rhel9" {
  slug       = "rhel9-server"
  project_id = data.rvav_project.core.id
}

data "rvav_user" "marc" {
  email = "marc.keller@corp.example"
}
```

## Manage policies and quotas

Each kind of policy is a resource, with the fields of its page in the web
app:

```hcl
resource "rvav_approval_policy" "prd" {
  name       = "Production web"
  project_id = rvav_project.web.id
  condition  = "environment == \"prd\""
  levels = [
    { name = "Leads", approvers = [data.rvav_user.marc.id] },
    { name = "Security", group = "rvav-security", timeout = "48h", on_timeout = "reject" },
  ]
}

resource "rvav_day2_policy" "delete" {
  name    = "Deleting needs the manager"
  actions = ["delete"]
  effect  = "approval"
  levels  = [{ name = "Manager", manager = true }]
}

resource "rvav_naming_policy" "web" {
  name     = "Web names"
  template = "{project}-{environment}-{role}{seq:03}"
  offers   = [{ project_id = rvav_project.web.id, environment = "dev" }]
}

resource "rvav_lease_policy" "web_dev" {
  environment    = "dev"
  project_id     = rvav_project.web.id
  default_days   = 7
  max_days       = 30
  max_extensions = 2
  grace_days     = 3
  expiry_action  = "delete"
}

resource "rvav_lease_rule" "blueprints" {
  name           = "Blueprints"
  condition      = "item.type == \"vm_blueprint\""
  max_extensions = 1
  grace_days     = 1
  expiry_action  = "notify_only"
}

resource "rvav_deployment_limit_policy" "web" {
  name                 = "Web limits"
  project_id           = rvav_project.web.id
  deployments_per_user = 5
}
```

- `rvav_lease_policy` without `project_id` sets an environment's own lease
  terms. Destroying it leaves them as they are, with a warning, because
  every environment has lease terms. With `project_id`, it is the
  project's override, and destroying it removes the override.
- Conditions are CEL expressions, as in the web app.

Quotas are one resource per project, and one for all of rvav. Terraform
then owns all of them: a limit that the configuration doesn't list is
removed.

```hcl
resource "rvav_project_quotas" "web" {
  project_id = rvav_project.web.id
  limits = [
    { resource = "vm_count", limit = 20, enforcement = "soft" },
    { environment = "prd", resource = "vcpu", limit = 64 },
  ]
  member_limits = [{ resource = "vcpu", limit = 16 }]
}

resource "rvav_global_quotas" "all" {
  limits = [{ resource = "vm_count", per_user = true, limit = 10 }]
}
```

## Manage roles and access

```hcl
resource "rvav_role" "operator" {
  key         = "operator"
  name        = "Operator"
  permissions = ["project.see", "day2.power:power_on", "day2.power:power_off"]
}

resource "rvav_role_assignment" "catalog" {
  user_id = data.rvav_user.marc.id
  role    = "catalog-admin"
}

resource "rvav_project_member" "marc" {
  project_id = rvav_project.web.id
  user_id    = data.rvav_user.marc.id
  roles      = ["project-admin"]
}

resource "rvav_group_mapping" "operators" {
  oidc_group = "web-operators"
  project_id = rvav_project.web.id
  role       = rvav_role.operator.key
}
```

- `rvav_role_assignment` gives a role across rvav, and works for the
  user's API tokens too.
- `rvav_project_member` sets a user's own roles in a project, besides
  those their groups give.
- `rvav_group_mapping` gives a role to an identity provider group's
  members, in a project or, without `project_id`, across rvav.

## Manage secrets and subscriptions

```hcl
variable "cmdb_token" {
  type      = string
  sensitive = true
}

resource "rvav_secret" "cmdb" {
  name  = "cmdb-hook"
  kind  = "token"
  value = var.cmdb_token
}

resource "rvav_subscription" "cmdb" {
  name      = "CMDB"
  topic     = "compute.provision.post"
  condition = "environment == \"prd\""
  target = {
    webhook = { url = "https://cmdb.corp.example/rvav", secret = rvav_secret.cmdb.name }
  }
}
```

- rvav never gives a secret's value back. Terraform keeps the value it
  sent in its state, so protect the state as you protect the secret. If
  someone changes the value in the web app, Terraform doesn't see it.
- A subscription's `target` is exactly one of `webhook`, `awx`
  (`template_id` or `job_template`) and `workflow` (`key`).
- A webhook's host must be one that rvav allows webhooks to call
  (**Administration › Integrations › Allowed hosts**, or
  `RVAV_WEBHOOK_HOSTS`).

## Request deployments

`rvav_deployment` requests a catalog item and waits until the request
ends, approvals included:

```hcl
resource "rvav_deployment" "app" {
  project_id    = "prj_web"
  environment   = "dev"
  catalog_item  = "rhel9-server"
  justification = "CHG-1234"
  inputs = jsonencode({
    size      = "M"
    cluster   = "GVA-DEV-CL01"
    datastore = "vsan-gva-dev-cl01"
    network   = "dvpg-app-dev-vl310"
  })
}

output "app_address" {
  value = rvav_deployment.app.fqdn
}
```

- `inputs` are the item's form fields, by name. To see them, run
  `rvavctl catalog show rhel9-server`. Terraform sends only the inputs you
  give, and rvav fills in the others from the form, as for a request made
  in the web app. To leave an input that has a default empty, set it to
  `null`.
- Without `version`, Terraform requests the newest version the project may
  request, and records its number.
- While a request waits for approval, `terraform apply` waits too.

What a change does:

| You change | Terraform |
|---|---|
| `inputs` or `version` | Makes an update request. The deployment's other inputs stay as they are. On a new version, the version's new fields take their initial values. |
| `name` or `description` | Runs **Edit deployment**. |
| `project_id`, `environment` or `catalog_item` | Replaces the deployment: deletes it and requests a new one. |
| Removes the resource, or `terraform destroy` | Runs **Delete** and waits for it. |

rvav refuses an update that it can't make in place, such as a smaller
disk: the apply fails with rvav's reason, and the deployment stays as it
was. Change the configuration back, or replace the deployment with
`terraform apply -replace=rvav_deployment.app`.

If a request fails, the apply fails and Terraform marks the deployment
tainted: the next apply deletes it and requests it again. To keep it
instead, run `terraform untaint rvav_deployment.app`. The same happens if
you stop Terraform while it waits: the request goes on in rvav.

## Import what rvav already has

To bring an object made in the web app under Terraform, write its resource
in your configuration, then import it by its ID:

```sh
terraform import rvav_project.web prj_01J…
```

Most resources import by the ID that rvav gives the object. Find it in the
API's answers, for example with the PowerShell module's
`Invoke-RvavApi admin/approval-policies`. The others import by:

| Resource | Import ID | Example |
|---|---|---|
| `rvav_lease_policy` | The environment's key, then the project's ID for an override | `dev`, `dev/prj_web` |
| `rvav_project_quotas` | The project's ID | `prj_web` |
| `rvav_global_quotas` | `global` | `global` |
| `rvav_role` | Its key | `operator` |
| `rvav_project_member` | The project's ID and the user's | `prj_web/usr_afavre` |
| `rvav_secret` | Its name, after the project's ID for a project's own | `cmdb-hook`, `prj_web/cmdb-hook` |

After an import, run `terraform plan`. If it shows changes you didn't
make:

- A list of quota limits in another order: write the limits in the order
  that `terraform state show` gives.
- A secret's value: rvav doesn't give it back, so Terraform sets the one
  in your configuration at the next apply.
- A deployment's `inputs` and `justification`: the import can't tell
  which inputs your configuration gives. The next apply sends your inputs
  as an update, and rvav makes no request when the deployment already has
  those values.

## Changes made outside Terraform

Every plan reads what rvav holds:

- A change made in the web app shows as a difference, and the next apply
  sets the configuration's value again.
- An object deleted in the web app is gone from the state, and the next
  apply makes it again. A deployment deleted in the web app is requested
  again.
- A catalog item version published in the web app shows as a difference
  in `definition`, and the next apply publishes the configuration's again.
- For a deployment, Terraform compares only the inputs your configuration
  gives, and not those of secret fields.
