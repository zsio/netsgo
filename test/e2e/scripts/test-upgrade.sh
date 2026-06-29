#!/usr/bin/env bash
# test-upgrade.sh: cross-version upgrade E2E harness.
#
# Independent cases (each with its own project/volume):
#   1. server-only:        upgrade server only, verify server_expose HTTP/TCP/UDP/SOCKS5 data paths
#   2. target-client-only: upgrade target-client only, verify server_expose HTTP/TCP/UDP/SOCKS5 data paths
#   3. ingress-client-only: upgrade ingress-client only, verify c2c TCP/SOCKS5 data paths
#   4. clients-only:       upgrade both clients, keep stable server, verify HTTP + c2c TCP/SOCKS5
#   5. server-rollback:    upgrade server, then roll back server image, verify same data dir
#   6. current-write-rollback:
#                          create tunnels on current server, then roll back server
#   7. all-upgrade:        upgrade all components, verify server_expose HTTP/TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5
#   8. client-first-rolling:
#                          upgrade clients first, then server, verify existing data paths
#   9. full-cold-upgrade:  stop all stable processes, restart all as current with same volumes
#
# All cases start from stable baseline with --no-build (images must be
# pre-built). Upgrades use --force-recreate --no-deps and verify Config.Image.
#
# This is an upgrade data-path harness. In-flight stream continuity and detailed
# auth/policy matrices are intentionally covered by separate tests.
#
# Required env (from Makefile): E2E_BASE_COMPOSE, E2E_PROXY_COMPOSE,
#   PROXY_PORT, UPSTREAM_PORT, port env vars, COMPAT_BASELINE,
#   E2E_CURRENT_IMAGE, E2E_STABLE_IMAGE, NETSGO_E2E_TOOLS_IMAGE.
set -euo pipefail

E2E_PROXY="${E2E_PROXY:-nginx}"
E2E_PROJECT_BASE="${E2E_PROJECT:-netsgo-upgrade}"
E2E_BASE_COMPOSE="${E2E_BASE_COMPOSE:?E2E_BASE_COMPOSE is required}"
E2E_PROXY_COMPOSE="${E2E_PROXY_COMPOSE:?E2E_PROXY_COMPOSE is required}"
PROXY_PORT="${PROXY_PORT:-19080}"
UPSTREAM_PORT="${UPSTREAM_PORT:-19081}"
SERVER_TCP_PORT="${SERVER_TCP_PORT:-19093}"
SERVER_UDP_PORT="${SERVER_UDP_PORT:-19094}"
SERVER_SOCKS5_PORT="${SERVER_SOCKS5_PORT:-19095}"
SERVER_TCP_ALT_PORT="${SERVER_TCP_ALT_PORT:-19104}"
SERVER_UDP_ALT_PORT="${SERVER_UDP_ALT_PORT:-19105}"
SERVER_SOCKS5_ALT_PORT="${SERVER_SOCKS5_ALT_PORT:-19106}"
C2C_SOCKS5_PORT="${C2C_SOCKS5_PORT:-19096}"
C2C_SOCKS5_DENY_PORT="${C2C_SOCKS5_DENY_PORT:-19097}"
C2C_TCP_PORT="${C2C_TCP_PORT:-19098}"
C2C_TCP_ALT_PORT="${C2C_TCP_ALT_PORT:-19099}"
C2C_TCP_SLOW_PORT="${C2C_TCP_SLOW_PORT:-19100}"
C2C_UDP_PORT="${C2C_UDP_PORT:-19101}"
C2C_SOCKS5_AUTH_PORT="${C2C_SOCKS5_AUTH_PORT:-19102}"
C2C_SOCKS5_SOURCE_DENY_PORT="${C2C_SOCKS5_SOURCE_DENY_PORT:-19103}"
COMPAT_BASELINE="${COMPAT_BASELINE:-v0.1.8}"
E2E_CURRENT_IMAGE="${E2E_CURRENT_IMAGE:-netsgo-e2e:current}"
E2E_STABLE_IMAGE="${E2E_STABLE_IMAGE:-netsgo-e2e:${COMPAT_BASELINE}}"
NETSGO_E2E_TOOLS_IMAGE="${NETSGO_E2E_TOOLS_IMAGE:-${E2E_STABLE_IMAGE}}"
NETSGO_E2E_DIR="${NETSGO_E2E_DIR:-.}"
RECOVERY_TIMEOUT_SECONDS="${UPGRADE_RECOVERY_TIMEOUT_SECONDS:-120}"
export SERVER_TCP_PORT SERVER_UDP_PORT SERVER_SOCKS5_PORT SERVER_TCP_ALT_PORT SERVER_UDP_ALT_PORT SERVER_SOCKS5_ALT_PORT
export C2C_SOCKS5_PORT C2C_SOCKS5_DENY_PORT C2C_TCP_PORT C2C_TCP_ALT_PORT C2C_TCP_SLOW_PORT C2C_UDP_PORT C2C_SOCKS5_AUTH_PORT C2C_SOCKS5_SOURCE_DENY_PORT

ADMIN_USER="admin"
MANAGEMENT_HOST="panel.system.local"
TARGET_HOSTNAME="system-target-client"
INGRESS_HOSTNAME="system-ingress-client"
BACKEND_HOST="tcp-backend"
BACKEND_PORT=18083
BACKEND_RESPONSE="system tcp backend response"
UDP_BACKEND_HOST="udp-backend"
UDP_BACKEND_PORT=18084

log() { echo "[upgrade] $*"; }

random_admin_password() {
	printf 'NetsGo1-%s' "$(openssl rand -hex 12 2>/dev/null || uuidgen)"
}

for cmd in docker jq curl nc; do
	command -v "${cmd}" >/dev/null 2>&1 || { log "ERROR: ${cmd} is required"; exit 1; }
done

# ---------- Project-scoped helpers ----------

PROJECT_NAME=""

set_project() {
	PROJECT_NAME="$1"
}

compose() {
	docker compose -f "${E2E_BASE_COMPOSE}" -f "${E2E_PROXY_COMPOSE}" -p "${PROJECT_NAME}" "$@"
}

cleanup_current() {
	if [ -n "${PROJECT_NAME}" ]; then
		log "cleanup: tearing down ${PROJECT_NAME}"
		compose down -v --remove-orphans 2>/dev/null || true
	fi
}

dump_current() {
	if [ -n "${PROJECT_NAME}" ]; then
		log "=== DIAGNOSTICS (${PROJECT_NAME}) ==="
		log "--- docker compose ps ---"
		compose ps 2>&1 || true
		log "--- docker compose logs (last 250 lines) ---"
		compose logs --no-color --tail 250 2>&1 || true
		log "=== END DIAGNOSTICS ==="
	fi
}

# Cleanup runs on exit; each case sets PROJECT_NAME and resets on completion.
cleanup() {
	local exit_code="$1"
	if [ "${exit_code}" -ne 0 ]; then
		dump_current
	fi
	cleanup_current
	exit "${exit_code}"
}
trap 'cleanup "$?"' EXIT

# ---------- API helpers ----------

api() {
	local method="$1" path="$2" token="${3:-}"
	shift 3 || true
	local args=(-sS -X "${method}" -H "Host: ${MANAGEMENT_HOST}" -H "Content-Type: application/json")
	[ -n "${token}" ] && args+=(-H "Authorization: Bearer ${token}")
	curl "${args[@]}" "$@" "http://127.0.0.1:${PROXY_PORT}${path}"
}

login_admin() {
	local pass="$1"
	local timeout="${2:-${RECOVERY_TIMEOUT_SECONDS}}"
	local token="" resp
	local end_ts="$(($(date +%s) + timeout))"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		resp="$(api POST /api/auth/login "" -d "{\"username\":\"${ADMIN_USER}\",\"password\":\"${pass}\"}" 2>/dev/null)" || resp=""
		if [ -n "${resp}" ]; then
			token="$(echo "${resp}" | jq -r '.token // empty' 2>/dev/null)" || token=""
			[ -n "${token}" ] && break
		fi
		sleep 2
	done
	echo "${token}"
}

