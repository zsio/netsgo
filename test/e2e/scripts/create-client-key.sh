#!/bin/sh
set -eu

BASE_URL="${NETSGO_BOOTSTRAP_BASE_URL:-http://server:8080}"
ADMIN_USER="${NETSGO_ADMIN_USER:-admin}"
ADMIN_PASS="${NETSGO_ADMIN_PASS:?NETSGO_ADMIN_PASS is required}"
CLIENT_KEY_FILE="${NETSGO_CLIENT_KEY_FILE:-/shared/client.key}"
ADMIN_TOKEN_FILE="${NETSGO_ADMIN_TOKEN_FILE:-/shared/admin.token}"
READY_FILE="${NETSGO_READY_FILE:-/shared/client-key.ready}"
WAIT_TIMEOUT="${NETSGO_BOOTSTRAP_WAIT_TIMEOUT:-180}"
MANAGEMENT_HOST="${NETSGO_MANAGEMENT_HOST:-}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

log() {
	printf '[client-key] %s\n' "$*"
}

deadline() {
	expr "$(date +%s)" + "${WAIT_TIMEOUT}"
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

login_payload="${tmpdir}/login.json"
login_resp="${tmpdir}/login.resp"
jq -n \
	--arg username "${ADMIN_USER}" \
	--arg password "${ADMIN_PASS}" \
	'{username:$username,password:$password}' >"${login_payload}"

log "waiting for admin API"
admin_token=""
end_ts="$(deadline)"
while [ "$(date +%s)" -lt "${end_ts}" ]; do
	if [ -n "${MANAGEMENT_HOST}" ]; then
		code="$(http_json POST "${BASE_URL}/api/auth/login" "${login_payload}" "${login_resp}" -H "Host: ${MANAGEMENT_HOST}")" || code=""
	else
		code="$(http_json POST "${BASE_URL}/api/auth/login" "${login_payload}" "${login_resp}")" || code=""
	fi
	if [ "${code}" = "200" ]; then
		admin_token="$(jq -r '.token // empty' "${login_resp}")"
		if [ -n "${admin_token}" ]; then
			break
		fi
	fi
	sleep 1
done

if [ -z "${admin_token}" ]; then
	log "failed to obtain admin token"
	cat "${login_resp}" >&2 || true
	exit 1
fi

mkdir -p "$(dirname "${ADMIN_TOKEN_FILE}")"
printf '%s' "${admin_token}" >"${ADMIN_TOKEN_FILE}"

key_payload="${tmpdir}/api-key.json"
key_resp="${tmpdir}/api-key.resp"
jq -n \
	--arg name "playwright-$(date +%s)" \
	'{name:$name,permissions:["connect"]}' >"${key_payload}"

if [ -n "${MANAGEMENT_HOST}" ]; then
	code="$(http_json POST "${BASE_URL}/api/admin/keys" "${key_payload}" "${key_resp}" -H "Host: ${MANAGEMENT_HOST}" -H "Authorization: Bearer ${admin_token}")" || code=""
else
	code="$(http_json POST "${BASE_URL}/api/admin/keys" "${key_payload}" "${key_resp}" -H "Authorization: Bearer ${admin_token}")" || code=""
fi
if [ "${code}" != "201" ]; then
	log "failed to create API key"
	cat "${key_resp}" >&2 || true
	exit 1
fi

api_key="$(jq -r '.raw_key // empty' "${key_resp}")"
if [ -z "${api_key}" ]; then
	log "empty API key returned by admin API"
	exit 1
fi

mkdir -p "$(dirname "${CLIENT_KEY_FILE}")"
printf '%s' "${api_key}" >"${CLIENT_KEY_FILE}"
mkdir -p "$(dirname "${READY_FILE}")"
cat >"${READY_FILE}" <<EOF
base_url=${BASE_URL}
EOF

log "client API key is ready"
