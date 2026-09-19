"""Opt-in real-model MiniMax Code acceptance through the pinned public client."""
import importlib.metadata
import json
import sys
import time
import uuid
from pathlib import Path

import httpx2
from openai import OpenAI


def main():
    base, token, foreign, model, stage, output = sys.argv[1:]
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    dist = importlib.metadata.distribution("openai")
    assert dist.version == pin["sdk_version"]
    assert json.loads(dist.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    http = httpx2.Client(trust_env=False, timeout=300)
    client = OpenAI(base_url=base + "/v1", api_key=token, max_retries=0,
                    _strict_response_validation=True, http_client=http)
    sessions = client.beta.agents.sessions
    headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
    record = {} if stage == "initial" else json.loads(Path(output).read_text())

    def message(text):
        return {"type": "agent.session.input.message", "input": [
            {"role": "user", "content": [{"type": "input_text", "text": text}]}]}

    def submit(sid, text, key):
        sessions.events.create(sid, events=[message(text)], idempotency_key=key)

    def execute(sid, text, steer=False, cancel=False):
        types, submitted = [], False
        with sessions.events.stream(sid, timeout=300) as stream:
            submit(sid, text, str(uuid.uuid4()))
            for event in stream:
                types.append(event.type)
                assert event.type != "agent.session.failed", event
                if event.type == "agent.session.turn.output_text.delta" and not submitted:
                    submitted = True
                    if steer:
                        submit(sid, "Stop the list now and reply MCODE-STEERED.", "steer")
                    if cancel:
                        sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}])
                if event.type == "agent.session.idle":
                    break
        end = "agent.session.turn.cancelled" if cancel else "agent.session.turn.completed"
        assert end in types, types
        assert types.index("agent.session.turn.created") < types.index(end) < len(types) - 1
        if steer or cancel:
            assert submitted, "No native output to trigger the operation"
        return types

    def answer(sid):
        items = sessions.items.list(sid, order="asc", limit=100).data
        answers = [i for i in items if i.type == "message" and i.role == "assistant"]
        return "\n".join(c.text for c in answers[-1].content if c.type == "output_text")

    try:
        if stage == "initial":
            marker = "MCODE-MEMORY-" + uuid.uuid4().hex[:12]
            session = sessions.create(agent={"model": model, "instructions": "Follow user instructions. Remember supplied markers. Do not use tools."}, environment={"type": "none"})
            record = {"session": session.id, "marker": marker, "model": model, "checks": []}
            Path(output).write_text(json.dumps(record, indent=2))
            for agent_patch, environment in [({"tools": [{"type": "function", "name": "f", "parameters": {"type": "object"}}]}, {"type": "none"}), ({"text": {"verbosity": "high"}}, {"type": "none"}), ({}, {"type": "openai_hosted"})]:
                r = http.post(base + "/v1/agents/sessions", headers=headers, json={"agent": {"model": model, **agent_patch}, "environment": environment})
                assert r.status_code == 400, r.status_code
            record["initial_events"] = execute(session.id, "Remember " + marker + ". Write 120 numbered lines explaining addition, one sentence per line. Start immediately.", steer=True)
            turns = sessions.turns.list(session.id, order="asc", limit=100).data
            assert len(turns) == 1 and turns[0].status == "completed", [(t.id, t.status) for t in turns]
            record["first_turn"] = turns[0].id
            record["checks"] += ["real_native_execution", "active_steering_same_turn", "unsupported_operations_rejected"]
        else:
            sid = record["session"]
            execute(sid, "Reply with the MCODE-MEMORY marker I gave you earlier, and nothing else.")
            assert record["marker"] in answer(sid), "Cold native history was not continued"
            foreign_headers = {**headers, "Authorization": "Bearer " + foreign}
            for path in ["", "/items", "/turns"]:
                assert http.get(base + "/v1/agents/sessions/" + sid + path, headers=foreign_headers).status_code == 404
            # The input must remain ordinary text instead of triggering ACP /model.
            execute(sid, "/model")
            assert sessions.turns.list(sid, order="asc", limit=100).data[-1].status == "completed"
            record["cancel_events"] = execute(sid, "Print the integers from 1 to 10000, one per line. Start with 1 immediately; no explanation or planning.", cancel=True)
            execute(sid, "Reply with the original MCODE-MEMORY marker only.")
            assert record["marker"] in answer(sid)
            record["checks"] += ["cold_daemon_history_continuation", "foreign_tenant_rejected", "slash_text_execution", "cancel_and_continue"]
            record["passed"] = True
        Path(output).write_text(json.dumps(record, indent=2))
    finally:
        client.close()


if __name__ == "__main__":
    main()
