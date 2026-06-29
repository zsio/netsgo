#!/usr/bin/env bash
# smoke-system.sh: minimal smoke test for a NetsGo compose stack.
#
# Verifies: server starts, admin login works, target-client and ingress-client
# come online. Does NOT create tunnels or test data paths.
#
# This script uses --no-build. Images MUST be pre-built before invoking.
#
# Required env: SMOKE_BASE_COMPOSE, SMOKE_PROXY_COMPOSE, SMOKE_ADMIN_PASS
# Optional env: SMOKE_PROJECT, SMOKE_PROXY_PORT, SMOKE_TIMEOUT,
#   SMOKE_ADMIN_USER, SMOKE_MANAGEMENT_HOST, SMOKE_TARGET_HOSTNAME,
#   SMOKE_INGRESS_HOSTNAME, and all port variables (PROXY_PORT, UPSTREAM_PORT,
#   SERVER_SOCKS5_PORT, C2C_*).
set -euo pipefail

SMOKE_PROJECT="${SMOKE_PROJECT:-netsgo-smoke}"
SMOKE_BASE_COMPOSE="${SMOKE_BASE_COMPOSE:?SMOKE_BASE_COMPOSE is required}"
SMOKE_PROXY_COMPOSE="${SMOKE_PROXY_COMPOSE:?SMOKE_PROXY_COMPOSE is required}"
SMOKE_ADMIN_USER="${SMOKE_ADMIN_USER:-admin}"
SMOKE_ADMIN_PASS="${SMOKE_ADMIN_PASS:?SMOKE_ADMIN_PASS is required}"
SMOKE_TIMEOUT="${SMOKE_TIMEOUT:-120}"
SMOKE_MANAGEMENT_HOST="${SMOKE_MANAGEMENT_HOST:-panel.system.local}"
SMOKE_PROXY_PORT="${SMOKE_PROXY_PORT:-19080}"
SMOKE_TARGET_HOSTNAME="${SMOKE_TARGET_HOSTNAME:-system-target-client}"
SMOKE_INGRESS_HOSTNAME="${SMOKE_INGRESS_HOSTNAME:-system-ingress-client}"

# ---------- Bridge SMOKE_* → NETSGO_* for docker-compose.system.yml ----------
# The compose file requires these NETSGO_* variables. Map from SMOKE_* if
# NETSGO_* is not already set by the caller (e.g. test-compat.sh).
export NETSGO_ADMIN_USER="${NETSGO_ADMIN_USER:-${SMOKE_ADMIN_USER}}"
export NETSGO_ADMIN_PASS="${NETSGO_ADMIN_PASS:-${SMOKE_ADMIN_PASS}}"
export NETSGO_SERVER_ADDR="${NETSGO_SERVER_ADDR:-http://${SMOKE_MANAGEMENT_HOST}}"
export NETSGO_TARGET_CLIENT_HOSTNAME="${NETSGO_TARGET_CLIENT_HOSTNAME:-${SMOKE_TARGET_HOSTNAME}}"
export NETSGO_INGRESS_CLIENT_HOSTNAME="${NETSGO_INGRESS_CLIENT_HOSTNAME:-${SMOKE_INGRESS_HOSTNAME}}"
# Tools image for helper services (tcp-backend-slow, udp-backend).
# Caller (test-compat.sh / test-upgrade.sh) should set this; default to local.
export NETSGO_E2E_TOOLS_IMAGE="${NETSGO_E2E_TOOLS_IMAGE:-netsgo-e2e:local}"
# Server/target/ingress images are passed via NETSGO_SERVER_IMAGE etc.
# Caller is responsible for setting those before invoking this script.

# Port variables: re-export for compose. These are normally already in the
# environment from the Makefile; ensure they are set.
export PROXY_PORT="${PROXY_PORT:-${SMOKE_PROXY_PORT}}"
export UPSTREAM_PORT="${UPSTREAM_PORT:-19081}"
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

log() { echo "[smoke] $*"; }

for cmd in docker jq curl; do
	command -v "${cmd}" >/dev/null 2>&1 || { log "ERROR: ${cmd} is required"; exit 1; }
done

api() {
	local method="$1" path="$2" token="${3:-}"
	shift 3 || true
	local args=(-sS -X "${method}" -H "Host: ${SMOKE_MANAGEMENT_HOST}" -H "Content-Type: application/json")
	[ -n "${token}" ] && args+=(-H "Authorization: Bearer ${token}")
	curl "${args[@]}" "$@" "http://127.0.0.1:${SMOKE_PROXY_PORT}${path}"
}

