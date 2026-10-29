# Backups and restores

rvav keeps everything it knows in PostgreSQL: its own database, and
Temporal's (`temporal` and `temporal_visibility`), where requests in flight
are. Restoring them **together, to the same moment** brings back a
consistent rvav: what it recorded, and where each workflow was. That is the
only backup rvav needs, with its secrets key (S14.3).

| What | How | Why |
|---|---|---|
| **The PostgreSQL server**: rvav's database, Temporal's two | Point-in-time: a base backup every day, and the WAL archived continuously (at least every minute) | Restore to any moment, both together |
| **`RVAV_SECRETS_KEY`** (and `RVAV_SECRETS_OLD_KEYS`) | In the organisation's vault, never with the database's backups | Without it, stored secrets, vCenter passwords and integration settings can't be opened |
| **The installation's settings**: Helm values, `/etc/rvav/*.env` | In Git (the plain ones) and the vault (the secret ones) | To install again |

Nothing else needs backing up. The inventory cache and the subnets are read
again from vCenter and EfficientIP. Temporal's server keeps nothing outside
its database.

## Targets

The 1.0 gate asks for **RPO 15 minutes and RTO 1 hour**:

- **RPO:** WAL is archived as it is written and at least every minute
  (`archive_timeout=60`), so at most about a minute is lost, plus whatever
  the archive's storage loses.
- **RTO:** restoring the last base backup, then replaying the WAL since,
  then starting Temporal and rvav. On the compose stack it took 14
  seconds: 3 to recover and promote PostgreSQL, the rest to start Temporal
  and rvav (see below). On a production database it is mostly the base
  backup's size and the WAL since. A daily base backup keeps the replay
  short.

## In production

Use the database platform's point-in-time backups. All of these do base
backups plus WAL archiving to object storage:

- CloudNativePG on Kubernetes;
- Patroni with pgBackRest or WAL-G;
- a managed PostgreSQL.

Keep them on another site than the database. The settings above (daily
base, WAL at least every minute) meet the gate.

If Temporal's databases are on another server than rvav's, restore both to
the same moment. When the two servers can't be restored to exactly the same
moment, restore Temporal's to a moment before rvav's. A workflow then
re-runs its last activities, which are idempotent; it never misses one.

## Trying it on the compose stack

`deploy/backup` has three parts:

- `docker-compose.backup.yml` archives the WAL to a backup volume;
- `backup.sh` takes a base backup (`pg_basebackup`);
- `restore.sh '<moment>'` restores the server to a moment and starts
  Temporal and rvav again.

`restore-test.sh` tests a restore end to end, with requests in flight:

1. A base backup is taken.
2. A production request is left **waiting for approval**, and another is
   **building** when the moment T is noted.
3. After T, another request is made, and the one building finishes.
4. The server is restored to T. Then:
   - the request made after T is gone;
   - the one waiting at T still waits, and once approved it succeeds;
   - the one building at T resumes from Temporal's state at T and
     finishes. Its activities run again and find what they had already
     done, the VM in vCenter among it. An AWX job AWX no longer knows is
     launched again, rather than failing the request;
   - a new request succeeds.

```
docker compose -f deploy/docker-compose.yml -f deploy/backup/docker-compose.backup.yml up -d --wait
deploy/backup/restore-test.sh
```

## After a restore

The world went on after the moment restored to:

- **VMs made after it** are in vCenter but not in rvav. They are offered
  for onboarding (Administration → Onboarding), and VMs rvav deleted after
  it are flagged missing on their deployments.
- **Addresses and DNS records** created or released after it: run the
  EfficientIP reconcile report (Integrations → EfficientIP).
- **Notifications and subscription deliveries** sent after it may be sent
  again: they are queued again with the rest of the database.
- **AWX jobs** started after it ran. Requests whose workflows run them
  again do so with the same inputs.
