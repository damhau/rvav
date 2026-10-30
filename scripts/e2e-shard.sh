#!/usr/bin/env bash
# One shard of the end-to-end tests: the tests of internal/e2e in name order,
# every TOTAL-th from SHARD (1-based), so the shards together run them all,
# once. CI runs the shards in parallel jobs.
set -euo pipefail
shard=${1:?usage: e2e-shard.sh SHARD TOTAL}
total=${2:?usage: e2e-shard.sh SHARD TOTAL}
go=${GO:-go}
# Listing runs TestMain, which starts PostgreSQL: should that fail, say why
# (go test prints the binary's errors on stdout).
if ! list=$($go test ./internal/e2e/ -list '^Test' 2>&1); then
	echo "$list"
	exit 1
fi
tests=$(grep '^Test' <<<"$list" | sort)
mine=$(awk -v s="$shard" -v t="$total" '(NR-1) % t == s-1' <<<"$tests" | paste -sd'|' -)
if [ -z "$mine" ]; then
	echo "shard $shard of $total: no tests"
	exit 0
fi
echo "shard $shard of $total: $(tr '|' '\n' <<<"$mine" | wc -l) of $(wc -l <<<"$tests") tests"
exec $go test ./internal/e2e/ -count=1 -v -run "^($mine)\$"
