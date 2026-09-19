"""Common real-harness acceptance for hosted functions and workspace execution."""

import importlib.metadata
import json
from pathlib import Path
import time
import uuid


def verify_hosted_functions(client, foreign, http, model, restart, evidence):
    """restart(session_id, environment_id) restarts the operator-owned deployment."""
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source["vcs_info"]["commit_id"] == pin["commit"]
    sessions = client.beta.agents.sessions
    endpoint = str(client.base_url).rstrip("/") + "/agents/sessions"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    marker = "function-memory-" + uuid.uuid4().hex
    private_error = "private-handler-" + uuid.uuid4().hex
    calls, observed, checks = [], [], []
    session = sessions.create(agent={"model": model, "instructions": "Follow the requested tool calls exactly. Never repeat a failed call.", "tools": [{
        "type": "function", "name": "lookup", "description": "Retrieve the requested test value.",
        "parameters": {"type": "object", "properties": {"key": {"type": "string"}}, "required": ["key"], "additionalProperties": False},
    }]}, environment={"type": "openai_hosted"})

    def until(predicate, timeout=120):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = predicate()
            if value:
                return value
            time.sleep(0.2)
        raise AssertionError("Public workflow did not reach expected state")

    def items():
        response = http.get(endpoint + "/" + session.id + "/items", headers=headers, params={"limit": 100, "order": "asc"})
        assert response.status_code == 200
        raw = response.json()["data"]
        assert raw == [item.to_dict() for item in sessions.items.list(session.id, limit=100, order="asc").data]
        return raw

    def invoke(text, key, handler):
        with sessions.stream(session.id, input=text, tool_handlers={"lookup": handler}, idempotency_key=key, timeout=240) as stream:
            events = [event.to_dict() for event in stream]
        observed.append(events)
        terminals = [e for e in events if e["type"] in ("agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled")]
        assert len(terminals) == 1 and terminals[0]["type"] == "agent.session.turn.completed"
        assert events[-1]["type"] == "agent.session.idle"
        function = [e for e in events if e["type"] == "agent.session.turn.item.added" and e["item"]["type"] == "function_call"]
        result = [e for e in events if e["type"] == "agent.session.turn.item.added" and e["item"]["type"] == "function_call_output"]
        assert len(function) == len(result) == 1
        assert function[0]["item"]["call_id"] == result[0]["item"]["call_id"]
        assert events.index(function[0]) < events.index(result[0]) < events.index(terminals[0]) < len(events) - 1
        assert sessions.retrieve(session.id).required_actions == []
        return terminals[0]["turn"]["id"], result[0]["item"]

    try:
        def success(arguments):
            assert arguments == {"key": "success"}
            calls.append(arguments)
            return marker

        turn, result = invoke("Call lookup once with key success. Remember its returned string in conversation. Then use the native shell to create /workspace/outputs/function.txt containing exactly that string, with no newline. Do not call any other function.", "function-success", success)
        assert result["output"] == marker and "error" not in result
        artifacts = list(sessions.artifacts.list(session.id, limit=100))
        output = [a for a in artifacts if a.turn_id == turn and a.path == "/workspace/outputs/function.txt"]
        assert len(output) == 1
        with sessions.artifacts.with_streaming_response.content(output[0].id, session_id=session.id) as response:
            assert response.read() == marker.encode()
        listing = client.beta.agents.environments.files.list(session.environment.id, path="/workspace/outputs")
        assert any(f.path == "/workspace/outputs/function.txt" for f in listing)
        assert any(i["type"] == "function_call_output" and i.get("output") == marker for i in items())
        checks.append("same_turn_function_native_file_and_public_artifact")

        restart(session.id, session.environment.id)

        def failure(arguments):
            assert arguments == {"key": "failure"}
            calls.append(arguments)
            raise RuntimeError(private_error)

        _, result = invoke("Call lookup exactly once with key failure. If it fails, do not retry. Then reply with the exact string returned by lookup in our first turn, using conversation history and no file tools.", "function-failure", failure)
        assert result["error"] == "Tool handler failed." and "output" not in result
        messages = [i for i in items() if i["type"] == "message" and i["role"] == "assistant"]
        assert marker in "\n".join(p.get("text", "") for p in messages[-1]["content"])
        assert len(calls) == 2
        checks.append("cold_history_continuation_and_public_handler_error")

        sessions.events.create(session.id, events=[{"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": "Call lookup once with key pending and wait for the result."}]}]}], idempotency_key="function-pending")
        until(lambda: sessions.retrieve(session.id).required_actions)
        pending = [i for i in items() if i["type"] == "function_call"][-1]
        foreign_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
        result_event = {"type": "agent.session.input.tool_result", "call_id": pending["call_id"], "turn_id": sessions.turns.list(session.id).data[0].id, "success": True, "output": "foreign-value"}
        response = http.post(endpoint + "/" + session.id + "/events", headers=foreign_headers, json={"events": [result_event]})
        assert response.status_code == 404
        cancel = [{"type": "agent.session.input.cancel"}]
        sessions.events.create(session.id, events=cancel, idempotency_key="cancel-pending")
        sessions.events.create(session.id, events=cancel, idempotency_key="cancel-pending")
        until(lambda: len(sessions.turns.list(session.id).data) == 3 and sessions.turns.list(session.id).data[0].status == "cancelled")
        assert sessions.retrieve(session.id).required_actions == []
        assert not any(i["type"] == "function_call_output" and i["call_id"] == pending["call_id"] for i in items())
        checks.append("pending_function_tenant_isolation_and_cancel_retry")
        proof = {"checks": checks, "session": session.id, "calls": calls, "events": observed, "items": items()}
        assert private_error not in json.dumps(proof)
        Path(evidence).write_text(json.dumps(proof, indent=2))
        return checks
    finally:
        sessions.delete(session.id)
