"""Saved-reference retry identity using real PostgreSQL and controlled source mutations."""

import concurrent.futures
import json
import sys
import time

import httpx2
from openai import OpenAI, ConflictError, NotFoundError, BadRequestError


def expect(kind, action):
    try:
        action()
    except kind:
        return
    raise AssertionError(f"expected {kind.__name__}")


def main():
    base, token, foreign, control, restarted = sys.argv[1:]
    with httpx2.Client(trust_env=False, timeout=15) as transport:
        client = OpenAI(api_key=token, base_url=base + "/v1", http_client=transport, max_retries=0)
        other = OpenAI(api_key=foreign, base_url=base + "/v1", http_client=transport, max_retries=0)
        sessions = client.beta.agents.sessions
        agent = client.beta.agents.create(model="original-model", instructions="frozen", text={"verbosity":"medium"})
        spec = {"agent_id":agent.id, "environment":{"type":"none"}, "input":"initial"}
        headers = {"Idempotency-Key":"source-independent"}
        first = sessions.create(**spec, extra_headers=headers)
        initial_items = [item.id for item in sessions.items.list(first.id)]
        initial_turns = [turn.id for turn in sessions.turns.list(first.id)]
        assert len(initial_items) == len(initial_turns) == 1

        def mutate(**values):
            response = transport.post(control, json={"id":agent.id, **values})
            assert response.status_code == 204

        mutate(patch={"model":"changed-model", "instructions":"changed"})
        assert sessions.create(**spec, extra_headers=headers) == first
        fresh = sessions.create(**spec)
        assert fresh.agent.model == "changed-model" and fresh.agent.instructions == "changed"
        assert first.agent.model == "original-model" and first.agent.instructions == "frozen"
        mutate(patch={"service_tier":"priority"})
        expect(BadRequestError, lambda:sessions.create(**spec))
        assert sessions.create(**spec, extra_headers=headers) == first
        for changed in ({"input":"different"}, {"metadata":{"changed":"yes"}}, {"agent":{"instructions":"frozen"}}, {"agent_id":fresh.id}):
            expect(ConflictError, lambda:sessions.create(**(spec | changed), extra_headers=headers))
        mutate(delete=True)
        expect(NotFoundError, lambda:sessions.create(**spec))
        expect(NotFoundError, lambda:other.beta.agents.sessions.create(**spec, extra_headers=headers))
        updated = sessions.update(first.id, metadata={"current":"retained"})
        assert sessions.create(**spec, metadata=None, stream=False, extra_headers=headers) == updated
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as workers:
            retries = list(workers.map(lambda _:sessions.create(**spec, extra_headers=headers), range(16)))
        assert all(result == updated for result in retries)
        assert [item.id for item in sessions.items.list(first.id)] == initial_items
        assert [turn.id for turn in sessions.turns.list(first.id)] == initial_turns
        restarted_client = OpenAI(api_key=token, base_url=restarted + "/v1", http_client=transport, max_retries=0)
        assert restarted_client.beta.agents.sessions.create(**spec, extra_headers=headers) == updated

        auth = {"Authorization":"Bearer " + token,"OpenAI-Beta":"agents=v1", **headers}
        endpoint = base + "/v1/agents/sessions"
        # Wire validation still precedes lookup, and string/array input normalization is stable.
        for bad in ({"stream":None}, {"agent_id":None}, {"metadata":{"bad":None}}):
            assert transport.post(endpoint, headers=auth, json=spec | bad).status_code == 400
        equivalent = spec | {"input":[{"role":"user","content":[{"type":"input_text","text":"initial"}]}], "metadata":{}, "stream":False}
        assert transport.post(endpoint, headers=auth, json=equivalent).json()["id"] == first.id
        with transport.stream("POST", endpoint, headers=auth, json=spec | {"stream":True}) as stream:
            assert stream.status_code == 200
            sessions.events.create(first.id, events=[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"future-after-retry"}]}]}])
            found = False
            deadline = time.monotonic() + 15
            for line in stream.iter_lines():
                assert time.monotonic() < deadline, "future event was not observed"
                if not line.startswith("data:"):
                    continue
                event = json.loads(line[5:])
                assert event["type"] != "agent.session.created"
                if event["type"] == "agent.session.turn.item.added":
                    text = json.dumps(event)
                    assert "initial" not in text, "retry replayed initial work"
                    if "future-after-retry" in text:
                        found = True
                        break
            assert found
        assert len(list(sessions.turns.list(first.id))) == 1
        assert len(list(sessions.items.list(first.id))) == 2
    print("Saved-reference retries: SDK/raw HTTP mutation/deletion, concurrent recovery, initial-input-once, current metadata, restart and future-only SSE passed.")


if __name__ == "__main__":
    main()
