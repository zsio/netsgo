#!/bin/sh

set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
SCRIPT="$ROOT/scripts/common-update.sh"
cd "$ROOT"

tmp="$(mktemp)"
block="$(mktemp)"
cleanup() {
  rm -f "$tmp" "$block"
}
trap cleanup EXIT

go run ./cmd/netsgo-release-sign public --shell > "$block"

awk -v block="$block" '
  BEGIN {
    while ((getline line < block) > 0) {
      replacement = replacement line "\n"
    }
    close(block)
  }
  /^# BEGIN NETSGO RELEASE PUBLIC KEYS$/ {
    printf "%s", replacement
    skipping = 1
    next
  }
  /^# END NETSGO RELEASE PUBLIC KEYS$/ {
    skipping = 0
    next
  }
  !skipping { print }
' "$SCRIPT" > "$tmp"

mv "$tmp" "$SCRIPT"
go run ./cmd/netsgo-release-sign verify-embedded "$SCRIPT"