create_api_key() {
	local token="$1"
	api POST /api/admin/keys "${token}" -d '{"name":"upgrade-test","permissions":["connect"]}' \
		| jq -r '.raw_key // empty'
}

wait_client_online() {
	local hostname="$1" token="$2" timeout="${3:-${RECOVERY_TIMEOUT_SECONDS}}"
	local end_ts="$(($(date +%s) + timeout))"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		local resp
		resp="$(api GET /api/clients "${token}" 2>/dev/null)" || resp=""
		if [ -n "${resp}" ]; then
			local cid
			cid="$(echo "${resp}" | jq -r --arg h "${hostname}" '.[] | select(.info.hostname == $h and .online == true) | .id' 2>/dev/null | head -1)" || cid=""
			if [ -n "${cid}" ]; then
				echo "${cid}"
				return 0
			fi
		fi
		sleep 3
	done
	return 1
}

wait_client_pair() {
	local token="$1" timeout="${2:-${RECOVERY_TIMEOUT_SECONDS}}"
	_target_cid="$(wait_client_online "${TARGET_HOSTNAME}" "${token}" "${timeout}")" || {
		log "ERROR: target-client (${TARGET_HOSTNAME}) not online within ${timeout}s"
		return 1
	}
	_ingress_cid="$(wait_client_online "${INGRESS_HOSTNAME}" "${token}" "${timeout}")" || {
		log "ERROR: ingress-client (${INGRESS_HOSTNAME}) not online within ${timeout}s"
		return 1
	}
	log "clients online: target=${_target_cid} ingress=${_ingress_cid}"
}

# ---------- Tunnel helpers ----------

create_server_expose_http() {
	local token="$1" name="$2" host="$3" target_cid="$4"
	api POST /api/tunnels "${token}" -d "{
		\"name\":\"${name}\",
		\"topology\":\"server_expose\",
		\"ingress\":{\"location\":\"server\",\"type\":\"http_host\",\"config\":{
			\"domain\":\"${host}\",
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"],
			\"auth\":{\"type\":\"none\"}
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"tcp_service\",\"config\":{\"host\":\"${BACKEND_HOST}\",\"port\":${BACKEND_PORT}}},
		\"transport_policy\":\"server_relay_only\"
	}" | jq -r '.id // empty'
}

create_server_expose_tcp() {
	local token="$1" name="$2" port="$3" target_cid="$4"
	api POST /api/tunnels "${token}" -d "{
		\"name\":\"${name}\",
		\"topology\":\"server_expose\",
		\"ingress\":{\"location\":\"server\",\"type\":\"tcp_listen\",\"config\":{
			\"bind_ip\":\"0.0.0.0\",
			\"port\":${port},
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"]
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"tcp_service\",\"config\":{\"host\":\"${BACKEND_HOST}\",\"port\":${BACKEND_PORT}}},
		\"transport_policy\":\"server_relay_only\"
	}" | jq -r '.id // empty'
}

create_server_expose_udp() {
	local token="$1" name="$2" port="$3" target_cid="$4"
	api POST /api/tunnels "${token}" -d "{
		\"name\":\"${name}\",
		\"topology\":\"server_expose\",
		\"ingress\":{\"location\":\"server\",\"type\":\"udp_listen\",\"config\":{
			\"bind_ip\":\"0.0.0.0\",
			\"port\":${port},
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"]
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"udp_service\",\"config\":{\"host\":\"${UDP_BACKEND_HOST}\",\"port\":${UDP_BACKEND_PORT}}},
		\"transport_policy\":\"server_relay_only\"
	}" | jq -r '.id // empty'
}

create_server_expose_socks5() {
	local token="$1" name="$2" port="$3" target_cid="$4"
	api POST /api/tunnels "${token}" -d "{
		\"name\":\"${name}\",
		\"topology\":\"server_expose\",
		\"ingress\":{\"location\":\"server\",\"type\":\"socks5_listen\",\"config\":{
			\"bind_ip\":\"0.0.0.0\",
			\"port\":${port},
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"],
			\"auth\":{\"type\":\"none\"}
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"socks5_connect_handler\",\"config\":{
			\"allowed_target_cidrs\":[\"0.0.0.0/0\",\"::/0\"],
			\"allowed_target_hosts\":[\"${BACKEND_HOST}\"],
			\"allowed_target_ports\":[${BACKEND_PORT}],
			\"dial_timeout_seconds\":5
		}},
		\"transport_policy\":\"server_relay_only\",
		\"confirm_no_auth_risk\":true
	}" | jq -r '.id // empty'
}

create_c2c_tcp() {
	local token="$1" name="$2" port="$3" ingress_cid="$4" target_cid="$5"
	api POST /api/tunnels "${token}" -d "{
		\"name\":\"${name}\",
		\"topology\":\"client_to_client\",
		\"ingress\":{\"location\":\"client\",\"client_id\":\"${ingress_cid}\",\"type\":\"tcp_listen\",\"config\":{
			\"bind_ip\":\"0.0.0.0\",
			\"port\":${port},
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"]
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"tcp_service\",\"config\":{\"host\":\"${BACKEND_HOST}\",\"port\":${BACKEND_PORT}}},
		\"transport_policy\":\"server_relay_only\"
	}" | jq -r '.id // empty'
}

create_c2c_udp() {
	local token="$1" name="$2" port="$3" ingress_cid="$4" target_cid="$5"
	api POST /api/tunnels "${token}" -d "{
		\"name\":\"${name}\",
		\"topology\":\"client_to_client\",
		\"ingress\":{\"location\":\"client\",\"client_id\":\"${ingress_cid}\",\"type\":\"udp_listen\",\"config\":{
			\"bind_ip\":\"0.0.0.0\",
			\"port\":${port},
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"]
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"udp_service\",\"config\":{\"host\":\"${UDP_BACKEND_HOST}\",\"port\":${UDP_BACKEND_PORT}}},
		\"transport_policy\":\"server_relay_only\"
	}" | jq -r '.id // empty'
}

create_c2c_socks5() {
	local token="$1" name="$2" port="$3" ingress_cid="$4" target_cid="$5"
	api POST /api/tunnels "${token}" -d "{
		\"name\":\"${name}\",
		\"topology\":\"client_to_client\",
		\"ingress\":{\"location\":\"client\",\"client_id\":\"${ingress_cid}\",\"type\":\"socks5_listen\",\"config\":{
			\"bind_ip\":\"0.0.0.0\",
			\"port\":${port},
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"],
			\"auth\":{\"type\":\"none\"}
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"socks5_connect_handler\",\"config\":{
			\"allowed_target_cidrs\":[\"0.0.0.0/0\",\"::/0\"],
			\"allowed_target_hosts\":[\"${BACKEND_HOST}\"],
			\"allowed_target_ports\":[${BACKEND_PORT}],
			\"dial_timeout_seconds\":5
		}},
		\"transport_policy\":\"server_relay_only\"
	}" | jq -r '.id // empty'
}

assert_no_tunnel_named() {
	local token="$1" name="$2" label="$3"
	local resp count
	resp="$(api GET /api/tunnels "${token}" 2>/dev/null)" || {
		log "FAIL: unable to list tunnels while checking ${label}"
		return 1
	}
	count="$(echo "${resp}" | jq -r --arg name "${name}" '[.[] | select(.name == $name)] | length' 2>/dev/null)" || count=""
	if [ "${count}" != "0" ]; then
		log "FAIL: rejected tunnel ${label} must not be persisted; found ${count}"
		return 1
	fi
}

