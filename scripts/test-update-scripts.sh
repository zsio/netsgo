#!/bin/sh

set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

bin="$tmp/bin"
mkdir -p "$bin"

cat > "$bin/uname" <<'SH'
#!/bin/sh
case "${1:-}" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) printf 'Linux\n' ;;
esac
SH

cat > "$bin/systemctl" <<'SH'
#!/bin/sh
case "${1:-}" in
  --version)
    printf 'systemd 255\n'
    exit 0
    ;;
  list-unit-files)
    if [ "${NETSGO_FAKE_SYSTEMD_UNITS:-0}" = "1" ]; then
      printf 'netsgo-server.service enabled enabled\n'
    fi
    exit 0
    ;;
esac
exit 0
SH

cat > "$bin/curl" <<'SH'
#!/bin/sh
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    -*)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done
[ -n "$out" ] || exit 2
printf '%s\n' "$url" >> "${NETSGO_FAKE_CURL_LOG:?}"
case "$url" in
  https://cnb.cool/zsio/netsgo/-/raw/release-index/updates/index-v1/latest.json)
    [ "${NETSGO_FAKE_CNB_INDEX_FAIL:-0}" = "1" ] && exit 1
    if [ "${NETSGO_FAKE_STABLE_HIGHER:-0}" = "1" ]; then
      stable_latest="v0.1.1"
      beta_latest="v0.1.1-beta.1"
    else
      stable_latest="v0.1.0"
      beta_latest="v0.1.1-beta.1"
    fi
    cat > "$out" <<JSON
{
  "schema": 1,
  "project": "netsgo",
  "channels": {
    "stable": { "latest": "${stable_latest}" },
    "beta": { "latest": "${beta_latest}" }
  }
}
JSON
    ;;
  https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/latest.json)
    if [ "${NETSGO_FAKE_STABLE_HIGHER:-0}" = "1" ]; then
      stable_latest="v0.1.1"
      beta_latest="v0.1.1-beta.1"
    else
      stable_latest="v0.1.0"
      beta_latest="v0.1.1-beta.1"
    fi
    cat > "$out" <<JSON
{
  "schema": 1,
  "project": "netsgo",
  "channels": {
    "stable": { "latest": "${stable_latest}" },
    "beta": { "latest": "${beta_latest}" }
  }
}
JSON
    ;;
  https://cnb.cool/zsio/netsgo/-/raw/release-index/updates/index-v1/releases/v0.1.0.json)
    [ "${NETSGO_FAKE_CNB_DETAIL_FAIL:-0}" = "1" ] && exit 1
    cat > "$out" <<'JSON'
{
  "schema": 1,
  "project": "netsgo",
  "version": "v0.1.0",
  "checksum_asset": {
    "name": "checksums.txt",
    "urls": [
      {"provider": "cnb", "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/checksums.txt"},
      {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt"}
    ]
  },
  "signature_assets": {
    "ed25519": {
      "name": "checksums.txt.sig",
      "urls": [
        {"provider": "cnb", "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/checksums.txt.sig"},
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt.sig"}
      ]
    },
    "sshsig": {
      "name": "checksums.txt.sshsig",
      "urls": [
        {"provider": "cnb", "url": "https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/checksums.txt.sshsig"},
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
    ;;
  https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.0.json)
    cat > "$out" <<'JSON'
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
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.0/netsgo_0.1.0_linux_amd64.tar.gz"}
      ]
    }
  ]
}
JSON
    ;;
  https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.1.json)
    cat > "$out" <<'JSON'
{
  "schema": 1,
  "project": "netsgo",
  "version": "v0.1.1",
  "checksum_asset": {
    "name": "checksums.txt",
    "urls": [
      {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1/checksums.txt"}
    ]
  },
  "signature_assets": {
    "ed25519": {
      "name": "checksums.txt.sig",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1/checksums.txt.sig"}
      ]
    },
    "sshsig": {
      "name": "checksums.txt.sshsig",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1/checksums.txt.sshsig"}
      ]
    }
  },
  "assets": [
    {
      "name": "netsgo_0.1.1_linux_amd64.tar.gz",
      "os": "linux",
      "arch": "amd64",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1/netsgo_0.1.1_linux_amd64.tar.gz"}
      ]
    }
  ]
}
JSON
    ;;
  https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.1-beta.1.json)
    cat > "$out" <<'JSON'
{
  "schema": 1,
  "project": "netsgo",
  "version": "v0.1.1-beta.1",
  "checksum_asset": {
    "name": "checksums.txt",
    "urls": [
      {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/checksums.txt"}
    ]
  },
  "signature_assets": {
    "ed25519": {
      "name": "checksums.txt.sig",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/checksums.txt.sig"}
      ]
    },
    "sshsig": {
      "name": "checksums.txt.sshsig",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/checksums.txt.sshsig"}
      ]
    }
  },
  "assets": [
    {
      "name": "netsgo_0.1.1-beta.1_linux_amd64.tar.gz",
      "os": "linux",
      "arch": "amd64",
      "urls": [
        {"provider": "github", "url": "https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/netsgo_0.1.1-beta.1_linux_amd64.tar.gz"}
      ]
    }
  ]
}
JSON
    ;;
  https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/checksums.txt|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1/checksums.txt|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/checksums.txt)
    if [ "${NETSGO_FAKE_BAD_CHECKSUM:-0}" = "1" ]; then
      printf 'bad999  netsgo_0.1.0_linux_amd64.tar.gz\n' > "$out"
    else
      case "$url" in
        *v0.1.1-beta.1*) printf 'abc123  netsgo_0.1.1-beta.1_linux_amd64.tar.gz\n' > "$out" ;;
        *v0.1.1*) printf 'abc123  netsgo_0.1.1_linux_amd64.tar.gz\n' > "$out" ;;
        *) printf 'abc123  netsgo_0.1.0_linux_amd64.tar.gz\n' > "$out" ;;
      esac
    fi
    ;;
  https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt)
    if [ "${NETSGO_FAKE_BAD_CHECKSUM:-0}" = "1" ]; then
      printf 'bad999  netsgo_0.1.0_linux_amd64.tar.gz\n' > "$out"
    else
      printf 'abc123  netsgo_0.1.0_linux_amd64.tar.gz\n' > "$out"
    fi
    ;;
  https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/checksums.txt.sig|\
  https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/checksums.txt.sshsig|\
  https://cnb.cool/zsio/netsgo/-/releases/download/v0.1.0/netsgo_0.1.0_linux_amd64.tar.gz|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1/checksums.txt.sig|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1/checksums.txt.sshsig|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1/netsgo_0.1.1_linux_amd64.tar.gz|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/checksums.txt.sig|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/checksums.txt.sshsig|\
  https://github.com/zsio/netsgo/releases/download/v0.1.1-beta.1/netsgo_0.1.1-beta.1_linux_amd64.tar.gz)
    printf 'fake\n' > "$out"
    ;;
  https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt.sig|\
  https://github.com/zsio/netsgo/releases/download/v0.1.0/checksums.txt.sshsig|\
  https://github.com/zsio/netsgo/releases/download/v0.1.0/netsgo_0.1.0_linux_amd64.tar.gz)
    printf 'fake\n' > "$out"
    ;;
  *)
    printf 'unexpected url: %s\n' "$url" >&2
    exit 1
    ;;
