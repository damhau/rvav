# The command line: rvavctl

`rvavctl` does from a terminal what you do in the web app: request from
the catalog, follow your requests, run day-2 actions on your deployments,
and decide approvals. It acts as you, with your roles
([ADR-0051](../adr/0051-command-line.md)).

To do the same from PowerShell, see [the Rvav module](powershell.md). To
keep rvav's configuration and deployments in Terraform, see
[the rvav provider](terraform.md).

## Install rvavctl

1. Download the archive for your system from the rvav release:
   `rvavctl_<version>_<os>_<arch>.tar.gz`, or `.zip` for Windows.
2. Check it against the release's `SHA256SUMS`:

   ```sh
   sha256sum --check --ignore-missing SHA256SUMS
   ```

3. Put `rvavctl` (`rvavctl.exe` on Windows) in a directory on your `PATH`.
4. Optional: turn on completion for your shell, for example for bash:

   ```sh
   rvavctl completion bash > ~/.local/share/bash-completion/completions/rvavctl
   ```

   `rvavctl completion --help` lists the other shells (zsh, fish,
   PowerShell).

To build it from the repository instead, run `make build`: it writes
`bin/rvavctl`.

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
only you can read:

| System | File |
|---|---|
| Linux | `~/.config/rvav/config.json` |
| macOS | `~/Library/Application Support/rvav/config.json` |
| Windows | `%AppData%\rvav\config.json` |

It renews the sign-in as needed. When your organisation ends it, commands
fail with `the sign-in has expired`: run `rvavctl login` again. To forget
the sign-in on this machine, run `rvavctl logout`.

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
profile file is needed, and they win over the profile. AWX jobs that rvav
launches already have both.

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

5. Request it. To wait until it's done, add `--wait`:

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
| `rvavctl requests list` | The requests you can see, newest first. `--mine`, `--status running,failed`, `--project`. |
| `rvavctl requests show REQ-10001` | A request and its steps. |
| `rvavctl requests wait REQ-10001` | Waits until it ends. `--timeout 30m` gives up after 30 minutes. |
| `rvavctl requests cancel REQ-10001` | Cancels a request that is waiting or running. |
| `rvavctl requests retry REQ-10001` | Runs a failed request again from the step that failed. |

You can name a request by its number or its ID. `wait`, and `--wait` on
the commands that make requests, exit with `0` when the request succeeds
and `1` when it fails, is rejected or cancelled.

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
| `rvavctl approvals list` | The approvals waiting for you, oldest first. `--status approved,rejected` for those you decided. `--all` for every approval, if you're an administrator or auditor. |
| `rvavctl approvals show ID` | What you need to decide: the request, why it needs approval, the facts, and the inputs you may change. |
| `rvavctl approvals approve ID -m "OK"` | Approves at your level. `--input memory_gb=32` changes an input that approvers may change. |
| `rvavctl approvals reject ID -m "Use the M size"` | Rejects. The comment is required: the requester gets it. |

## Output and scripting

- Tables go to standard output; progress and errors go to standard error.
- `-o json` prints what the API returns. A list is a JSON array of its
  items, with every page read up to `--limit`. `catalog show` prints
  `{"item": …, "form": …}`, and `requests show` prints
  `{"request": …, "steps": […]}`.
- A command exits with `0` when it succeeds and `1` when it fails.

For example, to wait for each of your running requests in turn:

```sh
rvavctl requests list --mine --status running -o json \
  | jq -r '.[].number' \
  | xargs -n 1 rvavctl requests wait
```

To see every command and flag, run `rvavctl --help`, or `--help` on any
command.
