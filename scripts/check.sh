#!/bin/sh
# Runs every check: format, vet, tests, dashboard build, installer syntax.
set -eu
cd "$(dirname "$0")/.."
test -z "$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
(cd web && npm run build >/dev/null)
go vet ./...
GOOS=linux go vet ./...
go test ./...
sh -n install.sh
node npm/bin/oss-railway.js help >/dev/null
echo "all checks passed"
