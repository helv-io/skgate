#!/bin/bash
# build + vet + test the staged tree, with the browser tests ON (CI runs them). Fails when anything fails or any test is skipped.
# A skipped browser test is a test that did not run: this used to pass locally and fail in CI. Set SKGATE_ALLOW_SKIP=1 only on a machine with no browser.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
if [ "${SKGATE_ALLOW_SKIP:-}" != 1 ]; then eval "$("$here/tools/browser-env.sh")"; fi
export GOFLAGS="${GOFLAGS:-} -buildvcs=false"  # the staged copy has no .git
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
git checkout-index -a --prefix="$T/"
cd "$T"
go build ./...
go vet ./...
AGENT_CHECKS_ALLOW_SKIP=${SKGATE_ALLOW_SKIP:-} .agent-checks/bin/agent-checks no-skips --hide '^(=== |--- PASS|\s+--- PASS|PASS$|ok\s)' -- go test -v ./...
