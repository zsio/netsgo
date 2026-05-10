#!/bin/sh

set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
cleanup_paths=""
cleanup() {
  for path in $cleanup_paths; do
    [ -n "$path" ] && rm -rf "$path"
  done
}
trap cleanup EXIT

if [ -r "$SCRIPT_DIR/common-update.sh" ]; then
  . "$SCRIPT_DIR/common-update.sh"
else
  tmp_common="$(mktemp)"
  cleanup_paths="$cleanup_paths $tmp_common"
  if curl -fsSL "https://cnb.cool/zsio/netsgo/-/raw/main/scripts/common-update.sh" -o "$tmp_common" ||
    curl -fsSL "https://raw.githubusercontent.com/zsio/netsgo/main/scripts/common-update.sh" -o "$tmp_common"; then
    . "$tmp_common"
  else
    printf '%s\n' "无法加载 common-update.sh" >&2
    exit 1
  fi
fi

source="auto"
channel="stable"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --source)
      [ "$#" -ge 2 ] || die "--source requires a value"
      source="$2"
      shift 2
      ;;
    --channel)
      [ "$#" -ge 2 ] || die "--channel requires a value"
      channel="$2"
      shift 2
      ;;
    -y|--yes)
      die "install.sh 不支持 -y/--yes；首次安装需要交互配置。"
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

case "$channel" in stable|beta) ;; *) die "--channel 仅支持 stable|beta" ;; esac

require_linux_systemd
require_tools

if systemctl list-unit-files 'netsgo-*.service' 2>/dev/null | grep -q '^netsgo-'; then
  die "检测到已有 NetsGo 托管服务。请使用 scripts/upgrade.sh 或 netsgo manage。"
fi

tmp="$(mktemp -d)"
cleanup_paths="$cleanup_paths $tmp"

provider="$(fetch_latest_index "$source" "$tmp/latest.json")" || die "无法获取 release index"
target="$(json_get_channel_latest "$tmp/latest.json" "$channel")"
[ -n "$target" ] && valid_release_tag "$target" || die "release index 中缺少有效 $channel 版本"

platform="$(canonical_platform)"
asset="$(asset_name_for "$target" "$platform")"
fetch_release_detail "$source" "$target" "$tmp/release.json" >/dev/null || die "无法获取 release detail: $target"
validate_release_detail "$tmp/release.json" "$target" "$asset"

download_release_detail_file "$tmp/release.json" "$source" checksums.txt "$tmp/checksums.txt" >/dev/null || die "无法下载 checksums.txt"
download_available_signatures "$tmp/release.json" "$source" "$tmp/checksums.txt.sig" "$tmp/checksums.txt.sshsig"
verify_signature "$tmp/checksums.txt" "$tmp/checksums.txt.sig" "$tmp/checksums.txt.sshsig"

download_release_detail_file "$tmp/release.json" "$source" "$asset" "$tmp/$asset" >/dev/null || die "无法下载 release archive: $asset"
verify_checksum "$tmp/checksums.txt" "$tmp/$asset" "$asset"
extract_netsgo "$tmp/$asset" "$tmp/netsgo"

version_output="$("$tmp/netsgo" --version)"
version="$(extract_exact_release_version "$version_output" || true)"
[ "$version" = "$target" ] || die "临时 netsgo 版本不匹配: want $target, got $version_output"

"$tmp/netsgo" install
