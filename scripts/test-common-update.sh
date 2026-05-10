#!/bin/sh

set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
. "$SCRIPT_DIR/common-update.sh"

assert_eq() {
  got="$1"
  want="$2"
  name="$3"
  if [ "$got" != "$want" ]; then
    printf 'FAIL %s: got %s want %s\n' "$name" "$got" "$want" >&2
    exit 1
  fi
}

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
cat > "$tmp" <<'JSON'
{
  "schema": 1,
  "project": "netsgo",
  "channels": {
    "stable": { "latest": "v0.2.0" },
    "beta": { "latest": "v0.2.1-beta.1" }
  }
}
JSON

assert_eq "$(json_get_channel_latest "$tmp" stable)" "v0.2.0" "stable latest"
assert_eq "$(json_get_channel_latest "$tmp" beta)" "v0.2.1-beta.1" "beta latest"
assert_eq "$(select_highest_version v0.2.0 v0.2.1-beta.1)" "v0.2.1-beta.1" "highest beta newer"
assert_eq "$(select_highest_version v0.1.0 v0.1.0-beta.6)" "v0.1.0" "stable beats prerelease"
assert_eq "$(channel_for_target v0.1.0)" "stable" "stable channel"
assert_eq "$(channel_for_target v0.1.0-beta.1)" "beta" "beta channel"
(
  uname() {
    case "${1:-}" in
      -s) printf 'Linux\n' ;;
      -m) printf 'armv7l\n' ;;
      *) printf 'Linux\n' ;;
    esac
  }
  assert_eq "$(canonical_platform)" "linux_armv7" "canonical linux armv7"
)
assert_eq "$(extract_comparable_version 'netsgo version v0.1.0-3-gabc123-dirty')" "v0.1.0" "describe stable base"
assert_eq "$(extract_comparable_version 'netsgo version v0.1.0-beta.5-3-gabc123')" "v0.1.0-beta.5" "describe beta base"
assert_eq "$(extract_exact_release_version 'netsgo version v0.1.0 (abcdef1, 2026-05-10)')" "v0.1.0" "exact release version"
if extract_exact_release_version "netsgo version v0.1.0-3-gabc123" >/dev/null 2>&1; then
  printf 'FAIL git describe accepted as exact release version\n' >&2
  exit 1
fi
if valid_release_tag "v1.2.3abc"; then
  printf 'FAIL invalid stable suffix accepted\n' >&2
  exit 1
fi
if valid_release_tag "v1.2.3-beta.0"; then
  printf 'FAIL beta.0 accepted\n' >&2
  exit 1
fi
if extract_comparable_version "netsgo version v1.2.3abc" >/dev/null 2>&1; then
  printf 'FAIL invalid comparable version accepted\n' >&2
  exit 1
fi
assert_eq "$(select_highest_version v0.2.0 v9.9.9oops v0.2.1-beta.1)" "v0.2.1-beta.1" "highest skips invalid"
if semver_gt v0.1.0 v0.1.0-beta.9; then :; else
  printf 'FAIL semver stable should beat prerelease\n' >&2
  exit 1
fi
if semver_gt v0.1.0 v0.2.0; then
  printf 'FAIL semver lower stable reported greater\n' >&2
  exit 1
fi
printf 'abc123  netsgo_0.1.0_linux_amd64.tar.gz\n' > "$tmp"
bad_archive="$(mktemp)"
printf 'different\n' > "$bad_archive"
if (verify_checksum "$tmp" "$bad_archive" "netsgo_0.1.0_linux_amd64.tar.gz") >/dev/null 2>&1; then
  printf 'FAIL checksum mismatch accepted\n' >&2
  exit 1
fi
rm -f "$bad_archive"

if official_url_allowed "http://127.0.0.1/latest.json"; then
  printf 'FAIL allowlist rejected localhost\n' >&2
  exit 1
fi
if ! official_url_allowed "https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt"; then
  printf 'FAIL allowlist official github release\n' >&2
  exit 1
fi
if ! official_url_allowed "https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.0.json"; then
  printf 'FAIL allowlist official github release detail\n' >&2
  exit 1
