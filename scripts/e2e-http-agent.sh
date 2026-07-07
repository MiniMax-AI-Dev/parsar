#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PARSAR_E2E_DIR="${PARSAR_E2E_DIR:-$HOME/.parsar/e2e/http-agent}"
mkdir -p "$PARSAR_E2E_DIR"

if ! docker info >/dev/null 2>&1; then
  echo "Docker is required for HTTP Agent E2E." >&2
  exit 1
fi

free_port() {
  python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

if [[ -z "${PARSAR_POSTGRES_PORT:-}" ]]; then
  PARSAR_POSTGRES_PORT="$(free_port)"
  export PARSAR_POSTGRES_PORT
fi

# shellcheck source=scripts/dev-env.sh
source "$ROOT_DIR/scripts/dev-env.sh"

API_PORT="${PARSAR_E2E_API_PORT:-$(free_port)}"
HTTP_AGENT_PORT="${PARSAR_E2E_HTTP_AGENT_PORT:-$(free_port)}"
DATABASE_URL="$(parsar_dev_database_url)"
export DATABASE_URL

cleanup() {
  if [[ -n "${API_PID:-}" ]]; then kill "$API_PID" >/dev/null 2>&1 || true; fi
  if [[ -n "${HTTP_AGENT_PID:-}" ]]; then kill "$HTTP_AGENT_PID" >/dev/null 2>&1 || true; fi
  docker compose -f docker-compose.dev.yml down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker compose -f docker-compose.dev.yml down -v --remove-orphans >/dev/null 2>&1 || true
docker compose -f docker-compose.dev.yml up -d postgres >/dev/null

for _ in {1..30}; do
  if docker compose -f docker-compose.dev.yml exec -T postgres pg_isready -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker compose -f docker-compose.dev.yml exec -T postgres pg_isready -U "$PARSAR_PG_USER" -d "$PARSAR_PG_DB" >/dev/null 2>&1; then
  echo "Postgres did not become ready" >&2
  exit 1
fi

(
  cd server
  go run ./cmd/migrate
  go run ./cmd/seeddev
)

python3 - "$HTTP_AGENT_PORT" >"$PARSAR_E2E_DIR/http-agent.log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

port = int(sys.argv[1])

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length", "0"))
        payload = json.loads(self.rfile.read(length) or b"{}")
        if self.path != "/agent" or payload.get("agent_slug") != "backend-agent" or "@backend-agent" not in payload.get("trigger_message_content", ""):
            self.send_response(400)
            self.end_headers()
            return
        response = {
            "content": "HTTP Agent completed the backend assessment, @test-agent please verify.",
            "usage": {
                "provider": "fake-http-agent",
                "model": "http-agent-v1",
                "input_tokens": 21,
                "output_tokens": 13,
                "cost_usd": 0.00021,
                "raw": {"e2e": "http-agent"},
            },
        }
        body = json.dumps(response).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        return

ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
HTTP_AGENT_PID=$!

(
  cd server
  PARSAR_ADDR="127.0.0.1:${API_PORT}" DATABASE_URL="$DATABASE_URL" go run ./cmd/server
) >"$PARSAR_E2E_DIR/api.log" 2>&1 &
API_PID=$!

for _ in {1..30}; do
  if curl -fsS "http://127.0.0.1:${API_PORT}/api/v1/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -fsS "http://127.0.0.1:${API_PORT}/api/v1/health" >/dev/null 2>&1; then
  echo "Parsar API did not become ready" >&2
  exit 1
fi

API_URL="http://127.0.0.1:${API_PORT}"
HTTP_AGENT_URL="http://127.0.0.1:${HTTP_AGENT_PORT}/agent"

im_response="$PARSAR_E2E_DIR/im-response.json"
first_inbound_response="$PARSAR_E2E_DIR/first-inbound-response.json"
connector_response="$PARSAR_E2E_DIR/connector-response.json"
conversation_ref_response="$PARSAR_E2E_DIR/conversation-ref-response.json"
invoke_response="$PARSAR_E2E_DIR/http-agent-invoke-response.json"
timeline_response="$PARSAR_E2E_DIR/timeline-response.json"
usage_response="$PARSAR_E2E_DIR/usage-response.json"
audit_response="$PARSAR_E2E_DIR/audit-response.json"
outbound_response="$PARSAR_E2E_DIR/outbound-response.json"
delivered_response="$PARSAR_E2E_DIR/delivered-response.json"
outbound_after_delivery_response="$PARSAR_E2E_DIR/outbound-after-delivery-response.json"
failure_response="$PARSAR_E2E_DIR/http-agent-failure-response.json"
failure_audit_response="$PARSAR_E2E_DIR/failure-audit-response.json"
requeue_response="$PARSAR_E2E_DIR/requeue-response.json"
retry_response="$PARSAR_E2E_DIR/retry-response.json"
retry_audit_response="$PARSAR_E2E_DIR/retry-audit-response.json"

curl -fsS "$API_URL/api/v1/conversations/00000000-0000-0000-0000-000000000012/external-ref" \
  -H 'Content-Type: application/json' \
  -d '{"gateway":"dev","external_chat_id":"oc_demo","external_thread_id":"om_demo"}' \
  > "$conversation_ref_response"

PARSAR_API_URL="$API_URL" go run ./server/cmd/devgateway send-inbound \
  --gateway dev \
  --message-id om_http_1 \
  --text '@backend-agent take a look at HTTP connector' \
  --actor-id ou_demo \
  --actor-email admin@example.com \
  --chat-id oc_demo \
  --thread-id om_demo \
  > "$first_inbound_response"

duplicate_response="$PARSAR_E2E_DIR/duplicate-inbound-response.json"
PARSAR_API_URL="$API_URL" go run ./server/cmd/devgateway send-inbound \
  --gateway dev \
  --message-id om_http_1 \
  --text '@backend-agent take a look at HTTP connector' \
  --actor-id ou_demo \
  --actor-email admin@example.com \
  --chat-id oc_demo \
  --thread-id om_demo \
  > "$duplicate_response"

run_id="$(python3 - "$first_inbound_response" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["run_ids"][0])
PY
)"

curl -fsS "$API_URL/api/v1/project-agents/00000000-0000-0000-0000-000000000010/connector" \
  -H 'Content-Type: application/json' \
  -d "{\"connector_type\":\"http\",\"endpoint\":\"$HTTP_AGENT_URL\"}" \
  > "$connector_response"

curl -fsS "$API_URL/dev/gateway/inbound" \
  -H 'Content-Type: application/json' \
  -d '{"gateway":"dev","conversation":"demo-group","sender":"admin@example.com","text":"@backend-agent run HTTP connector again"}' \
  > "$im_response"

run_id="$(python3 - "$im_response" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["run_ids"][0])
PY
)"

