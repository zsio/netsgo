#!/bin/sh

set -eu

# BEGIN NETSGO COMMON UPDATE HELPERS
NETSGO_LATEST_CNB="https://cnb.cool/zsio/netsgo/-/git/raw/release-index/updates/index-v1/latest.json"
NETSGO_LATEST_GITHUB="https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/latest.json"

# Release public keys are derived from the private release signing key stored in
# NETSGO_RELEASE_SIGNING_KEY_PEM. Commit public keys here so install/upgrade
# scripts can verify release checksums without trusting HTTPS alone.
# BEGIN NETSGO RELEASE PUBLIC KEYS
NETSGO_RELEASE_PUBLIC_KEY_PEM='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAH4VWaTpLBw8/WXELyluQChFm5Fi1qI2E8DSOwYKpRCc=
-----END PUBLIC KEY-----'
NETSGO_RELEASE_ALLOWED_SIGNERS='netsgo-release ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB+FVmk6SwcPP1lxC8pbkAoRZuRYtaiNhPA0jsGCqUQn'
# END NETSGO RELEASE PUBLIC KEYS

die() {
  printf '%s\n' "$*" >&2
  exit 1
}

log() {
  printf '==> %s\n' "$*" >&2
}

warn() {
  printf 'WARN: %s\n' "$*" >&2
}

require_linux_systemd() {
  log "检查 Linux + systemd 环境"
  [ "$(uname -s)" = "Linux" ] || die "此脚本只支持 Linux + systemd。请前往 GitHub Releases 手动下载。"
  command -v systemctl >/dev/null 2>&1 || die "未找到 systemctl。请前往 GitHub Releases 手动下载。"
  systemctl --version >/dev/null 2>&1 || die "systemd 不可用。请前往 GitHub Releases 手动下载。"
}

require_tools() {
  log "检查依赖工具"
  for tool in curl tar sha256sum jq awk sed sort grep head dirname rm mv mkdir mktemp chmod id stat; do
    command -v "$tool" >/dev/null 2>&1 || die "缺少依赖: $tool"
  done
}

source_order() {
  case "$1" in
    cnb) printf '%s\n' cnb github ;;
    github) printf '%s\n' github cnb ;;
    auto) printf '%s\n' cnb github ;;
    *) die "--source 仅支持 auto|cnb|github" ;;
  esac
}

latest_url_for_provider() {
  case "$1" in
    cnb) printf '%s\n' "$NETSGO_LATEST_CNB" ;;
    github) printf '%s\n' "$NETSGO_LATEST_GITHUB" ;;
    *) return 1 ;;
  esac
}

fetch_latest_index() {
  source="$1"
  out="$2"
  for provider in $(source_order "$source"); do
    url="$(latest_url_for_provider "$provider")"
    if curl -fsSL "$url" -o "$out"; then
      printf '%s\n' "$provider"
      return 0
    fi
  done
  return 1
}

release_detail_url() {
  provider="$1"
  tag="$2"
  case "$provider" in
    cnb) printf 'https://cnb.cool/zsio/netsgo/-/git/raw/release-index/updates/index-v1/releases/%s.json\n' "$tag" ;;
    github) printf 'https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/%s.json\n' "$tag" ;;
    *) return 1 ;;
  esac
}

fetch_release_detail() {
  source="$1"
  tag="$2"
  out="$3"
  for provider in $(source_order "$source"); do
    url="$(release_detail_url "$provider" "$tag")"
    if download_official "$url" "$out"; then
      printf '%s\n' "$provider"
      return 0
    fi
  done
  return 1
}

json_get_channel_latest() {
  file="$1"
  channel="$2"
  jq -r --arg channel "$channel" '.channels[$channel].latest // empty' "$file"
}

valid_release_tag() {
  printf '%s\n' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-beta\.[1-9][0-9]*)?$'
}

