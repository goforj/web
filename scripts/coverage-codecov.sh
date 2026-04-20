#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

OUTPUT_FILE="${COVERAGE_OUTPUT:-coverage.txt}"
TMP_ROOT="${COVERAGE_TMP_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/web-coverage.XXXXXX")}"
DEFAULT_GOCACHE="$(go env GOCACHE)"
DEFAULT_GOMODCACHE="$(go env GOMODCACHE)"
GOCACHE_DIR="${GOCACHE_DIR:-$DEFAULT_GOCACHE}"
GOMODCACHE_DIR="${GOMODCACHE_DIR:-$DEFAULT_GOMODCACHE}"

ROOT_COVER_DIR="$TMP_ROOT/root"
MERGED_DIR="$TMP_ROOT/merged"

rm -rf "$TMP_ROOT"
mkdir -p "$ROOT_COVER_DIR" "$MERGED_DIR" "$GOCACHE_DIR" "$GOMODCACHE_DIR"

echo "==> Root module coverage"
GOCACHE="$GOCACHE_DIR" GOMODCACHE="$GOMODCACHE_DIR" \
go test -cover ./... -args -test.gocoverdir="$ROOT_COVER_DIR"

echo "==> Merge coverage"
go tool covdata merge -i="$ROOT_COVER_DIR" -o="$MERGED_DIR"

mkdir -p "$(dirname "$OUTPUT_FILE")"
go tool covdata textfmt -i="$MERGED_DIR" -o="$OUTPUT_FILE"

echo "==> Combined coverage written to $OUTPUT_FILE"
go tool covdata percent -i="$MERGED_DIR"
