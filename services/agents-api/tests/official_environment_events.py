"""Validate retained connection observations against the pinned SDK and raw shape."""

import importlib.metadata
import json
from pathlib import Path
import sys

from openai._models import validate_type
from openai.types.beta.agent_session_environment_connected_event import AgentSessionEnvironmentConnectedEvent
from openai.types.beta.agent_session_environment_disconnected_event import AgentSessionEnvironmentDisconnectedEvent


def main():
    root = Path(__file__).resolve().parents[3]
    pin = json.loads((root / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == "3.13.0"
    assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]
    events = json.loads(Path(sys.argv[1]).read_text())
    models = {"connected": AgentSessionEnvironmentConnectedEvent,
              "disconnected": AgentSessionEnvironmentDisconnectedEvent}
    assert len(events) >= 3
    ids = set()
    previous = None
    for event in events:
        assert set(event) == {"type", "event_id", "session_id", "environment"}
        state = event["environment"]
        assert set(state) == {"id", "type", "status", "error"}
        assert state["type"] == "self_hosted" and state["error"] is None
        status = state["status"]
        assert status in models and status != previous
        assert event["type"] == "agent.session.environment." + status
        parsed = validate_type(type_=models[status], value=event)
        assert parsed.environment.id == state["id"]
        assert parsed.session_id == event["session_id"] and parsed.turn_id is None
        assert event["event_id"] not in ids
        ids.add(event["event_id"])
        previous = status
    print(f"Pinned SDK and raw Environment event snapshots passed: {len(events)} transitions.")


if __name__ == "__main__":
    main()