compose() {
	docker compose -f "${SMOKE_BASE_COMPOSE}" -f "${SMOKE_PROXY_COMPOSE}" -p "${SMOKE_PROJECT}" "$@"
}

cleanup() {
	local exit_code="$1"
	if [ "${exit_code}" -ne 0 ]; then
		log "=== DIAGNOSTICS ==="
		log "--- docker compose ps ---"
		compose ps 2>&1 || true
		log "--- docker compose logs (last 200 lines) ---"
		compose logs --no-color --tail 200 2>&1 || true
		log "=== END DIAGNOSTICS ==="
	fi
	log "cleanup: tearing down ${SMOKE_PROJECT}"
	compose down -v --remove-orphans 2>/dev/null || true
	exit "${exit_code}"
}
trap 'cleanup "$?"' EXIT

fail_with() {
	local exit_code="$1"
	shift
	log "ERROR: $*"
	# trap EXIT will handle diagnostics + cleanup + exit
	exit "${exit_code}"
}

# ---------- Start infrastructure (no-build) ----------
log "starting infrastructure (project=${SMOKE_PROJECT}, --no-build)"
log "  server image:     ${NETSGO_SERVER_IMAGE:-<default>}"
log "  target image:     ${NETSGO_TARGET_CLIENT_IMAGE:-<default>}"
log "  ingress image:    ${NETSGO_INGRESS_CLIENT_IMAGE:-<default>}"
log "  tools image:      ${NETSGO_E2E_TOOLS_IMAGE}"
compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
	|| fail_with $? "compose up failed for infrastructure services"

log "waiting for admin API (timeout=${SMOKE_TIMEOUT}s)..."
admin_token=""
end_ts="$(($(date +%s) + SMOKE_TIMEOUT))"
while [ "$(date +%s)" -lt "${end_ts}" ]; do
	resp="$(api POST /api/auth/login "" -d "{\"username\":\"${SMOKE_ADMIN_USER}\",\"password\":\"${SMOKE_ADMIN_PASS}\"}" 2>/dev/null)" || resp=""
	if [ -n "${resp}" ]; then
		admin_token="$(echo "${resp}" | jq -r '.token // empty' 2>/dev/null)" || admin_token=""
		[ -n "${admin_token}" ] && break
	fi
	sleep 2
done
[ -z "${admin_token}" ] && fail_with 1 "admin API did not become available within ${SMOKE_TIMEOUT}s"
log "admin login OK"

log "creating client API key..."
key_resp="$(api POST /api/admin/keys "${admin_token}" -d '{"name":"smoke","permissions":["connect"]}')"
client_key="$(echo "${key_resp}" | jq -r '.raw_key // empty')"
[ -z "${client_key}" ] && fail_with 1 "failed to create client API key: ${key_resp}"
log "client API key created"

export NETSGO_CLIENT_KEY="${client_key}"
log "starting target-client and ingress-client (--no-build)"
compose up -d --no-build --remove-orphans target-client ingress-client \
	|| fail_with $? "compose up failed for client services"

log "waiting for client pair (target=${SMOKE_TARGET_HOSTNAME}, ingress=${SMOKE_INGRESS_HOSTNAME}, timeout=${SMOKE_TIMEOUT}s)..."
target_found=false
ingress_found=false
end_ts="$(($(date +%s) + SMOKE_TIMEOUT))"
while [ "$(date +%s)" -lt "${end_ts}" ]; do
	clients_resp="$(api GET /api/clients "${admin_token}" 2>/dev/null)" || clients_resp=""
	if [ -n "${clients_resp}" ]; then
		if ! ${target_found}; then
			tc="$(echo "${clients_resp}" | jq --arg h "${SMOKE_TARGET_HOSTNAME}" '[.[] | select(.info.hostname == $h and .online == true)] | length')" 2>/dev/null || tc=0
			[ "${tc}" -gt 0 ] 2>/dev/null && target_found=true
		fi
		if ! ${ingress_found}; then
			ic="$(echo "${clients_resp}" | jq --arg h "${SMOKE_INGRESS_HOSTNAME}" '[.[] | select(.info.hostname == $h and .online == true)] | length')" 2>/dev/null || ic=0
			[ "${ic}" -gt 0 ] 2>/dev/null && ingress_found=true
		fi
	fi
	${target_found} && ${ingress_found} && break
	sleep 3
done

${target_found} || fail_with 1 "target-client (${SMOKE_TARGET_HOSTNAME}) did not come online within ${SMOKE_TIMEOUT}s"
${ingress_found} || fail_with 1 "ingress-client (${SMOKE_INGRESS_HOSTNAME}) did not come online within ${SMOKE_TIMEOUT}s"
log "both clients online"
log "SMOKE CONNECTIVITY PASS"