esac
SH

cat > "$bin/sha256sum" <<'SH'
#!/bin/sh
printf 'abc123  %s\n' "$1"
SH

cat > "$bin/openssl" <<'SH'
#!/bin/sh
exit 0
SH

cat > "$bin/ssh-keygen" <<'SH'
#!/bin/sh
exit 127
SH

cat > "$bin/tar" <<'SH'
#!/bin/sh
dest=""
archive=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-C" ]; then
    dest="$2"
    shift 2
  elif [ -f "$1" ]; then
    archive="$1"
    shift
  else
    shift
  fi
done
[ -n "$dest" ] || exit 2
version="v0.1.0"
case "$archive" in
  *0.1.1-beta.1*) version="v0.1.1-beta.1" ;;
  *0.1.1*) version="v0.1.1" ;;
esac
[ "${NETSGO_FAKE_VERSION_MISMATCH:-0}" = "1" ] && version="v9.9.9"
cat > "$dest/netsgo" <<'NETSGO'
#!/bin/sh
case "${1:-}" in
  --version)
    printf 'netsgo version __VERSION__\n'
    ;;
  install)
    printf 'install\n' >> "${NETSGO_FAKE_EXEC_LOG:?}"
    ;;
  upgrade)
    printf 'upgrade %s %s\n' "${2:-}" "${3:-}" >> "${NETSGO_FAKE_EXEC_LOG:?}"
    ;;
esac
NETSGO
sed "s/__VERSION__/$version/g" "$dest/netsgo" > "$dest/netsgo.tmp"
mv "$dest/netsgo.tmp" "$dest/netsgo"
chmod +x "$dest/netsgo"
SH

