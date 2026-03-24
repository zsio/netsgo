#!/bin/sh
set -eu

BASE_URL="${NETSGO_BOOTSTRAP_BASE_URL:-http://proxy}"
SETUP_TOKEN="${NETSGO_SETUP_TOKEN:-}"
ADMIN_USER="${NETSGO_ADMIN_USER:-admin}"
ADMIN_PASS="${NETSGO_ADMIN_PASS:-password123}"
SERVER_ADDR="${NETSGO_SERVER_ADDR:-http://panel.compose.local}"
CLIENT_HOSTNAME="${NETSGO_CLIENT_HOSTNAME:-compose-client}"
CLIENT_KEY_FILE="${NETSGO_CLIENT_KEY_FILE:-/shared/client.key}"
ADMIN_TOKEN_FILE="${NETSGO_ADMIN_TOKEN_FILE:-/shared/admin.token}"
READY_FILE="${NETSGO_READY_FILE:-/shared/bootstrap.ready}"
TUNNEL_NAME="${NETSGO_TUNNEL_NAME:-compose-tunnel}"
TUNNEL_TYPE="${NETSGO_TUNNEL_TYPE:-http}"
TUNNEL_DOMAIN="${NETSGO_TUNNEL_DOMAIN:-app.compose.local}"
TUNNEL_REMOTE_PORT="${NETSGO_TUNNEL_REMOTE_PORT:-19082}"
TUNNEL_LOCAL_IP="${NETSGO_TUNNEL_LOCAL_IP:-backend}"
TUNNEL_LOCAL_PORT="${NETSGO_TUNNEL_LOCAL_PORT:-18083}"
WAIT_TIMEOUT="${NETSGO_BOOTSTRAP_WAIT_TIMEOUT:-180}"
MANAGEMENT_HOST="${NETSGO_MANAGEMENT_HOST:-}"

if [ -z "${SETUP_TOKEN}" ]; then
	echo "NETSGO_SETUP_TOKEN is required" >&2
	exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

log() {
	printf '[bootstrap] %s\n' "$*"
}

derive_management_host() {
	printf '%s' "$1" | sed -E 's#^[a-zA-Z]+://##; s#/.*$##'
}

if [ -z "${MANAGEMENT_HOST}" ]; then
	MANAGEMENT_HOST="$(derive_management_host "${SERVER_ADDR}")"
fi

deadline() {
	expr "$(date +%s)" + "${WAIT_TIMEOUT}"
}

wait_for_http() {
	url="$1"
	end_ts="$(deadline)"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		if curl -fsS -H "Host: ${MANAGEMENT_HOST}" "${url}" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	return 1
}

http_json() {
	method="$1"
	url="$2"
	body_file="$3"
	output_file="$4"
	shift 4
	if [ -n "${body_file}" ]; then
		curl -sS -o "${output_file}" -w '%{http_code}' -X "${method}" \
			-H 'Content-Type: application/json' "$@" \
			--data @"${body_file}" \
			"${url}"
		return
	fi
	curl -sS -o "${output_file}" -w '%{http_code}' -X "${method}" "$@" "${url}"
}

login_admin() {
	login_payload="${tmpdir}/login.json"
	login_resp="${tmpdir}/login.resp"
	jq -n \
		--arg username "${ADMIN_USER}" \
		--arg password "${ADMIN_PASS}" \
		'{username:$username,password:$password}' >"${login_payload}"

	end_ts="$(deadline)"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		code="$(http_json POST "${BASE_URL}/api/auth/login" "${login_payload}" "${login_resp}" -H "Host: ${MANAGEMENT_HOST}")" || code=""
		if [ "${code}" = "200" ]; then
			token="$(jq -r '.token // empty' "${login_resp}")"
			if [ -n "${token}" ]; then
				printf '%s' "${token}" >"${ADMIN_TOKEN_FILE}"
				printf '%s' "${token}"
				return 0
			fi
		fi
		sleep 1
	done
	return 1
}

wait_for_client() {
	token="$1"
	clients_resp="${tmpdir}/clients.json"
	end_ts="$(deadline)"
	while [ "$(date +%s)" -lt "${end_ts}" ]; do
		code="$(http_json GET "${BASE_URL}/api/clients" "" "${clients_resp}" -H "Host: ${MANAGEMENT_HOST}" -H "Authorization: Bearer ${token}")" || code=""
		if [ "${code}" = "200" ]; then
			client_id="$(jq -r --arg hostname "${CLIENT_HOSTNAME}" 'map(select(.info.hostname == $hostname and .online == true))[0].id // empty' "${clients_resp}")"
			if [ -n "${client_id}" ]; then
				printf '%s' "${client_id}"
				return 0
			fi
		fi
		sleep 1
	done
	return 1
}

log "waiting for ${BASE_URL}/api/setup/status"
if ! wait_for_http "${BASE_URL}/api/setup/status"; then
	log "timed out waiting for proxy/server readiness"
	exit 1
fi

status_resp="${tmpdir}/setup-status.json"
init_payload="${tmpdir}/setup-init.json"
init_resp="${tmpdir}/setup-init.resp"

code="$(http_json GET "${BASE_URL}/api/setup/status" "" "${status_resp}" -H "Host: ${MANAGEMENT_HOST}")"
if [ "${code}" != "200" ]; then
	log "unexpected setup status response: ${code}"
	cat "${status_resp}" >&2 || true
	exit 1
