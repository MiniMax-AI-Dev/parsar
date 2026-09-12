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

    def until_idle(stream):
        events = []
        for event in stream:
            events.append(event)
            assert len(events) < 1000
            if event.type == "agent.session.turn.output_text.delta":
                assert sessions.turns.retrieve(event.turn_id, session_id=event.session_id).status == "in_progress"
            if event.type == "agent.session.idle":
                assert event.session.status == "idle"
                assert len({value.event_id for value in events}) == len(events)
                return events
        raise AssertionError("stream ended without an idle Session")

    try:
        session = create()
        assert session.agent.tools == []
        event = message("Search the web for this answer.", "Second message in the same event.")
        with sessions.events.stream(session.id, timeout=20) as stream:
            assert sessions.events.create(session.id, events=[event], idempotency_key="first") is None
            sessions.events.create(session.id, events=[event], idempotency_key="first")
            first_events = until_idle(stream)
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
        types = [value.type for value in first_events]
        for kind in ("created", "in_progress", "completed", "item.added", "item.done",
                     "content_part.added", "content_part.done", "output_text.delta", "output_text.done"):
            assert "agent.session.turn." + kind in types, types
        terminal = next(value for value in first_events if value.type == "agent.session.turn.completed")
        assert terminal.turn.usage.model_dump() == expected_usage
        assert first_events[-1].session.usage.model_dump() == expected_usage
        text_events = [value for value in first_events if value.type.startswith("agent.session.turn.output_text.")]
        assert all(value.item_id == answers[0].id and value.output_index == 0 and value.content_index == 0 for value in text_events)
        assert text_events[-1].text == answers[0].content[0].text
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
        with sessions.events.stream(session.id, timeout=20, extra_headers={"Last-Event-ID": first_events[-1].event_id}) as stream:
            sessions.events.create(session.id, events=[message("Continue the same native conversation.")], idempotency_key="second")
            second_events = until_idle(stream)
        second = wait_turn(session.id, "completed", 2)
        assert all(getattr(value, "turn_id", None) != first.id for value in second_events)
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
        retained = sessions.items.list(cancelled.id, limit=100).data
        with sessions.events.stream(cancelled.id, timeout=20) as stream:
            sessions.events.create(cancelled.id, events=[{"type": "agent.session.input.cancel"}], idempotency_key="cancel")
            cancelled_events = until_idle(stream)
        stopped = wait_turn(cancelled.id, "cancelled")
        assert any(value.type == "agent.session.turn.cancelled" and value.turn.id == stopped.id for value in cancelled_events)
        assert not any(value.type == "agent.session.turn.created" for value in cancelled_events)
        assert {item.id for item in retained} <= {item.id for item in sessions.items.list(cancelled.id, limit=100).data}
        assert sessions.retrieve(cancelled.id).status == "idle"
        for verbosity in ("low", "medium", "high"):
            agent = {"model": "gpt-5.5", "text": {"verbosity": verbosity, "format": {"type": "text"}}}
            key = "text-" + verbosity
            configured = sessions.create(agent=agent, environment={"type": "none"}, extra_headers={"Idempotency-Key": key})
            agent["text"]["format"] = None
            assert sessions.create(agent=agent, environment={"type": "none"}, extra_headers={"Idempotency-Key": key}).id == configured.id
            assert configured.agent.text.model_dump() == {"format": {"type": "text"}, "verbosity": verbosity}
            for count in (1, 2):
                sessions.events.create(configured.id, events=[message("TEXT-VERBOSITY:" + verbosity)])
                wait_turn(configured.id, "completed", count)
                assert sessions.retrieve(configured.id).agent.text == configured.agent.text
            agent["text"]["verbosity"] = "high" if verbosity != "high" else "low"
            try:
                sessions.create(agent=agent, environment={"type": "none"}, extra_headers={"Idempotency-Key": key})
                raise AssertionError("changed text configuration reused a retry key")
            except ConflictError:
                pass
        default_agent = {"model": "custom-provider-model"}
        default = sessions.create(agent=default_agent, environment={"type": "none"},
                                  extra_headers={"Idempotency-Key": "native-default"})
        for text in (None, {"verbosity": None}, {"verbosity": "medium"}):
            configured = sessions.create(agent=dict(default_agent, text=text), environment={"type": "none"},
                                         extra_headers={"Idempotency-Key": "native-default"})
            assert configured.id == default.id and configured.agent.text.verbosity == "medium"
        for count in (1, 2):
            sessions.events.create(default.id, events=[message("DEFAULT-VERBOSITY")])
            wait_turn(default.id, "completed", count)
            assert sessions.retrieve(default.id).agent.text.verbosity == "medium"
        unsupported = sessions.create(agent={"model": "custom-provider-model", "text": {"verbosity": "high"}},
                                      environment={"type": "none"})
        sessions.events.create(unsupported.id, events=[message("UNSUPPORTED-VERBOSITY")])
        failed = wait_turn(unsupported.id, "failed")
        assert failed.error is not None and failed.error.code == "internal_error", failed.error
        assert sessions.retrieve(unsupported.id).status == "failed"
        Path(evidence).write_text(json.dumps({"session": session.id, "turns": [first.id, second.id],
                                              "cancelled_session": cancelled.id, "cancelled_turn": stopped.id,
                                              "stream_types": types, "reconnected_events": len(second_events),
                                              "cancelled_events": [value.type for value in cancelled_events],
                                              "verbosity_new_and_resumed": ["low", "medium", "high"],
                                              "native_default_session": default.id,
                                              "unsupported_verbosity": failed.error.model_dump()}))
    finally:
        client.close()
        foreign.close()


if __name__ == "__main__":
    main()
