#!/usr/bin/env bash
# AWX 24.6.1 in a kind cluster, for the AWX contract tests (S14.9):
#
#   deploy/awx/ci/up.sh        # prints RVAV_AWX_TEST_URL and RVAV_AWX_TEST_TOKEN
#   deploy/awx/ci/up.sh down
#
# The cluster's kubeconfig is its own file, never ~/.kube/config. AWX
# reaches GitHub to sync rvav's test project.
set -euo pipefail
cd "$(dirname "$0")"
name=${AWX_CLUSTER:-rvav-awx}
export KUBECONFIG=${AWX_KUBECONFIG:-${TMPDIR:-/tmp}/$name.kubeconfig}
url=http://127.0.0.1:18052
if [ "${1:-}" = down ]; then
  kind delete cluster --name "$name" --kubeconfig "$KUBECONFIG"
  exit
fi
log() { echo "$(date +%T) $*" >&2; }

kind get clusters | grep -qx "$name" || kind create cluster --name "$name" --config kind.yaml --kubeconfig "$KUBECONFIG" --wait 2m >&2
kubectl create namespace awx --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl apply -k . >/dev/null
kubectl -n awx rollout status deployment/awx-operator-controller-manager --timeout 5m >&2
kubectl apply -f awx.yaml >/dev/null
log "waiting for AWX (its database migrations take minutes)"
for _ in $(seq 180); do
  if curl -fsS "$url/api/v2/ping/" 2>/dev/null | grep -q '"version"'; then break; fi
  sleep 10
done
version=$(curl -fsS "$url/api/v2/ping/" | jq -r .version)
log "AWX $version answers at $url"
# The task pod runs project syncs and jobs: ready too.
for d in awx-web awx-task; do
  until kubectl -n awx get deployment "$d" >/dev/null 2>&1; do sleep 5; done
  kubectl -n awx rollout status "deployment/$d" --timeout 15m >&2
done
# Migrations run while the API answers: wait until the admin can sign in.
for _ in $(seq 60); do
  curl -fsS -u admin:rvav-ci-only "$url/api/v2/me/" >/dev/null 2>&1 && break
  sleep 10
done
# And until a task instance has registered: before that AWX answers 500 to
# a project sync ("No task instances are READY and Enabled").
for _ in $(seq 60); do
  [ "$(curl -fsS -u admin:rvav-ci-only "$url/api/v2/instances/?node_state=ready&enabled=true" | jq .count)" -gt 0 ] && break
  sleep 5
done
token=$(curl -fsS -u admin:rvav-ci-only -H 'Content-Type: application/json' -X POST "$url/api/v2/tokens/" \
  -d '{"description":"rvav contract tests","scope":"write"}' | jq -r .token)
echo "RVAV_AWX_TEST_URL=$url"
echo "RVAV_AWX_TEST_TOKEN=$token"