assert_c2c_clean_reject() {
	local token="$1" phase="$2" ingress_cid="$3" target_cid="$4" port="$5"
	local name="upgrade-clean-reject-${phase}"
	local resp http_status code error_code field
	resp="$(api POST /api/tunnels "${token}" -w '\n%{http_code}' -d "{
		\"name\":\"${name}\",
		\"topology\":\"client_to_client\",
		\"ingress\":{\"location\":\"client\",\"client_id\":\"${ingress_cid}\",\"type\":\"future_ingress\",\"config\":{
			\"bind_ip\":\"0.0.0.0\",
			\"port\":${port},
			\"allowed_source_cidrs\":[\"0.0.0.0/0\",\"::/0\"]
		}},
		\"target\":{\"location\":\"client\",\"client_id\":\"${target_cid}\",\"type\":\"tcp_service\",\"config\":{\"host\":\"${BACKEND_HOST}\",\"port\":${BACKEND_PORT}}},
		\"transport_policy\":\"server_relay_only\"
	}" 2>/dev/null)" || {
		log "FAIL: ${phase} clean reject request failed"
		return 1
	}
	http_status="$(printf '%s' "${resp}" | tail -n 1)"
	resp="$(printf '%s' "${resp}" | sed '$d')"
	if [ "${http_status}" != "400" ]; then
		log "FAIL: ${phase} clean reject status got ${http_status}, want 400 body=${resp}"
		return 1
	fi
	code="$(echo "${resp}" | jq -r '.code // empty' 2>/dev/null)" || code=""
	error_code="$(echo "${resp}" | jq -r '.error_code // empty' 2>/dev/null)" || error_code=""
	field="$(echo "${resp}" | jq -r '.field // empty' 2>/dev/null)" || field=""
	if [ "${code}" != "unsupported_endpoint_type" ] && [ "${error_code}" != "unsupported_endpoint_type" ]; then
		log "FAIL: ${phase} clean reject code mismatch body=${resp}"
		return 1
	fi
	if [ "${field}" != "ingress.type" ]; then
		log "FAIL: ${phase} clean reject field got ${field}, want ingress.type body=${resp}"
		return 1
	fi
	assert_no_tunnel_named "${token}" "${name}" "${phase}" || return 1
	expect_no_listener_at "ingress-client" "${port}" "tcp" "${phase} rejected c2c ingress" || return 1
}

wait_tunnel_state() {
	local token="$1" tunnel_id="$2" expected_state="$3" timeout="${4:-${RECOVERY_TIMEOUT_SECONDS}}"
	local end_ts="$(($(date +%s) + timeout))"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		local resp state
		resp="$(api GET "/api/tunnels/${tunnel_id}" "${token}" 2>/dev/null)" || resp=""
		if [ -n "${resp}" ]; then
			state="$(echo "${resp}" | jq -r '.runtime_state // empty' 2>/dev/null)" || state=""
			[ "${state}" = "${expected_state}" ] && return 0
		fi
		sleep 3
	done
	return 1
}

wait_tunnel_active() {
	local token="$1" tunnel_id="$2" timeout="${3:-${RECOVERY_TIMEOUT_SECONDS}}"
	wait_tunnel_state "${token}" "${tunnel_id}" "active" "${timeout}"
}

assert_tunnel_no_issues() {
	local token="$1" tunnel_id="$2" label="$3"
	local resp count
	resp="$(api GET "/api/tunnels/${tunnel_id}" "${token}" 2>/dev/null)" || {
		log "FAIL: unable to fetch tunnel ${label} (${tunnel_id})"
		return 1
	}
	count="$(echo "${resp}" | jq -r '(.issues // []) | length' 2>/dev/null)" || count=""
	if [ "${count}" != "0" ]; then
		log "FAIL: tunnel ${label} (${tunnel_id}) has issues: ${resp}"
		return 1
	fi
}

wait_server_expose_suite_active() {
	local token="$1" phase="$2" http_tid="$3" tcp_tid="$4" udp_tid="$5" socks5_tid="$6"
	wait_tunnel_active "${token}" "${http_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: ${phase} HTTP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: ${phase} server_expose TCP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: ${phase} server_expose UDP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: ${phase} server_expose SOCKS5 tunnel not active"; return 1; }
	assert_tunnel_no_issues "${token}" "${http_tid}" "${phase} HTTP" || return 1
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "${phase} server TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "${phase} server UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "${phase} server SOCKS5" || return 1
}

listener_count_at() {
	local service="$1" port="$2" proto="$3" label="$4"
	local container output awk_proto
	container="$(compose ps -q "${service}" 2>/dev/null || true)"
	[ -n "${container}" ] || { log "FAIL: missing container for ${service} while checking ${label}"; return 1; }
	case "${proto}" in
		tcp)
			output="$(docker exec "${container}" sh -c 'netstat -ltn 2>/dev/null || netstat -ln 2>/dev/null' || true)"
			awk_proto="^tcp"
			;;
		udp)
			output="$(docker exec "${container}" sh -c 'netstat -lun 2>/dev/null || netstat -ln 2>/dev/null' || true)"
			awk_proto="^udp"
			;;
		*)
			log "FAIL: unsupported listener protocol ${proto} for ${label}"
			return 1
			;;
	esac
	echo "${output}" | awk -v p=":${port}" -v proto="${awk_proto}" '$1 ~ proto && $4 ~ p"$" { n++ } END { print n+0 }'
}

assert_listener_count() {
	local service="$1" port="$2" proto="$3" want="$4" label="$5"
	local count
	count="$(listener_count_at "${service}" "${port}" "${proto}" "${label}")" || return 1
	if [ "${count}" != "${want}" ]; then
		log "FAIL: ${label} ${proto} listener count on ${service}:${port}: got ${count}, want ${want}"
		return 1
	fi
}

assert_tcp_listener_count() {
	local service="$1" port="$2" want="$3" label="$4"
	assert_listener_count "${service}" "${port}" "tcp" "${want}" "${label}"
}

expect_no_listener_at() {
	local service="$1" port="$2" proto="$3" label="$4"
	assert_listener_count "${service}" "${port}" "${proto}" 0 "${label}"
}

assert_server_expose_tcp_listeners() {
	local phase="$1"
	assert_tcp_listener_count "server" "${SERVER_TCP_PORT}" 1 "${phase} server_expose TCP" || return 1
	assert_tcp_listener_count "server" "${SERVER_SOCKS5_PORT}" 1 "${phase} server_expose SOCKS5" || return 1
	assert_listener_count "server" "${SERVER_UDP_PORT}" "udp" 1 "${phase} server_expose UDP" || return 1
}

tunnel_action() {
	local token="$1" tunnel_id="$2" action="$3"
	local resp success
	resp="$(api PUT "/api/tunnels/${tunnel_id}/${action}" "${token}" -d '{}' 2>/dev/null)" || return 1
	success="$(echo "${resp}" | jq -r '.success // false' 2>/dev/null)" || success="false"
	[ "${success}" = "true" ]
}

verify_http() {
	local host="$1" expected="$2" timeout="${3:-30}"
	local end_ts="$(($(date +%s) + timeout))"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		local resp
		resp="$(curl -sS -H "Host: ${host}" "http://127.0.0.1:${PROXY_PORT}/" 2>/dev/null)" || resp=""
		if echo "${resp}" | grep -qF "${expected}" 2>/dev/null; then
			return 0
		fi
		sleep 2
	done
	return 1
}

verify_tcp_http() {
	local port="$1" host="$2" expected="$3" timeout="${4:-15}"
	local end_ts="$(($(date +%s) + timeout))"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		local resp=""
		resp="$(curl -sS --max-time 5 -H "Host: ${host}" "http://127.0.0.1:${port}/" 2>/dev/null || true)"
		if echo "${resp}" | grep -qF "${expected}" 2>/dev/null; then
			return 0
		fi
		sleep 2
	done
	return 1
}

verify_udp_echo() {
	local port="$1" payload="$2" timeout="${3:-15}"
	local end_ts="$(($(date +%s) + timeout))"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		local resp=""
		if resp="$(printf '%s' "${payload}" | nc -u -w 5 127.0.0.1 "${port}" 2>/dev/null)"; then
			if [ "${resp}" = "${payload}" ]; then
				return 0
			fi
		fi
		sleep 2
	done
	return 1
}

verify_socks5_http() {
	local port="$1" host="$2" expected="$3" timeout="${4:-30}"
	local end_ts="$(($(date +%s) + timeout))"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		local resp=""
		if resp="$(curl -sS --socks5-hostname "127.0.0.1:${port}" "http://${host}:${BACKEND_PORT}/" 2>/dev/null)"; then
			if echo "${resp}" | grep -qF "${expected}" 2>/dev/null; then
				return 0
			fi
		fi
		sleep 2
	done
	return 1
}

