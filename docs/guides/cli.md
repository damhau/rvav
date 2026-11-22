# The command line: rvavctl

`rvavctl` does from a terminal what you do in the web app: request from
the catalog, follow your requests, run day-2 actions on your deployments,
and decide approvals. It acts as you, with your roles
([ADR-0051](../adr/0051-command-line.md)).

To do the same from PowerShell, see [the Rvav module](powershell.md). To
keep rvav's configuration and deployments in Terraform, see
[the rvav provider](terraform.md).

## Install rvavctl

rvav's releases don't include rvavctl's archives yet. Build them from
rvav's repository, at the version of your rvav. You need Go, at the
version that `go.mod` names, and `zip` for the Windows archives.

1. Build the archives:

   ```sh
   VERSION=1.4.0 make cli-dist
   ```

   This writes one archive per system into `dist/rvavctl`,
   `rvavctl_1.4.0_<os>_<arch>.tar.gz` or `.zip` for Windows, and their
   `SHA256SUMS`.

2. Check the archive for your system against `SHA256SUMS`:

   ```sh
   cd dist/rvavctl && sha256sum --check --ignore-missing SHA256SUMS
   ```

3. Extract the archive, and put `rvavctl` (`rvavctl.exe` on Windows) in a
   directory on your `PATH`.
4. Optional: turn on completion for your shell, for example for bash:

   ```sh
   rvavctl completion bash > ~/.local/share/bash-completion/completions/rvavctl
   ```

   `rvavctl completion --help` lists the other shells (zsh, fish,
   PowerShell).

To build `rvavctl` for your own machine only, run `make build`, which
writes `bin/rvavctl`, or install it with Go:

```sh
go install github.com/damhau/rvav/cmd/rvavctl@latest
```

Replace `latest` with the commit of your rvav to get the same version.
`rvavctl --version` then says `dev`.

## Sign in

To sign in, give rvav's address, the one you open in the browser:

```sh
rvavctl login --server https://rvav.corp.example
```

Your browser opens your organisation's sign-in page. When you have signed
in, the page says you can go back to the terminal. If the browser doesn't
open, open the address that `rvavctl` prints.

On a machine without a browser, such as an SSH session, sign in on another
device:

```sh
rvavctl login --server https://rvav.corp.example --device
```

`rvavctl` prints an address and a code. Open the address on any device,
enter the code if asked, and sign in.

Check who rvav takes you for, and your roles in each project:

```sh
rvavctl whoami
```

`rvavctl` keeps the sign-in in your configuration directory, in a file
only you can read, in a directory only you can open:

| System | File |
|---|---|
| Linux | `~/.config/rvav/config.json` |
| macOS | `~/Library/Application Support/rvav/config.json` |
| Windows | `%AppData%\rvav\config.json` |

It renews the sign-in as needed. Several `rvavctl` commands, and the
[Terraform provider](terraform.md), can run at once on one sign-in: each
takes the sign-in that another one renewed, so a provider that lets each
refresh token serve once, such as Keycloak or Okta, doesn't end it.

When your organisation ends the sign-in, commands fail with
`the sign-in has expired`: run `rvavctl login` again. When the identity
provider can't be reached, they fail with that error instead, and the
sign-in stays.

To sign out, run `rvavctl logout`. If the identity provider offers
revocation (RFC 7009), as Keycloak and Okta do, `rvavctl` revokes the
sign-in there first. If it doesn't, as with Entra ID, the sign-in stays
valid at the provider until it expires, and `rvavctl logout` says so.

`rvavctl` sends your token to an `http://` address only on your own
machine, such as `http://localhost:8080`. To call an rvav elsewhere over
`http://`, add `--insecure-http` to `rvavctl login`, which the profile
keeps, or to each command that uses `RVAV_TOKEN`: your token then crosses
the network unencrypted.

rvav's audit log records each sign-in, failed or not, with the address it
came from, and each `rvavctl logout`. rvavctl sends the provider's error
when a sign-in fails, never a code or a token.

### Several rvavs

To sign in to more than one rvav, give each a profile name, and the same
name to the commands for it:

```sh
rvavctl login --server https://rvav-test.corp.example --profile test
rvavctl catalog list --profile test
```

Without `--profile`, commands use the profile `default`.

### API tokens and scripts

To sign in with an API token instead (your menu in the web app, **API
tokens**), pass it on standard input, so that it stays out of your shell's
history:

```sh
rvavctl login --server https://rvav.corp.example --with-token < token.txt
```

