#!/usr/bin/env bash
set -euo pipefail

app_path="${1:-}"
dmg_path="${2:-}"

if [ -z "$app_path" ] || [ -z "$dmg_path" ]; then
  echo "Usage: scripts/package-macos-dmg.sh /path/to/NetsGo.app /path/to/NetsGo.dmg" >&2
  exit 2
fi

if [ "$(uname -s)" != "Darwin" ]; then
  echo "macOS DMG packaging requires macOS." >&2
  exit 1
fi

if [ ! -d "$app_path" ]; then
  echo "macOS app not found: $app_path" >&2
  exit 1
fi

mkdir -p "$(dirname "$dmg_path")"
rm -f "$dmg_path"

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

cp -R "$app_path" "$tmp_dir/"
ln -s /Applications "$tmp_dir/Applications"

volume_name="${DMG_VOLUME_NAME:-NetsGo}"
echo "💿 Creating macOS DMG: $dmg_path"
hdiutil create \
  -volname "$volume_name" \
  -srcfolder "$tmp_dir" \
  -ov \
  -format UDZO \
  "$dmg_path"

echo "✅ macOS DMG: $dmg_path"
