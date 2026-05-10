#!/bin/sh

set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
validator="$SCRIPT_DIR/validate-release-tag.sh"
beta_validator="$SCRIPT_DIR/validate-beta-increment.sh"

for tag in v0.1.0 v1.2.3 v0.1.0-beta.1 v1.2.3-beta.10; do
  "$validator" "$tag" >/dev/null
done

for tag in 0.1.0 v01.2.3 v1.02.3 v1.2.03 v1.2.3-beta.0 v1.2.3-beta v1.2.3-rc.1 v1.2.3+build; do
  if "$validator" "$tag" >/dev/null 2>&1; then
    printf 'FAIL invalid release tag accepted: %s\n' "$tag" >&2
    exit 1
  fi
done

"$beta_validator" v0.1.0-beta.2 v0.1.0-beta.1 v0.1.0-beta.2 >/dev/null
"$beta_validator" v0.1.1-beta.1 v0.1.0-beta.9 >/dev/null
if "$beta_validator" v0.1.0-beta.2 v0.1.0-beta.3 >/dev/null 2>&1; then
  printf 'FAIL non-incrementing beta tag accepted\n' >&2
  exit 1
fi

printf 'ok\n'