verify_server_expose_suite() {
	local host="$1" udp_payload="$2" phase="$3"
	# Large-payload TCP regression is covered by TestSystemE2E/compat full; upgrade stays a short data-path gate.
	verify_http "${host}" "${BACKEND_RESPONSE}" 60 || { log "FAIL: ${phase} HTTP data path failed"; return 1; }
	verify_tcp_http "${SERVER_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: ${phase} server_expose TCP data path failed"; return 1; }
	verify_udp_echo "${SERVER_UDP_PORT}" "${udp_payload}" 30 || { log "FAIL: ${phase} server_expose UDP data path failed"; return 1; }
	verify_socks5_http "${SERVER_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: ${phase} server_expose SOCKS5 data path failed"; return 1; }
	assert_server_expose_tcp_listeners "${phase}" || return 1
}

assert_new_server_expose_suite_works() {
	local token="$1" phase="$2" name_prefix="$3" host="$4" target_cid="$5"
	local http_tid tcp_tid udp_tid socks5_tid
	http_tid="$(create_server_expose_http "${token}" "${name_prefix}-http" "${host}" "${target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: ${phase} failed to create new HTTP tunnel"; return 1; }
	tcp_tid="$(create_server_expose_tcp "${token}" "${name_prefix}-tcp" "${SERVER_TCP_ALT_PORT}" "${target_cid}")"
	[ -z "${tcp_tid}" ] && { log "FAIL: ${phase} failed to create new server_expose TCP tunnel"; return 1; }
	udp_tid="$(create_server_expose_udp "${token}" "${name_prefix}-udp" "${SERVER_UDP_ALT_PORT}" "${target_cid}")"
	[ -z "${udp_tid}" ] && { log "FAIL: ${phase} failed to create new server_expose UDP tunnel"; return 1; }
	socks5_tid="$(create_server_expose_socks5 "${token}" "${name_prefix}-socks5" "${SERVER_SOCKS5_ALT_PORT}" "${target_cid}")"
	[ -z "${socks5_tid}" ] && { log "FAIL: ${phase} failed to create new server_expose SOCKS5 tunnel"; return 1; }

	wait_server_expose_suite_active "${token}" "${phase} new server_expose suite" "${http_tid}" "${tcp_tid}" "${udp_tid}" "${socks5_tid}" || return 1
	verify_http "${host}" "${BACKEND_RESPONSE}" 60 || { log "FAIL: ${phase} new HTTP data path failed"; return 1; }
	verify_tcp_http "${SERVER_TCP_ALT_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: ${phase} new server_expose TCP data path failed"; return 1; }
	verify_udp_echo "${SERVER_UDP_ALT_PORT}" "${phase} new server udp" 30 || { log "FAIL: ${phase} new server_expose UDP data path failed"; return 1; }
	verify_socks5_http "${SERVER_SOCKS5_ALT_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: ${phase} new server_expose SOCKS5 data path failed"; return 1; }
	assert_tcp_listener_count "server" "${SERVER_TCP_ALT_PORT}" 1 "${phase} new server_expose TCP" || return 1
	assert_listener_count "server" "${SERVER_UDP_ALT_PORT}" "udp" 1 "${phase} new server_expose UDP" || return 1
	assert_tcp_listener_count "server" "${SERVER_SOCKS5_ALT_PORT}" 1 "${phase} new server_expose SOCKS5" || return 1
}

# ---------- Image upgrade helper ----------

# upgrade_service <service> <new_image>
# Stops the service, sets the image env, and force-recreates with --no-deps --no-build.
# Verifies the container Config.Image matches the expected image.
upgrade_service() {
	local service="$1" new_image="$2"
	local old_container old_image
	old_container="$(compose ps -q "${service}" 2>/dev/null || true)"
	if [ -n "${old_container}" ]; then
		old_image="$(docker inspect "${old_container}" --format '{{.Config.Image}}' 2>/dev/null || echo "unknown")"
	else
		old_image="unknown"
	fi
	log "  upgrading ${service}: ${old_image} -> ${new_image}"

	compose stop "${service}"
	# The image env var must be set by the caller before calling this function.
	compose up -d --force-recreate --no-deps --no-build --remove-orphans "${service}"

	local actual_container actual_image
	actual_container="$(compose ps -q "${service}" 2>/dev/null || true)"
	actual_image="$(docker inspect "${actual_container}" --format '{{.Config.Image}}' 2>/dev/null || echo "unknown")"
	if [ "${actual_image}" != "${new_image}" ]; then
		log "  WARNING: ${service} Config.Image is '${actual_image}', expected '${new_image}'"
		return 1
	fi
	if [ "${actual_image}" = "${old_image}" ]; then
		log "  WARNING: ${service} Config.Image unchanged (${old_image}); upgrade may not have taken effect"
		return 1
	fi
	log "  ${service} image verified: ${actual_image}"
}

# ---------- Build images ----------

if ! docker image inspect "${E2E_CURRENT_IMAGE}" >/dev/null 2>&1; then
	log "building current image (${E2E_CURRENT_IMAGE})..."
	(cd "${NETSGO_E2E_DIR}" && make docker-build-e2e-current E2E_CURRENT_IMAGE="${E2E_CURRENT_IMAGE}")
fi
if ! docker image inspect "${E2E_STABLE_IMAGE}" >/dev/null 2>&1; then
	log "building stable image (${E2E_STABLE_IMAGE}) from ${COMPAT_BASELINE}..."
	bash "${NETSGO_E2E_DIR}/test/e2e/scripts/build-e2e-stable.sh" "${COMPAT_BASELINE}" "${E2E_STABLE_IMAGE}"
fi

log "============================================="
log "UPGRADE E2E HARNESS"
log "============================================="
log "baseline:        ${COMPAT_BASELINE}"
log "current image:   ${E2E_CURRENT_IMAGE}"
log "stable image:    ${E2E_STABLE_IMAGE}"
log "tools image:     ${NETSGO_E2E_TOOLS_IMAGE}"
log "proxy:           ${E2E_PROXY}"
log "NOTE: This is an upgrade data-path harness. In-flight stream continuity"
log "      and detailed auth/policy matrices are covered by separate tests."
log "      Set E2E_PLATFORM to override the default stable-image build platform."
log "============================================="

passed=0
failed=0
TUNNEL_HOST="upgrade-test.system.local"

# ============================================================
# case_server_only: upgrade server, keep stable clients
# Verifies: server_expose HTTP/TCP/UDP/SOCKS5 data paths
# ============================================================
case_server_only() {
	local project="${E2E_PROJECT_BASE}-server-only"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="srv-up.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: server-only (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  server -> current"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	local http_tid server_tcp_tid server_udp_tid server_socks5_tid
	http_tid="$(create_server_expose_http "${token}" "srv-only-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: failed to create HTTP tunnel"; return 1; }
	server_tcp_tid="$(create_server_expose_tcp "${token}" "srv-only-server-tcp" "${SERVER_TCP_PORT}" "${_target_cid}")"
	[ -z "${server_tcp_tid}" ] && { log "FAIL: failed to create server_expose TCP tunnel"; return 1; }
	server_udp_tid="$(create_server_expose_udp "${token}" "srv-only-server-udp" "${SERVER_UDP_PORT}" "${_target_cid}")"
	[ -z "${server_udp_tid}" ] && { log "FAIL: failed to create server_expose UDP tunnel"; return 1; }
	server_socks5_tid="$(create_server_expose_socks5 "${token}" "srv-only-server-socks5" "${SERVER_SOCKS5_PORT}" "${_target_cid}")"
	[ -z "${server_socks5_tid}" ] && { log "FAIL: failed to create server_expose SOCKS5 tunnel"; return 1; }
	wait_server_expose_suite_active "${token}" "baseline" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	verify_server_expose_suite "${tunnel_host}" "server only udp baseline" "baseline" || return 1
	log "baseline OK (HTTP + server TCP/UDP/SOCKS5)"

	# Upgrade server
	export NETSGO_SERVER_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "server" "${E2E_CURRENT_IMAGE}" || return 1

	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after server upgrade"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after server upgrade"; return 1; }
	wait_server_expose_suite_active "${token}" "after server upgrade" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	verify_server_expose_suite "${tunnel_host}" "server only udp after upgrade" "after server upgrade" || return 1
	assert_c2c_clean_reject "${token}" "after-server-only-upgrade" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1

	log "PASS: server-only upgrade (HTTP + server TCP/UDP/SOCKS5)"
	return 0
}