extract_comparable_version() {
  text="$1"
  for word in $text; do
    word="${word%,}"
    word="${word#(}"
    word="${word%)}"
    case "$word" in
      v*-*-g*)
        base="${word%%-[0-9]*-g*}"
        if valid_release_tag "$base"; then
          printf '%s\n' "$base"
          return 0
        fi
        ;;
      v*)
        if valid_release_tag "$word"; then
          printf '%s\n' "$word"
          return 0
        fi
        ;;
    esac
  done
  return 1
}

extract_exact_release_version() {
  text="$1"
  for word in $text; do
    word="${word%,}"
    word="${word#(}"
    word="${word%)}"
    if valid_release_tag "$word"; then
      printf '%s\n' "$word"
      return 0
    fi
  done
  return 1
}

canonical_platform() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    armv7l|armv7*) arch="armv7" ;;
    *) die "不支持的架构: $arch" ;;
  esac
  [ "$os" = "linux" ] || die "脚本只支持 Linux"
  printf '%s_%s\n' "$os" "$arch"
}

asset_name_for() {
  tag="$1"
  platform="$2"
  printf 'netsgo_%s_%s.tar.gz\n' "${tag#v}" "$platform"
}

official_url_allowed() {
  case "$1" in
    https://github.com/zsio/netsgo/releases/download/*) return 0 ;;
    https://raw.githubusercontent.com/zsio/netsgo/release-index/*) return 0 ;;
    https://cnb.cool/zsio/netsgo/-/releases/download/*) return 0 ;;
    https://cnb.cool/zsio/netsgo/-/git/raw/release-index/*) return 0 ;;
    *) return 1 ;;
  esac
}

download_official() {
  url="$1"
  out="$2"
  official_url_allowed "$url" || die "拒绝非官方下载 URL: $url"
  reject_symlink_path "$out"
  tmp_out="${out}.part.$$"
  rm -f "$tmp_out"
  log "下载 $url"
  if curl -fL --progress-bar "$url" -o "$tmp_out"; then
    reject_symlink_path "$out"
    mv "$tmp_out" "$out"
    chmod 600 "$out" 2>/dev/null || true
    return 0
  fi
  rm -f "$tmp_out"
  return 1
}

json_url_for_name_provider() {
  file="$1"
  name="$2"
  provider="$3"
  jq -r --arg name "$name" --arg provider "$provider" '
    [
      .checksum_asset?,
      .signature_assets.ed25519?,
      .signature_assets.sshsig?,
      (.assets[]?)
    ]
    | map(select(.name == $name) | .urls[]? | select(.provider == $provider) | .url)
    | .[0] // empty
  ' "$file"
}

download_release_detail_file() {
  detail="$1"
  source="$2"
  name="$3"
  out="$4"
  for provider in $(source_order "$source"); do
    url="$(json_url_for_name_provider "$detail" "$name" "$provider")"
    [ -n "$url" ] || continue
    if download_official "$url" "$out"; then
      printf '%s\n' "$provider"
      return 0
    fi
  done
  return 1
}

validate_release_detail() {
	detail="$1"
	tag="$2"
	asset="$3"
	jq -e --arg tag "$tag" --arg asset "$asset" '
	  .schema == 1 and
	  .project == "netsgo" and
	  .version == $tag and
	  .checksum_asset.name == "checksums.txt" and
	  (.checksum_asset.urls | type == "array" and length > 0) and
	  .signature_assets.ed25519.name == "checksums.txt.sig" and
	  (.signature_assets.ed25519.urls | type == "array" and length > 0) and
	  .signature_assets.sshsig.name == "checksums.txt.sshsig" and
	  (.signature_assets.sshsig.urls | type == "array" and length > 0) and
	  any(.assets[]?; .name == $asset and .os == "linux" and (.urls | type == "array" and length > 0))
	' "$detail" >/dev/null || die "release detail 无效或缺少当前平台资产: $asset"
}

verify_checksum() {
  checksums="$1"
  archive="$2"
  name="$3"
  checksum_matches "$checksums" "$archive" "$name" || die "checksum mismatch: $name"
}

