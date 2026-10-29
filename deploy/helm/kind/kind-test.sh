#!/usr/bin/env bash
# The Helm chart on a kind cluster of its own (S14.2): builds the images,
# loads them into kind with what rvav needs (deps.yaml), installs the chart,
# signs in with Keycloak and runs the smoke test through the web app, kills
# an API pod, and upgrades with a changed setting.
#
#   deploy/helm/kind/kind-test.sh          (kind, kubectl, helm, docker, curl, jq)
#   kind delete cluster --name rvav-chart  (afterwards)
set -euo pipefail
cd "$(dirname "$0")/../../.."
cluster=rvav-chart
ns=rvav
ctx="kind-$cluster"
k() { kubectl --context "$ctx" "$@"; }
log() { printf '%(%H:%M:%S)T %s\n' -1 "$*"; }

kind get clusters | grep -qx "$cluster" || kind create cluster --name "$cluster" --wait 120s
log "building and loading the images"
docker build -q -t ghcr.io/damhau/rvav:1.0.0 . >/dev/null
docker build -q -t ghcr.io/damhau/rvav-web:1.0.0 web >/dev/null
for img in ghcr.io/damhau/rvav:1.0.0 ghcr.io/damhau/rvav-web:1.0.0 postgres:16.4 temporalio/auto-setup:1.25.2 \
	quay.io/keycloak/keycloak:25.0.6 vmware/vcsim:v0.52.0; do
	docker image inspect "$img" >/dev/null 2>&1 || docker pull -q "$img" >/dev/null
	kind load docker-image --name "$cluster" "$img" >/dev/null
done

log "what rvav needs"
k create namespace rvav-deps --dry-run=client -o yaml | k apply -f - >/dev/null
k -n rvav-deps create configmap keycloak-realm --from-file=deploy/keycloak/realm-rvav.json --dry-run=client -o yaml | k apply -f - >/dev/null
k apply -f deploy/helm/kind/deps.yaml >/dev/null
k -n rvav-deps rollout status deploy/postgres deploy/temporal deploy/keycloak deploy/vcsim --timeout=600s >/dev/null

log "installing the chart"
k create namespace "$ns" --dry-run=client -o yaml | k apply -f - >/dev/null
helm --kube-context "$ctx" upgrade --install rvav deploy/helm/rvav -n "$ns" -f deploy/helm/kind/values.yaml --wait --timeout 10m
k -n rvav-deps wait --for=condition=complete job/vcsim-bootstrap --timeout=300s >/dev/null
# It failed until the chart's pods migrated the database: started again.
k -n rvav-deps rollout restart deploy/efficientip >/dev/null
k -n rvav-deps rollout status deploy/efficientip --timeout=300s >/dev/null
k -n "$ns" get pods -o wide

log "port-forwards: the web app on 18180, Keycloak on 18181"
k -n "$ns" port-forward svc/rvav-web 18180:80 >/dev/null 2>&1 &
k -n rvav-deps port-forward svc/keycloak 18181:8080 >/dev/null 2>&1 &
trap 'kill $(jobs -p) 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do curl -fsS -o /dev/null http://localhost:18180/ 2>/dev/null && break; sleep 1; done
token() {
	curl -fsS http://localhost:18181/realms/rvav/protocol/openid-connect/token \
		-d grant_type=password -d client_id=rvav -d username=lea -d password=rvav -d scope=openid | jq -r .access_token
}
curl -fsS http://localhost:18180/config.js
echo

log "smoke test through the web app, signed in as lea"
RVAV_SMOKE_TOKEN=$(token) deploy/smoke.sh http://localhost:18180

log "an API pod killed: the other answers"
pod=$(k -n "$ns" get pods -l app.kubernetes.io/component=api -o jsonpath='{.items[0].metadata.name}')
k -n "$ns" delete pod "$pod" --wait=false >/dev/null
bad=0
for _ in $(seq 1 50); do
	curl -fsS -o /dev/null -H "Authorization: Bearer $(token)" http://localhost:18180/api/v1/me || bad=$((bad + 1))
	sleep 0.2
done
echo "reads while an API pod was replaced: $((50 - bad)) answered, $bad not"

log "upgrade with a changed setting: the pods roll"
helm --kube-context "$ctx" upgrade rvav deploy/helm/rvav -n "$ns" -f deploy/helm/kind/values.yaml \
	--set config.RVAV_LOG_LEVEL=debug --wait --timeout 10m >/dev/null
k -n "$ns" rollout status deploy/rvav-api deploy/rvav-worker deploy/rvav-sync --timeout=300s
RVAV_SMOKE_TOKEN=$(token) deploy/smoke.sh http://localhost:18180
[ "$bad" -le 2 ] || { echo "FAIL: $bad reads failed while an API pod was replaced" >&2; exit 1; }
log "the chart works"
