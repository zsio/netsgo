#!/bin/sh
set -eu

KEY_FILE="${NETSGO_CLIENT_KEY_FILE:-/shared/client.key}"
SERVER_ADDR="${NETSGO_SERVER:-http://proxy}"
WAIT_TIMEOUT="${NETSGO_CLIENT_KEY_WAIT_TIMEOUT:-180}"
TLS_SKIP_VERIFY="${NETSGO_TLS_SKIP_VERIFY:-false}"

end_ts="$(expr "$(date +%s)" + "${WAIT_TIMEOUT}")"
while [ ! -s "${KEY_FILE}" ]; do
	if [ "$(date +%s)" -ge "${end_ts}" ]; then
		echo "[client] timed out waiting for API key file: ${KEY_FILE}" >&2
		exit 1
	fi
	sleep 1
done

api_key="$(tr -d '\r\n' < "${KEY_FILE}")"
if [ -z "${api_key}" ]; then
	echo "[client] API key file is empty: ${KEY_FILE}" >&2
	exit 1
fi

set -- /usr/local/bin/netsgo client --server "${SERVER_ADDR}" --key "${api_key}"
if [ "${TLS_SKIP_VERIFY}" = "true" ]; then
	set -- "$@" --tls-skip-verify
fi

exec "$@"
