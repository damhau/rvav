# PowerShell: the Rvav module

The Rvav module does from PowerShell what you do in the web app: request
from the catalog, follow your requests, run day-2 actions on your
deployments, and decide approvals. It acts as the user whose API token it
connects with ([ADR-0053](../adr/0053-terraform-and-powershell.md)).

You need PowerShell 7.2 or later, on Windows, Linux or macOS. Windows
PowerShell 5.1 isn't supported.

## Install the module

1. Build the module's archive from rvav's repository, at the version of
   your rvav:

   ```sh
   VERSION=1.4.0 make powershell-dist
   ```

   This writes `dist/powershell/Rvav-1.4.0.zip` and its `SHA256SUMS`.

2. Extract the archive into a directory of your module path. For
   yourself, that's `$HOME\Documents\PowerShell\Modules` on Windows and
   `~/.local/share/powershell/Modules` on Linux and macOS:

   ```powershell
   $modules = ($env:PSModulePath -split [IO.Path]::PathSeparator)[0]
   Expand-Archive Rvav-1.4.0.zip -DestinationPath $modules
   ```

   The archive holds `Rvav/1.4.0/`, so several versions can sit side by
   side.

3. If your execution policy is `RemoteSigned` and the archive came from
   another computer, unblock its files:

   ```powershell
   Get-ChildItem (Join-Path $modules Rvav) -Recurse | Unblock-File
   ```

4. Check that PowerShell finds it:

   ```powershell
   Get-Command -Module Rvav
   ```

To use the module from a checkout of the repository instead, import its
manifest: `Import-Module ./powershell/Rvav/Rvav.psd1`.

## Connect

1. Make an API token in the web app: your menu, **API tokens**.
2. Connect with it. `Read-Host -AsSecureString` keeps it off the screen
   and out of your history:

   ```powershell
   Connect-Rvav -Server https://rvav.corp.example -Token (Read-Host -AsSecureString -Prompt 'API token')
   ```

In scripts and CI jobs, set `RVAV_URL` and `RVAV_TOKEN` instead, and run
`Connect-Rvav` without parameters. AWX jobs that rvav launches already
have both.

```powershell
Connect-Rvav
```

`Connect-Rvav` returns who rvav takes you for. The connection lasts for
your session; `Disconnect-Rvav` forgets it.

An API token acts with the roles given to its user in rvav itself, not
those of their groups. To approve as a member of an approver group, use
the web app or `rvavctl login`.

## Request from the catalog

1. List the items a project may request:

   ```powershell
   Get-RvavCatalogItem -ProjectId prj_web
   ```

   `-Search` and `-Category` narrow the list.

2. Request one, with the inputs you want to give. To wait until the
   request ends, add `-Wait`:

   ```powershell
   New-RvavRequest rhel9-server -ProjectId prj_web -Environment dev -Wait -Inputs @{
       size      = 'M'
       cluster   = 'GVA-DEV-CL01'
       datastore = 'vsan-gva-dev-cl01'
       network   = 'dvpg-app-dev-vl310'
   }
   ```

- `-Inputs` are the form's fields, by name. To see them, with their types
  and choices, run `rvavctl catalog show rhel9-server`.
- Inputs you don't give take the values the form starts with in the web
  app, its initial values and defaults: rvav fills them in. To leave an
  input that has a default empty, give it `$null`.
- To check what you request first, add `-WhatIf`.

You can also pipe an item into `New-RvavRequest`, which takes its slug and
project:

```powershell
Get-RvavCatalogItem rhel9-server -ProjectId prj_web |
    New-RvavRequest -Environment prd -Justification 'CHG-1234' -Inputs @{ role = 'front' }
```

| Parameter | What it does |
|---|---|
| `-Justification` | Why, for the approvers. |
| `-Name`, `-Description` | The deployment's name and what it's for. |
| `-Version` | Requests another version the item offers, such as `1.2`. |
| `-AcceptWarnings` | Goes on despite a soft policy's warnings. |
| `-Timeout` | With `-Wait`, gives up after this long, such as `(New-TimeSpan -Minutes 30)`. |

## Follow requests

| Command | What it does |
|---|---|
| `Get-RvavRequest -Mine` | Your requests, newest first. `-Status running, failed` and `-ProjectId` narrow the list. |
| `Get-RvavRequest REQ-10042` | A request, by its number or ID. |
| `Wait-RvavRequest REQ-10042` | Waits until it ends, and returns it. `-Timeout` gives up after a while. |

`Wait-RvavRequest`, and `-Wait` on the commands that make requests, return
the request when it ends. If it doesn't succeed, they also write an error
with rvav's reason. To stop a script there, add `-ErrorAction Stop`, or
set `$ErrorActionPreference = 'Stop'`. Add `-Verbose` to see each step as
the request runs.

## Act on your deployments

1. List your deployments, or get one:

   ```powershell
   Get-RvavDeployment -Mine -ProjectId prj_web
   Get-RvavDeployment dep_01J…
   ```

2. List its day-2 actions, and whether you may run each now:

   ```powershell
   Get-RvavDeployment dep_01J… | Get-RvavDeploymentAction
   ```

   The `form` property of an action holds the inputs it takes.

3. Run one by its key. If the deployment has several VMs, name the one
   with `-Resource`:

   ```powershell
   Invoke-RvavDeploymentAction dep_01J… power_off -Resource web-dev-app001 -Wait
   Invoke-RvavDeploymentAction dep_01J… resize -Inputs @{ cpu_count = 4; restart = $true }
   ```

An action's form starts from the deployment's current values, as in the
web app: inputs you don't give keep them.

If an action deletes something or can't be undone, PowerShell asks you to
confirm. To run it without asking, as a script without a console must, add
`-Force`.

To delete a deployment, its VMs, IP addresses and DNS records:

```powershell
Remove-RvavDeployment dep_01J… -Wait
```

It asks you to confirm, unless you add `-Force` or `-Confirm:$false`.
`-WhatIf` shows what it would delete.

## Approve

| Command | What it does |
|---|---|
| `Get-RvavApproval` | The approvals waiting for you. `-Status approved, rejected` for those you decided. `-All` for every approval, if you're an administrator or auditor. |
| `Approve-RvavApproval apr_01J… -Comment 'OK'` | Approves at your level. `-Inputs @{ memory_gb = 32 }` changes an input that approvers may change. |
| `Deny-RvavApproval apr_01J… -Comment 'Use the M size'` | Rejects. The comment is required: the requester gets it. |

For example, to approve a request by its number:

```powershell
Get-RvavApproval | Where-Object { $_.request.number -eq 'REQ-10042' } | Approve-RvavApproval -Comment 'OK for the release'
```

## Call the rest of the API

`Invoke-RvavApi` calls any operation of rvav's API as the connected user,
and returns its answer. Give the path after `/api/v1/`:

```powershell
Invoke-RvavApi admin/approval-policies
Invoke-RvavApi -Method POST -Path requests/REQ-10042/cancel
```

- `-Query @{ status = 'running', 'failed' }` adds query parameters; a list
  repeats one.
- `-Body` takes an object or a hashtable, sent as JSON.
- `-Idempotent` sends an `Idempotency-Key`, which the operations that make
  requests need.

The API's reference is at `/api/docs` on your rvav.

## Output and errors

- The cmdlets return the API's objects, with the API's field names. Tables
  show a few of them: to see all of them, pipe to `Format-List *`, or to
  `ConvertTo-Json -Depth 10`.
- When rvav refuses a call, the cmdlet stops with a terminating error that
  gives rvav's reasons, one line per field. A `try`/`catch` block catches
  it, whatever `-ErrorAction` says.
