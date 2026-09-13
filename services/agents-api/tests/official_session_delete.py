"""Public Session removal against the pinned SDK and actual PostgreSQL."""

import sys
import uuid

import httpx2
from openai import APIStatusError, OpenAI


def rejected(status, action):
    try:
        action()
    except APIStatusError as exc:
        assert exc.status_code == status, exc
        return
    raise AssertionError(f"expected local status {status}")


def main():
    base, token, foreign, restarted = sys.argv[1:]
    with httpx2.Client(trust_env=False, timeout=10) as http:
        def client(url, key):
            return OpenAI(api_key=key, base_url=url + "/v1", http_client=http,
                          max_retries=0, _strict_response_validation=True)
        api = client(base, token)
        sessions = api.beta.agents.sessions
        other = client(base, foreign).beta.agents.sessions
        recovered = client(restarted, token).beta.agents.sessions
        agent = api.beta.agents.create(model="test-model")
        peer = sessions.create(agent_id=agent.id, environment={"type": "none"})
        foreign_session = other.create(agent={"model": "test-model"}, environment={"type": "none"})
        headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
        for saved in (False, True):
            for streaming in (False, True):
                spec = {"environment": {"type": "none"}, "input": "Queued controlled input"}
                spec.update({"agent_id": agent.id} if saved else {"agent": {"model": "test-model"}})
                key = {"Idempotency-Key": str(uuid.uuid4())}
                result = sessions.create(**spec, stream=streaming, extra_headers=key)
                if streaming:
                    with result:
                        first = next(iter(result))
                        assert first.type == "agent.session.created"
                        session = first.session
                else:
                    session = result
                turn = list(sessions.turns.list(session.id))[0]
                assert list(sessions.items.list(session.id))
                endpoint = base + "/v1/agents/sessions/" + session.id
                rejected(404, lambda: other.delete(session.id))
                assert http.delete(endpoint, headers={"Authorization": "Bearer " + token}).status_code == 400
                assert http.delete(endpoint, headers={"OpenAI-Beta": "agents=v1"}).status_code == 401
                assert http.delete(endpoint + "?cascade=true", headers=headers).status_code == 400
                for body in ("null", "{}"):
                    assert http.request("DELETE", endpoint, headers=headers, content=body).status_code == 400
                assert sessions.retrieve(session.id).id == session.id
                # Existing live streams close on public removal without a fabricated event.
                with http.stream("GET", endpoint + "/events", headers=headers) as stream:
                    assert stream.status_code == 200
                    raw = sessions.with_raw_response.delete(session.id.upper())
                    expected = {"id": session.id, "object": "agent.session.deleted", "deleted": True}
                    assert raw.status_code == 200 and raw.http_response.json() == expected
                    assert raw.parse().to_dict() == expected
                    assert not any(line.startswith(("event:", "data:")) for line in stream.iter_lines())
                for reader in (sessions, recovered):
                    rejected(404, lambda: reader.retrieve(session.id))
                    rejected(404, lambda: reader.update(session.id, metadata={"no": "resurrection"}))
                    rejected(404, lambda: list(reader.items.list(session.id)))
                    rejected(404, lambda: list(reader.turns.list(session.id)))
                    rejected(404, lambda: reader.turns.retrieve(turn.id, session_id=session.id))
                    rejected(404, lambda: reader.delete(session.id))
                    for stream in (False, True):
                        rejected(409, lambda: reader.create(**spec, stream=stream, extra_headers=key))
                    assert session.id not in {s.id for s in reader.list()}
                    assert session.id not in {s.id for s in reader.list(agent_id=session.agent.id)}
                assert http.get(endpoint + "/events", headers=headers).status_code == 404
                assert http.post(endpoint + "/events", headers=headers, json={"events": [{"type": "agent.session.input.cancel"}]}).status_code == 404
                fresh = sessions.create(**spec)
                assert fresh.id != session.id
                sessions.delete(fresh.id)
        assert sessions.retrieve(peer.id) == peer
        assert api.beta.agents.retrieve(agent.id) == agent
        assert other.retrieve(foreign_session.id) == foreign_session
        rejected(404, lambda: sessions.delete(foreign_session.id))
        for missing in (str(uuid.uuid4()), "invalid", str(uuid.UUID(int=0))):
            rejected(404, lambda: sessions.delete(missing))
    print("Session deletion: fixed SDK/raw HTTP, public history/stream removal, tenant isolation, durable retry rejection and independent resources passed.")


if __name__ == "__main__":
    main()
