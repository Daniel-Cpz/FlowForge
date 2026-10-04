#!/bin/sh
set -eu
echo 'gofmt check'
unformatted=$(gofmt -l cmd internal migrations tests scripts/validate-phase-state)
if [ -n "$unformatted" ]; then
    echo "$unformatted"
    exit 1
fi
echo 'phase state validation'
go run ./scripts/validate-phase-state
echo 'go vet ./...'
go vet ./...
echo 'go test ./...'
go test -count=1 ./...
echo 'go build ./...'
go build ./...
