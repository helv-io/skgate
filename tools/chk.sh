#!/bin/bash
# build + vet + test the staged tree; fails when any of them fails
set -euo pipefail
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
git checkout-index -a --prefix="$T/"
cd "$T"
go build ./...
go vet ./...
go test ./...