# ============================================================
# case_target_only: upgrade target-client, keep stable server+ingress
# Verifies: server_expose HTTP/TCP/UDP/SOCKS5 data paths
# ============================================================
case_target_only() {
	local project="${E2E_PROJECT_BASE}-target-only"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="tgt-up.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: target-client-only (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  target-client -> current"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	local http_tid server_tcp_tid server_udp_tid server_socks5_tid
	http_tid="$(create_server_expose_http "${token}" "tgt-only-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: failed to create HTTP tunnel"; return 1; }
	server_tcp_tid="$(create_server_expose_tcp "${token}" "tgt-only-server-tcp" "${SERVER_TCP_PORT}" "${_target_cid}")"
	[ -z "${server_tcp_tid}" ] && { log "FAIL: failed to create server_expose TCP tunnel"; return 1; }
	server_udp_tid="$(create_server_expose_udp "${token}" "tgt-only-server-udp" "${SERVER_UDP_PORT}" "${_target_cid}")"
	[ -z "${server_udp_tid}" ] && { log "FAIL: failed to create server_expose UDP tunnel"; return 1; }
	server_socks5_tid="$(create_server_expose_socks5 "${token}" "tgt-only-server-socks5" "${SERVER_SOCKS5_PORT}" "${_target_cid}")"
	[ -z "${server_socks5_tid}" ] && { log "FAIL: failed to create server_expose SOCKS5 tunnel"; return 1; }
	wait_server_expose_suite_active "${token}" "baseline" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	verify_server_expose_suite "${tunnel_host}" "target only udp baseline" "baseline" || return 1
	log "baseline OK (HTTP + server TCP/UDP/SOCKS5)"

	# Upgrade target-client
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "target-client" "${E2E_CURRENT_IMAGE}" || return 1

	wait_client_online "${TARGET_HOSTNAME}" "${token}" "${RECOVERY_TIMEOUT_SECONDS}" >/dev/null || { log "FAIL: target-client not online after upgrade"; return 1; }
	wait_server_expose_suite_active "${token}" "after target upgrade" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	verify_server_expose_suite "${tunnel_host}" "target only udp after upgrade" "after target upgrade" || return 1

	log "PASS: target-client-only upgrade (HTTP + server TCP/UDP/SOCKS5)"
	return 0
}

# ============================================================
# case_ingress_only: upgrade ingress-client, keep stable server+target
# Verifies: c2c TCP and SOCKS5 data paths
# ============================================================
case_ingress_only() {
	local project="${E2E_PROJECT_BASE}-ingress-only"
	local admin_pass
	admin_pass="$(random_admin_password)"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: ingress-client-only (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  ingress-client -> current"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	local tcp_tid socks5_tid
	tcp_tid="$(create_c2c_tcp "${token}" "ing-only-tcp" "${C2C_TCP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${tcp_tid}" ] && { log "FAIL: failed to create c2c TCP tunnel"; return 1; }
	socks5_tid="$(create_c2c_socks5 "${token}" "ing-only-socks5" "${C2C_SOCKS5_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${socks5_tid}" ] && { log "FAIL: failed to create c2c SOCKS5 tunnel"; return 1; }
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "baseline c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "baseline c2c SOCKS5" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c TCP data path failed"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c SOCKS5 data path failed"; return 1; }
	log "baseline c2c TCP/SOCKS5 OK"

	# Upgrade ingress-client
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "ingress-client" "${E2E_CURRENT_IMAGE}" || return 1

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: client pair not online after ingress upgrade"; return 1; }
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active after ingress upgrade"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active after ingress upgrade"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "after ingress upgrade c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "after ingress upgrade c2c SOCKS5" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c TCP data path broken after ingress upgrade"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c SOCKS5 data path broken after ingress upgrade"; return 1; }
	assert_c2c_clean_reject "${token}" "after-ingress-upgrade" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1

	log "PASS: ingress-client-only upgrade (c2c TCP/SOCKS5)"
	return 0
}

# ============================================================
# case_clients_only: upgrade both clients, keep stable server
# Verifies: old server + current clients keep existing HTTP + c2c TCP/SOCKS5 paths
# ============================================================
case_clients_only() {
	local project="${E2E_PROJECT_BASE}-clients-only"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="clients-up.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: clients-only (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  target-client + ingress-client -> current"
	log "  server:   remains stable"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	local http_tid tcp_tid socks5_tid
	http_tid="$(create_server_expose_http "${token}" "clients-up-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: failed to create HTTP tunnel"; return 1; }
	tcp_tid="$(create_c2c_tcp "${token}" "clients-up-tcp" "${C2C_TCP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${tcp_tid}" ] && { log "FAIL: failed to create c2c TCP tunnel"; return 1; }
	socks5_tid="$(create_c2c_socks5 "${token}" "clients-up-socks5" "${C2C_SOCKS5_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${socks5_tid}" ] && { log "FAIL: failed to create c2c SOCKS5 tunnel"; return 1; }

	wait_tunnel_active "${token}" "${http_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: HTTP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active"; return 1; }
	assert_tunnel_no_issues "${token}" "${http_tid}" "baseline HTTP" || return 1
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "baseline c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "baseline c2c SOCKS5" || return 1
	verify_http "${tunnel_host}" "${BACKEND_RESPONSE}" 60 || { log "FAIL: baseline HTTP data path failed"; return 1; }
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c TCP data path failed"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c SOCKS5 data path failed"; return 1; }
	log "baseline OK (HTTP + c2c TCP/SOCKS5)"

	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "target-client" "${E2E_CURRENT_IMAGE}" || return 1
	wait_client_online "${TARGET_HOSTNAME}" "${token}" "${RECOVERY_TIMEOUT_SECONDS}" >/dev/null || { log "FAIL: target-client not online after upgrade"; return 1; }

	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "ingress-client" "${E2E_CURRENT_IMAGE}" || return 1
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: client pair not online after clients upgrade"; return 1; }

	wait_tunnel_active "${token}" "${http_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: HTTP tunnel not active after clients upgrade"; return 1; }
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active after clients upgrade"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active after clients upgrade"; return 1; }
	assert_tunnel_no_issues "${token}" "${http_tid}" "after clients upgrade HTTP" || return 1
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "after clients upgrade c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "after clients upgrade c2c SOCKS5" || return 1
	verify_http "${tunnel_host}" "${BACKEND_RESPONSE}" 60 || { log "FAIL: HTTP data path broken after clients upgrade"; return 1; }
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c TCP data path broken after clients upgrade"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c SOCKS5 data path broken after clients upgrade"; return 1; }
	assert_c2c_clean_reject "${token}" "after-clients-upgrade" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1
	assert_new_server_expose_suite_works "${token}" "after-clients-upgrade" "clients-up-new" "new.${tunnel_host}" "${_target_cid}" || return 1

	log "PASS: clients-only upgrade (old server + current clients, existing HTTP + c2c TCP/SOCKS5 and new server_expose HTTP/TCP/UDP/SOCKS5)"
	return 0
}

