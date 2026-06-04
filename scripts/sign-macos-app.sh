#!/usr/bin/env bash
set -euo pipefail

app_path="${1:-}"
identity="${CODESIGN_IDENTITY:--}"
clear_quarantine="${CLEAR_QUARANTINE:-1}"

if [ -z "$app_path" ]; then
  echo "Usage: CODESIGN_IDENTITY=- CLEAR_QUARANTINE=1 scripts/sign-macos-app.sh /path/to/NetsGo.app" >&2
  exit 2
fi

if [ "$(uname -s)" != "Darwin" ]; then
  echo "macOS app signing requires macOS." >&2
  exit 1
fi

if [ ! -d "$app_path" ]; then
  echo "macOS app not found: $app_path" >&2
  exit 1
fi

macos_dir="$app_path/Contents/MacOS"

echo "🔏 Signing macOS app: $app_path"
echo "   identity: $identity"

if [ -d "$macos_dir" ]; then
  while IFS= read -r -d '' executable; do
    echo "   signing executable: $executable"
    codesign --force --options runtime --sign "$identity" "$executable"
  done < <(find "$macos_dir" -type f -perm -111 -print0)
fi

codesign --force --deep --options runtime --sign "$identity" "$app_path"
codesign --verify --deep --strict --verbose=2 "$app_path"

if [ "$clear_quarantine" = "1" ]; then
  xattr -dr com.apple.quarantine "$app_path" 2>/dev/null || true
fi

echo "✅ macOS app signed: $app_path"
