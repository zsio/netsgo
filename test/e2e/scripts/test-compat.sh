#!/usr/bin/env bash
# test-compat.sh: cross-version compatibility E2E harness.
#
# Builds or verifies current and stable images, then runs a matrix of
# server/target-client/ingress-client image combinations.
#
# COMPAT_MODE=full runs TestSystemE2E against each fresh mixed-version stack,
# then runs focused TestSystemSingleTargetClientE2E and
# TestSystemClientToClientCleanRejectE2E scenarios. It intentionally does not
# run every TestSystem*E2E test for every matrix row; focused cases are separate
# matrix rows to keep the full compatibility gate bounded.
# COMPAT_MODE=smoke verifies stack startup and client connectivity only.
#
# Required env (from Makefile): E2E_BASE_COMPOSE, E2E_PROXY_COMPOSE,
#   PROXY_PORT, UPSTREAM_PORT, port env vars, COMPAT_BASELINE,
#   E2E_CURRENT_IMAGE, E2E_STABLE_IMAGE, NETSGO_E2E_TOOLS_IMAGE.
set -euo pipefail

E2E_PROXY="${E2E_PROXY:-nginx}"
E2E_PROJECT_BASE="${E2E_PROJECT:-netsgo-compat}"
E2E_BASE_COMPOSE="${E2E_BASE_COMPOSE:?E2E_BASE_COMPOSE is required}"
E2E_PROXY_COMPOSE="${E2E_PROXY_COMPOSE:?E2E_PROXY_COMPOSE is required}"
PROXY_PORT="${PROXY_PORT:-19080}"
UPSTREAM_PORT="${UPSTREAM_PORT:-19081}"
COMPAT_BASELINE="${COMPAT_BASELINE:-v0.1.8}"
E2E_CURRENT_IMAGE="${E2E_CURRENT_IMAGE:-netsgo-e2e:current}"
E2E_STABLE_IMAGE="${E2E_STABLE_IMAGE:-netsgo-e2e:${COMPAT_BASELINE}}"
NETSGO_E2E_TOOLS_IMAGE="${NETSGO_E2E_TOOLS_IMAGE:-${E2E_STABLE_IMAGE}}"
NETSGO_E2E_DIR="${NETSGO_E2E_DIR:-.}"

log() { echo "[compat] $*"; }

random_admin_password() {
	printf 'NetsGo1-%s' "$(openssl rand -hex 12 2>/dev/null || uuidgen)"
}

for cmd in docker jq curl; do
	command -v "${cmd}" >/dev/null 2>&1 || { log "ERROR: ${cmd} is required"; exit 1; }
done

# ---------- Build / verify images ----------

if ! docker image inspect "${E2E_CURRENT_IMAGE}" >/dev/null 2>&1; then
	log "building current image (${E2E_CURRENT_IMAGE})..."
	(cd "${NETSGO_E2E_DIR}" && make docker-build-e2e-current E2E_CURRENT_IMAGE="${E2E_CURRENT_IMAGE}")
else
	log "current image exists: ${E2E_CURRENT_IMAGE}"
fi

if ! docker image inspect "${E2E_STABLE_IMAGE}" >/dev/null 2>&1; then
	log "building stable image (${E2E_STABLE_IMAGE}) from ${COMPAT_BASELINE}..."
	bash "${NETSGO_E2E_DIR}/test/e2e/scripts/build-e2e-stable.sh" "${COMPAT_BASELINE}" "${E2E_STABLE_IMAGE}"
else
	log "stable image exists: ${E2E_STABLE_IMAGE}"
fi

# ---------- Shared env for smoke / e2e ----------

SMOKE_BASE_COMPOSE="${E2E_BASE_COMPOSE}"
SMOKE_PROXY_COMPOSE="${E2E_PROXY_COMPOSE}"
SMOKE_PROXY_PORT="${PROXY_PORT}"
export SMOKE_BASE_COMPOSE SMOKE_PROXY_COMPOSE SMOKE_PROXY_PORT
export NETSGO_E2E_TOOLS_IMAGE

run_smoke() {
	local project="$1"
	SMOKE_PROJECT="${project}" \
	SMOKE_ADMIN_PASS="$(random_admin_password)" \
	bash "${NETSGO_E2E_DIR}/test/e2e/scripts/smoke-system.sh"
}

