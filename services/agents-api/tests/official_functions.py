"""Verify public function configuration and execution against a real daemon/Codex."""

import importlib.metadata
import json
from pathlib import Path
import sys

import httpx2
from openai import ConflictError, OpenAI

base, token, output_path, evidence = sys.argv[1:]
pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
source = json.loads(importlib.metadata.distribution("openai").read_text("direct_url.json") or "{}")
assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]
output = json.loads(Path(output_path).read_text())
tool = {"type":"function","name":"lookup_ticket","description":"Read a synthetic ticket",
        "parameters":{"type":"object","properties":{"ticket":{"type":"string"}},"required":["ticket"],"additionalProperties":False}}
agent = {"model":"gpt-5.5","tools":[tool]}
with OpenAI(base_url=base+"/v1", api_key=token, max_retries=0, _strict_response_validation=True,
            http_client=httpx2.Client(trust_env=False, timeout=30)) as client:
    sessions = client.beta.agents.sessions
    session = sessions.create(agent=agent, environment={"type":"none"}, extra_headers={"Idempotency-Key":"functions"})
    expected = dict(tool, defer_loading=False)
    assert session.agent.tools[0].to_dict() == expected, session.agent.tools
    tool["defer_loading"] = False
    assert sessions.create(agent=agent, environment={"type":"none"}, extra_headers={"Idempotency-Key":"functions"}).id == session.id
    tool["description"] = "changed"
    try:
        sessions.create(agent=agent, environment={"type":"none"}, extra_headers={"Idempotency-Key":"functions"})
        raise AssertionError("changed tools reused a creation identity")
    except ConflictError:
        pass
    turns, calls = [], []
    for index in range(3):
        handled = False
        with sessions.events.stream(session.id, timeout=30) as stream:
            message = {"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"Look up ticket 42"}]}]}
            sessions.events.create(session.id, events=[message], idempotency_key="message-"+str(index))
            for event in stream:
                if event.type in ("agent.session.turn.item.added", "agent.session.turn.item.done") and event.item.type == "function_call_output":
                    assert event.type == "agent.session.turn.item.added" and event.output_index is None
                    submitted = event.item.to_dict()
                    assert submitted["output"] == output
                    if index == 1:
                        assert submitted["error"] == "synthetic failure"
                    else:
                        assert "error" not in submitted
                if event.type == "agent.session.requires_action" and not handled:
                    assert event.session.agent.tools[0].to_dict() == expected
                    action = event.session.required_actions[0]
                    assert action.name == "lookup_ticket" and action.arguments == {"ticket":"42"}
                    assert sessions.turns.retrieve(action.turn_id, session_id=session.id).status == "waiting"
                    turns.append(action.turn_id); calls.append(action.call_id); handled = True
                    if index == 2:
                        sessions.events.create(session.id, events=[{"type":"agent.session.input.cancel"}], idempotency_key="cancel")
                    else:
                        result = {"type":"agent.session.input.tool_result","turn_id":action.turn_id,"call_id":action.call_id,"success":index==0,"output":output}
                        if index == 1:
                            result["error"] = "synthetic failure"
                        for _ in range(2):
                            assert sessions.events.create(session.id, events=[result], idempotency_key="result-"+str(index)) is None
                if event.type == "agent.session.idle":
                    assert handled and event.session.required_actions == []
                    break
                assert event.type != "agent.session.failed", event.to_dict()
        assert sessions.turns.retrieve(turns[-1], session_id=session.id).status == ("cancelled" if index==2 else "completed")
        assert len(sessions.turns.list(session.id).data) == index+1
    items = sessions.items.list(session.id, limit=100, order="asc").data
    assert all(any(item.type=="function_call" and item.call_id==call for item in items) for call in calls)
    results = {item.call_id: item.to_dict() for item in items if item.type=="function_call_output"}
    assert len(results)==2
    for index, call in enumerate(calls[:2]):
        assert results[call]["output"] == output
        if index == 1:
            assert results[call]["error"] == "synthetic failure"
        else:
            assert "error" not in results[call]
    answers = [item for item in items if item.type=="message" and item.role=="assistant"]
    assert len(answers)==2 and all(item.content[0].text=="FUNCTION-EXECUTION-OK" for item in answers)
    assert sessions.retrieve(session.id).agent.tools[0].to_dict() == expected
    Path(evidence).write_text(json.dumps({"session":session.id,"turns":turns,"calls":calls,"configured_tool":expected}))
