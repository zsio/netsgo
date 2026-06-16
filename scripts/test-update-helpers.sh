#!/bin/sh

set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  haystack="$1"
  needle="$2"
  case "$haystack" in
    *"$needle"*) ;;
    *) fail "expected output to contain: $needle; actual: $haystack" ;;
  esac
}

stat_mode_text_local() {
  stat -c '%A' "$1" 2>/dev/null || stat -f '%Sp' "$1"
}

helpers_without_shebang() {
  # Keep the test process in control: load helper functions but not set -eu.
  sed '1,3d' "$ROOT/scripts/common-update.sh"
}

run_helper() {
  code="$1"
  sh -s <<EOF
$(helpers_without_shebang)
$code
EOF
}

safe_root="$(mktemp -d)"
trap 'rm -rf "$safe_root"' EXIT

safe_cache="$safe_root/cache"
NETSGO_UPDATE_CACHE_DIR="$safe_cache" run_helper '
  dir="$(cache_dir_for v1.2.3 linux_amd64)"
  [ "$dir" = "$NETSGO_UPDATE_CACHE_DIR/v1.2.3/linux_amd64" ] || die "unexpected cache dir: $dir"
  [ -d "$NETSGO_UPDATE_CACHE_DIR" ] || die "override cache root was not created"
  mode="$(stat_mode_text "$NETSGO_UPDATE_CACHE_DIR")"
  case "$mode" in ?????w*|????????w*) die "override cache root stayed writable: $mode" ;; esac
' || fail "safe NETSGO_UPDATE_CACHE_DIR should pass"

unsafe_cache="$safe_root/world-writable-cache"
mkdir -p "$unsafe_cache"
chmod 0777 "$unsafe_cache"
if output="$(NETSGO_UPDATE_CACHE_DIR="$unsafe_cache" run_helper 'cache_dir_for v1.2.3 linux_amd64' 2>&1)"; then
  fail "world-writable NETSGO_UPDATE_CACHE_DIR should be rejected"
fi
assert_contains "$output" "不得 group/world 可写"

symlink_target="$safe_root/symlink-target"
mkdir -p "$symlink_target"
symlink_cache="$safe_root/symlink-cache"
ln -s "$symlink_target" "$symlink_cache"
if output="$(NETSGO_UPDATE_CACHE_DIR="$symlink_cache" run_helper 'cache_dir_for v1.2.3 linux_amd64' 2>&1)"; then
  fail "symlink NETSGO_UPDATE_CACHE_DIR should be rejected"
fi
assert_contains "$output" "符号链接更新缓存路径"

unset NETSGO_UPDATE_CACHE_DIR

# Default cache roots must be private mktemp directories, not a predictable /tmp/netsgo-update-cache tree.
default_output="$(TMPDIR="$safe_root" run_helper 'cache_dir_for v1.2.3 linux_amd64')"
case "$default_output" in
  "$safe_root"/netsgo-update-cache.*'/v1.2.3/linux_amd64') ;;
  *) fail "default cache dir should use private mktemp root, got: $default_output" ;;
esac
default_root="${default_output%/v1.2.3/linux_amd64}"
[ -d "$default_root" ] || fail "default private cache root missing"
mode="$(stat_mode_text_local "$default_root")"
case "$mode" in ?????w*|????????w*) fail "default private cache root is writable by group/world: $mode" ;; esac
case "$default_root" in "$safe_root/netsgo-update-cache") fail "default cache root is still predictable" ;; esac

printf 'PASS: update helper cache hardening\n'