DATABASE_URL="$DATABASE_URL" go run ./server/cmd/httprunner --once > "$invoke_response"

curl -fsS "$API_URL/api/v1/conversations/00000000-0000-0000-0000-000000000012/timeline?limit=100" > "$timeline_response"
curl -fsS "$API_URL/api/v1/projects/00000000-0000-0000-0000-000000000004/usage?limit=100" > "$usage_response"
curl -fsS "$API_URL/api/v1/projects/00000000-0000-0000-0000-000000000004/audit-records?limit=100" > "$audit_response"
PARSAR_API_URL="$API_URL" go run ./server/cmd/devgateway drain-outbound --gateway dev --limit 100 --ack=false > "$outbound_response"

python3 - "$conversation_ref_response" "$connector_response" "$invoke_response" "$timeline_response" "$usage_response" "$audit_response" "$first_inbound_response" "$duplicate_response" "$outbound_response" <<'PY'
import json
import sys

conversation_ref = json.load(open(sys.argv[1]))
connector = json.load(open(sys.argv[2]))
invoke = json.load(open(sys.argv[3]))
timeline = json.load(open(sys.argv[4]))
usage = json.load(open(sys.argv[5]))
audit = json.load(open(sys.argv[6]))
first_inbound = json.load(open(sys.argv[7]))
duplicate_inbound = json.load(open(sys.argv[8]))
outbound = json.load(open(sys.argv[9]))

