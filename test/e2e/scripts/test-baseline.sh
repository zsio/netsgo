#!/usr/bin/env bash
# test-baseline.sh: stable-only E2E baseline harness.
#
# This script intentionally does not read or build E2E_CURRENT_IMAGE. It proves
# the selected COMPAT_BASELINE can run by itself before mixed-version tests use
# that image as a compatibility baseline.
#
# Modes:
#   BASELINE_MODE=full  - run TestSystem*E2E against stable-only images.
#   BASELINE_MODE=smoke - only verify stack startup, admin login, and clients.
set -euo pipefail

E2E_PROXY="${E2E_PROXY:-nginx}"
E2E_PROJECT_BASE="${E2E_PROJECT:-netsgo-baseline}"
E2E_BASE_COMPOSE="${E2E_BASE_COMPOSE:?E2E_BASE_COMPOSE is required}"
E2E_PROXY_COMPOSE="${E2E_PROXY_COMPOSE:?E2E_PROXY_COMPOSE is required}"
PROXY_PORT="${PROXY_PORT:-19080}"
UPSTREAM_PORT="${UPSTREAM_PORT:-19081}"
COMPAT_BASELINE="${COMPAT_BASELINE:-v0.1.8}"
E2E_STABLE_IMAGE="${E2E_STABLE_IMAGE:-netsgo-e2e:${COMPAT_BASELINE}}"
NETSGO_E2E_DIR="${NETSGO_E2E_DIR:-.}"
MODE="${BASELINE_MODE:-full}"
REBUILD_IMAGE="${BASELINE_REBUILD_IMAGE:-false}"

log() { echo "[baseline] $*"; }

random_admin_password() {
	printf 'NetsGo1-%s' "$(openssl rand -hex 12 2>/dev/null || uuidgen)"
}

compose() {
	docker compose -f "${E2E_BASE_COMPOSE}" -f "${E2E_PROXY_COMPOSE}" -p "${project}" "$@"
}

cleanup() {
	local exit_code="$1"
	if [ -n "${project:-}" ]; then
		log "cleanup: tearing down ${project}"
		compose down -v --remove-orphans 2>/dev/null || true
	fi
	exit "${exit_code}"
}
trap 'cleanup "$?"' EXIT

case "${MODE}" in
	smoke|full) ;;
	*)
		log "ERROR: unsupported BASELINE_MODE=${MODE}; expected smoke or full"
		exit 1
		;;
esac

for cmd in docker jq curl; do
	command -v "${cmd}" >/dev/null 2>&1 || { log "ERROR: ${cmd} is required"; exit 1; }
done
if [ "${MODE}" = "full" ]; then
	command -v go >/dev/null 2>&1 || { log "ERROR: go is required for BASELINE_MODE=full"; exit 1; }
fi

if [ "${REBUILD_IMAGE}" = "true" ] || ! docker image inspect "${E2E_STABLE_IMAGE}" >/dev/null 2>&1; then
	log "building stable image (${E2E_STABLE_IMAGE}) from ${COMPAT_BASELINE}..."
	bash "${NETSGO_E2E_DIR}/test/e2e/scripts/build-e2e-stable.sh" "${COMPAT_BASELINE}" "${E2E_STABLE_IMAGE}"
else
	log "stable image exists: ${E2E_STABLE_IMAGE}"
fi

export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
export NETSGO_E2E_TOOLS_IMAGE="${E2E_STABLE_IMAGE}"

export PROXY_PORT
export UPSTREAM_PORT
export SERVER_TCP_PORT="${SERVER_TCP_PORT:-19093}"
export SERVER_UDP_PORT="${SERVER_UDP_PORT:-19094}"
export SERVER_SOCKS5_PORT="${SERVER_SOCKS5_PORT:-19095}"
export C2C_SOCKS5_PORT="${C2C_SOCKS5_PORT:-19096}"
export C2C_SOCKS5_DENY_PORT="${C2C_SOCKS5_DENY_PORT:-19097}"
export C2C_TCP_PORT="${C2C_TCP_PORT:-19098}"
export C2C_TCP_ALT_PORT="${C2C_TCP_ALT_PORT:-19099}"
export C2C_TCP_SLOW_PORT="${C2C_TCP_SLOW_PORT:-19100}"
export C2C_UDP_PORT="${C2C_UDP_PORT:-19101}"
export C2C_SOCKS5_AUTH_PORT="${C2C_SOCKS5_AUTH_PORT:-19102}"
export C2C_SOCKS5_SOURCE_DENY_PORT="${C2C_SOCKS5_SOURCE_DENY_PORT:-19103}"

project="${E2E_PROJECT_BASE}-stable-only"
admin_pass="$(random_admin_password)"

log "============================================="
log "STABLE-ONLY BASELINE E2E"
log "============================================="
log "baseline:      ${COMPAT_BASELINE}"
log "stable image:  ${E2E_STABLE_IMAGE}"
log "server image:  ${NETSGO_SERVER_IMAGE}"
log "target image:  ${NETSGO_TARGET_CLIENT_IMAGE}"
log "ingress image: ${NETSGO_INGRESS_CLIENT_IMAGE}"
log "tools image:   ${NETSGO_E2E_TOOLS_IMAGE}"
log "proxy:         ${E2E_PROXY}"
log "mode:          ${MODE}"
log "rebuild image: ${REBUILD_IMAGE}"
log "project:       ${project}"
log "============================================="

if [ "${MODE}" = "smoke" ]; then
	SMOKE_PROJECT="${project}" \
	SMOKE_BASE_COMPOSE="${E2E_BASE_COMPOSE}" \
	SMOKE_PROXY_COMPOSE="${E2E_PROXY_COMPOSE}" \
	SMOKE_PROXY_PORT="${PROXY_PORT}" \
	SMOKE_ADMIN_PASS="${admin_pass}" \
	bash "${NETSGO_E2E_DIR}/test/e2e/scripts/smoke-system.sh"
else
	(cd "${NETSGO_E2E_DIR}" && \
	NETSGO_E2E_COMPOSE_PROJECT="${project}" \
	NETSGO_E2E_COMPOSE_FILES="${E2E_BASE_COMPOSE},${E2E_PROXY_COMPOSE}" \
	NETSGO_ADMIN_PASS="${admin_pass}" \
	NETSGO_E2E_COMPOSE_BUILD=0 \
	PROXY_PORT="${PROXY_PORT}" \
	UPSTREAM_PORT="${UPSTREAM_PORT}" \
	SERVER_TCP_PORT="${SERVER_TCP_PORT}" \
	SERVER_UDP_PORT="${SERVER_UDP_PORT}" \
	SERVER_SOCKS5_PORT="${SERVER_SOCKS5_PORT}" \
	C2C_SOCKS5_PORT="${C2C_SOCKS5_PORT}" \
	C2C_SOCKS5_DENY_PORT="${C2C_SOCKS5_DENY_PORT}" \
	C2C_TCP_PORT="${C2C_TCP_PORT}" \
	C2C_TCP_ALT_PORT="${C2C_TCP_ALT_PORT}" \
	C2C_TCP_SLOW_PORT="${C2C_TCP_SLOW_PORT}" \
	C2C_UDP_PORT="${C2C_UDP_PORT}" \
	C2C_SOCKS5_AUTH_PORT="${C2C_SOCKS5_AUTH_PORT}" \
	C2C_SOCKS5_SOURCE_DENY_PORT="${C2C_SOCKS5_SOURCE_DENY_PORT}" \
	go test -tags=e2e ./test/e2e -run 'TestSystem.*E2E' -count=1 -timeout 20m)
fi

log "STABLE-ONLY BASELINE PASS"
