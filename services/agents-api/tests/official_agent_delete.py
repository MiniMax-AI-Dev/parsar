"""Public saved-Agent deletion with the fixed SDK and real PostgreSQL."""

import sys
import uuid

import httpx2
from openai import NotFoundError, OpenAI


def absent(action):
    try:
        action()
    except NotFoundError:
        return
    raise AssertionError("expected local not-found behavior")


def main():
    base, token, foreign, restarted = sys.argv[1:]
    with httpx2.Client(trust_env=False, timeout=10) as http:
        client = OpenAI(api_key=token, base_url=base + "/v1", http_client=http,
                        max_retries=0, _strict_response_validation=True)
        other = OpenAI(api_key=foreign, base_url=base + "/v1", http_client=http,
                       max_retries=0, _strict_response_validation=True)
        agents, sessions = client.beta.agents, client.beta.agents.sessions
        original = agents.create(model="saved-model", instructions="Saved instructions.", metadata={"source": "only"})
        peer = agents.create(model="peer-model", name="Unaffected peer")
        foreign_agent = other.beta.agents.create(model="foreign-model")
        spec = {"agent_id": original.id, "environment": {"type": "none"}, "metadata": {"original": "session"}}
        retry = {"Idempotency-Key": "saved-before-delete"}
        accepted = sessions.create(**spec, extra_headers=retry)
        current = sessions.update(accepted.id, metadata={"current": "session"})
        endpoint = base + "/v1/agents/" + original.id
        headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
        # Rejected requests must not remove any resource.
        assert http.delete(endpoint, headers=headers | {"Authorization": "Bearer " + foreign}).status_code == 404
        assert http.delete(base + "/v1/agents/" + foreign_agent.id, headers=headers).status_code == 404
        assert http.delete(endpoint, headers={"Authorization": "Bearer " + token}).status_code == 400
        assert http.delete(endpoint, headers={"OpenAI-Beta": "agents=v1"}).status_code == 401
        assert http.delete(endpoint + "?cascade=true", headers=headers).status_code == 400
        assert http.request("DELETE", endpoint, headers=headers, json={"cascade": True}).status_code == 400
        assert agents.retrieve(original.id) == original
        assert sessions.retrieve(current.id) == current
        # The result carries the stored canonical ID, independently of path spelling.
        raw = agents.with_raw_response.delete(original.id.upper())
        expected = {"id": original.id, "object": "agent.deleted", "deleted": True}
        assert raw.status_code == 200 and raw.http_response.json() == expected
        assert raw.parse().to_dict() == expected
        absent(lambda: agents.retrieve(original.id))
        absent(lambda: agents.update(original.id, name="cannot resurrect"))
        absent(lambda: agents.delete(original.id))
        for missing in (str(uuid.uuid4()), "invalid", str(uuid.UUID(int=0))):
            absent(lambda: agents.delete(missing))
        assert [a.id for a in agents.list()] == [peer.id]
        assert agents.retrieve(peer.id) == peer
        assert other.beta.agents.retrieve(foreign_agent.id) == foreign_agent
        assert sessions.retrieve(current.id) == current
        assert sessions.create(**spec, extra_headers=retry) == current
        absent(lambda: sessions.create(**spec))
        assert {s.id for s in sessions.list()} == {current.id}
        assert list(sessions.items.list(current.id)) == []
        assert list(sessions.turns.list(current.id)) == []
        recovered = OpenAI(api_key=token, base_url=restarted + "/v1", http_client=http,
                           max_retries=0, _strict_response_validation=True)
        absent(lambda: recovered.beta.agents.retrieve(original.id))
        assert recovered.beta.agents.retrieve(peer.id) == peer
        assert recovered.beta.agents.sessions.retrieve(current.id) == current
        assert recovered.beta.agents.sessions.create(**spec, extra_headers=retry) == current
        assert agents.delete(peer.id).to_dict() == {"id": peer.id, "object": "agent.deleted", "deleted": True}
        assert list(agents.list()) == []
        assert other.beta.agents.retrieve(foreign_agent.id) == foreign_agent
    print("Agent deletion: SDK/raw HTTP canonical response, tenant rejection, read/list persistence, untouched peers/Sessions and recorded retry recovery passed.")


if __name__ == "__main__":
    main()
