# Operating rvav

For the people who install and run rvav. What rvav does for its users is
in the [user](../guides/user.md) and [administrator](../guides/admin.md)
guides.

| Guide | What it covers |
|---|---|
| [installation.md](installation.md) | What rvav needs; Kubernetes with Helm, or VMs with Docker Compose and systemd. |
| [configuration.md](configuration.md) | Every setting, its default, and which are secrets. |
| [high-availability.md](high-availability.md) | Replicas of each process, and what happens when one dies. |
| [backups.md](backups.md) | What to back up, how, and tested restores. |
| [upgrades.md](upgrades.md) | Upgrading with requests in flight; migrations and workflow changes. |
| [observability.md](observability.md) | Metrics, the dashboard, alerts, traces, logs and their retention. |
| [runbooks.md](runbooks.md) | What to do when an alert fires. |
| [security.md](security.md) | Threat model, scanning, SBOMs, and the installation's part. |
| [vra-import.md](vra-import.md) | Moving vRA's content and deployments to rvav: reading, mapping, importing, the cutover. |
| [load-test.md](load-test.md) | 200 people and 500 requests an hour: results and bottlenecks. |
| [../accessibility.md](../accessibility.md) | What is checked, and what needs a person. |
| [../integrations.md](../integrations.md) | vCenter, AWX and EfficientIP: accounts, permissions, contract tests. |

## API

- **Every rvav** serves its own description at `GET /api/v1/openapi.json`
  (OpenAPI 3.1), for tools and generated clients, to anyone signed in.
- **The reference** built from it is attached to each release as
  `api-reference.html`. Build it locally with `make api-docs`
  (`dist/api-reference.html`).
- **The source** is [api/openapi.yaml](../../api/openapi.yaml).
- **Scripts:** sign in with an API token (`Authorization: Bearer
  rvav_pat_…`), made under API tokens in the app.