# ============================================================
# case_server_rollback: upgrade server, then roll back to stable server image
# Verifies: upgraded server does not leave persisted tunnel data unreadable by stable
# ============================================================
case_server_rollback() {
	local project="${E2E_PROJECT_BASE}-server-rollback"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="rollback.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: server-rollback (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  server -> current"
	log "  rollback: server -> stable, same compose volume"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	local http_tid server_tcp_tid server_udp_tid server_socks5_tid
	http_tid="$(create_server_expose_http "${token}" "rollback-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: failed to create HTTP tunnel"; return 1; }
	server_tcp_tid="$(create_server_expose_tcp "${token}" "rollback-server-tcp" "${SERVER_TCP_PORT}" "${_target_cid}")"
	[ -z "${server_tcp_tid}" ] && { log "FAIL: failed to create server_expose TCP tunnel"; return 1; }
	server_udp_tid="$(create_server_expose_udp "${token}" "rollback-server-udp" "${SERVER_UDP_PORT}" "${_target_cid}")"
	[ -z "${server_udp_tid}" ] && { log "FAIL: failed to create server_expose UDP tunnel"; return 1; }
	server_socks5_tid="$(create_server_expose_socks5 "${token}" "rollback-server-socks5" "${SERVER_SOCKS5_PORT}" "${_target_cid}")"
	[ -z "${server_socks5_tid}" ] && { log "FAIL: failed to create server_expose SOCKS5 tunnel"; return 1; }
	wait_server_expose_suite_active "${token}" "baseline" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	verify_server_expose_suite "${tunnel_host}" "rollback udp baseline" "baseline" || return 1
	log "baseline OK (HTTP + server TCP/UDP/SOCKS5)"

	export NETSGO_SERVER_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "server" "${E2E_CURRENT_IMAGE}" || return 1

	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after server upgrade"; return 1; }
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after server upgrade"; return 1; }
	wait_server_expose_suite_active "${token}" "after server upgrade" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	verify_server_expose_suite "${tunnel_host}" "rollback udp after server upgrade" "after server upgrade" || return 1
	tunnel_action "${token}" "${http_tid}" "stop" || { log "FAIL: current server failed to stop tunnel before rollback"; return 1; }
	wait_tunnel_state "${token}" "${http_tid}" "idle" 60 || { log "FAIL: stopped tunnel did not become idle before rollback"; return 1; }
	tunnel_action "${token}" "${http_tid}" "resume" || { log "FAIL: current server failed to resume tunnel before rollback"; return 1; }
	wait_tunnel_active "${token}" "${http_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: resumed tunnel not active before rollback"; return 1; }
	verify_http "${tunnel_host}" "${BACKEND_RESPONSE}" 60 || { log "FAIL: HTTP data path broken after current stop/resume write"; return 1; }
	log "current server OK after stop/resume write"

	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	upgrade_service "server" "${E2E_STABLE_IMAGE}" || return 1

	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after server rollback"; return 1; }
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after server rollback"; return 1; }
	wait_server_expose_suite_active "${token}" "after server rollback" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	verify_server_expose_suite "${tunnel_host}" "rollback udp after server rollback" "after server rollback" || return 1
	assert_c2c_clean_reject "${token}" "after-server-rollback" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1
	assert_new_server_expose_suite_works "${token}" "after-server-rollback" "rollback-new" "new.${tunnel_host}" "${_target_cid}" || return 1

	log "PASS: server rollback (stable reads same data dir after current server, HTTP + server TCP/UDP/SOCKS5)"
	return 0
}