assert conversation_ref["platform"] == "dev" and conversation_ref["external_id"] == "oc_demo", conversation_ref
assert duplicate_inbound["message_id"] == first_inbound["message_id"] and duplicate_inbound["run_ids"] == first_inbound["run_ids"], (first_inbound, duplicate_inbound)
assert connector["connector_type"] == "http", connector
assert connector["agent_config"]["endpoint"].endswith("/agent"), connector
assert invoke["attempts"] == 1 and invoke["completed"] == 1, invoke
invoke_result = invoke["results"][0]
assert invoke_result["claimed"] is True, invoke

completion = invoke_result["completion"]
run_id = completion.get("run_id") or completion.get("RunID")
child_run_ids = completion.get("child_run_ids") or completion.get("ChildRunIDs")
usage_row = completion.get("usage") or completion.get("Usage")
assert (completion.get("status") or completion.get("Status")) == "completed", completion
assert child_run_ids, completion
assert usage_row["provider"] == "fake-http-agent", completion
assert usage_row["raw"]["source"] == "http_agent", completion
assert invoke_result["agent_response"]["content"].startswith("HTTP Agent completed"), invoke

runs = timeline["agent_runs"]
assert any(run["id"] == run_id and run["status"] == "completed" and run["connector_type"] == "http" for run in runs), runs
assert any(run["id"] in child_run_ids and run["agent_slug"] == "test-agent" for run in runs), runs
assert any(message["metadata"].get("source") == "gateway" and message["metadata"].get("gateway") == "dev" and message["metadata"].get("external_chat_id") == "oc_demo" for message in timeline["messages"]), timeline
assert any("HTTP Agent completed the backend assessment" in message["content"] for message in timeline["messages"]), timeline
assert any(row["provider"] == "fake-http-agent" and row["raw"].get("source") == "http_agent" for row in usage["usage_logs"]), usage
assert any(row["event_type"] == "im.message.created" and row["payload"].get("source") == "gateway" and row["payload"].get("gateway") == "dev" for row in audit["audit_records"]), audit
assert any(row["event_type"] == "http_agent.completed" and row["target_id"] == run_id for row in audit["audit_records"]), audit
outbound_message = next(message for message in outbound["messages"] if "HTTP Agent completed the backend assessment" in message["content"])
assert outbound_message["external_chat_id"] == "oc_demo" and outbound_message["gateway"] == "dev", outbound
delivery = next(delivery for delivery in outbound["deliveries"] if delivery["message_id"] == outbound_message["id"])
assert delivery["external_chat_id"] == "oc_demo" and delivery["gateway"] == "dev" and "HTTP Agent completed the backend assessment" in delivery["text"], outbound

print("HTTP Agent connector E2E passed")
PY

outbound_message_id="$(python3 - "$outbound_response" <<'PY'
import json, sys
outbound = json.load(open(sys.argv[1]))
print(next(message["id"] for message in outbound["messages"] if "HTTP Agent completed the backend assessment" in message["content"]))
PY
)"

PARSAR_API_URL="$API_URL" go run ./server/cmd/devgateway drain-outbound --gateway dev --limit 100 --ack=true > "$delivered_response"
curl -fsS "$API_URL/dev/gateway/outbound?gateway=dev&limit=100" > "$outbound_after_delivery_response"

python3 - "$delivered_response" "$outbound_after_delivery_response" "$outbound_message_id" <<'PY'
import json, sys
delivered = json.load(open(sys.argv[1]))
after = json.load(open(sys.argv[2]))
message_id = sys.argv[3]
assert any(delivery["message_id"] == message_id for delivery in delivered["deliveries"]), delivered
assert all(message["id"] != message_id for message in after["messages"]), after
print("Gateway outbound delivery E2E passed")
PY

kill "$HTTP_AGENT_PID" >/dev/null 2>&1 || true
wait "$HTTP_AGENT_PID" 2>/dev/null || true
unset HTTP_AGENT_PID

