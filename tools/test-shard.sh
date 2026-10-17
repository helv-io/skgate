#!/bin/bash
# Runs one shard of the test suite: usage  tools/test-shard.sh <shard 1..N> <N>
# internal/app holds most of the run time (each test starts its own app), so its tests are dealt round robin to the
# shards; every other package runs in shard 1 together with build and vet. All shards together run exactly what
# `go test ./...` runs.
set -euo pipefail
shard=${1:?shard}; total=${2:?total}
# A skipped test did not run; every shard fails on one (set SKGATE_ALLOW_SKIP=1 only on a machine with no browser).
gotest() { AGENT_CHECKS_ALLOW_SKIP=${SKGATE_ALLOW_SKIP:-} .agent-checks/bin/agent-checks no-skips --hide '^(=== |--- PASS|\s+--- PASS|PASS$|ok\s)' -- go test -v "$@"; }
if [ "$shard" = 1 ]; then
  go build ./...
  go vet ./...
  gotest $(go list ./... | grep -v '/internal/app$')
fi
tests=$(go test -list '.*' ./internal/app | grep -E '^(Test|Example)' | awk -v s="$shard" -v n="$total" '(NR - 1) % n == s - 1' | paste -sd'|')
[ -n "$tests" ] || { echo "no tests in shard $shard"; exit 0; }
gotest ./internal/app -run "^($tests)\$"