checksum_matches() {
  checksums="$1"
  archive="$2"
  name="$3"
  [ -s "$checksums" ] || return 1
  [ -s "$archive" ] || return 1
  expected="$(awk -v n="$name" '$2 == n {print $1}' "$checksums" | head -1)"
  [ -n "$expected" ] || return 1
  actual="$(sha256sum "$archive" | awk '{print $1}')"
  [ "$actual" = "$expected" ]
}

verify_signature_openssl() {
  checksums="$1"
  sig="$2"
  [ -n "$NETSGO_RELEASE_PUBLIC_KEY_PEM" ] || return 1
  command -v openssl >/dev/null 2>&1 || return 1
  pub="$(mktemp)"
  printf '%s\n' "$NETSGO_RELEASE_PUBLIC_KEY_PEM" > "$pub"
  if openssl pkeyutl -verify -pubin -inkey "$pub" -rawin -in "$checksums" -sigfile "$sig" >/dev/null 2>&1; then
    rm -f "$pub"
    return 0
  fi
  rm -f "$pub"
  return 1
}

verify_signature_sshsig() {
  checksums="$1"
  sshsig="$2"
  [ -n "$NETSGO_RELEASE_ALLOWED_SIGNERS" ] || return 1
  command -v ssh-keygen >/dev/null 2>&1 || return 1
  allowed="$(mktemp)"
  printf '%s\n' "$NETSGO_RELEASE_ALLOWED_SIGNERS" > "$allowed"
  if ssh-keygen -Y verify -f "$allowed" -I netsgo-release -n file -s "$sshsig" < "$checksums" >/dev/null 2>&1; then
    rm -f "$allowed"
    return 0
  fi
  rm -f "$allowed"
  return 1
}

verify_signature() {
  checksums="$1"
  sig="$2"
  sshsig="$3"
  signature_valid "$checksums" "$sig" "$sshsig" || die "无法验证 checksums.txt 签名，已终止。"
}

signature_valid() {
  checksums="$1"
  sig="$2"
  sshsig="$3"
  if verify_signature_openssl "$checksums" "$sig"; then
    return 0
  fi
  if verify_signature_sshsig "$checksums" "$sshsig"; then
    return 0
  fi
  return 1
}

download_available_signatures() {
  detail="$1"
  source="$2"
  sig="$3"
  sshsig="$4"
  downloaded=0
  if command -v openssl >/dev/null 2>&1; then
    if download_release_detail_file "$detail" "$source" checksums.txt.sig "$sig" >/dev/null; then
      downloaded=1
    fi
  fi
  if command -v ssh-keygen >/dev/null 2>&1; then
    if download_release_detail_file "$detail" "$source" checksums.txt.sshsig "$sshsig" >/dev/null; then
      downloaded=1
    fi
  fi
  [ "$downloaded" -eq 1 ] || die "无法下载可用的 checksums.txt 签名，已终止。"
}

extract_netsgo() {
  archive="$1"
  dest="$2"
  log "解压 NetsGo 二进制"
  mkdir -p "$(dirname "$dest")"
  tar -xzf "$archive" -C "$(dirname "$dest")" --strip-components=1 --wildcards '*/netsgo' 2>/dev/null ||
    tar -xzf "$archive" -C "$(dirname "$dest")" netsgo
  chmod +x "$dest"
}

stat_owner_uid() {
  stat -c '%u' "$1" 2>/dev/null || stat -f '%u' "$1"
}

stat_mode_text() {
  stat -c '%A' "$1" 2>/dev/null || stat -f '%Sp' "$1"
}

reject_symlink_path() {
  [ ! -L "$1" ] || die "拒绝使用符号链接更新缓存路径: $1"
}

default_cache_root() {
  base="${TMPDIR:-/tmp}"
  root="$(mktemp -d "$base/netsgo-update-cache.XXXXXXXXXX")" || die "无法创建私有更新缓存目录"
  chmod 700 "$root" || die "无法保护私有更新缓存目录: $root"
  printf '%s\n' "$root"
}

