"""Pinned SDK and raw HTTP Session filtering against real PostgreSQL."""

import importlib.metadata
import json
from pathlib import Path
import sys

import httpx2
from openai import OpenAI


def main():
    base, token, foreign, restarted = sys.argv[1:]
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"]
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    with httpx2.Client(trust_env=False, timeout=10) as http:
        client = OpenAI(api_key=token, base_url=base + "/v1", http_client=http,
                        max_retries=0, _strict_response_validation=True)
        other = OpenAI(api_key=foreign, base_url=base + "/v1", http_client=http,
                       max_retries=0, _strict_response_validation=True)
        agents, sessions = client.beta.agents, client.beta.agents.sessions
        root = agents.create(model="first-model", instructions="Original")
        peer = agents.create(model="peer-model")
        selected, all_ids = [], []
        for index in range(7):
            agent = root if index % 2 == 0 else peer
            session = sessions.create(agent_id=agent.id, environment={"type": "none"})
            all_ids.append(session.id)
            if agent.id == root.id:
                selected.append(session)
        inline = sessions.create(agent={"model": "inline-model"}, environment={"type": "none"})
        all_ids.append(inline.id)
        foreign_agent = other.beta.agents.create(model="foreign-model")
        foreign_session = other.beta.agents.sessions.create(agent_id=foreign_agent.id, environment={"type": "none"})
        assert [s.id for s in sessions.list(agent_id=root.id, limit=2, order="asc")] == [s.id for s in selected]
        assert [s.id for s in sessions.list(agent_id=root.id, limit=2)] == [s.id for s in reversed(selected)]
        assert [s.id for s in sessions.list(agent_id=inline.agent.id)] == [inline.id]
        assert [s.id for s in sessions.list(order="asc", limit=2)] == all_ids
        assert list(sessions.list(agent_id=foreign_agent.id)) == []
        assert list(other.beta.agents.sessions.list(agent_id=root.id)) == []
        assert [s.id for s in other.beta.agents.sessions.list(agent_id=foreign_agent.id)] == [foreign_session.id]
        agents.update(root.id, model="changed-model", instructions="Changed")
        assert list(sessions.list(agent_id=root.id, order="asc")) == selected
        agents.delete(root.id)
        assert list(sessions.list(agent_id=root.id, order="asc")) == selected
        recovered = OpenAI(api_key=token, base_url=restarted + "/v1", http_client=http,
                           max_retries=0, _strict_response_validation=True)
        assert list(recovered.beta.agents.sessions.list(agent_id=root.id, order="asc", limit=2)) == selected
        headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
        endpoint = base + "/v1/agents/sessions"
        first = http.get(endpoint, headers=headers, params={"agent_id": root.id, "order": "asc", "limit": 2})
        assert first.status_code == 200 and first.json()["has_more"] is True
        assert [s["id"] for s in first.json()["data"]] == [s.id for s in selected[:2]]
        tail = http.get(endpoint, headers=headers, params={"agent_id": root.id, "order": "asc", "limit": 2, "after": selected[1].id})
        assert tail.status_code == 200 and tail.json()["has_more"] is False
        assert [s["id"] for s in tail.json()["data"]] == [s.id for s in selected[2:]]
        for value in ("", "unknown", root.id + " ", "' OR true --"):
            reply = http.get(endpoint, headers=headers, params={"agent_id": value})
            assert reply.status_code == 200 and reply.json() == {"data": [], "has_more": False}
        for query in ([("agent_id", root.id), ("agent_id", peer.id)], {"agent_id": root.id, "tenant_id": "other"}):
            assert http.get(endpoint, headers=headers, params=query).status_code == 400
        assert http.get(endpoint, headers=headers, params={"agent_id": root.id, "after": foreign_session.id}).status_code == 404
        assert http.get(endpoint, headers={"Authorization": "Bearer " + token}, params={"agent_id": root.id}).status_code == 400
        assert http.get(endpoint, headers={"OpenAI-Beta": "agents=v1"}, params={"agent_id": root.id}).status_code == 401
        for path in ("/v1/agents", "/v1/agents/sessions/" + inline.id + "/items", "/v1/agents/sessions/" + inline.id + "/turns"):
            assert http.get(base + path, headers=headers, params={"agent_id": root.id}).status_code == 400
    print("Session Agent filter: SDK/raw HTTP, saved/inline IDs, both pagination orders, tenant isolation, source update/deletion, reconnect and unfiltered behavior passed.")


if __name__ == "__main__":
    main()
