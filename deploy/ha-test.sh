#!/usr/bin/env bash
# Failover under load (S14.1), on the HA stack:
#
#   docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.ha.yml up -d --build --wait
#   deploy/ha-test.sh
#
# Requests are submitted, and reads made, through the web app's proxy while
# an API replica, a worker, the active sync and a Temporal node are killed
# in turn. Every request must succeed, reads must keep answering, a live
# event stream must carry on after reconnecting, and the standby sync must
# take over. The killed containers are started again at the end.
#
# Needs bash, curl, jq and docker. Exits non-zero, saying why, on failure.
set -euo pipefail

cd "$(dirname "$0")/.."
base="${RVAV_HA_API:-http://localhost:${RVAV_WEB_PORT:-3000}}/api/v1"
count="${RVAV_HA_REQUESTS:-24}"
compose=(docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.ha.yml)
work=$(mktemp -d)
# On any exit: the background loops and what they started (the event
# stream's curl) stop, and the killed containers are started again.
cleanup() {
	for p in $(jobs -p); do
		pkill -P "$p" 2>/dev/null || true
		kill "$p" 2>/dev/null || true
	done
	if [ "${#killed[@]}" -gt 0 ] && [ -z "${restarted:-}" ]; then
		"${compose[@]}" up -d --wait >/dev/null 2>&1 || true
	fi
	rm -rf "$work"
}
killed=()
trap cleanup EXIT
project=prj_web

call() {
	curl -fsS --max-time 20 -H "X-Rvav-Dev-User: lmartin" -H "Content-Type: application/json" "$@"
}
log() { printf '%(%H:%M:%S)T %s\n' -1 "$*"; }

item=$(call "$base/catalog/items/rhel9-server?project_id=$project" | jq -r .id)
version=$(call "$base/catalog/items/rhel9-server/form?project_id=$project" | jq -r .version_id)
draft=$(jq -n --arg item "$item" --arg version "$version" --arg project "$project" '{
	catalog_item_id: $item, version_id: $version, project_id: $project, environment: "dev",
	inputs: {role: "app", size: "S", cluster: "GVA-DEV-CL01", datastore: "vsan-gva-dev-cl01",
		network: "dvpg-app-dev-vl310", post_config: ["linux-baseline"]},
	justification: "Failover test."}')

# Reads, every 200 ms: how many answered, how many didn't.
(
	ok=0 bad=0
	while :; do
		if curl -fsS --max-time 5 -o /dev/null -H "X-Rvav-Dev-User: lmartin" "$base/requests?project_id=$project&limit=5"; then
			ok=$((ok + 1))
		else
			bad=$((bad + 1))
		fi
		echo "$ok $bad" >"$work/reads"
		sleep 0.2
	done
) &

# A live event stream that reconnects, as the browser's does, from the last
# event it saw; it counts the events.
(
	last=""
	while :; do
		curl -sSN --max-time 600 -H "X-Rvav-Dev-User: lmartin" ${last:+-H "Last-Event-ID: $last"} \
			"$base/events?topics=requests&project_id=$project" 2>/dev/null |
			while IFS= read -r line; do
				case "$line" in
				id:*)
					last="${line#id: }"
					echo "$last" >"$work/last-event"
					echo x >>"$work/events"
					;;
				esac
			done || true
		[ -f "$work/last-event" ] && last=$(cat "$work/last-event")
		sleep 1
	done
) &

# Requests, one every 5 s; a submission refused by a dying replica is sent
# again with the same Idempotency-Key.
(
	for i in $(seq 1 "$count"); do
		key=$(cat /proc/sys/kernel/random/uuid)
		for attempt in 1 2 3 4 5; do
			if req=$(call -X POST "$base/requests" -H "Idempotency-Key: $key" -d "$draft"); then
				jq -r .id <<<"$req" >>"$work/requests"
				break
			fi
			sleep 2
		done
		sleep 5
	done
) &
submitter=$!

active_sync() {
	for c in $("${compose[@]}" ps -q sync); do
		name=$(docker inspect -f '{{.Name}}' "$c" | tr -d /)
		if docker logs "$name" 2>&1 | grep -q '"inventory sync started"'; then
			echo "$name"
			return
		fi
	done
}

kill_one() {
	log "kill $1"
	docker kill "$1" >/dev/null
	killed+=("$1")
}
sleep 20
kill_one rvav-api-1
sleep 20
kill_one rvav-worker-1
sleep 20
leader=$(active_sync)
synced_before=$(docker exec rvav-postgres-1 psql -U rvav -d rvav -tAc "select max(synced_at) from inv_vms")
kill_one "$leader"
sleep 20
kill_one rvav-temporal-2-1

wait "$submitter"
log "all $(wc -l <"$work/requests") requests submitted; waiting for them to finish"
deadline=$((SECONDS + 600))
pending=1
while [ "$pending" -gt 0 ] && [ "$SECONDS" -lt "$deadline" ]; do
	pending=0 failed=0
	while read -r id; do
		case "$(call "$base/requests/$id" | jq -r .status)" in
		succeeded) ;;
		failed | rejected | cancelled) failed=$((failed + 1)) ;;
		*) pending=$((pending + 1)) ;;
		esac
	done <"$work/requests"
	[ "$pending" -gt 0 ] && sleep 5
done

# Written by the reads loop as it goes: read it until it holds both counts.
until read -r reads_ok reads_bad <"$work/reads" 2>/dev/null && [ -n "${reads_bad:-}" ]; do sleep 0.2; done
events=$(wc -l <"$work/events" 2>/dev/null || echo 0)
standby=$(active_sync)
synced_after=$(docker exec rvav-postgres-1 psql -U rvav -d rvav -tAc "select max(synced_at) from inv_vms")
submitted=$(wc -l <"$work/requests")

log "starting the killed containers again"
"${compose[@]}" up -d --wait >/dev/null 2>&1 || true
restarted=1

echo
echo "requests: $submitted submitted of $count, $((submitted - pending - failed)) succeeded, $failed failed, $pending unfinished"
echo "reads:    $reads_ok answered, $reads_bad not"
echo "events:   $events received on a stream that reconnects"
echo "sync:     $leader killed, $standby took over; inventory read at $synced_before, then $synced_after"

fail=0
[ "$submitted" -eq "$count" ] || { echo "FAIL: not every request could be submitted" >&2; fail=1; }
[ "$failed" -eq 0 ] && [ "$pending" -eq 0 ] || { echo "FAIL: requests didn't all succeed" >&2; fail=1; }
[ "$events" -gt 0 ] || { echo "FAIL: no live events" >&2; fail=1; }
[ -n "$standby" ] && [ "$standby" != "$leader" ] && [[ "$synced_after" > "$synced_before" ]] ||
	{ echo "FAIL: the standby sync didn't take over" >&2; fail=1; }
# A read can fail while a killed replica's address is still tried: a few.
[ "$reads_bad" -le 3 ] || { echo "FAIL: $reads_bad reads failed" >&2; fail=1; }
exit "$fail"
