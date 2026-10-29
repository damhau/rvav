#!/usr/bin/env bash
# An upgrade with requests in flight (S14.4, docs/operations/upgrades.md):
# rvav at an earlier version runs as a compose project of its own
# (rvavup, on other ports); a production request is left waiting for
# approval and the PostgreSQL cluster composite is caught while one of its
# AWX jobs runs; then rvav is upgraded to this checkout (its images rebuilt,
# migrations applied, every process replaced). Then the composite finishes,
# the waiting request, approved, succeeds, and a new request succeeds.
#
#   deploy/upgrade-test.sh [old-ref]    (default 88e59ca, S13.7: before S14)
#
# Needs git, bash, curl, jq and docker. Removes its project at the end.
set -euo pipefail
cd "$(dirname "$0")/.."
old_ref="${1:-88e59ca}"
export COMPOSE_PROJECT_NAME=rvavup RVAV_DEV_USER=mkeller
export RVAV_API_PORT=28080 RVAV_WEB_PORT=23000 RVAV_KEYCLOAK_PORT=28081 RVAV_TEMPORAL_PORT=27233 \
	RVAV_TEMPORAL_UI_PORT=28233 RVAV_VCSIM_PORT=28989 RVAV_POSTGRES_PORT=25432 RVAV_MAILPIT_PORT=28025
base="http://localhost:$RVAV_API_PORT/api/v1"
work=$(mktemp -d)
cleanup() {
	docker compose -f deploy/docker-compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
	git worktree remove --force "$work/old" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
log() { printf '%(%H:%M:%S)T %s\n' -1 "$*"; }
call() {
	local user=$1
	shift
	curl -fsS --max-time 20 -H "X-Rvav-Dev-User: $user" -H "Content-Type: application/json" "$@"
}
# The administrator sees every project.
status() { call mkeller "$base/requests/$1" | jq -r .status; }
wait_for() {
	local id=$1 want=$2 deadline=$((SECONDS + ${3:-300}))
	while [ "$SECONDS" -lt "$deadline" ]; do
		[ "$(status "$id")" = "$want" ] && return 0
		sleep 2
	done
	echo "FAIL: $id is $(status "$id"), not $want" >&2
	exit 1
}
submit() {
	local user=$1 slug=$2 project=$3 env=$4 inputs=$5 item version
	item=$(call "$user" "$base/catalog/items/$slug?project_id=$project" | jq -r .id)
	version=$(call "$user" "$base/catalog/items/$slug/form?project_id=$project" | jq -r .version_id)
	call "$user" -X POST "$base/requests" -H "Idempotency-Key: $(cat /proc/sys/kernel/random/uuid)" \
		-d "$(jq -n --arg i "$item" --arg v "$version" --arg p "$project" --arg e "$env" --argjson in "$inputs" \
			'{catalog_item_id: $i, version_id: $v, project_id: $p, environment: $e, inputs: $in}')" | jq -r .id
}

log "rvav at $old_ref"
git worktree add --detach "$work/old" "$old_ref" >/dev/null 2>&1
docker compose -f "$work/old/deploy/docker-compose.yml" up -d --build --wait >/dev/null 2>&1
old_version=$(docker exec rvavup-postgres-1 psql -U rvav -d rvav -tAc "select max(version_id) from goose_db_version")
log "  migrations at $old_version"
# Everything answers before requests are left in flight.
deploy/smoke.sh "http://localhost:$RVAV_API_PORT" >/dev/null

waiting=$(submit tnguyen rhel9-server prj_web prd \
	'{"role": "front", "size": "XL", "cluster": "GVA-PRD-CL02", "datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210", "post_config": ["linux-baseline"]}')
wait_for "$waiting" pending_approval 60
composite=$(submit sbrunner postgres-cluster prj_data dev \
	'{"name": "upgrade", "pg_version": "16.6", "size": "M", "data_gb": 40, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320"}')
log "waiting for one of the composite's AWX jobs to run"
deadline=$((SECONDS + 300))
until call sbrunner "$base/requests/$composite/steps" | jq -e '[.. | objects | select(.status? == "running" and ((.title? // "") | startswith("AWX job")))] | length > 0' >/dev/null; do
	if [ "$SECONDS" -ge "$deadline" ] || [ "$(status "$composite")" = failed ]; then
		echo "FAIL: no AWX job of $composite ran; it is $(status "$composite")" >&2
		call sbrunner "$base/requests/$composite/steps" | jq -r '.. | objects | select(.title? != null) | "\(.status)\t\(.title)\t\(.error // "")"' >&2
		exit 1
	fi
	sleep 1
done
running=$(call sbrunner "$base/requests/$composite/steps" | jq -r '[.. | objects | select(.status? == "running" and ((.title? // "") | startswith("AWX job")))][0].title')
log "in flight: $waiting waits for approval; $composite runs \"$running\""

log "upgrading to this checkout"
start=$SECONDS
docker compose -f deploy/docker-compose.yml up -d --build --wait >/dev/null 2>&1
new_version=$(docker exec rvavup-postgres-1 psql -U rvav -d rvav -tAc "select max(version_id) from goose_db_version")
log "  upgraded in $((SECONDS - start))s; migrations at $new_version"

fail=0
check() { if eval "$2"; then echo "ok:   $1"; else echo "FAIL: $1" >&2; fail=1; fi; }
check "the migrations moved on" '[ "$new_version" -gt "$old_version" ]'
wait_for "$composite" succeeded 600
check "the composite in flight finished" '[ "$(status "$composite")" = succeeded ]'
check "the request waiting still waits" '[ "$(status "$waiting")" = pending_approval ]'
for _ in 1 2 3; do
	for level in $(call mkeller "$base/approvals?view=all&status=pending" | jq -r --arg r "$waiting" '.items[] | select(.request.id == $r) | .id'); do
		call mkeller -X POST "$base/approvals/$level/decision" -d '{"decision": "approve"}' >/dev/null
	done
	[ "$(status "$waiting")" != pending_approval ] && break
	sleep 2
done
wait_for "$waiting" succeeded 300
check "the request waiting, approved after the upgrade, succeeded" '[ "$(status "$waiting")" = succeeded ]'
new=$(submit tnguyen rhel9-server prj_web dev \
	'{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": ["linux-baseline"]}')
wait_for "$new" succeeded
check "a new request succeeds" '[ "$(status "$new")" = succeeded ]'
exit "$fail"
