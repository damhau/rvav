# AWX test content

What rvav needs in an AWX to try it for real: an organisation `rvav-test`
with a project pointing to this repository, an inventory, job templates
with surveys and a workflow ([content.yml](content.yml)). The playbooks in
[playbooks/](playbooks/) run on the execution environment
(`connection: local`) and only report what they would do to each host, so
they work with the lab's vcsim VMs.

## Setting it up

With an AWX token of a user who may create an organisation (a superuser on
a test AWX):

```sh
RVAV_AWX_URL=http://awx.example:8052 RVAV_AWX_TOKEN=... rvav awx-bootstrap
```

It creates what is missing and updates what exists, then syncs the project
from GitHub (so the AWX needs to reach github.com). It never touches other
organisations. Run it again after changing `content.yml`; the project
follows `main`, so push playbook changes first.

## Contract tests

The AWX client's tests against a real AWX (templates by organisation and
label, surveys, launches with limit and variables, events, a failed task,
a refused survey answer, cancel, a workflow and its nodes) run when these
are set:

```sh
RVAV_AWX_TEST_URL=http://awx.example:8052 RVAV_AWX_TEST_TOKEN=... \
  go test -run TestContract ./internal/integrations/awx/
```

They set the content up first. They are not part of `make check` or CI,
which have no AWX. Last run against AWX 24.6.1: all passing.

## What they found
- AWX filters job templates on labels (`labels__name__in`) but refuses to
  for workflow job templates; rvav filters those itself.
- A survey question needs a default; AWX's "no default" is `""`.
