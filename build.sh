#!/usr/bin/env bash
# Build the launcher for Linux and Windows. CGO stays off so the binaries are static.
set -euo pipefail
cd "$(dirname "$0")"
version=${1:-0.1.0}
mkdir -p dist
export CGO_ENABLED=0
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.Version=${version}" -o dist/modpack-launcher-linux-amd64 .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.Version=${version}" -o dist/modpack-launcher-windows-amd64.exe .
echo "built dist/modpack-launcher-linux-amd64"
echo "built dist/modpack-launcher-windows-amd64.exe"