In scripts and CI jobs, set `RVAV_URL` and `RVAV_TOKEN` instead: no
profile file is needed, and they win over the profile.

```sh
export RVAV_URL=https://rvav.corp.example RVAV_TOKEN=rvav_pat_…
rvavctl requests list --mine
```

An API token acts with the roles given to you in rvav itself, not those
from your groups. To approve as a member of an approver group, sign in
with `rvavctl login`.

## Request from the catalog

1. List the items you can request:

   ```sh
   rvavctl catalog list
   ```

   If you request in several projects, add `--project` with the project's
   code, name or ID. `--search` and `--category` narrow the list.

2. See the inputs an item's request form takes, with their types,
   defaults and choices:

   ```sh
   rvavctl catalog show rhel9-server
   ```

3. For an input whose choices rvav lists, such as clusters or networks,
   ask for them. Give the environment and the inputs they depend on:

   ```sh
   rvavctl catalog choices rhel9-server cluster --env dev
   rvavctl catalog choices rhel9-server datastore --env dev --input cluster=GVA-DEV-CL01
   ```

4. Check the request without making it:

   ```sh
   rvavctl catalog request rhel9-server --env dev \
     --input cluster=GVA-DEV-CL01 --input datastore=vsan-gva-dev-cl01 \
     --input network=dvpg-app-dev-vl310 --dry-run
   ```

   `--dry-run` shows the VM names, the placement and whether the request
   needs approval. If rvav would refuse the request, it lists why and
   exits with `1`.

5. Request it. To wait until it's done, add `--wait`, and `--timeout 1h`
   to give up waiting after an hour:

   ```sh
   rvavctl catalog request rhel9-server --env dev \
     --input cluster=GVA-DEV-CL01 --input datastore=vsan-gva-dev-cl01 \
     --input network=dvpg-app-dev-vl310 --wait
   ```

### Give inputs

- `--input name=value` gives one input. Repeat it for each.
- An input takes the type its form gives it: `--input cpu_count=4` is a
  number, `--input restart=true` is true or false.
- A list is `a,b,c` or JSON (`--input 'ports=[80,443]'`). An object is
  JSON.
- `--inputs FILE` reads inputs from a JSON or YAML file, `-` for standard
  input. `--input` flags win over the file.
- Inputs you don't give take the values the form starts with in the web
  app, its initial values and defaults: rvav fills them in.
- To leave an input that has a default empty, set it to `null` in the
  `--inputs` file.

For example, with `server.yaml`:

```yaml
role: app
size: M
cluster: GVA-DEV-CL01
datastore: vsan-gva-dev-cl01
network: dvpg-app-dev-vl310
post_config: [linux-baseline, monitoring-agent]
```

```sh
rvavctl catalog request rhel9-server --env dev --inputs server.yaml --input size=L
```

### Other request flags

| Flag | What it does |
|---|---|
| `--justification TEXT` | Why, for the approvers. |
| `--count N` | Makes N identical deployments. |
| `--name NAME`, `--description TEXT` | The deployment's name and what it's for. |
| `--lease-days N` | A lease other than the default, within the policy. |
| `--at 2026-12-01T06:00:00Z` | Starts the request later. |
| `--on-behalf-of USER_ID` | Requests for someone else, if you're a project admin. |
| `--version NUMBER` | Requests another version the item offers. |
| `--accept-warnings` | Goes on despite a soft policy's warnings. |

## Follow requests

| Command | What it does |
|---|---|
| `rvavctl requests list` | The requests you can see, newest first: the first 50, or `--limit 200`, or `--all`. `--mine`, `--status running,failed`, `--project`. |
| `rvavctl requests show REQ-10001` | A request and its steps. |
| `rvavctl requests wait REQ-10001` | Waits until it ends. `--timeout 30m` gives up after 30 minutes. |
| `rvavctl requests cancel REQ-10001` | Cancels a request that is waiting or running. With `--wait`, succeeds when the request ends cancelled. |
| `rvavctl requests retry REQ-10001` | Runs a failed request again from the step that failed. |
| `rvavctl requests signal REQ-10001 cab-approved` | Sends the signal a composite step waits for. `--data '{"by": "me"}'` gives what it brings, `--data -` reads it from standard input. |

