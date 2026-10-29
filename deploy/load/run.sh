#!/usr/bin/env bash
# rvav's load test on the compose stack (S14.7, docs/operations/load-test.md).
# The stack runs with Keycloak sign-in and a fake SOLIDserver that answers
# in 100 ms; Prometheus (deploy/observability) records the server's side:
#
#   RVAV_AUTH_MODE=oidc RVAV_FAKE_EFFICIENTIP_LATENCY=100ms docker compose \
#     -f deploy/docker-compose.yml -f deploy/observability/docker-compose.observability.yml up -d --build --wait
#   deploy/load/run.sh            # USERS=200 RATE=500 DURATION=60m by default
#   SCRIPT=burst.js deploy/load/run.sh   # 20 VMs of one name prefix at once
#
# It lifts the web project's quotas for the run and puts them back, runs
# k6 (deploy/load/rvav.js), waits for the requests made to finish, and
# writes the results to deploy/load/results/<time>/.
set -euo pipefail
cd "$(dirname "$0")/../.."
API=${RVAV_URL:-http://localhost:3000}/api/v1
IDP=${RVAV_IDP:-http://localhost:8081/realms/rvav}
PROM=${RVAV_PROMETHEUS:-http://localhost:9091}
K6=grafana/k6:1.4.0
out=deploy/load/results/$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$out"
log() { echo "$(date +%T) $*" | tee -a "$out/run.log"; }

token() {
  curl -fsS "$IDP/protocol/openid-connect/token" -d grant_type=password -d client_id=rvav \
    -d username="$1" -d password=rvav -d scope=openid | jq -r .access_token
}
admin() { curl -fsS -H "Authorization: Bearer $(token marc)" -H 'Content-Type: application/json' "$@"; }

# The web project's quotas, lifted for the run and put back after.
admin "$API/admin/projects/prj_web/quotas" > "$out/quotas.json"
restore() { admin -X PUT "$API/admin/projects/prj_web/quotas" -d @"$out/quotas.json" >/dev/null && log "quotas put back"; }
trap restore EXIT
admin -X PUT "$API/admin/projects/prj_web/quotas" \
  -d "$(jq '.items |= map(.limit = null)' "$out/quotas.json")" >/dev/null
log "quotas lifted; k6: USERS=${USERS:-200} RATE=${RATE:-500} DURATION=${DURATION:-60m}"

start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' > "$out/stats-before.txt"
# Server's side every minute while k6 runs.
( while sleep 60; do echo "== $(date +%T)"; docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' | grep '^rvav-'; done ) > "$out/stats.txt" 2>&1 &
stats=$!
set +e
docker run --rm --network host -v "$PWD/deploy/load:/load:ro" -v "$PWD/$out:/out" -u "$(id -u)" \
  -e USERS -e RATE -e DURATION -e RVAV_URL -e RVAV_IDP \
  -e BURST $K6 run --quiet --summary-export /out/summary.json "/load/${SCRIPT:-rvav.js}" 2>&1 | tee "$out/k6.txt"
k6=${PIPESTATUS[0]}
set -e
kill $stats 2>/dev/null || true
end=$(date -u +%Y-%m-%dT%H:%M:%SZ)
log "k6 exited $k6"

# Every request the test made, run to its end (up to 10 minutes more).
sql() { docker exec rvav-postgres-1 psql -U rvav -d rvav -At -c "$1"; }
mine="submitted_at between '$start' and '$end' and requester_id in (select id from users where email in ('lea.martin@corp.example', 'thomas.nguyen@corp.example'))"
for _ in $(seq 120); do
  [ "$(sql "select count(*) from requests where $mine and status in ('queued', 'running', 'cancelling')")" = 0 ] && break
  sleep 5
done
sql "select kind, coalesce(action, 'vm'), status, count(*) from requests where $mine group by 1, 2, 3 order by 1, 2, 3" | tee "$out/outcomes.txt"
sql "select error_message, count(*) from requests where $mine and status = 'failed' group by 1 order by 2 desc" | tee "$out/failures.txt"
sql "select percentile_cont(array[0.5, 0.95]) within group (order by extract(epoch from finished_at - started_at))
  from requests where $mine and status = 'succeeded' and kind = 'provision'" | tee "$out/vm-duration.txt"

# The server's view: latency by route and integration, from Prometheus.
range=$(( ($(date -d "$end" +%s) - $(date -d "$start" +%s)) ))s
q() { curl -fsS --get "$PROM/api/v1/query" --data-urlencode "query=$1" --data-urlencode "time=$end" | jq -r '.data.result[] | "\(.metric | to_entries | map(.value) | join(" ")) \(.value[1])"'; }
{
  echo "== API p95 by route (s)"
  q "topk(15, histogram_quantile(0.95, sum by (le, route) (increase(rvav_http_request_duration_seconds_bucket[$range]))))"
  echo "== API calls by status code"
  q "sum by (code) (increase(rvav_http_requests_total[$range]))"
  echo "== integration p95 (s)"
  q "histogram_quantile(0.95, sum by (le, system) (increase(rvav_integration_call_duration_seconds_bucket[$range])))"
  echo "== step p95 (s)"
  q "histogram_quantile(0.95, sum by (le, system) (increase(rvav_step_duration_seconds_bucket[$range])))"
} > "$out/prometheus.txt" 2>&1 || true
log "results in $out"
exit "$k6"
