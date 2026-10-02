#!/bin/bash
# build + vet + test the staged tree
set -e
T=$(mktemp -d)
git checkout-index -a --prefix=$T/ 
cd $T && go build ./... && go vet ./... && go test ./... 2>&1 | grep -v "no test files" | grep -v "^ok" || true
rm -rf $T