fi
if [ "$(release_detail_url github v0.1.0)" != "https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.0.json" ]; then
  printf 'FAIL github release detail url\n' >&2
  exit 1
fi
if [ "$(source_order github | tr '\n' ',' | sed 's/,$//')" != "github,cnb" ]; then
  printf 'FAIL github source order\n' >&2
  exit 1
fi
if [ "$(source_order cnb | tr '\n' ',' | sed 's/,$//')" != "cnb,github" ]; then
  printf 'FAIL cnb source order\n' >&2
  exit 1
fi
cat > "$tmp" <<'JSON'
{
  "schema": 1,
  "project": "netsgo",
  "version": "v0.1.0",
  "checksum_asset": {
    "name": "checksums.txt",
    "urls": [
      {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt"}
    ]
  },
  "signature_assets": {
    "ed25519": {
      "name": "checksums.txt.sig",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt.sig"}
      ]
    },
    "sshsig": {
      "name": "checksums.txt.sshsig",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt.sshsig"}
      ]
    }
  },
  "assets": [
    {
      "name": "netsgo_0.1.0_linux_amd64.tar.gz",
      "os": "linux",
      "arch": "amd64",
      "urls": [
        {"provider": "cnb", "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/netsgo_0.1.0_linux_amd64.tar.gz"},
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/netsgo_0.1.0_linux_amd64.tar.gz"}
      ]
    }
  ]
}
JSON
validate_release_detail "$tmp" "v0.1.0" "netsgo_0.1.0_linux_amd64.tar.gz"
if (validate_release_detail "$tmp" "v0.1.0" "netsgo_0.1.0_linux_arm64.tar.gz") >/dev/null 2>&1; then
  printf 'FAIL release detail missing asset accepted\n' >&2
  exit 1
fi
assert_eq "$(json_url_for_name_provider "$tmp" "netsgo_0.1.0_linux_amd64.tar.gz" cnb)" "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/netsgo_0.1.0_linux_amd64.tar.gz" "detail cnb asset url"
assert_eq "$(json_url_for_name_provider "$tmp" "checksums.txt" github)" "https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt" "detail checksum url"
boundary_tmp="$(mktemp)"
cat > "$boundary_tmp" <<'JSON'
{
  "assets": [
    {
      "name": "netsgo_0.1.0_linux_amd64.tar.gz",
      "os": "linux",
      "arch": "amd64",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/netsgo_0.1.0_linux_amd64.tar.gz"}
      ]
    },
    {
      "name": "netsgo_0.1.0_linux_arm64.tar.gz",
      "os": "linux",
      "arch": "arm64",
      "urls": [
        {"provider": "cnb", "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/netsgo_0.1.0_linux_arm64.tar.gz"},
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/netsgo_0.1.0_linux_arm64.tar.gz"}
      ]
    }
  ]
}
JSON
if json_url_for_name_provider "$boundary_tmp" "netsgo_0.1.0_linux_amd64.tar.gz" cnb | grep -q .; then
  printf 'FAIL detail parser crossed into another asset URL\n' >&2
  exit 1
fi
rm -f "$boundary_tmp"

