#!/bin/sh

set -eu

tag="${1:-}"
[ -n "$tag" ] || {
  printf '%s\n' "usage: validate-release-tag.sh <tag>" >&2
  exit 2
}

stable_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
beta_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-beta\.([1-9][0-9]*)$'

if printf '%s\n' "$tag" | grep -Eq "$stable_re"; then
  exit 0
fi
if printf '%s\n' "$tag" | grep -Eq "$beta_re"; then
  exit 0
fi

printf 'invalid release tag: %s\n' "$tag" >&2
exit 1
