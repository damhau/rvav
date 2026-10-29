#!/usr/bin/env bash
# A base backup of the compose stack's PostgreSQL server (rvav's database
# and Temporal's), next to the archived WAL (docker-compose.backup.yml).
# Together they restore any moment since (restore.sh).
#
#   deploy/backup/backup.sh            prints the backup's name
set -euo pipefail
pg=${RVAV_PG_CONTAINER:-rvav-postgres-1}
name=base-$(date -u +%Y%m%dT%H%M%SZ)
docker exec -u root "$pg" sh -c 'mkdir -p /backups/wal /backups/base && chown -R postgres:postgres /backups'
docker exec -u postgres "$pg" pg_basebackup -U rvav -D "/backups/base/$name" -Ft -z -X stream -c fast >&2
echo "$name"