validate_cache_root() {
  root="$1"
  case "$root" in
    /*) ;;
    *) die "NETSGO_UPDATE_CACHE_DIR 必须是绝对路径: $root" ;;
  esac
  reject_symlink_path "$root"
  if [ -e "$root" ] && [ ! -d "$root" ]; then
    die "更新缓存路径不是目录: $root"
  fi
  if [ ! -e "$root" ]; then
    old_umask="$(umask)"
    umask 077
    mkdir -p "$root" || { umask "$old_umask"; die "无法创建更新缓存目录: $root"; }
    umask "$old_umask"
  fi
  reject_symlink_path "$root"
  [ -d "$root" ] || die "更新缓存路径不是目录: $root"

  owner_uid="$(stat_owner_uid "$root")" || die "无法读取更新缓存目录属主: $root"
  current_uid="$(id -u)"
  if [ "$owner_uid" != "$current_uid" ]; then
    die "更新缓存目录必须归当前用户所有: $root"
  fi

  mode_text="$(stat_mode_text "$root")" || die "无法读取更新缓存目录权限: $root"
  case "$mode_text" in
    ?????w*|????????w*) die "更新缓存目录不得 group/world 可写: $root" ;;
  esac
}

cache_root() {
  if [ -n "${NETSGO_UPDATE_CACHE_DIR:-}" ]; then
    validate_cache_root "$NETSGO_UPDATE_CACHE_DIR"
    printf '%s\n' "$NETSGO_UPDATE_CACHE_DIR"
    return 0
  fi
  default_cache_root
}

cache_dir_for() {
  if [ -n "${NETSGO_UPDATE_CACHE_DIR:-}" ]; then
    validate_cache_root "$NETSGO_UPDATE_CACHE_DIR"
    root="$NETSGO_UPDATE_CACHE_DIR"
  else
    root="$(default_cache_root)"
  fi
  tag="$1"
  platform="$2"
  printf '%s/%s/%s\n' "$root" "$tag" "$platform"
}

ensure_cache_dir() {
  cache_dir="$1"
  parent="$(dirname "$cache_dir")"
  reject_symlink_path "$parent"
  reject_symlink_path "$cache_dir"
  mkdir -p "$cache_dir" || die "无法创建更新缓存目录: $cache_dir"
  reject_symlink_path "$parent"
  reject_symlink_path "$cache_dir"
  [ -d "$cache_dir" ] || die "更新缓存路径不是目录: $cache_dir"
  chmod 700 "$parent" "$cache_dir" || die "无法保护更新缓存目录: $cache_dir"
}

cleanup_empty_cache_parents() {
  cache_dir="$1"
  if [ -n "${NETSGO_UPDATE_CACHE_DIR:-}" ]; then
    root="$NETSGO_UPDATE_CACHE_DIR"
  else
    root="$(dirname "$(dirname "$cache_dir")")"
  fi
  parent="$(dirname "$cache_dir")"
  if [ "$parent" != "$root" ] && [ -d "$parent" ]; then
    rmdir "$parent" 2>/dev/null || true
  fi
  if [ -d "$root" ]; then
    rmdir "$root" 2>/dev/null || true
  fi
}

ensure_release_detail_cached() {
  source="$1"
  tag="$2"
  asset="$3"
  cache_dir="$4"
  out="$cache_dir/release.json"
  ensure_cache_dir "$cache_dir"
  reject_symlink_path "$out"
  if [ -s "$out" ] && validate_release_detail "$out" "$tag" "$asset" >/dev/null 2>&1; then
    log "复用已下载的 release detail: $out"
    printf '%s\n' "$out"
    return 0
  fi
  [ ! -e "$out" ] || warn "已下载的 release detail 无效，将重新下载: $out"
  rm -f "$out"
  fetch_release_detail "$source" "$tag" "$out" >/dev/null || die "无法获取 release detail: $tag"
  validate_release_detail "$out" "$tag" "$asset"
  printf '%s\n' "$out"
}

ensure_checksums_cached() {
  detail="$1"
  source="$2"
  cache_dir="$3"
  checksums="$cache_dir/checksums.txt"
  sig="$cache_dir/checksums.txt.sig"
  sshsig="$cache_dir/checksums.txt.sshsig"
  ensure_cache_dir "$cache_dir"
  reject_symlink_path "$checksums"
  reject_symlink_path "$sig"
  reject_symlink_path "$sshsig"
  if [ -s "$checksums" ] && signature_valid "$checksums" "$sig" "$sshsig"; then
    log "复用已下载并验签的 checksums.txt"
    printf '%s\n' "$checksums"
    return 0
  fi
  if [ -e "$checksums" ] || [ -e "$sig" ] || [ -e "$sshsig" ]; then
    warn "已下载的 checksum 或签名无效，将重新下载"
  fi
  rm -f "$checksums" "$sig" "$sshsig"
  download_release_detail_file "$detail" "$source" checksums.txt "$checksums" >/dev/null || die "无法下载 checksums.txt"
  download_available_signatures "$detail" "$source" "$sig" "$sshsig"
  verify_signature "$checksums" "$sig" "$sshsig"
  log "checksums.txt 签名验证通过"
  printf '%s\n' "$checksums"
}

ensure_archive_cached() {
  detail="$1"
  source="$2"
  asset="$3"
  cache_dir="$4"
  checksums="$5"
  archive="$cache_dir/$asset"
  ensure_cache_dir "$cache_dir"
  reject_symlink_path "$archive"
  if [ -s "$archive" ] && checksum_matches "$checksums" "$archive" "$asset"; then
    log "复用已下载并校验的 release archive: $archive"
    printf '%s\n' "$archive"
    return 0
  fi
  [ ! -e "$archive" ] || warn "已下载的 release archive 校验失败，将重新下载: $archive"
  rm -f "$archive"
  download_release_detail_file "$detail" "$source" "$asset" "$archive" >/dev/null || die "无法下载 release archive: $asset"
  if ! checksum_matches "$checksums" "$archive" "$asset"; then
    rm -f "$archive"
    die "checksum mismatch: $asset"
  fi
  log "release archive SHA256 校验通过"
  printf '%s\n' "$archive"
}

version_sort_key() {
  v="${1#v}"
  core="${v%%-*}"
  pre=""
  [ "$core" = "$v" ] || pre="${v#*-}"
  major="$(printf '%s' "$core" | awk -F. '{print $1}')"
  minor="$(printf '%s' "$core" | awk -F. '{print $2}')"
  patch="$(printf '%s' "$core" | awk -F. '{print $3}')"
  if [ -z "$pre" ]; then
    pre_rank=1
    beta_num=999999999
  else
    pre_rank=0
    beta_num="$(printf '%s' "$pre" | sed -n 's/^beta\.\([1-9][0-9]*\)$/\1/p')"
    [ -n "$beta_num" ] || beta_num=0
  fi
  printf '%09d.%09d.%09d.%d.%09d\n' "$major" "$minor" "$patch" "$pre_rank" "$beta_num"
}

semver_gt() {
  a="$1"
  b="$2"
  [ "$(printf '%s %s\n%s %s\n' "$(version_sort_key "$a")" "$a" "$(version_sort_key "$b")" "$b" | sort | tail -1 | awk '{print $2}')" = "$a" ] && [ "$a" != "$b" ]
}

semver_eq() {
  [ "$1" = "$2" ]
}

select_highest_version() {
  best=""
  for candidate in "$@"; do
    [ -n "$candidate" ] || continue
    valid_release_tag "$candidate" || continue
    if [ -z "$best" ] || semver_gt "$candidate" "$best"; then
      best="$candidate"
    fi
  done
  [ -n "$best" ] || return 1
  printf '%s\n' "$best"
}

channel_for_target() {
  case "$1" in
    *-beta.*) printf '%s\n' beta ;;
    *) printf '%s\n' stable ;;
  esac
}
# END NETSGO COMMON UPDATE HELPERS

cleanup_paths=""
cache_dir=""
completed=0
cleanup() {
  for path in $cleanup_paths; do
    [ -n "$path" ] && rm -rf "$path"
  done
  if [ "$completed" -eq 1 ]; then
    if [ -n "$cache_dir" ]; then
      if rm -rf "$cache_dir"; then
        cleanup_empty_cache_parents "$cache_dir"
        log "已清理下载缓存: $cache_dir"
      else
        warn "安装已完成，但清理下载缓存失败: $cache_dir"
      fi
    fi
  elif [ -n "$cache_dir" ]; then
    warn "安装未完成，已保留下载缓存以便下次重试: $cache_dir"
  fi
}
trap cleanup EXIT

run_interactive_install() {
  tty_path="${NETSGO_INSTALL_TTY:-/dev/tty}"
  [ -r "$tty_path" ] && [ -w "$tty_path" ] || die "install must be run from an interactive TTY"
  "$1" install <"$tty_path" >"$tty_path" 2>&1
}

run_noninteractive_client_install() {
  binary="$1"
  [ -n "$client_server" ] || die "--client requires --server"
  [ -n "$client_key" ] || die "--client requires --key"
  "$binary" install --client --server "$client_server" --key "$client_key"
}

source="auto"
channel="stable"
install_mode="interactive"
client_server=""
client_key=""

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
    --client)
      install_mode="client"
      shift
      ;;
    --server)
      [ "$#" -ge 2 ] || die "--server requires a value"
      client_server="$2"
      shift 2
      ;;
    --key)
      [ "$#" -ge 2 ] || die "--key requires a value"
      client_key="$2"
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
case "$install_mode" in
  interactive) [ -z "$client_server" ] && [ -z "$client_key" ] || die "--server/--key must be used with --client" ;;
  client) ;;
  *) die "unknown install mode: $install_mode" ;;
esac

require_linux_systemd
require_tools

log "检查是否已有 NetsGo 托管服务"
if [ "$install_mode" = "client" ]; then
  if systemctl list-unit-files 'netsgo-client.service' 2>/dev/null | grep -q '^netsgo-client\.service'; then
    die "检测到已有 netsgo-client.service。此一键安装命令只用于首次安装，不会覆盖现有 client 配置。请使用 netsgo manage 管理或重新安装 client。"
  fi
elif systemctl list-unit-files 'netsgo-*.service' 2>/dev/null | grep -q '^netsgo-'; then
  die "检测到已有 NetsGo 托管服务。请使用 scripts/upgrade.sh 或 netsgo manage。"
fi

tmp="$(mktemp -d)"
cleanup_paths="$cleanup_paths $tmp"

log "获取 release index（source=${source}, channel=${channel}）"
provider="$(fetch_latest_index "$source" "$tmp/latest.json")" || die "无法获取 release index"
target="$(json_get_channel_latest "$tmp/latest.json" "$channel")"
[ -n "$target" ] && valid_release_tag "$target" || die "release index 中缺少有效 $channel 版本"
log "目标版本: $target"

platform="$(canonical_platform)"
asset="$(asset_name_for "$target" "$platform")"
log "当前平台: $platform"
cache_dir="$(cache_dir_for "$target" "$platform")"
log "下载缓存目录: $cache_dir"

release_detail="$(ensure_release_detail_cached "$source" "$target" "$asset" "$cache_dir")"
checksums="$(ensure_checksums_cached "$release_detail" "$source" "$cache_dir")"
archive="$(ensure_archive_cached "$release_detail" "$source" "$asset" "$cache_dir" "$checksums")"
extract_netsgo "$archive" "$tmp/netsgo"

log "验证临时 NetsGo 版本"
version_output="$("$tmp/netsgo" --version)"
version="$(extract_exact_release_version "$version_output" || true)"
[ "$version" = "$target" ] || die "临时 netsgo 版本不匹配: want $target, got $version_output"

if [ "$install_mode" = "client" ]; then
  run_noninteractive_client_install "$tmp/netsgo"
else
  log "进入交互安装"
  run_interactive_install "$tmp/netsgo"
fi
completed=1