# ============================================================
# case_current_write_rollback: current server writes a tunnel, stable reads it
# Verifies: stable server can recover HTTP, server TCP/UDP/SOCKS5, and c2c TCP/UDP/SOCKS5 data
# persisted by current server
# ============================================================
case_current_write_rollback() {
	local project="${E2E_PROJECT_BASE}-current-write-rollback"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="current-write-rollback.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: current-write-rollback (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  server -> current"
	log "  write:    current server creates HTTP + server TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5 tunnels"
	log "  rollback: server -> stable, same compose volume"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1
	log "baseline stable stack OK"

	export NETSGO_SERVER_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "server" "${E2E_CURRENT_IMAGE}" || return 1

	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after server upgrade"; return 1; }
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after server upgrade"; return 1; }

	local http_tid server_tcp_tid server_udp_tid server_socks5_tid tcp_tid udp_tid socks5_tid
	http_tid="$(create_server_expose_http "${token}" "current-write-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: current server failed to create HTTP tunnel"; return 1; }
	server_tcp_tid="$(create_server_expose_tcp "${token}" "current-write-server-tcp" "${SERVER_TCP_PORT}" "${_target_cid}")"
	[ -z "${server_tcp_tid}" ] && { log "FAIL: current server failed to create server_expose TCP tunnel"; return 1; }
	server_udp_tid="$(create_server_expose_udp "${token}" "current-write-server-udp" "${SERVER_UDP_PORT}" "${_target_cid}")"
	[ -z "${server_udp_tid}" ] && { log "FAIL: current server failed to create server_expose UDP tunnel"; return 1; }
	server_socks5_tid="$(create_server_expose_socks5 "${token}" "current-write-server-socks5" "${SERVER_SOCKS5_PORT}" "${_target_cid}")"
	[ -z "${server_socks5_tid}" ] && { log "FAIL: current server failed to create server_expose SOCKS5 tunnel"; return 1; }
	tcp_tid="$(create_c2c_tcp "${token}" "current-write-c2c-tcp" "${C2C_TCP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${tcp_tid}" ] && { log "FAIL: current server failed to create c2c TCP tunnel"; return 1; }
	udp_tid="$(create_c2c_udp "${token}" "current-write-c2c-udp" "${C2C_UDP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${udp_tid}" ] && { log "FAIL: current server failed to create c2c UDP tunnel"; return 1; }
	socks5_tid="$(create_c2c_socks5 "${token}" "current-write-c2c-socks5" "${C2C_SOCKS5_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${socks5_tid}" ] && { log "FAIL: current server failed to create c2c SOCKS5 tunnel"; return 1; }

	wait_server_expose_suite_active "${token}" "current-created" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: current-created c2c TCP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: current-created c2c UDP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: current-created c2c SOCKS5 tunnel not active"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "current-created c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "current-created c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "current-created c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "current write udp before rollback" "current-created" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: current-created c2c TCP data path failed"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "current write c2c udp before rollback" 30 || { log "FAIL: current-created c2c UDP data path failed"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: current-created c2c SOCKS5 data path failed"; return 1; }
	log "current server write OK (HTTP + server TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5)"

	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	upgrade_service "server" "${E2E_STABLE_IMAGE}" || return 1

	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after server rollback"; return 1; }
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after server rollback"; return 1; }
	wait_server_expose_suite_active "${token}" "current-created after rollback" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: current-created c2c TCP tunnel not active after rollback"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: current-created c2c UDP tunnel not active after rollback"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: current-created c2c SOCKS5 tunnel not active after rollback"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "current-created c2c TCP after rollback" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "current-created c2c UDP after rollback" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "current-created c2c SOCKS5 after rollback" || return 1
	verify_server_expose_suite "${tunnel_host}" "current write udp after rollback" "current-created after rollback" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: current-created c2c TCP data path broken after rollback"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "current write c2c udp after rollback" 30 || { log "FAIL: current-created c2c UDP data path broken after rollback"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: current-created c2c SOCKS5 data path broken after rollback"; return 1; }
	assert_c2c_clean_reject "${token}" "after-current-write-rollback" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1
	assert_new_server_expose_suite_works "${token}" "after-current-write-rollback" "current-write-rollback-new" "new.${tunnel_host}" "${_target_cid}" || return 1

	log "PASS: current-write rollback (stable reads current-created HTTP + server TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5 tunnels)"
	return 0
}

# ============================================================
# case_all_upgrade: upgrade all components
# Verifies: server_expose HTTP/TCP/UDP/SOCKS5 + c2c TCP/SOCKS5
# ============================================================
case_all_upgrade() {
	local project="${E2E_PROJECT_BASE}-all-upgrade"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="all-up.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: all-upgrade (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  all -> current"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	# Create representative tunnel types
	local http_tid server_tcp_tid server_udp_tid server_socks5_tid tcp_tid udp_tid socks5_tid
	http_tid="$(create_server_expose_http "${token}" "all-up-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: failed to create HTTP tunnel"; return 1; }
	server_tcp_tid="$(create_server_expose_tcp "${token}" "all-up-server-tcp" "${SERVER_TCP_PORT}" "${_target_cid}")"
	[ -z "${server_tcp_tid}" ] && { log "FAIL: failed to create server_expose TCP tunnel"; return 1; }
	server_udp_tid="$(create_server_expose_udp "${token}" "all-up-server-udp" "${SERVER_UDP_PORT}" "${_target_cid}")"
	[ -z "${server_udp_tid}" ] && { log "FAIL: failed to create server_expose UDP tunnel"; return 1; }
	server_socks5_tid="$(create_server_expose_socks5 "${token}" "all-up-server-socks5" "${SERVER_SOCKS5_PORT}" "${_target_cid}")"
	[ -z "${server_socks5_tid}" ] && { log "FAIL: failed to create server_expose SOCKS5 tunnel"; return 1; }
	tcp_tid="$(create_c2c_tcp "${token}" "all-up-tcp" "${C2C_TCP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${tcp_tid}" ] && { log "FAIL: failed to create c2c TCP tunnel"; return 1; }
	udp_tid="$(create_c2c_udp "${token}" "all-up-udp" "${C2C_UDP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${udp_tid}" ] && { log "FAIL: failed to create c2c UDP tunnel"; return 1; }
	socks5_tid="$(create_c2c_socks5 "${token}" "all-up-socks5" "${C2C_SOCKS5_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${socks5_tid}" ] && { log "FAIL: failed to create c2c SOCKS5 tunnel"; return 1; }

	wait_server_expose_suite_active "${token}" "baseline" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c UDP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "baseline c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "baseline c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "baseline c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "server udp baseline" "baseline" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c TCP data path failed"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "all upgrade c2c udp baseline" 30 || { log "FAIL: baseline c2c UDP data path failed"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c SOCKS5 data path failed"; return 1; }
	log "baseline OK (HTTP + server TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5)"

	# Upgrade all: server first, then clients
	export NETSGO_SERVER_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "server" "${E2E_CURRENT_IMAGE}" || return 1

	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after server upgrade"; return 1; }
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after server upgrade"; return 1; }
	wait_server_expose_suite_active "${token}" "after server upgrade before client upgrades" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active after server upgrade before client upgrades"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c UDP tunnel not active after server upgrade before client upgrades"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active after server upgrade before client upgrades"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "after server upgrade before client upgrades c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "after server upgrade before client upgrades c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "after server upgrade before client upgrades c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "server udp after server-only phase" "after server upgrade before client upgrades" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c TCP data path broken after server upgrade before client upgrades"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "all upgrade c2c udp after server-only phase" 30 || { log "FAIL: c2c UDP data path broken after server upgrade before client upgrades"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c SOCKS5 data path broken after server upgrade before client upgrades"; return 1; }

	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "target-client" "${E2E_CURRENT_IMAGE}" || return 1
	wait_client_online "${TARGET_HOSTNAME}" "${token}" "${RECOVERY_TIMEOUT_SECONDS}" >/dev/null || { log "FAIL: target-client not online after upgrade"; return 1; }

	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "ingress-client" "${E2E_CURRENT_IMAGE}" || return 1
	wait_client_online "${INGRESS_HOSTNAME}" "${token}" "${RECOVERY_TIMEOUT_SECONDS}" >/dev/null || { log "FAIL: ingress-client not online after upgrade"; return 1; }

	# Verify data paths
	wait_server_expose_suite_active "${token}" "after full upgrade" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active after full upgrade"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c UDP tunnel not active after full upgrade"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active after full upgrade"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "after full upgrade c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "after full upgrade c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "after full upgrade c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "server udp after full upgrade" "after full upgrade" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c TCP data path broken after full upgrade"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "all upgrade c2c udp after full upgrade" 30 || { log "FAIL: c2c UDP data path broken after full upgrade"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c SOCKS5 data path broken after full upgrade"; return 1; }
	assert_c2c_clean_reject "${token}" "after-full-upgrade" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1

	log "PASS: all-upgrade (HTTP + server TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5 verified)"
	return 0
}

# ============================================================
# case_client_first_rolling: upgrade clients first, then server
# Verifies: client-first rolling order keeps existing server-expose and c2c paths
# ============================================================
case_client_first_rolling() {
	local project="${E2E_PROJECT_BASE}-client-first-rolling"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="client-first.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: client-first-rolling (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  target-client + ingress-client -> current, then server -> current"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	local http_tid server_tcp_tid server_udp_tid server_socks5_tid tcp_tid udp_tid socks5_tid
	http_tid="$(create_server_expose_http "${token}" "client-first-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: failed to create HTTP tunnel"; return 1; }
	server_tcp_tid="$(create_server_expose_tcp "${token}" "client-first-server-tcp" "${SERVER_TCP_PORT}" "${_target_cid}")"
	[ -z "${server_tcp_tid}" ] && { log "FAIL: failed to create server_expose TCP tunnel"; return 1; }
	server_udp_tid="$(create_server_expose_udp "${token}" "client-first-server-udp" "${SERVER_UDP_PORT}" "${_target_cid}")"
	[ -z "${server_udp_tid}" ] && { log "FAIL: failed to create server_expose UDP tunnel"; return 1; }
	server_socks5_tid="$(create_server_expose_socks5 "${token}" "client-first-server-socks5" "${SERVER_SOCKS5_PORT}" "${_target_cid}")"
	[ -z "${server_socks5_tid}" ] && { log "FAIL: failed to create server_expose SOCKS5 tunnel"; return 1; }
	tcp_tid="$(create_c2c_tcp "${token}" "client-first-tcp" "${C2C_TCP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${tcp_tid}" ] && { log "FAIL: failed to create c2c TCP tunnel"; return 1; }
	udp_tid="$(create_c2c_udp "${token}" "client-first-udp" "${C2C_UDP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${udp_tid}" ] && { log "FAIL: failed to create c2c UDP tunnel"; return 1; }
	socks5_tid="$(create_c2c_socks5 "${token}" "client-first-socks5" "${C2C_SOCKS5_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${socks5_tid}" ] && { log "FAIL: failed to create c2c SOCKS5 tunnel"; return 1; }
	wait_server_expose_suite_active "${token}" "baseline" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: baseline c2c TCP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: baseline c2c UDP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: baseline c2c SOCKS5 tunnel not active"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "baseline c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "baseline c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "baseline c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "client first server udp baseline" "baseline" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c TCP data path failed"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "client first c2c udp baseline" 30 || { log "FAIL: baseline c2c UDP data path failed"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c SOCKS5 data path failed"; return 1; }

	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "target-client" "${E2E_CURRENT_IMAGE}" || return 1
	wait_client_online "${TARGET_HOSTNAME}" "${token}" "${RECOVERY_TIMEOUT_SECONDS}" >/dev/null || { log "FAIL: target-client not online after client-first target upgrade"; return 1; }
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "ingress-client" "${E2E_CURRENT_IMAGE}" || return 1
	wait_client_online "${INGRESS_HOSTNAME}" "${token}" "${RECOVERY_TIMEOUT_SECONDS}" >/dev/null || { log "FAIL: ingress-client not online after client-first ingress upgrade"; return 1; }

	wait_server_expose_suite_active "${token}" "after client upgrades" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active after client upgrades"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c UDP tunnel not active after client upgrades"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active after client upgrades"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "after client upgrades c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "after client upgrades c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "after client upgrades c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "client first server udp after client upgrades" "after client upgrades" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c TCP data path broken after client upgrades"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "client first c2c udp after client upgrades" 30 || { log "FAIL: c2c UDP data path broken after client upgrades"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c SOCKS5 data path broken after client upgrades"; return 1; }

	export NETSGO_SERVER_IMAGE="${E2E_CURRENT_IMAGE}"
	upgrade_service "server" "${E2E_CURRENT_IMAGE}" || return 1
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after final server upgrade"; return 1; }
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after final server upgrade"; return 1; }
	wait_server_expose_suite_active "${token}" "after client-first rolling" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active after client-first rolling upgrade"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c UDP tunnel not active after client-first rolling upgrade"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active after client-first rolling upgrade"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "after client-first rolling c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "after client-first rolling c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "after client-first rolling c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "client first server udp after rolling upgrade" "after client-first rolling" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c TCP data path broken after client-first rolling upgrade"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "client first c2c udp after rolling upgrade" 30 || { log "FAIL: c2c UDP data path broken after client-first rolling upgrade"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c SOCKS5 data path broken after client-first rolling upgrade"; return 1; }
	assert_c2c_clean_reject "${token}" "after-client-first-rolling" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1

	log "PASS: client-first rolling upgrade (server-expose HTTP/TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5)"
	return 0
}

# ============================================================
# case_full_cold_upgrade: stop all stable processes, then start all current
# Verifies: same persisted server/client volumes recover after cold replacement
# ============================================================
case_full_cold_upgrade() {
	local project="${E2E_PROJECT_BASE}-full-cold-upgrade"
	local admin_pass tunnel_host
	admin_pass="$(random_admin_password)"
	tunnel_host="cold-up.${TUNNEL_HOST}"

	set_project "${project}"
	log ""
	log "============================================="
	log "Case: full-cold-upgrade (project=${project})"
	log "  baseline: stable/stable/stable"
	log "  upgrade:  stop all, then start server + clients as current with same volumes"
	log "============================================="

	export NETSGO_ADMIN_USER="${ADMIN_USER}"
	export NETSGO_ADMIN_PASS="${admin_pass}"
	export NETSGO_SERVER_ADDR="http://${MANAGEMENT_HOST}"
	export NETSGO_TARGET_CLIENT_HOSTNAME="${TARGET_HOSTNAME}"
	export NETSGO_INGRESS_CLIENT_HOSTNAME="${INGRESS_HOSTNAME}"
	export NETSGO_E2E_TOOLS_IMAGE
	export NETSGO_SERVER_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_STABLE_IMAGE}"

	compose down -v --remove-orphans 2>/dev/null || true
	compose up -d --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: baseline compose up failed"; return 1; }

	local token
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed on stable server"; return 1; }

	local client_key
	client_key="$(create_api_key "${token}")"
	[ -z "${client_key}" ] && { log "FAIL: failed to create API key"; return 1; }

	export NETSGO_CLIENT_KEY="${client_key}"
	compose up -d --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: client compose up failed"; return 1; }

	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || return 1

	local http_tid server_tcp_tid server_udp_tid server_socks5_tid tcp_tid udp_tid socks5_tid
	http_tid="$(create_server_expose_http "${token}" "cold-up-http" "${tunnel_host}" "${_target_cid}")"
	[ -z "${http_tid}" ] && { log "FAIL: failed to create HTTP tunnel"; return 1; }
	server_tcp_tid="$(create_server_expose_tcp "${token}" "cold-up-server-tcp" "${SERVER_TCP_PORT}" "${_target_cid}")"
	[ -z "${server_tcp_tid}" ] && { log "FAIL: failed to create server_expose TCP tunnel"; return 1; }
	server_udp_tid="$(create_server_expose_udp "${token}" "cold-up-server-udp" "${SERVER_UDP_PORT}" "${_target_cid}")"
	[ -z "${server_udp_tid}" ] && { log "FAIL: failed to create server_expose UDP tunnel"; return 1; }
	server_socks5_tid="$(create_server_expose_socks5 "${token}" "cold-up-server-socks5" "${SERVER_SOCKS5_PORT}" "${_target_cid}")"
	[ -z "${server_socks5_tid}" ] && { log "FAIL: failed to create server_expose SOCKS5 tunnel"; return 1; }
	tcp_tid="$(create_c2c_tcp "${token}" "cold-up-tcp" "${C2C_TCP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${tcp_tid}" ] && { log "FAIL: failed to create c2c TCP tunnel"; return 1; }
	udp_tid="$(create_c2c_udp "${token}" "cold-up-udp" "${C2C_UDP_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${udp_tid}" ] && { log "FAIL: failed to create c2c UDP tunnel"; return 1; }
	socks5_tid="$(create_c2c_socks5 "${token}" "cold-up-socks5" "${C2C_SOCKS5_PORT}" "${_ingress_cid}" "${_target_cid}")"
	[ -z "${socks5_tid}" ] && { log "FAIL: failed to create c2c SOCKS5 tunnel"; return 1; }
	wait_server_expose_suite_active "${token}" "baseline" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: baseline c2c TCP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: baseline c2c UDP tunnel not active"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: baseline c2c SOCKS5 tunnel not active"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "baseline c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "baseline c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "baseline c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "cold upgrade server udp baseline" "baseline" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c TCP data path failed"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "cold upgrade c2c udp baseline" 30 || { log "FAIL: baseline c2c UDP data path failed"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: baseline c2c SOCKS5 data path failed"; return 1; }

	compose stop ingress-client target-client server

	export NETSGO_SERVER_IMAGE="${E2E_CURRENT_IMAGE}"
	export NETSGO_TARGET_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${E2E_CURRENT_IMAGE}"
	compose up -d --force-recreate --no-build --remove-orphans server proxy tcp-backend tcp-backend-alt tcp-backend-slow udp-backend \
		|| { log "FAIL: current server compose up failed during cold upgrade"; return 1; }
	token="$(login_admin "${admin_pass}")"
	[ -z "${token}" ] && { log "FAIL: admin login failed after cold server replacement"; return 1; }
	compose up -d --force-recreate --no-build --remove-orphans target-client ingress-client \
		|| { log "FAIL: current clients compose up failed during cold upgrade"; return 1; }
	wait_client_pair "${token}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: clients did not reconnect after cold upgrade"; return 1; }
	wait_server_expose_suite_active "${token}" "after cold upgrade" "${http_tid}" "${server_tcp_tid}" "${server_udp_tid}" "${server_socks5_tid}" || return 1
	wait_tunnel_active "${token}" "${tcp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c TCP tunnel not active after cold upgrade"; return 1; }
	wait_tunnel_active "${token}" "${udp_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c UDP tunnel not active after cold upgrade"; return 1; }
	wait_tunnel_active "${token}" "${socks5_tid}" "${RECOVERY_TIMEOUT_SECONDS}" || { log "FAIL: c2c SOCKS5 tunnel not active after cold upgrade"; return 1; }
	assert_tunnel_no_issues "${token}" "${tcp_tid}" "after cold upgrade c2c TCP" || return 1
	assert_tunnel_no_issues "${token}" "${udp_tid}" "after cold upgrade c2c UDP" || return 1
	assert_tunnel_no_issues "${token}" "${socks5_tid}" "after cold upgrade c2c SOCKS5" || return 1
	verify_server_expose_suite "${tunnel_host}" "cold upgrade server udp after restart" "after cold upgrade" || return 1
	verify_tcp_http "${C2C_TCP_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c TCP data path broken after cold upgrade"; return 1; }
	verify_udp_echo "${C2C_UDP_PORT}" "cold upgrade c2c udp after restart" 30 || { log "FAIL: c2c UDP data path broken after cold upgrade"; return 1; }
	verify_socks5_http "${C2C_SOCKS5_PORT}" "${BACKEND_HOST}" "${BACKEND_RESPONSE}" 30 || { log "FAIL: c2c SOCKS5 data path broken after cold upgrade"; return 1; }
	assert_c2c_clean_reject "${token}" "after-cold-upgrade" "${_ingress_cid}" "${_target_cid}" "${C2C_TCP_ALT_PORT}" || return 1

	log "PASS: full cold upgrade (server-expose HTTP/TCP/UDP/SOCKS5 + c2c TCP/UDP/SOCKS5)"
	return 0
}

# ========== Run all cases ==========

run_case() {
	local name="$1"
	if "${name}"; then
		passed=$((passed + 1))
	else
		log "FAIL: ${name}"
		failed=$((failed + 1))
		dump_current
	fi
	cleanup_current
	unset _target_cid _ingress_cid
	PROJECT_NAME=""
}

run_case case_server_only
run_case case_target_only
run_case case_ingress_only
run_case case_clients_only
run_case case_server_rollback
run_case case_current_write_rollback
run_case case_all_upgrade
run_case case_client_first_rolling
run_case case_full_cold_upgrade

# ========== Summary ==========

total=$((passed + failed))
log ""
log "============================================="
log "UPGRADE E2E SUMMARY"
log "============================================="
log "passed: ${passed}/${total}"
log "failed: ${failed}/${total}"
log ""
log "NOTE: Results cover upgrade data paths for HTTP, TCP, UDP, SOCKS5,"
log "      rollback, current-write rollback, rolling, and cold-upgrade cases."
log "      In-flight stream continuity and detailed auth/policy matrices are covered elsewhere."
log "============================================="

[ "${failed}" -eq 0 ]
