#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PARSAR_E2E_DIR="${PARSAR_E2E_DIR:-$HOME/.parsar/e2e/feishu-gateway}"
mkdir -p "$PARSAR_E2E_DIR"

evidence_files=(
  "api.log"
  "http-agent.log"
  "feishu-send.log"
  "feishu-send-requests.jsonl"
  "conversation-ref-response.json"
  "connector-response.json"
  "feishu-event.json"
  "feishu-event-response.json"
  "http-runner-response.json"
  "timeline-response.json"
  "agent-run-response.json"
  "usage-response.json"
  "audit-response.json"
  "outbound-before-response.json"
  "drain-feishu-response.json"
  "outbound-after-response.json"
  "agent-run-after-delivery-response.json"
)
for evidence_file in "${evidence_files[@]}"; do
  : >"$PARSAR_E2E_DIR/$evidence_file"
done

if ! docker info >/dev/null 2>&1; then
  echo "Docker is required for Feishu Gateway E2E." >&2
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
FEISHU_SEND_PORT="${PARSAR_E2E_FEISHU_SEND_PORT:-$(free_port)}"
DATABASE_URL="$(parsar_dev_database_url)"
export DATABASE_URL

cleanup() {
  if [[ -n "${API_PID:-}" ]]; then kill "$API_PID" >/dev/null 2>&1 || true; fi
  if [[ -n "${HTTP_AGENT_PID:-}" ]]; then kill "$HTTP_AGENT_PID" >/dev/null 2>&1 || true; fi
  if [[ -n "${FEISHU_SEND_PID:-}" ]]; then kill "$FEISHU_SEND_PID" >/dev/null 2>&1 || true; fi
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
        if self.path != "/agent" or payload.get("agent_slug") != "backend-agent":
            self.send_response(400)
            self.end_headers()
            return
        if "@后端Agent" not in payload.get("trigger_message_content", ""):
            self.send_response(400)
            self.end_headers()
            return
        response = {
            "content": "HTTP Agent 完成了 Feishu Gateway 验证，准备回发飞书。",
            "usage": {
                "provider": "mock-http-agent",
                "model": "http-agent-v1",
                "input_tokens": 34,
                "output_tokens": 18,
                "cost_usd": 0.00034,
                "raw": {"e2e": "feishu-gateway"},
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

python3 - "$FEISHU_SEND_PORT" "$PARSAR_E2E_DIR/feishu-send-requests.jsonl" >"$PARSAR_E2E_DIR/feishu-send.log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

port = int(sys.argv[1])
requests_path = sys.argv[2]

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length", "0"))
        raw = self.rfile.read(length) or b"{}"
        payload = json.loads(raw)
        record = {
            "path": self.path,
            "authorization": self.headers.get("authorization", ""),
            "payload": payload,
        }
        with open(requests_path, "a", encoding="utf-8") as fp:
            fp.write(json.dumps(record, ensure_ascii=False) + "\n")
        if self.path != "/open-apis/im/v1/messages?receive_id_type=chat_id":
            self.send_response(404)
            self.end_headers()
            return
        response = {"code": 0, "msg": "ok", "data": {"message_id": "om_mock_delivered_1"}}
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
FEISHU_SEND_PID=$!

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
FEISHU_SEND_URL="http://127.0.0.1:${FEISHU_SEND_PORT}"

conversation_ref_response="$PARSAR_E2E_DIR/conversation-ref-response.json"
connector_response="$PARSAR_E2E_DIR/connector-response.json"
feishu_event_payload="$PARSAR_E2E_DIR/feishu-event.json"
feishu_event_response="$PARSAR_E2E_DIR/feishu-event-response.json"
runner_response="$PARSAR_E2E_DIR/http-runner-response.json"
timeline_response="$PARSAR_E2E_DIR/timeline-response.json"
run_response="$PARSAR_E2E_DIR/agent-run-response.json"
usage_response="$PARSAR_E2E_DIR/usage-response.json"
audit_response="$PARSAR_E2E_DIR/audit-response.json"
outbound_before_response="$PARSAR_E2E_DIR/outbound-before-response.json"
drain_response="$PARSAR_E2E_DIR/drain-feishu-response.json"
outbound_after_response="$PARSAR_E2E_DIR/outbound-after-response.json"
delivered_run_response="$PARSAR_E2E_DIR/agent-run-after-delivery-response.json"

curl -fsS "$API_URL/api/v1/conversations/00000000-0000-0000-0000-000000000012/external-ref" \
  -H 'Content-Type: application/json' \
  -d '{"gateway":"feishu","external_chat_id":"oc_feishu_e2e","external_thread_id":"om_feishu_thread"}' \
  > "$conversation_ref_response"

curl -fsS "$API_URL/api/v1/project-agents/00000000-0000-0000-0000-000000000010/connector" \
  -H 'Content-Type: application/json' \
  -d "{\"connector_type\":\"http\",\"endpoint\":\"$HTTP_AGENT_URL\"}" \
  > "$connector_response"

cat > "$feishu_event_payload" <<'JSON'
{
  "event": {
    "message": {
      "message_id": "om_feishu_inbound_1",
      "chat_id": "oc_feishu_e2e",
      "chat_type": "group",
      "thread_id": "om_feishu_thread",
      "content": "{\"text\":\"@后端Agent 通过 Feishu Gateway 跑一次 HTTP connector\"}"
    },
    "sender": {
      "sender_id": {"open_id": "ou_feishu_admin"},
      "tenant_key": "tenant_feishu_e2e"
    }
  }
}
JSON

curl -fsS "$API_URL/api/v1/feishu/events/message" \
  -H 'Content-Type: application/json' \
  --data-binary "@$feishu_event_payload" \
  > "$feishu_event_response"

run_id="$(python3 - "$feishu_event_response" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["run_ids"][0])
PY
)"

DATABASE_URL="$DATABASE_URL" go run ./server/cmd/httprunner --once > "$runner_response"

curl -fsS "$API_URL/api/v1/conversations/00000000-0000-0000-0000-000000000012/timeline?limit=100" > "$timeline_response"
curl -fsS "$API_URL/api/v1/agent-runs/$run_id" > "$run_response"
curl -fsS "$API_URL/api/v1/projects/00000000-0000-0000-0000-000000000004/usage?agent_run_id=$run_id&limit=100" > "$usage_response"
curl -fsS "$API_URL/api/v1/projects/00000000-0000-0000-0000-000000000004/audit-records?limit=100" > "$audit_response"
curl -fsS "$API_URL/dev/gateway/outbound?gateway=feishu&limit=100" > "$outbound_before_response"

python3 - "$conversation_ref_response" "$connector_response" "$feishu_event_response" "$runner_response" "$timeline_response" "$run_response" "$usage_response" "$audit_response" "$outbound_before_response" "$run_id" <<'PY'
import json
import sys

conversation_ref = json.load(open(sys.argv[1]))
connector = json.load(open(sys.argv[2]))
event_response = json.load(open(sys.argv[3]))
runner = json.load(open(sys.argv[4]))
timeline = json.load(open(sys.argv[5]))
run = json.load(open(sys.argv[6]))
usage = json.load(open(sys.argv[7]))
audit = json.load(open(sys.argv[8]))
outbound = json.load(open(sys.argv[9]))
run_id = sys.argv[10]

assert conversation_ref["platform"] == "feishu", conversation_ref
assert conversation_ref["external_id"] == "oc_feishu_e2e", conversation_ref
assert conversation_ref["external_thread_id"] == "om_feishu_thread", conversation_ref
assert connector["connector_type"] == "http", connector
assert connector["agent_config"]["endpoint"].endswith("/agent"), connector
assert event_response["gateway"] == "feishu", event_response
assert event_response["run_ids"] == [run_id], event_response

assert runner["attempts"] == 1 and runner["completed"] == 1, runner
runner_result = runner["results"][0]
assert runner_result["claimed"] is True and not runner_result.get("failed"), runner
completion = runner_result["completion"]
completion_run_id = completion.get("run_id") or completion.get("RunID")
completion_status = completion.get("status") or completion.get("Status")
completion_usage = completion.get("usage") or completion.get("Usage")
output_message_id = completion.get("message_id") or completion.get("MessageID")
assert completion_run_id == run_id and completion_status == "completed", completion
assert completion_usage["provider"] == "mock-http-agent", completion
assert completion_usage["raw"]["source"] == "http_agent", completion
assert output_message_id, completion

assert run["id"] == run_id and run["status"] == "completed", run
assert run["connector_type"] == "http", run
assert run["output_message_id"] == output_message_id, run
assert any(row["provider"] == "mock-http-agent" and row["raw"].get("source") == "http_agent" for row in run["usage"]), run

inbound = next(message for message in timeline["messages"] if message["id"] == event_response["message_id"])
assert inbound["metadata"]["source"] == "gateway", inbound
assert inbound["metadata"]["gateway"] == "feishu", inbound
assert inbound["metadata"]["external_chat_id"] == "oc_feishu_e2e", inbound
assert inbound["metadata"]["external_thread_id"] == "om_feishu_thread", inbound
assert inbound["metadata"]["external_message_id"] == "om_feishu_inbound_1", inbound
assert inbound["metadata"]["external_user_id"] == "ou_feishu_admin", inbound

assert any(item["id"] == run_id and item["status"] == "completed" and item["connector_type"] == "http" for item in timeline["agent_runs"]), timeline
assert any(row["agent_run_id"] == run_id and row["provider"] == "mock-http-agent" and row["raw"].get("source") == "http_agent" for row in usage["usage_logs"]), usage
assert any(row["event_type"] == "im.message.created" and row["target_id"] == inbound["id"] and row["payload"].get("gateway") == "feishu" and row["payload"].get("external_message_id") == "om_feishu_inbound_1" for row in audit["audit_records"]), audit
assert any(row["event_type"] == "agent_run.created" and row["target_id"] == run_id and row["payload"].get("gateway") == "feishu" for row in audit["audit_records"]), audit
assert any(row["event_type"] == "http_agent.completed" and row["target_id"] == run_id and row["payload"].get("source") == "http_agent" for row in audit["audit_records"]), audit

outbound_message = next(message for message in outbound["messages"] if message["id"] == output_message_id)
assert outbound_message["gateway"] == "feishu", outbound
assert outbound_message["external_chat_id"] == "oc_feishu_e2e", outbound
assert outbound_message["external_thread_id"] == "om_feishu_thread", outbound
assert outbound_message["metadata"].get("source") == "http_agent", outbound_message
delivery = next(delivery for delivery in outbound["deliveries"] if delivery["message_id"] == output_message_id)
assert delivery["gateway"] == "feishu", delivery
assert delivery["external_chat_id"] == "oc_feishu_e2e", delivery
assert "HTTP Agent 完成了 Feishu Gateway 验证" in delivery["text"], delivery

print("Feishu Gateway inbound + HTTP runner + outbound evidence passed")
PY

PARSAR_API_URL="$API_URL" PARSAR_FEISHU_BASE_URL="$FEISHU_SEND_URL" PARSAR_FEISHU_TOKEN="mock-tenant-token" \
  go run ./server/cmd/devgateway drain-outbound --gateway feishu --mode feishu --ack=true --limit 100 \
  > "$drain_response"
curl -fsS "$API_URL/dev/gateway/outbound?gateway=feishu&limit=100" > "$outbound_after_response"
curl -fsS "$API_URL/api/v1/agent-runs/$run_id" > "$delivered_run_response"

python3 - "$drain_response" "$outbound_after_response" "$delivered_run_response" "$PARSAR_E2E_DIR/feishu-send-requests.jsonl" <<'PY'
import json
import sys

drain = json.load(open(sys.argv[1]))
after = json.load(open(sys.argv[2]))
run = json.load(open(sys.argv[3]))
requests_path = sys.argv[4]
requests = [json.loads(line) for line in open(requests_path, encoding="utf-8") if line.strip()]

assert len(requests) == 1, requests
request = requests[0]
assert request["path"] == "/open-apis/im/v1/messages?receive_id_type=chat_id", request
assert request["authorization"] == "Bearer mock-tenant-token", request
payload = request["payload"]
assert payload["receive_id_type"] == "chat_id", payload
assert payload["receive_id"] == "oc_feishu_e2e", payload
assert payload["msg_type"] == "text", payload
content = json.loads(payload["content"])
assert "HTTP Agent 完成了 Feishu Gateway 验证" in content["text"], payload

output_message_id = run["output_message_id"]
assert any(delivery["message_id"] == output_message_id for delivery in drain["deliveries"]), drain
assert all(message["id"] != output_message_id for message in after["messages"]), after
assert run["output_message"]["metadata"]["gateway_delivery_status"] == "delivered", run
assert run["output_message"]["metadata"]["gateway_delivery_id"] == "om_mock_delivered_1", run
assert run["output_message"]["metadata"].get("gateway_delivered_at"), run

print("Feishu Gateway outbound send + delivered ack passed")
PY

echo "Feishu Gateway E2E passed. Evidence: $PARSAR_E2E_DIR"
