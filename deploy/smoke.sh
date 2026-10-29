#!/usr/bin/env bash
# Smoke test for a running stack in dev auth mode: submits a RHEL 9 server
# in dev for Web Platform and waits until the request succeeds. That needs
# the API, Temporal, the worker, vcsim and the fakes to work together.
#
#   deploy/smoke.sh [api-url]    default http://localhost:${RVAV_API_PORT:-8080}
#
# Needs curl and jq. Exits non-zero, with the request's steps, on failure.
set -euo pipefail

base="${1:-http://localhost:${RVAV_API_PORT:-8080}}/api/v1"
timeout="${RVAV_SMOKE_TIMEOUT:-300}"
project=prj_web

# With RVAV_SMOKE_TOKEN (an OIDC access token), it signs in as that user
# instead (deploy/helm/kind/kind-test.sh).
call() {
	if [ -n "${RVAV_SMOKE_TOKEN:-}" ]; then
		curl -fsS -H "Authorization: Bearer $RVAV_SMOKE_TOKEN" -H "Content-Type: application/json" "$@"
	else
		curl -fsS -H "X-Rvav-Dev-User: lmartin" -H "Content-Type: application/json" "$@"
	fi
}

item=$(call "$base/catalog/items/rhel9-server?project_id=$project" | jq -r .id)
version=$(call "$base/catalog/items/rhel9-server/form?project_id=$project" | jq -r .version_id)

draft=$(jq -n --arg item "$item" --arg version "$version" --arg project "$project" '{
	catalog_item_id: $item, version_id: $version, project_id: $project, environment: "dev",
	inputs: {role: "app", size: "S", cluster: "GVA-DEV-CL01", datastore: "vsan-gva-dev-cl01",
		network: "dvpg-app-dev-vl310", post_config: ["linux-baseline"]},
	justification: "Smoke test of the compose stack."}')

key=$(cat /proc/sys/kernel/random/uuid 2>/dev/null || uuidgen)
req=$(call -X POST "$base/requests" -H "Idempotency-Key: $key" -d "$draft")
id=$(jq -r .id <<<"$req")
echo "submitted $(jq -r .number <<<"$req") ($id)"

deadline=$((SECONDS + timeout))
status=$(jq -r .status <<<"$req")
while [ "$SECONDS" -lt "$deadline" ]; do
	status=$(call "$base/requests/$id" | jq -r .status)
	case "$status" in
	succeeded)
		echo "succeeded after ${SECONDS}s"
		exit 0
		;;
	failed | rejected | cancelled)
		break
		;;
	esac
	sleep 2
done

echo "request $id is $status" >&2
call "$base/requests/$id/steps" | jq -r '.items[] | "\(.status)\t\(.title)\t\(.error // "")"' >&2 || true
exit 1
