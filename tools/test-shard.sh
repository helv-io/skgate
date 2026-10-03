#!/bin/bash
# Runs one shard of the test suite: usage  tools/test-shard.sh <shard 1..N> <N>
# internal/app holds most of the run time (each test starts its own app), so its tests are dealt round robin to the
# shards; every other package runs in shard 1 together with build and vet. All shards together run exactly what
# `go test ./...` runs.
set -euo pipefail
shard=${1:?shard}; total=${2:?total}
if [ "$shard" = 1 ]; then
  go build ./...
  go vet ./...
  go test $(go list ./... | grep -v '/internal/app$')
fi
tests=$(go test -list '.*' ./internal/app | grep -E '^(Test|Example)' | awk -v s="$shard" -v n="$total" '(NR - 1) % n == s - 1' | paste -sd'|')
[ -n "$tests" ] || { echo "no tests in shard $shard"; exit 0; }
go test ./internal/app -run "^($tests)\$"
