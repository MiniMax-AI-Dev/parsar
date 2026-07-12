#!/bin/sh
set -eu

server_url="${PARSAR_SERVER_URL:-http://parsar-server:8080}"
token="${PARSAR_SHARED_RUNTIME_TOKEN:-}"
profile="${PARSAR_RUNTIME_PROFILE:-default-runtime}"
name="${PARSAR_RUNTIME_NAME:-local-sandbox}"

if [ -z "$token" ]; then
  echo "parsar-runtime: PARSAR_SHARED_RUNTIME_TOKEN is required" >&2
  exit 1
fi

mkdir -p "$HOME/.parsar" /workspace

while :; do
  if parsar-daemon connect \
    --profile "$profile" \
    --url "$server_url" \
    --token "$token" \
    --device-name "$name"; then
    exit 0
  fi
  echo "parsar-runtime: connect failed; retrying in 2s" >&2
  sleep 2
done
