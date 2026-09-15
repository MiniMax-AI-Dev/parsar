"""Initial creation checks shared by the public self-hosted native fixture."""

import time


def create_initial_session(sessions, request, key, mode, assert_empty, proof):
    began = time.monotonic()
    arguments = {**request, "extra_headers": {"Idempotency-Key": key}, "timeout": 10}
    if mode == "ordinary_initial":
        created = sessions.create(**arguments)
        proof["creation_response"] = created.to_dict()
        elapsed = time.monotonic() - began
    else:
        assert mode == "streamed_initial"
        with sessions.create(**arguments, stream=True) as stream:
            first = next(stream)
            elapsed = time.monotonic() - began
            assert first.type == "agent.session.created"
            assert set(first.to_dict()) == {"type", "event_id", "session"}
            assert_empty(first.session)
            action = next(stream)
            assert action.type == "agent.session.requires_action"
            assert set(action.to_dict()) == {"type", "event_id", "session"}
            assert action.event_id != first.event_id
            assert action.session.id == first.session.id
            assert action.session.environment == first.session.environment
            proof["creation_events"] = [first.to_dict(), action.to_dict()]
            created = action.session
        proof["creation_stream_closed_before_connection"] = True
    assert elapsed < 10, "initial creation waited for an offline executor"
    assert created.status == "requires_action" and created.error is None and created.usage is None
    proof["creation_response_elapsed_seconds"] = elapsed
    return created


def assert_creation_retry(sessions, raw, request, key, session_id):
    assert sessions.create(**request, extra_headers={"Idempotency-Key": key}).id == session_id
    response = raw.post("/agents/sessions", json={**request, "input": "Changed initial retry"},
                        headers={"Idempotency-Key": key})
    assert response.status_code == 409
