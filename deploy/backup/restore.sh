#!/usr/bin/env bash
# Brings the compose stack's PostgreSQL server (rvav's database and
# Temporal's, together) back to a moment, from a base backup and the
# archived WAL, then starts rvav again (S14.3).
#
#   deploy/backup/restore.sh '2026-10-29 15:04:05+00' [base-...]
#
# The latest base backup taken before the moment by default. Nothing is
# stopped until the backup is found; the data it replaces is kept in the
# backup volume (pre-restore-<time>.tar.gz) until deleted by hand.
set -euo pipefail
cd "$(dirname "$0")/../.."
target="$1"
compose=(docker compose -f deploy/docker-compose.yml -f deploy/backup/docker-compose.backup.yml)
pg=${RVAV_PG_CONTAINER:-rvav-postgres-1}
log() { printf '%(%H:%M:%S)T %s\n' -1 "$*" >&2; }

# Base backups are named by the UTC time they were taken.
at=$(date -u -d "$target" +%Y%m%dT%H%M%SZ) || { log "not a moment: $target"; exit 1; }
base="${2:-$(docker exec "$pg" ls /backups/base | sort | awk -v t="base-$at" '$0 <= t' | tail -1)}"
if [ -z "$base" ] || ! docker exec "$pg" test -f "/backups/base/$base/base.tar.gz"; then
  log "no base backup ${base:-taken before $target}: nothing was changed"
  exit 1
fi

log "stopping rvav and Temporal"
"${compose[@]}" stop web api worker sync temporal-ui temporal >/dev/null
log "stopping PostgreSQL; restoring $base, then the WAL until $target"
"${compose[@]}" stop postgres >/dev/null
docker run --rm --volumes-from "$pg" -u postgres --entrypoint sh postgres:16.4 -c "
	set -e
	cd /var/lib/postgresql/data
	tar -czf /backups/pre-restore-$(date -u +%Y%m%dT%H%M%SZ).tar.gz .
	find . -mindepth 1 -delete
	tar -xzf /backups/base/$base/base.tar.gz -C /var/lib/postgresql/data
	mkdir -p /var/lib/postgresql/data/pg_wal && tar -xzf /backups/base/$base/pg_wal.tar.gz -C /var/lib/postgresql/data/pg_wal
	cat >>/var/lib/postgresql/data/postgresql.auto.conf <<CONF
restore_command = 'cp /backups/wal/%f %p'
recovery_target_time = '$target'
recovery_target_action = 'promote'
CONF
	touch /var/lib/postgresql/data/recovery.signal"
start=$SECONDS
"${compose[@]}" up -d --wait postgres >/dev/null
until docker exec "$pg" psql -U rvav -d rvav -tAc 'select not pg_is_in_recovery()' 2>/dev/null | grep -q t; do sleep 1; done
log "PostgreSQL recovered and promoted in $((SECONDS - start))s"
# The next restore starts from these settings again.
docker exec "$pg" sh -c "sed -i '/^restore_command\|^recovery_target/d' /var/lib/postgresql/data/postgresql.auto.conf"
log "starting Temporal and rvav"
"${compose[@]}" up -d --wait >/dev/null
log "restored to $target in $((SECONDS - start))s"
