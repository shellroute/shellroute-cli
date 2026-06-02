#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --dirty 2>/dev/null || echo dev)}"
echo "Building CLI (${VERSION})..."
go build -ldflags "-X github.com/shellroute/shellroute-cli/internal/cli.rawVersion=${VERSION}" -o shellroute ./cmd/shellroute
echo "Done."
