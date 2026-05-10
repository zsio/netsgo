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
channel="auto"
force=0
yes=0

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
    -f|--force)
      force=1
      shift
      ;;
    -y|--yes)
      yes=1
      shift
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

case "$channel" in auto|stable|beta) ;; *) die "--channel 仅支持 auto|stable|beta" ;; esac

require_linux_systemd
require_tools

if ! systemctl list-unit-files 'netsgo-*.service' 2>/dev/null | grep -q '^netsgo-'; then
  die "未检测到 NetsGo 托管服务，拒绝下载。"
fi

installed_bin="${NETSGO_INSTALLED_BIN:-/usr/local/bin/netsgo}"
[ -x "$installed_bin" ] || die "未找到已安装二进制 $installed_bin"
installed_version="$("$installed_bin" --version || true)"
installed_base="$(extract_comparable_version "$installed_version" || true)"

tmp="$(mktemp -d)"
cleanup_paths="$cleanup_paths $tmp"

provider="$(fetch_latest_index "$source" "$tmp/latest.json")" || die "无法获取 release index"
target_channel="$channel"
if [ "$channel" = "auto" ]; then
  case "$installed_base" in
    *-beta.*) target_channel="auto-beta" ;;
    *) target_channel="stable" ;;
  esac
fi
if [ "$target_channel" = "auto-beta" ]; then
  stable_target="$(json_get_channel_latest "$tmp/latest.json" stable || true)"
  beta_target="$(json_get_channel_latest "$tmp/latest.json" beta || true)"
  target="$(select_highest_version "$stable_target" "$beta_target")" || die "release index 中缺少有效 stable/beta 版本"
  target_channel="$(channel_for_target "$target")"
else
  target="$(json_get_channel_latest "$tmp/latest.json" "$target_channel")"
fi
[ -n "$target" ] && valid_release_tag "$target" || die "release index 中缺少有效 $target_channel 版本"

if [ "$force" -ne 1 ]; then
  [ -n "$installed_base" ] || die "当前版本不可比较；如需强制替换，请使用 -f。"
  if semver_eq "$installed_base" "$target"; then
    printf '当前已是目标版本 %s，不下载、不替换、不重启。\n' "$target"
    exit 0
  fi
  if semver_gt "$installed_base" "$target"; then
    die "目标版本 $target 低于当前版本 $installed_base；如需强制降级，请使用 -f。"
  fi
fi

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

args="upgrade"
[ "$force" -eq 1 ] && args="$args -f"
[ "$yes" -eq 1 ] && args="$args -y"
"$tmp/netsgo" $args
