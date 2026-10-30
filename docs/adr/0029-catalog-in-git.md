# ADR-0029: The catalog in Git

## Status
Accepted (C1, after N2).

## Context
Catalog items are versioned in rvav (ADR-0019): drafts, published and
deprecated versions, YAML export and import. Teams that keep their
infrastructure in Git want the catalog there too: changes reviewed in merge
requests, history and blame, the same files in every installation. vRA
offers the same for cloud templates (its Git integration). Two writers,
rvav's catalog admins and the repository's committers, must not overwrite
each other.

## Decision

### One repository, one file per item
- rvav has at most one repository (Administration › Integrations › Git, or
  `RVAV_GIT_*`): an HTTPS or SSH URL, a branch (`main` by default) and a
  directory (`catalog` by default).
- Each item is one file, `<directory>/<slug>.yaml`, as the YAML export writes
  it with one version: the item's published version, or, when it has none,
  the one deprecated last. Its state is in the file. The repository's
  history is the item's history; rvav keeps its own versions as before.
- **Never in the repository:** secret values (blueprints and forms name
  secrets, they never hold them), drafts (they are rvav's work in
  progress), and **entitlements**: which projects may request an item names
  projects of one installation, and differs from one to the next. Items
  imported from AWX aren't kept there either: AWX's sync owns them
  (ADR-0016).
- When rvav first reads a repository, it adds each item it has that the
  repository doesn't; a file that is the same as rvav's is taken as
  agreed. Another repository (URL, branch or directory) starts afresh.

### Which side wins
**rvav publishes; the repository proposes.**
- **From rvav to the repository:** publishing a version, deprecating it, or
  rolling back to an earlier one, commits the item's file: the
  administrator as author, the changelog as message, rvav as committer.
  The commit is made by the sync process, never while the administrator
  waits: publishing works when Git doesn't answer. A failure is shown on
  the item, retried, and audited.
- **From the repository to rvav:** a file changed in the repository (a
  merge, a push) becomes a **draft** of its item, with the diff and the
  commit that made it. It is never published by itself: a catalog admin
  publishes it in rvav, which commits it back, and both sides agree again.
  Changed again while that draft is untouched, the draft is made again.
  A new file becomes a new item, as a draft (its last version only). A
  deleted file is reported, never deleted in rvav: the item's next change
  adds it back. A file the YAML import refuses, or whose name isn't its
  slug, is reported with its problems. The item's details (name,
  summary...) follow rvav: a change to them in the repository is put back
  by a commit, and audited. A draft of the repository's change discarded in
  rvav sets it aside: rvav's file is committed back.
- **A conflict:** the item changed on both sides since they last agreed (a
  version published in rvav and not yet committed, and the file changed in
  the repository; or a draft open in rvav). It is shown with both sides,
  and the administrator chooses: rvav's (committed over the repository's)
  or the repository's (a draft in rvav).

### Pushing
Commits are made on the branch as fetched. A push refused because the
branch moved meanwhile is tried again on the new head when the item's file
didn't change there; when it did, it is a conflict. Nothing is ever forced.

### Reading
The sync process reads the repository every `RVAV_GIT_SYNC_INTERVAL` (5
minutes), on demand ("read the repository now"), and when the Git server
calls rvav's webhook (`/api/v1/integrations/git/webhook`, with the secret of
`RVAV_GIT_WEBHOOK_SECRET`: GitHub's and Gitea's signature, or GitLab's
token). One process syncs at a time.

### Signing in
- **HTTPS:** a token (or a password) and a user name, as the Git server
  wants them.
- **SSH:** a deploy key (its private key, PEM) and the server's host keys
  (`known_hosts` lines).
- The credential is sealed like the other integrations' secrets
  (ADR-0020); the webhook's secret is an environment variable.
- A pure-Go client (go-git): no git binary in rvav's image. Local paths
  aren't accepted, but tests serve a local repository in process.

## Consequences
- **Plan coverage:** feature 35, Git integration for templates, is built
  (it was counted as done with YAML import and export).
- **Two writers, one rule:** rvav never publishes what the repository
  proposes on its own, and never overwrites a change it hasn't seen.
- **What the repository holds is what users can request**, give or take
  what waits to be committed or published: a team can review the catalog
  in merge requests.
- **Entitlements, drafts and secrets stay in rvav:** moving a catalog to
  another installation still needs its entitlements set there.
- **A real Git server must confirm it:** tests run against local
  repositories and the compose stack's Git server; the Lab acceptance runs
  against the organisation's.