chmod +x "$bin"/*

curl_log="$tmp/curl.log"
exec_log="$tmp/exec.log"
touch "$curl_log" "$exec_log"

PATH="$bin:$PATH" \
NETSGO_FAKE_CURL_LOG="$curl_log" \
NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/install.sh" --source github --channel stable

if ! grep -qx 'install' "$exec_log"; then
  printf 'FAIL install.sh did not execute temporary netsgo install\n' >&2
  exit 1
fi

if ! grep -q 'releases/v0.1.0.json' "$curl_log"; then
  printf 'FAIL install.sh did not fetch release detail\n' >&2
  exit 1
fi
for name in checksums.txt checksums.txt.sig netsgo_0.1.0_linux_amd64.tar.gz; do
  if ! grep -q "https://github.com/zsio/netsgo/releases/download/v0.1.0/${name}" "$curl_log"; then
    printf 'FAIL install.sh did not download %s from release detail URL\n' "$name" >&2
    exit 1
  fi
done

PATH="$bin:$PATH" \
NETSGO_FAKE_CURL_LOG="$curl_log" \
NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/install.sh" --source github --channel beta

if ! grep -q 'releases/v0.1.1-beta.1.json' "$curl_log"; then
  printf 'FAIL install.sh --channel beta did not fetch beta release detail\n' >&2
  exit 1
fi

cat > "$bin/netsgo" <<'SH'
#!/bin/sh
case "${1:-}" in
  --version)
    printf 'netsgo version v0.0.9\n'
    ;;
esac
SH
chmod +x "$bin/netsgo"
PATH="$bin:$PATH" \
NETSGO_FAKE_SYSTEMD_UNITS=1 \
NETSGO_INSTALLED_BIN="$bin/netsgo" \
NETSGO_FAKE_CURL_LOG="$curl_log" \
NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/upgrade.sh" --source github --channel stable -y

if ! grep -qx 'upgrade -y ' "$exec_log"; then
  printf 'FAIL upgrade.sh did not execute temporary netsgo upgrade -y\n' >&2
  exit 1
fi

cat > "$bin/netsgo" <<'SH'
#!/bin/sh
case "${1:-}" in
  --version)
    printf 'netsgo version v0.1.1-beta.1\n'
    ;;
esac
SH
chmod +x "$bin/netsgo"
PATH="$bin:$PATH" \
NETSGO_FAKE_SYSTEMD_UNITS=1 \
NETSGO_INSTALLED_BIN="$bin/netsgo" \
NETSGO_FAKE_STABLE_HIGHER=1 \
NETSGO_FAKE_CURL_LOG="$curl_log" \
NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/upgrade.sh" --source github --channel auto -y

if ! grep -q 'releases/v0.1.1.json' "$curl_log"; then
  printf 'FAIL upgrade.sh --channel auto did not choose stable when it is highest for beta current\n' >&2
  exit 1
fi

PATH="$bin:$PATH" \
NETSGO_FAKE_SYSTEMD_UNITS=1 \
NETSGO_INSTALLED_BIN="$bin/netsgo" \
NETSGO_FAKE_CURL_LOG="$curl_log" \
NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/upgrade.sh" --source github --channel auto -f -y

if ! grep -q 'releases/v0.1.1-beta.1.json' "$curl_log"; then
  printf 'FAIL upgrade.sh --channel auto did not choose beta when it is highest for beta current\n' >&2
  exit 1
fi

cat > "$bin/netsgo" <<'SH'
#!/bin/sh
case "${1:-}" in
  --version)
    printf 'netsgo version v0.0.9\n'
    ;;
esac
SH
chmod +x "$bin/netsgo"
PATH="$bin:$PATH" \
NETSGO_FAKE_SYSTEMD_UNITS=1 \
NETSGO_INSTALLED_BIN="$bin/netsgo" \
NETSGO_FAKE_CNB_INDEX_FAIL=1 \
NETSGO_FAKE_CNB_DETAIL_FAIL=1 \
NETSGO_FAKE_CURL_LOG="$curl_log" \
NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/upgrade.sh" --source auto --channel stable -f -y

if ! grep -qx 'upgrade -f -y' "$exec_log"; then
  printf 'FAIL upgrade.sh did not pass through -f -y\n' >&2
  exit 1
fi
if ! grep -q 'cnb.cool.*/latest.json' "$curl_log" || ! grep -q 'raw.githubusercontent.com.*/latest.json' "$curl_log"; then
  printf 'FAIL upgrade.sh --source auto did not fall back from CNB to GitHub latest index\n' >&2
  exit 1
fi

if PATH="$bin:$PATH" \
  NETSGO_FAKE_SYSTEMD_UNITS=1 \
  NETSGO_INSTALLED_BIN="$bin/netsgo" \
    NETSGO_FAKE_BAD_CHECKSUM=1 \
  NETSGO_FAKE_CURL_LOG="$curl_log" \
  NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/upgrade.sh" --source github --channel stable -f -y >/dev/null 2>&1; then
  printf 'FAIL upgrade.sh accepted checksum mismatch\n' >&2
  exit 1
fi

if PATH="$bin:$PATH" \
    NETSGO_FAKE_VERSION_MISMATCH=1 \
  NETSGO_FAKE_CURL_LOG="$curl_log" \
  NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/install.sh" --source github --channel stable >/dev/null 2>&1; then
  printf 'FAIL install.sh accepted extracted binary version mismatch\n' >&2
  exit 1
fi

before_lines="$(wc -l < "$curl_log" | tr -d ' ')"
if PATH="$bin:$PATH" \
    NETSGO_FAKE_CURL_LOG="$curl_log" \
  NETSGO_FAKE_EXEC_LOG="$exec_log" \
  "$ROOT/scripts/upgrade.sh" --source github --channel stable >/dev/null 2>&1; then
  printf 'FAIL upgrade.sh succeeded without managed units\n' >&2
  exit 1
fi
after_lines="$(wc -l < "$curl_log" | tr -d ' ')"
if [ "$before_lines" != "$after_lines" ]; then
  printf 'FAIL upgrade.sh downloaded before detecting missing managed units\n' >&2
  exit 1
fi

printf 'ok\n'
