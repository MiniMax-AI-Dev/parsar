"""Verify the pinned stream helper through the public API and native execution."""

import importlib.metadata
import json
from pathlib import Path
import sys

import httpx2
from openai import OpenAI

base, token, evidence = sys.argv[1:]
pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
source = json.loads(importlib.metadata.distribution("openai").read_text("direct_url.json") or "{}")
assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]
assert importlib.metadata.version("openai") == pin["sdk_version"]
agent = {"model": "gpt-5.5", "tools": [{
    "type": "function", "name": "lookup_ticket", "description": "Read a synthetic ticket",
    "parameters": {"type": "object", "properties": {"ticket": {"type": "string"}},
                   "required": ["ticket"], "additionalProperties": False},
}]}
expected = [{"output": '{"ticket":"42","status":"open"}'}, {"error": "Tool handler failed."}]
with OpenAI(base_url=base + "/v1", api_key=token, max_retries=0, _strict_response_validation=True,
            http_client=httpx2.Client(trust_env=False, timeout=30)) as client:
    sessions = client.beta.agents.sessions
    session = sessions.create(agent=agent, environment={"type": "none"})
    turns, calls, handler_calls, observed = [], [], [], []
    for index in range(2):
        def lookup_ticket(arguments):
            assert arguments == {"ticket": "42"}, arguments
            handler_calls.append(arguments)
            if index == 1:
                raise RuntimeError("private-handler-exception-must-not-be-exposed")
            return {"ticket": arguments["ticket"], "status": "open"}

        with sessions.stream(session.id, input="Look up ticket 42", timeout=30,
                             tool_handlers={"lookup_ticket": lookup_ticket},
                             idempotency_key="helper-" + str(index)) as stream:
            events = [event.to_dict() for event in stream]
        observed.append(events)
        assert len(handler_calls) == index + 1, handler_calls
        created = [event for event in events if event["type"] == "agent.session.turn.created"]
        assert len(created) == 1, events
        turn_id = created[0]["turn"]["id"]
        turns.append(turn_id)
        added = [event for event in events if event["type"] == "agent.session.turn.item.added"]
        functions = [event for event in added if event["item"]["type"] == "function_call"]
        assert len(functions) == 1 and functions[0]["turn_id"] == turn_id, events
        call_id = functions[0]["item"]["call_id"]
        calls.append(call_id)
        results = [event for event in added if event["item"]["type"] == "function_call_output"]
        assert len(results) == 1 and results[0]["turn_id"] == turn_id, events
        assert results[0].get("output_index") is None
        result = results[0]["item"]
        assert result["call_id"] == call_id
        assert {key: result[key] for key in ("output", "error") if key in result} == expected[index], result
        assert not any(event["type"] == "agent.session.turn.item.done" and
                       event["item"]["type"] == "function_call_output" for event in events)
        terminal = [event for event in events if event["type"] in (
            "agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled")]
        assert len(terminal) == 1 and terminal[0]["type"] == "agent.session.turn.completed", events
        assert terminal[0]["turn"]["id"] == turn_id
        assert events.index(functions[0]) < events.index(results[0]) < events.index(terminal[0]) < len(events) - 1
        assert events[-1]["type"] == "agent.session.idle" and events[-1]["session"]["required_actions"] == []
        assert not any(event["type"] == "agent.session.failed" for event in events)
        assert sessions.turns.retrieve(turn_id, session_id=session.id).status == "completed"
        current = sessions.retrieve(session.id)
        assert current.status == "idle" and current.required_actions == []
    items = sessions.items.list(session.id, limit=100, order="asc").data
    results = {item.call_id: item.to_dict() for item in items if item.type == "function_call_output"}
    assert len(results) == 2 and len(sessions.turns.list(session.id).data) == 2
    for index, call_id in enumerate(calls):
        assert {key: results[call_id][key] for key in ("output", "error") if key in results[call_id]} == expected[index]
    answers = [item for item in items if item.type == "message" and item.role == "assistant"]
    assert len(answers) == 2 and all(item.content[0].text == "FUNCTION-EXECUTION-OK" for item in answers)
    proof = {"session": session.id, "turns": turns, "calls": calls, "handler_calls": handler_calls,
             "events": observed, "results": results}
    assert "private-handler-exception-must-not-be-exposed" not in json.dumps(proof)
    Path(evidence).write_text(json.dumps(proof))