run_main_system_e2e() {
	local project="$1"
	local admin_pass
	admin_pass="$(random_admin_password)"
	(cd "${NETSGO_E2E_DIR}" && \
	NETSGO_E2E_COMPOSE_PROJECT="${project}" \
	NETSGO_E2E_COMPOSE_FILES="${E2E_BASE_COMPOSE},${E2E_PROXY_COMPOSE}" \
	NETSGO_ADMIN_PASS="${admin_pass}" \
	NETSGO_E2E_COMPOSE_BUILD=0 \
	PROXY_PORT="${PROXY_PORT}" \
	UPSTREAM_PORT="${UPSTREAM_PORT}" \
	SERVER_TCP_PORT="${SERVER_TCP_PORT:-19093}" \
	SERVER_UDP_PORT="${SERVER_UDP_PORT:-19094}" \
	SERVER_SOCKS5_PORT="${SERVER_SOCKS5_PORT:-19095}" \
	SERVER_TCP_ALT_PORT="${SERVER_TCP_ALT_PORT:-19104}" \
	SERVER_UDP_ALT_PORT="${SERVER_UDP_ALT_PORT:-19105}" \
	SERVER_SOCKS5_ALT_PORT="${SERVER_SOCKS5_ALT_PORT:-19106}" \
	C2C_SOCKS5_PORT="${C2C_SOCKS5_PORT:-19096}" \
	C2C_SOCKS5_DENY_PORT="${C2C_SOCKS5_DENY_PORT:-19097}" \
	C2C_TCP_PORT="${C2C_TCP_PORT:-19098}" \
	C2C_TCP_ALT_PORT="${C2C_TCP_ALT_PORT:-19099}" \
	C2C_TCP_SLOW_PORT="${C2C_TCP_SLOW_PORT:-19100}" \
	C2C_UDP_PORT="${C2C_UDP_PORT:-19101}" \
	C2C_SOCKS5_AUTH_PORT="${C2C_SOCKS5_AUTH_PORT:-19102}" \
	C2C_SOCKS5_SOURCE_DENY_PORT="${C2C_SOCKS5_SOURCE_DENY_PORT:-19103}" \
	go test -tags=e2e ./test/e2e -run '^TestSystemE2E$' -count=1 -timeout 20m)
}

run_single_target_e2e() {
	local project="$1"
	local admin_pass
	admin_pass="$(random_admin_password)"
	(cd "${NETSGO_E2E_DIR}" && \
	NETSGO_E2E_COMPOSE_PROJECT="${project}" \
	NETSGO_E2E_COMPOSE_FILES="${E2E_BASE_COMPOSE},${E2E_PROXY_COMPOSE}" \
	NETSGO_ADMIN_PASS="${admin_pass}" \
	NETSGO_E2E_COMPOSE_BUILD=0 \
	PROXY_PORT="${PROXY_PORT}" \
	UPSTREAM_PORT="${UPSTREAM_PORT}" \
	SERVER_TCP_PORT="${SERVER_TCP_PORT:-19093}" \
	SERVER_UDP_PORT="${SERVER_UDP_PORT:-19094}" \
	SERVER_SOCKS5_PORT="${SERVER_SOCKS5_PORT:-19095}" \
	go test -tags=e2e ./test/e2e -run TestSystemSingleTargetClientE2E -count=1 -timeout 10m)
}

run_c2c_clean_reject_e2e() {
	local project="$1"
	local admin_pass
	admin_pass="$(random_admin_password)"
	(cd "${NETSGO_E2E_DIR}" && \
	NETSGO_E2E_COMPOSE_PROJECT="${project}" \
	NETSGO_E2E_COMPOSE_FILES="${E2E_BASE_COMPOSE},${E2E_PROXY_COMPOSE}" \
	NETSGO_ADMIN_PASS="${admin_pass}" \
	NETSGO_E2E_COMPOSE_BUILD=0 \
	PROXY_PORT="${PROXY_PORT}" \
	UPSTREAM_PORT="${UPSTREAM_PORT}" \
	SERVER_TCP_PORT="${SERVER_TCP_PORT:-19093}" \
	SERVER_UDP_PORT="${SERVER_UDP_PORT:-19094}" \
	SERVER_SOCKS5_PORT="${SERVER_SOCKS5_PORT:-19095}" \
	C2C_SOCKS5_PORT="${C2C_SOCKS5_PORT:-19096}" \
	C2C_SOCKS5_DENY_PORT="${C2C_SOCKS5_DENY_PORT:-19097}" \
	C2C_TCP_PORT="${C2C_TCP_PORT:-19098}" \
	C2C_TCP_ALT_PORT="${C2C_TCP_ALT_PORT:-19099}" \
	C2C_TCP_SLOW_PORT="${C2C_TCP_SLOW_PORT:-19100}" \
	C2C_UDP_PORT="${C2C_UDP_PORT:-19101}" \
	C2C_SOCKS5_AUTH_PORT="${C2C_SOCKS5_AUTH_PORT:-19102}" \
	C2C_SOCKS5_SOURCE_DENY_PORT="${C2C_SOCKS5_SOURCE_DENY_PORT:-19103}" \
		go test -tags=e2e ./test/e2e -run TestSystemClientToClientCleanRejectE2E -count=1 -timeout 10m)
}

