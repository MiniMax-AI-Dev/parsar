"""Fixed SDK and raw HTTP checks for creation streams on a paused worker."""
import json
import uuid


def event_data(lines):
    for line in lines:
        if line.startswith("data: "):
            return json.loads(line[6:])
    raise AssertionError("stream ended before an event")


def verify_creation_streams(client, raw, base, headers, foreign, unsupported):
    sessions = client.beta.agents.sessions
    spec = {"agent": {"model": "test-model"}, "environment": {"type": "none"}}
    saved = client.beta.agents.create(model="test-model", instructions="Saved stream configuration.")
    forms = [None, "First", [{"role": "user", "content": [{"type": "input_text", "text": "First"}]},
                              {"role": "user", "content": [{"type": "input_text", "text": "Second"}]}]]
    for index, initial in enumerate(forms):
        config = spec if index != 2 else {"agent_id": saved.id, "environment": {"type": "none"}}
        request = {**config, "input": initial}
        key = {"Idempotency-Key": str(uuid.uuid4())}
        with sessions.create(**request, stream=True, extra_headers=key) as stream:
            first = next(stream)
            assert first.type == "agent.session.created"
            assert set(first.to_dict()) == {"type", "event_id", "session"}
            session = first.session
            assert session.status == "idle" and session.last_active_at == session.created_at
            if index == 2:
                assert session.agent.id == saved.id and session.agent.instructions == saved.instructions
            if initial is None:
                sessions.events.create(session.id, events=[{"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": "First"}]}]}])
            count = 2 if index == 2 else 1
            events = [next(stream) for _ in range(2 + count)]
            assert [event.type for event in events] == ["agent.session.turn.created", "agent.session.in_progress"] + ["agent.session.turn.item.added"] * count
            assert len({event.event_id for event in [first, *events]}) == 3 + count
            assert events[0].turn.id == list(sessions.turns.list(session.id))[0].id
            assert events[1].session.status == "in_progress"
            items = list(sessions.items.list(session.id, order="asc"))
            assert [event.item.to_dict() for event in events[2:]] == [item.to_dict() for item in items]
            assert [item.content[0].text for item in items] == (["First", "Second"] if count == 2 else ["First"])
            sessions.events.create(session.id, events=[{"type": "agent.session.input.cancel"}])
            assert [next(stream).type, next(stream).type] == ["agent.session.turn.cancelled", "agent.session.idle"]
            # An idle creation stream stays available for a later Turn.
            sessions.events.create(session.id, events=[{"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": "Next"}]}]}])
            assert next(stream).type == "agent.session.turn.created"
        current = sessions.retrieve(session.id)
        assert current.status == "in_progress", "disconnect cancelled admitted work"
        assert sessions.create(**request, stream=False, extra_headers=key).id == session.id
        assert len(list(sessions.turns.list(session.id))) == 2
        sessions.events.create(session.id, events=[{"type": "agent.session.input.cancel"}])
        # A creation retry observes from the upsert cursor, with no old created/Turn/Item replay.
        with raw.stream("POST", base + "/v1/agents/sessions", headers={**headers, **key, "Last-Event-ID": first.event_id},
                        json={**request, "stream": True}) as response:
            assert response.status_code == 200 and response.headers["content-type"] == "text/event-stream"
            lines = response.iter_lines()
            assert next(lines) == ": connected"
            previous_turns = {turn.id for turn in sessions.turns.list(session.id)}
            sessions.events.create(session.id, events=[{"type": "agent.session.input.message", "input": [{"role": "user", "content": [{"type": "input_text", "text": "After retry"}]}]}])
            event = event_data(lines)
            assert event["type"] == "agent.session.turn.created" and event["event_id"] != events[0].event_id
            assert event["turn"]["id"] not in previous_turns
        assert len(list(sessions.turns.list(session.id))) == 3
        sessions.events.create(session.id, events=[{"type": "agent.session.input.cancel"}])
        response = raw.get(base + "/v1/agents/sessions/" + session.id, headers={**headers, "Authorization": "Bearer " + foreign})
        assert response.status_code == 404

    # Raw first-frame shape, early disconnect and same-key JSON recovery.
    key = {"Idempotency-Key": str(uuid.uuid4())}
    request = {**spec, "input": "Disconnect after creation"}
    with raw.stream("POST", base + "/v1/agents/sessions", headers={**headers, **key}, json={**request, "stream": True}) as response:
        event = event_data(response.iter_lines())
        assert set(event) == {"type", "event_id", "session"} and event["type"] == "agent.session.created"
    recovered = sessions.create(**request, extra_headers=key)
    assert recovered.id == event["session"]["id"] and recovered.status == "in_progress"
    assert len(list(sessions.turns.list(recovered.id))) == 1
    sessions.events.create(recovered.id, events=[{"type": "agent.session.input.cancel"}])

    before = {session.id for session in sessions.list()}
    for changed_headers, fields, status in [({"Authorization": "Bearer invalid"}, {}, 401),
                                            ({"OpenAI-Beta": ""}, {}, 400),
                                            ({}, {"input": []}, 400),
                                            ({}, {"stream": None}, 400),
                                            ({**key}, {"input": "Changed"}, 409)]:
        response = raw.post(base + "/v1/agents/sessions", headers={**headers, **changed_headers},
                            json={**request, "stream": True, **fields})
        assert response.status_code == status and response.headers["content-type"].startswith("application/json")
    response = raw.post(unsupported + "/v1/agents/sessions", headers=headers, json={**request, "stream": True})
    assert response.status_code == 400 and response.headers["content-type"].startswith("application/json")
    assert {session.id for session in sessions.list()} == before
    print("Creation streams: fixed SDK/raw HTTP, initial and idle creation, snapshots/order, saved Agents, safe retries, later Turns, disconnect recovery and pre-stream errors passed.")
