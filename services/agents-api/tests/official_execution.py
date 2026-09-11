"""Verify public execution against a native-daemon integration fixture."""

import importlib.metadata
import json
import sys
import time
from pathlib import Path

import httpx2
from openai import ConflictError, NotFoundError, OpenAI


def main():
    base, token, foreign_token, evidence = sys.argv[1:]
    root = Path(__file__).resolve().parents[3]
    pin = json.loads((root / "contracts/agents-api/upstream.json").read_text())
    source = json.loads(importlib.metadata.distribution("openai").read_text("direct_url.json"))
    assert source["vcs_info"]["commit_id"] == pin["commit"]
    client = OpenAI(base_url=base + "/v1", api_key=token, max_retries=0,
                    _strict_response_validation=True, http_client=httpx2.Client(trust_env=False))
    foreign = OpenAI(base_url=base + "/v1", api_key=foreign_token, max_retries=0,
                     _strict_response_validation=True, http_client=httpx2.Client(trust_env=False))
    sessions = client.beta.agents.sessions

    def message(*texts):
        return {"type": "agent.session.input.message", "input": [
            {"role": "user", "content": [{"type": "input_text", "text": text}]} for text in texts]}

    def wait_turn(session, status, count=1):
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            turns = sessions.turns.list(session, limit=100, order="asc").data
            if len(turns) == count and turns[-1].status == status:
                return turns[-1]
            time.sleep(0.05)
        raise AssertionError([(turn.id, turn.status, turn.error) for turn in turns])

    def create():
        return sessions.create(agent={"model": "gpt-5.5", "instructions": "Keep the conversation."},
                               environment={"type": "none"})

    try:
        session = create()
        event = message("First public message.", "Second message in the same event.")
        assert sessions.events.create(session.id, events=[event], idempotency_key="first") is None
        sessions.events.create(session.id, events=[event], idempotency_key="first")
        first = wait_turn(session.id, "completed")
        assert sessions.retrieve(session.id).status == "idle"
        expected_usage = {"input_tokens": 10, "input_tokens_details": {"cached_tokens": 4},
                          "output_tokens": 3, "output_tokens_details": {"reasoning_tokens": 2}, "total_tokens": 13}
        assert first.usage is not None and first.usage.model_dump() == expected_usage, first.usage
        assert sessions.retrieve(session.id).usage.model_dump() == expected_usage
        items = sessions.items.list(session.id, limit=100, order="asc").data
        users = [item for item in items if item.type == "message" and item.role == "user"]
        answers = [item for item in items if item.type == "message" and item.role == "assistant"]
        assert len(users) == 2 and len(answers) == 1
        assert answers[0].content[0].text == "NO-ENVIRONMENT-OK"
        try:
            sessions.events.create(session.id, events=[message("changed")], idempotency_key="first")
            raise AssertionError("changed retry accepted")
        except ConflictError:
            pass
        try:
            foreign.beta.agents.sessions.events.create(session.id, events=[event])
            raise AssertionError("foreign tenant admitted")
        except NotFoundError:
            pass
        sessions.events.create(session.id, events=[message("Continue the same native conversation.")], idempotency_key="second")
        second = wait_turn(session.id, "completed", 2)
        assert second.usage.model_dump() == expected_usage, second.usage
        expected_total = {"input_tokens": 20, "input_tokens_details": {"cached_tokens": 8},
                          "output_tokens": 6, "output_tokens_details": {"reasoning_tokens": 4}, "total_tokens": 26}
        assert sessions.retrieve(session.id).usage.model_dump() == expected_total
        sessions.events.create(session.id, events=[event], idempotency_key="first")
        assert len(sessions.turns.list(session.id).data) == 2
        client.close()
        client = OpenAI(base_url=base + "/v1", api_key=token, max_retries=0,
                        _strict_response_validation=True, http_client=httpx2.Client(trust_env=False))
        sessions = client.beta.agents.sessions
        assert sessions.turns.retrieve(second.id, session_id=session.id).status == "completed"
        assert len(sessions.items.list(session.id, limit=100).data) == 5
        assert sessions.retrieve(session.id).usage.model_dump() == expected_total
        assert sessions.turns.retrieve(first.id, session_id=session.id).usage.model_dump() == expected_usage
        cancelled = create()
        sessions.events.create(cancelled.id, events=[message("PUBLIC-CANCEL")])
        wait_turn(cancelled.id, "in_progress")
        time.sleep(0.5)
        sessions.events.create(cancelled.id, events=[{"type": "agent.session.input.cancel"}], idempotency_key="cancel")
        stopped = wait_turn(cancelled.id, "cancelled")
        assert sessions.retrieve(cancelled.id).status == "idle"
        Path(evidence).write_text(json.dumps({"session": session.id, "turns": [first.id, second.id],
                                              "cancelled_session": cancelled.id, "cancelled_turn": stopped.id}))
    finally:
        client.close()
        foreign.close()


if __name__ == "__main__":
    main()