# ---------- Matrix ----------
#
# Format: label|server_image|target_client_image|ingress_client_image
#
# Labels must not contain '+' (used as compose project name fragments).
# This matrix covers the combinations required by the compatibility plan.
# Smoke mode is intentionally light. Full mode creates tunnels and verifies
# data paths through the Go system E2E test driver.

scenarios=(
	"current-all|${E2E_CURRENT_IMAGE}|${E2E_CURRENT_IMAGE}|${E2E_CURRENT_IMAGE}"
	"stable-all|${E2E_STABLE_IMAGE}|${E2E_STABLE_IMAGE}|${E2E_STABLE_IMAGE}"
	"current-server-stable-target|${E2E_CURRENT_IMAGE}|${E2E_STABLE_IMAGE}|${E2E_CURRENT_IMAGE}"
	"current-server-stable-ingress|${E2E_CURRENT_IMAGE}|${E2E_CURRENT_IMAGE}|${E2E_STABLE_IMAGE}"
	"current-server-stable-both|${E2E_CURRENT_IMAGE}|${E2E_STABLE_IMAGE}|${E2E_STABLE_IMAGE}"
	"stable-server-current-target-stable-ingress|${E2E_STABLE_IMAGE}|${E2E_CURRENT_IMAGE}|${E2E_STABLE_IMAGE}"
	"stable-server-stable-target-current-ingress|${E2E_STABLE_IMAGE}|${E2E_STABLE_IMAGE}|${E2E_CURRENT_IMAGE}"
	"stable-server-current-both|${E2E_STABLE_IMAGE}|${E2E_CURRENT_IMAGE}|${E2E_CURRENT_IMAGE}"
)

single_target_scenarios=(
	"stable-server-current-single-target|${E2E_STABLE_IMAGE}|${E2E_CURRENT_IMAGE}"
)

c2c_clean_reject_scenarios=(
	"stable-target-current-ingress-clean-reject|${E2E_STABLE_IMAGE}|${E2E_STABLE_IMAGE}|${E2E_CURRENT_IMAGE}"
	"stable-server-current-both-clean-reject|${E2E_STABLE_IMAGE}|${E2E_CURRENT_IMAGE}|${E2E_CURRENT_IMAGE}"
)

MODE="${COMPAT_MODE:-full}"
ABORT_ON_FAILURE="${COMPAT_ABORT_ON_FAILURE:-false}"
case "${MODE}" in
	smoke|full) ;;
	*)
		log "ERROR: unsupported COMPAT_MODE=${MODE}; expected smoke or full"
		exit 1
		;;
