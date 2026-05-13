#!/usr/bin/env bash
set -euo pipefail

target_triple="${1:-}"
if [ -z "$target_triple" ]; then
  if target_triple="$(rustc --print host-tuple 2>/dev/null)" && [ -n "$target_triple" ]; then
    :
  else
    target_triple="$(rustc -vV 2>/dev/null | sed -n 's/^host: //p')"
  fi
fi

if [ -z "$target_triple" ]; then
  echo "Unable to determine Rust target triple. Install rustc or pass a target triple explicitly." >&2
  exit 1
fi

goos=""
goarch=""
goarm=""
ext=""
case "$target_triple" in
  x86_64-apple-darwin)
    goos="darwin"; goarch="amd64" ;;
  aarch64-apple-darwin)
    goos="darwin"; goarch="arm64" ;;
  x86_64-pc-windows-msvc|x86_64-pc-windows-gnu)
    goos="windows"; goarch="amd64"; ext=".exe" ;;
  aarch64-pc-windows-msvc|aarch64-pc-windows-gnu)
    goos="windows"; goarch="arm64"; ext=".exe" ;;
  x86_64-unknown-linux-gnu|x86_64-unknown-linux-musl)
    goos="linux"; goarch="amd64" ;;
  aarch64-unknown-linux-gnu|aarch64-unknown-linux-musl)
    goos="linux"; goarch="arm64" ;;
  armv7-unknown-linux-gnueabihf|armv7-unknown-linux-musleabihf)
    goos="linux"; goarch="arm"; goarm="7" ;;
  *)
    echo "Unsupported desktop target triple: $target_triple" >&2
    exit 1
    ;;
esac

version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
commit="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
date="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
ldflags="${LDFLAGS:--s -w -X netsgo/pkg/version.Current=${version} -X netsgo/pkg/version.Commit=${commit} -X netsgo/pkg/version.Date=${date}}"

out="desktop/src-tauri/binaries/netsgo-${target_triple}${ext}"
mkdir -p "$(dirname "$out")"

echo "🔨 Building desktop sidecar: ${target_triple} (${goos}/${goarch}${goarm:+/v${goarm}})"
env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" ${goarm:+GOARM="$goarm"} \
  go build -tags dev -trimpath -ldflags "$ldflags" -o "$out" ./cmd/netsgo/

if [ "$goos" != "windows" ]; then
  chmod +x "$out"
fi

echo "✅ Sidecar generated: $out"
