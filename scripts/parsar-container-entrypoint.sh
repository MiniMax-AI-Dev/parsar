#!/bin/sh
set -eu

if [ ! -w /workspace ]; then
  echo "parsar-container-entrypoint: /workspace is not writable" >&2
  exit 1
fi

parsar-migrate

parsar-server &
server_pid=$!

shutdown() {
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}

trap shutdown INT TERM EXIT

until wget --quiet --output-document=- http://127.0.0.1:8080/healthz >/dev/null; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    exit 1
  fi
  sleep 1
done

parsar-daemon managed-connect --url http://127.0.0.1:8080
