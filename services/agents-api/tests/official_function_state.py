"""Verify function-action reads using real storage fixtures, without claiming execution coverage."""

import importlib.metadata
import json
from pathlib import Path
import sys

import httpx2
from openai import NotFoundError, OpenAI

base, token, foreign, session_id, turn_id = sys.argv[1:]
pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
source = json.loads(importlib.metadata.distribution("openai").read_text("direct_url.json") or "{}")
assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]


def client(key):
    return OpenAI(api_key=key, base_url=base + "/v1", max_retries=0,
                  _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=15))


def verify_actions(session, ids):
    assert session.status == ("requires_action" if ids else "in_progress")
    assert [action.call_id for action in session.required_actions] == ids
    for action in session.required_actions:
        assert action.to_dict() == {"type": "function_call", "call_id": action.call_id,
                                   "name": "lookup", "turn_id": turn_id,
                                   "arguments": {"ticket": 9007199254740993}}, action.to_dict()


with client(token) as api, client(foreign) as stranger:
    sessions = api.beta.agents.sessions
    verify_actions(sessions.retrieve(session_id), ["first"])
    verify_actions(sessions.list().data[0], ["first"])
    assert sessions.turns.retrieve(turn_id, session_id=session_id).status == "waiting"
    assert sessions.turns.list(session_id).data[0].status == "waiting"
    try:
        stranger.beta.agents.sessions.retrieve(session_id)
        raise AssertionError("foreign session was visible")
    except NotFoundError:
        pass
    assert stranger.beta.agents.sessions.list().data == []
    try:
        stranger.beta.agents.sessions.events.stream(session_id)
        raise AssertionError("foreign event stream was visible")
    except NotFoundError:
        pass
    states = []
    with sessions.events.stream(session_id) as stream:
        print("connected", flush=True)
        for event in stream:
            if event.type.startswith("agent.session.turn."):
                assert event.type != "agent.session.turn.waiting"
                continue
            wire = event.to_dict()
            assert set(wire) == {"event_id", "session", "type"}, wire
            states.append(event.type)
            if event.type == "agent.session.idle":
                assert event.session.status == "idle" and event.session.required_actions == []
                break
            verify_actions(event.session, [["first", "second"], ["second"], []][len(states)-1])
    assert states == ["agent.session.requires_action", "agent.session.requires_action",
                      "agent.session.in_progress", "agent.session.idle"]
    restored = sessions.retrieve(session_id)
    assert restored.status == "idle" and restored.required_actions == []
    assert sessions.turns.retrieve(turn_id, session_id=session_id).status == "completed"