curl -fsS "$API_URL/dev/gateway/inbound" \
  -H 'Content-Type: application/json' \
  -d '{"gateway":"dev","conversation":"demo-group","sender":"admin@example.com","text":"@backend-agent trigger failure path"}' \
  > "$im_response"

DATABASE_URL="$DATABASE_URL" go run ./server/cmd/httprunner --once > "$failure_response"
curl -fsS "$API_URL/api/v1/projects/00000000-0000-0000-0000-000000000004/audit-records?limit=100" > "$failure_audit_response"

python3 - "$failure_response" "$failure_audit_response" <<'PY'
import json
import sys

failure = json.load(open(sys.argv[1]))
audit = json.load(open(sys.argv[2]))

assert failure["attempts"] == 1 and failure["completed"] == 0, failure
failed = failure["results"][0]
assert failed["claimed"] is True and failed["failed"] is True, failure
assert "http agent request failed" in failed["error"], failure
failed_run_id = (failed.get("completion") or {}).get("RunID") or (failed.get("completion") or {}).get("run_id")
if not failed_run_id:
    # The failed result has no completion payload; use newest http_agent.failed audit row as ground truth.
    failed_run_id = next(row["target_id"] for row in audit["audit_records"] if row["event_type"] == "http_agent.failed")
assert any(row["event_type"] == "http_agent.failed" and row["payload"].get("source") == "http_agent" for row in audit["audit_records"]), audit

print("HTTP Agent failure E2E passed")
PY

failed_run_id="$(python3 - "$failure_audit_response" <<'PY'
import json, sys
audit = json.load(open(sys.argv[1]))
print(next(row["target_id"] for row in audit["audit_records"] if row["event_type"] == "http_agent.failed"))
PY
)"

python3 - "$HTTP_AGENT_PORT" >"$PARSAR_E2E_DIR/http-agent-recovered.log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

port = int(sys.argv[1])

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length", "0"))
        payload = json.loads(self.rfile.read(length) or b"{}")
        if self.path != "/agent" or payload.get("agent_slug") != "backend-agent":
            self.send_response(400)
            self.end_headers()
            return
        response = {
            "content": "HTTP Agent recovered and succeeded on retry, @test-agent please verify.",
            "usage": {"provider": "fake-http-agent", "model": "http-agent-v1", "input_tokens": 11, "output_tokens": 7, "cost_usd": 0.00011},
        }
        body = json.dumps(response).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        return

ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
HTTP_AGENT_PID=$!

curl -fsS "$API_URL/api/v1/agent-runs/$failed_run_id/requeue" \
  -H 'Content-Type: application/json' \
  -d '{"reason":"agent recovered"}' \
  > "$requeue_response"

DATABASE_URL="$DATABASE_URL" go run ./server/cmd/httprunner --once > "$retry_response"
curl -fsS "$API_URL/api/v1/projects/00000000-0000-0000-0000-000000000004/audit-records?limit=100" > "$retry_audit_response"

python3 - "$requeue_response" "$retry_response" "$retry_audit_response" "$failed_run_id" <<'PY'
import json
import sys

requeue = json.load(open(sys.argv[1]))
retry = json.load(open(sys.argv[2]))
audit = json.load(open(sys.argv[3]))
failed_run_id = sys.argv[4]

assert requeue["run_id"] == failed_run_id and requeue["status"] == "queued", requeue
assert retry["attempts"] == 1 and retry["completed"] == 1, retry
retry_result = retry["results"][0]
assert retry_result["claimed"] is True and not retry_result.get("failed"), retry
completion = retry_result["completion"]
run_id = completion.get("RunID") or completion.get("run_id")
assert run_id == failed_run_id, retry
assert any(row["event_type"] == "agent_run.requeued" and row["target_id"] == failed_run_id for row in audit["audit_records"]), audit
assert any(row["event_type"] == "http_agent.completed" and row["target_id"] == failed_run_id for row in audit["audit_records"]), audit

print("HTTP Agent retry E2E passed")
PY