sig_tmp="$(mktemp -d)"
sig_bin="$sig_tmp/bin"
mkdir -p "$sig_bin"
cat > "$sig_bin/openssl" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$sig_bin/curl" <<'SH'
#!/bin/sh
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
printf '%s\n' "$url" >> "${NETSGO_FAKE_CURL_LOG:?}"
printf 'sig\n' > "$out"
SH
chmod +x "$sig_bin"/*
for tool in awk head grep sed sort jq; do
  tool_path="$(command -v "$tool")"
  ln -s "$tool_path" "$sig_bin/$tool"
done
sig_log="$sig_tmp/curl.log"
touch "$sig_log"
(
  PATH="$sig_bin"
  export PATH
  NETSGO_FAKE_CURL_LOG="$sig_log"
  export NETSGO_FAKE_CURL_LOG
  download_available_signatures "$tmp" github "$sig_tmp/checksums.txt.sig" "$sig_tmp/checksums.txt.sshsig"
)
if ! grep -q 'checksums.txt.sig' "$sig_log"; then
  printf 'FAIL available openssl signature not downloaded\n' >&2
  exit 1
fi
if grep -q 'checksums.txt.sshsig' "$sig_log"; then
  printf 'FAIL unavailable sshsig verifier still downloaded sshsig\n' >&2
  exit 1
fi
rm -rf "$sig_tmp"

sig_tmp="$(mktemp -d)"
sig_bin="$sig_tmp/bin"
mkdir -p "$sig_bin"
cat > "$sig_bin/openssl" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$sig_bin/ssh-keygen" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$sig_bin/curl" <<'SH'
#!/bin/sh
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
printf '%s\n' "$url" >> "${NETSGO_FAKE_CURL_LOG:?}"
case "$url" in
  *checksums.txt.sig) exit 1 ;;
  *checksums.txt.sshsig) printf 'sshsig\n' > "$out" ;;
  *) exit 1 ;;
esac
SH
chmod +x "$sig_bin"/*
for tool in awk head grep sed sort jq; do
  tool_path="$(command -v "$tool")"
  ln -s "$tool_path" "$sig_bin/$tool"
done
sig_log="$sig_tmp/curl.log"
touch "$sig_log"
(
  PATH="$sig_bin"
  export PATH
  NETSGO_FAKE_CURL_LOG="$sig_log"
  export NETSGO_FAKE_CURL_LOG
  download_available_signatures "$tmp" github "$sig_tmp/checksums.txt.sig" "$sig_tmp/checksums.txt.sshsig"
)
if ! grep -q 'checksums.txt.sshsig' "$sig_log"; then
  printf 'FAIL sshsig fallback signature not downloaded\n' >&2
  exit 1
fi
rm -rf "$sig_tmp"

no_sig_tmp="$(mktemp -d)"
if (
  PATH="$no_sig_tmp"
  export PATH
  download_available_signatures "$tmp" github "$no_sig_tmp/checksums.txt.sig" "$no_sig_tmp/checksums.txt.sshsig"
) >/dev/null 2>&1; then
  printf 'FAIL unavailable verifiers accepted\n' >&2
  exit 1
fi
rm -rf "$no_sig_tmp"

if (
  NETSGO_RELEASE_PUBLIC_KEY_PEM=""
  NETSGO_RELEASE_ALLOWED_SIGNERS=""
  export NETSGO_RELEASE_PUBLIC_KEY_PEM NETSGO_RELEASE_ALLOWED_SIGNERS
  verify_signature "$tmp" "$tmp" "$tmp"
) >/dev/null 2>&1; then
  printf 'FAIL unsigned release accepted\n' >&2
  exit 1
fi

if command -v openssl >/dev/null 2>&1; then
  sigdir="$(mktemp -d)"
  trap 'rm -f "$tmp"; rm -rf "$sigdir"' EXIT
  openssl genpkey -algorithm Ed25519 -out "$sigdir/private.pem" >/dev/null 2>&1
  openssl pkey -in "$sigdir/private.pem" -pubout -out "$sigdir/public.pem" >/dev/null 2>&1
  printf 'abc123  netsgo_0.1.0_linux_amd64.tar.gz\n' > "$sigdir/checksums.txt"
  openssl pkeyutl -sign -inkey "$sigdir/private.pem" -rawin -in "$sigdir/checksums.txt" -out "$sigdir/checksums.txt.sig" >/dev/null 2>&1
  NETSGO_RELEASE_PUBLIC_KEY_PEM="$(cat "$sigdir/public.pem")"
  export NETSGO_RELEASE_PUBLIC_KEY_PEM
  if ! verify_signature_openssl "$sigdir/checksums.txt" "$sigdir/checksums.txt.sig"; then
    printf 'FAIL openssl signature verification\n' >&2
    exit 1
  fi
  printf 'changed\n' > "$sigdir/checksums.txt"
  if verify_signature_openssl "$sigdir/checksums.txt" "$sigdir/checksums.txt.sig"; then
    printf 'FAIL openssl signature accepted changed input\n' >&2
    exit 1
  fi
fi

printf 'ok\n'
