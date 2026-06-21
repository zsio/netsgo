#!/bin/sh
set -eu

KEY_FILE="${NETSGO_CLIENT_KEY_FILE:-/shared/client.key}"
KEY_VALUE="${NETSGO_CLIENT_KEY:-}"
SERVER_ADDR="${NETSGO_SERVER:-http://proxy}"
WAIT_TIMEOUT="${NETSGO_CLIENT_KEY_WAIT_TIMEOUT:-180}"
TLS_SKIP_VERIFY="${NETSGO_TLS_SKIP_VERIFY:-false}"
DATA_DIR="${NETSGO_DATA_DIR:-/var/lib/netsgo}"

if [ -n "${KEY_VALUE}" ]; then
	api_key="${KEY_VALUE}"
else
	end_ts="$(expr "$(date +%s)" + "${WAIT_TIMEOUT}")"
	while [ ! -s "${KEY_FILE}" ]; do
		if [ "$(date +%s)" -ge "${end_ts}" ]; then
			echo "[client] timed out waiting for API key file: ${KEY_FILE}" >&2
			exit 1
		fi
		sleep 1
	done
	api_key="$(tr -d '\r\n' < "${KEY_FILE}")"
fi

if [ -z "${api_key}" ]; then
	echo "[client] API key is empty" >&2
	exit 1
fi

set -- /usr/local/bin/netsgo client --data-dir "${DATA_DIR}" --server "${SERVER_ADDR}" --key "${api_key}"
if [ "${TLS_SKIP_VERIFY}" = "true" ]; then
	set -- "$@" --tls-skip-verify
fi

exec "$@"