fi

initialized="$(jq -r '.initialized' "${status_resp}")"
if [ "${initialized}" != "true" ]; then
	log "initializing server"
	jq -n \
		--arg username "${ADMIN_USER}" \
		--arg password "${ADMIN_PASS}" \
		--arg server_addr "${SERVER_ADDR}" \
		--arg setup_token "${SETUP_TOKEN}" \
		'{admin:{username:$username,password:$password},server_addr:$server_addr,allowed_ports:[],setup_token:$setup_token}' >"${init_payload}"
	code="$(http_json POST "${BASE_URL}/api/setup/init" "${init_payload}" "${init_resp}" -H "Host: ${MANAGEMENT_HOST}")" || code=""
	case "${code}" in
	201)
		;;
	403)
		code="$(http_json GET "${BASE_URL}/api/setup/status" "" "${status_resp}" -H "Host: ${MANAGEMENT_HOST}")"
		if [ "${code}" != "200" ] || [ "$(jq -r '.initialized' "${status_resp}")" != "true" ]; then
			log "server initialization rejected"
			cat "${init_resp}" >&2 || true
			exit 1
		fi
		;;
	*)
		log "server initialization failed with status ${code}"
		cat "${init_resp}" >&2 || true
		exit 1
		;;
	esac
fi

log "logging in as admin"
admin_token="$(login_admin)" || {
	log "failed to obtain admin token"
	exit 1
}

key_payload="${tmpdir}/api-key.json"
key_resp="${tmpdir}/api-key.resp"
jq -n \
	--arg name "compose-$(date +%s)" \
	'{name:$name,permissions:["connect"]}' >"${key_payload}"
code="$(http_json POST "${BASE_URL}/api/admin/keys" "${key_payload}" "${key_resp}" -H "Host: ${MANAGEMENT_HOST}" -H "Authorization: Bearer ${admin_token}")" || code=""
if [ "${code}" != "201" ]; then
	log "failed to create API key"
	cat "${key_resp}" >&2 || true
	exit 1
fi

mkdir -p "$(dirname "${CLIENT_KEY_FILE}")"
api_key="$(jq -r '.raw_key // empty' "${key_resp}")"
if [ -z "${api_key}" ]; then
	log "empty API key returned by admin API"
	exit 1
fi
printf '%s' "${api_key}" >"${CLIENT_KEY_FILE}"

log "waiting for live client ${CLIENT_HOSTNAME}"
client_id="$(wait_for_client "${admin_token}")" || {
	log "timed out waiting for client to come online"
	exit 1
}

clients_resp="${tmpdir}/clients-post-online.json"
code="$(http_json GET "${BASE_URL}/api/clients" "" "${clients_resp}" -H "Host: ${MANAGEMENT_HOST}" -H "Authorization: Bearer ${admin_token}")"
if [ "${code}" != "200" ]; then
	log "failed to fetch client list after client became live"
	exit 1
fi

existing_tunnel="$(jq -r --arg client_id "${client_id}" --arg tunnel_name "${TUNNEL_NAME}" 'map(select(.id == $client_id))[0].proxies // [] | map(select(.name == $tunnel_name))[0].name // empty' "${clients_resp}")"
if [ -z "${existing_tunnel}" ]; then
	tunnel_payload="${tmpdir}/tunnel.json"
	tunnel_resp="${tmpdir}/tunnel.resp"
	if [ "${TUNNEL_TYPE}" = "http" ]; then
		jq -n \
			--arg name "${TUNNEL_NAME}" \
			--arg local_ip "${TUNNEL_LOCAL_IP}" \
			--argjson local_port "${TUNNEL_LOCAL_PORT}" \
			--arg domain "${TUNNEL_DOMAIN}" \
			'{name:$name,type:"http",local_ip:$local_ip,local_port:$local_port,domain:$domain}' >"${tunnel_payload}"
	else
		jq -n \
			--arg name "${TUNNEL_NAME}" \
			--arg local_ip "${TUNNEL_LOCAL_IP}" \
			--argjson local_port "${TUNNEL_LOCAL_PORT}" \
			--argjson remote_port "${TUNNEL_REMOTE_PORT}" \
			'{name:$name,type:"tcp",local_ip:$local_ip,local_port:$local_port,remote_port:$remote_port}' >"${tunnel_payload}"
	fi
	code="$(http_json POST "${BASE_URL}/api/clients/${client_id}/tunnels" "${tunnel_payload}" "${tunnel_resp}" -H "Host: ${MANAGEMENT_HOST}" -H "Authorization: Bearer ${admin_token}")" || code=""
	case "${code}" in
	201)
		;;
	409)
		log "tunnel already exists, continuing"
		;;
	*)
		log "failed to create managed tunnel"
		cat "${tunnel_resp}" >&2 || true
		exit 1
		;;
	esac
fi

mkdir -p "$(dirname "${READY_FILE}")"
cat >"${READY_FILE}" <<EOF
base_url=${BASE_URL}
client_id=${client_id}
tunnel_name=${TUNNEL_NAME}
management_host=${MANAGEMENT_HOST}
tunnel_host=${TUNNEL_DOMAIN}
EOF

log "compose environment is ready"