You can name a request by its number or its ID. `wait`, and `--wait` on
the commands that make requests, succeed when the request succeeds. They
fail when it fails, is rejected or is cancelled; see
[exit codes](#output-and-scripting). `--timeout` works with each `--wait`.

A wait goes on through errors that don't last, such as a proxy's
`502 Bad Gateway` during a rolling restart or a dropped connection: it
says so on standard error and tries again, waiting longer each time, up
to a minute. It stops on any other error, such as a request that rvav
doesn't know.

## Act on your deployments

1. List your deployments, and see one with its resources:

   ```sh
   rvavctl deployments list --mine
   rvavctl deployments show dep_01J…
   ```

2. List its day-2 actions, and whether you may run each now. To see the
   inputs an action takes, add its key:

   ```sh
   rvavctl deployments actions dep_01J…
   rvavctl deployments actions dep_01J… resize --resource web-dev-app001
   ```

3. Run one by its key. If the deployment has several VMs, name the one
   with `--resource`. If the action deletes something or can't be undone,
   add `--yes`:

   ```sh
   rvavctl deployments run dep_01J… power_off --resource web-dev-app001 --yes --wait
   rvavctl deployments run dep_01J… resize --input cpu_count=4 --input restart=true
   rvavctl deployments run dep_01J… snapshot_create --input name=before-upgrade
   ```

An action's form starts from the deployment's current values, as in the
web app: inputs you don't give keep them. If an administrator designed
the action's form, rvav also works out the defaults its rules give on
the VM or the deployment.

## Approve

| Command | What it does |
|---|---|
| `rvavctl approvals list` | The approvals waiting for you, oldest first: the first 50, or `--limit 200`, or `--all`. `--status approved,rejected` for those you decided. `--view all` for everyone's approvals, if you're an administrator or auditor. |
| `rvavctl approvals show ID` | What you need to decide: the request, why it needs approval, the facts, and the inputs you may change. |
| `rvavctl approvals approve ID -m "OK"` | Approves at your level. `--input memory_gb=32` changes an input that approvers may change. |
| `rvavctl approvals reject ID -m "Use the M size"` | Rejects. The comment is required: the requester gets it. |

## See IP ranges and who holds their addresses

On networks without EfficientIP, rvav gives VMs addresses from its own IP
ranges ([ADR-0063](../adr/0063-ip-ranges.md)). To see the ranges and the
VM that holds each address, you need the right to see the infrastructure
for all of rvav. Administrators set ranges up in the web app or with
[Terraform](terraform.md#manage-ip-ranges).

| Command | What it does |
|---|---|
| `rvavctl ip-ranges list` | The ranges, with their network and free addresses. `--network dvpg-lab-vl510` for one network's. |
| `rvavctl ip-ranges show Lab` | A range, by name or ID, and each address it gave: the VM, its adapter and its deployment. |
| `rvavctl ip-ranges release Lab 10.51.0.12` | Gives back an address of a VM rvav no longer has, such as an unregistered VM's, so that the range can give it again. Needs the right to manage the infrastructure. |

rvav refuses to give back an address that a deployment's VM holds:
delete the VM, or move its adapter to another network, and rvav gives the
address back itself. If the VM still runs on the address in vCenter,
exclude the address from the range instead.

## Output and scripting

- Tables go to standard output; progress and errors go to standard error.
  Text that others wrote, such as a justification, shows without its
  control characters, so that it can't move the cursor or hide a link.
- `-o json` prints what the API returns. A list is a JSON array of its
  items, with every page read up to `--limit`, or all of them with
  `--all`. `catalog show` prints `{"item": …, "form": …}`,
  `requests show` prints `{"request": …, "steps": […]}`, and
  `ip-ranges show` prints `{"range": …, "addresses": […]}`.
- A list that shows only the first of its items says so on standard
  error. `requests list` and `approvals list` show the first 50, and
  `catalog list` and `deployments list` the first 200.

A command's exit code says how it went:

| Code | What happened |
|---|---|
| `0` | The command succeeded, and the request it waited for ended as asked. |
| `1` | The request didn't succeed: it failed, was rejected or was cancelled. A dry run that rvav would refuse, and other errors, also exit with `1`. |
| `2` | The command, a flag or an input isn't right, such as a missing `--yes` or `--comment`. rvavctl changed nothing. |
| `3` | `--timeout` ended the wait. rvav goes on with the request. |
| `4` | Calling rvav, or its identity provider, failed: rvav refused the call, you aren't signed in, or no answer came. |
| `130` | You pressed Ctrl-C. |

For example, to wait for each of your running requests in turn:

```sh
rvavctl requests list --mine --status running --all -o json \
  | jq -r '.[].number' \
  | xargs -r -n 1 rvavctl requests wait
```

`xargs` goes on to the next request when one fails, and exits with `123`
at the end if any did.

To see every command and flag, run `rvavctl --help`, or `--help` on any
command.