esac
passed=0
failed=0
total=${#scenarios[@]}
if [ "${MODE}" = "full" ]; then
	total=$((total + ${#single_target_scenarios[@]} + ${#c2c_clean_reject_scenarios[@]}))
fi

log "============================================="
log "COMPATIBILITY E2E HARNESS"
log "============================================="
log "baseline:        ${COMPAT_BASELINE}"
log "current image:   ${E2E_CURRENT_IMAGE}"
log "stable image:    ${E2E_STABLE_IMAGE}"
log "proxy:           ${E2E_PROXY}"
log "mode:            ${MODE}"
log "scenarios:       ${total}"
log "NOTE: smoke mode checks startup/connectivity."
log "      full mode runs main TestSystemE2E matrix plus focused single-target and c2c clean-reject cases."
log "============================================="

for scenario in "${scenarios[@]}"; do
	IFS='|' read -r label server_img target_img ingress_img <<< "${scenario}"
	project="${E2E_PROJECT_BASE}-${label}"

	log "---------------------------------------------"
	log "scenario:    ${label}"
	log "  baseline:        ${COMPAT_BASELINE}"
	log "  server:          ${server_img}"
	log "  target-client:   ${target_img}"
	log "  ingress-client:  ${ingress_img}"
	log "  project name:    ${project}"

	export NETSGO_SERVER_IMAGE="${server_img}"
	export NETSGO_TARGET_CLIENT_IMAGE="${target_img}"
	export NETSGO_INGRESS_CLIENT_IMAGE="${ingress_img}"
	export NETSGO_E2E_TOOLS_IMAGE="${server_img}"
	log "  tools image:     ${NETSGO_E2E_TOOLS_IMAGE}"

	if [ "${MODE}" = "full" ]; then
		if run_main_system_e2e "${project}"; then
			log "PASS: ${label} (full E2E, --no-build)"
			passed=$((passed + 1))
		else
			log "FAIL: ${label} (full E2E)"
			failed=$((failed + 1))
			[ "${ABORT_ON_FAILURE}" = "true" ] && break
		fi
	else
		if run_smoke "${project}"; then
			log "PASS: ${label} (smoke connectivity only: stack up + clients online, --no-build)"
			passed=$((passed + 1))
		else
			log "FAIL: ${label}"
			failed=$((failed + 1))
			[ "${ABORT_ON_FAILURE}" = "true" ] && break
		fi
	fi
done

if [ "${MODE}" = "full" ]; then
	for scenario in "${single_target_scenarios[@]}"; do
		IFS='|' read -r label server_img target_img <<< "${scenario}"
		project="${E2E_PROJECT_BASE}-${label}"

		log "---------------------------------------------"
		log "scenario:    ${label}"
		log "  baseline:        ${COMPAT_BASELINE}"
		log "  server:          ${server_img}"
		log "  target-client:   ${target_img}"
		log "  ingress-client:  <not started>"
		log "  project name:    ${project}"

		export NETSGO_SERVER_IMAGE="${server_img}"
		export NETSGO_TARGET_CLIENT_IMAGE="${target_img}"
		export NETSGO_INGRESS_CLIENT_IMAGE="${target_img}"
		export NETSGO_E2E_TOOLS_IMAGE="${server_img}"
		log "  tools image:     ${NETSGO_E2E_TOOLS_IMAGE}"

		if run_single_target_e2e "${project}"; then
			log "PASS: ${label} (single target-client server_expose E2E, --no-build)"
			passed=$((passed + 1))
		else
			log "FAIL: ${label} (single target-client E2E)"
			failed=$((failed + 1))
			[ "${ABORT_ON_FAILURE}" = "true" ] && break
		fi
	done
fi

if [ "${MODE}" = "full" ]; then
	for scenario in "${c2c_clean_reject_scenarios[@]}"; do
		IFS='|' read -r label server_img target_img ingress_img <<< "${scenario}"
		project="${E2E_PROJECT_BASE}-${label}"

		log "---------------------------------------------"
		log "scenario:    ${label}"
		log "  baseline:        ${COMPAT_BASELINE}"
		log "  server:          ${server_img}"
		log "  target-client:   ${target_img}"
		log "  ingress-client:  ${ingress_img}"
		log "  project name:    ${project}"

		export NETSGO_SERVER_IMAGE="${server_img}"
		export NETSGO_TARGET_CLIENT_IMAGE="${target_img}"
		export NETSGO_INGRESS_CLIENT_IMAGE="${ingress_img}"
		export NETSGO_E2E_TOOLS_IMAGE="${server_img}"
		log "  tools image:     ${NETSGO_E2E_TOOLS_IMAGE}"

		if run_c2c_clean_reject_e2e "${project}"; then
			log "PASS: ${label} (c2c clean-reject E2E, --no-build)"
			passed=$((passed + 1))
		else
			log "FAIL: ${label} (c2c clean-reject E2E)"
			failed=$((failed + 1))
			[ "${ABORT_ON_FAILURE}" = "true" ] && break
		fi
	done
fi

log "============================================="
log "COMPATIBILITY E2E SUMMARY"
log "============================================="
log "passed: ${passed}/${total}"
log "failed: ${failed}/${total}"
if [ "${MODE}" = "smoke" ]; then
	log ""
	log "NOTE: Results reflect smoke harness only."
	log "      Re-run with COMPAT_MODE=full for tunnel/data-path assertions."
fi
log "============================================="

[ "${failed}" -eq 0 ]
