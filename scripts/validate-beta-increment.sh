#!/bin/sh

set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
tag="${1:-}"
[ -n "$tag" ] || {
  printf '%s\n' "usage: validate-beta-increment.sh <tag> [existing-tags...]" >&2
  exit 2
}
shift || true

"$SCRIPT_DIR/validate-release-tag.sh" "$tag"

case "$tag" in
  *-beta.*) ;;
  *) exit 0 ;;
esac

base="${tag%.*}."
current="${tag##*.}"

if [ "$#" -gt 0 ]; then
  existing_tags="$(printf '%s\n' "$@")"
else
  existing_tags="$(git tag -l "${base}*" 2>/dev/null || true)"
fi

previous="$(printf '%s\n' "$existing_tags" |
  grep -Fvx "$tag" |
  sed -n "s/^$(printf '%s' "$base" | sed 's/[.[\*^$()+?{}|\\]/\\&/g')\([1-9][0-9]*\)$/\1/p" |
  sort -n |
  tail -1)"

if [ -n "$previous" ] && [ "$current" -le "$previous" ]; then
  printf 'beta.N must increase. current=%s, previous=%s\n' "$current" "$previous" >&2
  exit 1
fi
