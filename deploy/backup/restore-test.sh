#!/usr/bin/env bash
# A tested restore, end to end (S14.3), on the compose stack with
# docker-compose.backup.yml: a base backup is taken; a production request
# is left waiting for approval and another is building when the moment T
# is noted; more is done after T. The server is restored to T. Then what
# was done after T is gone, the request waiting at T is approved and
# succeeds, the one building at T finishes (its workflow resumes from
# Temporal's state at T), and a new request succeeds.
#
# Needs bash, curl, jq and docker. Exits non-zero, saying why, on failure.
set -euo pipefail
cd "$(dirname "$0")/../.."
base="${RVAV_API:-http://localhost:${RVAV_WEB_PORT:-3000}}/api/v1"
pg=${RVAV_PG_CONTAINER:-rvav-postgres-1}
project=prj_web
log() { printf '%(%H:%M:%S)T %s\n' -1 "$*"; }
call() {
	local user=$1
	shift
	curl -fsS --max-time 20 -H "X-Rvav-Dev-User: $user" -H "Content-Type: application/json" "$@"
}
sql() { docker exec "$pg" psql -U rvav -d rvav -tAc "$1"; }
status() { call tnguyen "$base/requests/$1" | jq -r .status; }
submit() {
	local env=$1 inputs=$2
	call tnguyen -X POST "$base/requests" -H "Idempotency-Key: $(cat /proc/sys/kernel/random/uuid)" \
		-d "$(jq -n --arg i "$item" --arg v "$version" --arg e "$env" --argjson in "$inputs" \
			'{catalog_item_id: $i, version_id: $v, project_id: "prj_web", environment: $e, inputs: $in}')" | jq -r .id
}
wait_for() {
	local id=$1 want=$2 deadline=$((SECONDS + ${3:-300}))
	while [ "$SECONDS" -lt "$deadline" ]; do
		[ "$(status "$id")" = "$want" ] && return 0
		sleep 2
	done
	echo "FAIL: $id is $(status "$id"), not $want" >&2
	exit 1
}
dev='{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": ["linux-baseline"]}'
prd='{"role": "front", "size": "XL", "cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210", "post_config": ["linux-baseline"]}'
item=$(call tnguyen "$base/catalog/items/rhel9-server?project_id=$project" | jq -r .id)
version=$(call tnguyen "$base/catalog/items/rhel9-server/form?project_id=$project" | jq -r .version_id)

log "base backup"
backup=$(deploy/backup/backup.sh)
log "  $backup"

waiting=$(submit prd "$prd")
wait_for "$waiting" pending_approval 60
building=$(submit dev "$dev")
sleep 3 # building, mid-way, at T
[ "$(status "$building")" = running ] || { echo "FAIL: $building isn't building at T" >&2; exit 1; }
T=$(sql "select now()")
log "T = $T: $waiting waits for approval, $building is $(status "$building")"
sleep 2

after=$(submit dev "$dev")
wait_for "$after" succeeded
wait_for "$building" succeeded
log "after T: $after succeeded, and $building finished"
# The WAL past T reaches the archive.
sql "select pg_switch_wal()" >/dev/null
sleep 5

log "restoring to T"
deploy/backup/restore.sh "$T" "$backup"

fail=0
check() { if ! eval "$2"; then echo "FAIL: $1" >&2; fail=1; else echo "ok:   $1"; fi; }
check "what was done after T is gone" '[ "$(sql "select count(*) from requests where id = '"'$after'"'")" = 0 ]'
check "the request waiting at T still waits" '[ "$(status "$waiting")" = pending_approval ]'
log "the request building at T resumes"
wait_for "$building" succeeded 300
check "the request building at T finished" '[ "$(status "$building")" = succeeded ]'
log "approving the request waiting at T"
for _ in 1 2 3; do
	for level in $(call mkeller "$base/approvals?view=all&status=pending" | jq -r --arg r "$waiting" '.items[] | select(.request.id == $r) | .id'); do
		call mkeller -X POST "$base/approvals/$level/decision" -d '{"decision": "approve"}' >/dev/null
	done
	[ "$(status "$waiting")" != pending_approval ] && break
	sleep 2
done
wait_for "$waiting" succeeded 300
check "the request waiting at T, approved, succeeded" '[ "$(status "$waiting")" = succeeded ]'
new=$(submit dev "$dev")
wait_for "$new" succeeded
check "a new request succeeds" '[ "$(status "$new")" = succeeded ]'
exit "$fail"
