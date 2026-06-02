#!/usr/bin/env bash
set -uo pipefail

cd "$(dirname "$0")/.."

failures=()
logdir=$(mktemp -d)
trap 'rm -rf "$logdir"' EXIT

run_stage() {
    local name="$1"
    shift
    local safename="${name// /_}"
    safename="${safename////-}"
    local log="$logdir/${safename}.log"
    echo "=== $name ==="
    if "$@" 2>&1 | tee "$log"; then
        echo "--- $name: PASS ---"
    else
        echo "--- $name: FAIL ---"
        failures+=("$name|$log")
    fi
    echo ""
}

run_stage "Go vet" go vet ./...

run_stage "Go fmt" bash -c 'test -z "$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)'

run_stage "Go build" go build ./cmd/shellroute

run_stage "Go unit tests" go test -race ./...

run_stage "Public audit" bash scripts/audit-public.sh

# Cross-compile check — catch platform-specific build failures
run_stage "Cross-compile darwin/arm64" bash -c 'GOOS=darwin GOARCH=arm64 go build ./cmd/shellroute'
run_stage "Cross-compile darwin/amd64" bash -c 'GOOS=darwin GOARCH=amd64 go build ./cmd/shellroute'
run_stage "Cross-compile linux/arm64" bash -c 'GOOS=linux GOARCH=arm64 go build ./cmd/shellroute'
run_stage "Cross-compile linux/amd64" bash -c 'GOOS=linux GOARCH=amd64 go build ./cmd/shellroute'

echo "=============================="
if [ ${#failures[@]} -eq 0 ]; then
    echo "=== All tests passed ==="
else
    echo "=== ${#failures[@]} stage(s) FAILED ==="
    for entry in "${failures[@]}"; do
        name="${entry%%|*}"
        log="${entry##*|}"
        echo ""
        echo "  ✗ $name"
        grep -E '(--- FAIL:|FAIL\t|_test\.go:[0-9]+:|error|FAIL:)' "$log" 2>/dev/null | sed 's/^/    /'
    done
    exit 1
fi
