"""Principal Agent list behavior against the pinned client and real HTTP."""

import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError


def verify_agent_list(client, other, invalid, saved, expect_error):
    agents = client.beta.agents
    expected = {agent.id: agent for agent in saved}
    # The pinned SDK omits limit=None and empty after from the HTTP query.
    assert agents.list(limit=None).data == agents.list().data
    assert agents.list(after="").data == agents.list().data
    asc = list(agents.list(limit=1, order="asc"))
    desc = list(agents.list(limit=2))
    assert {agent.id for agent in asc} == set(expected)
    assert [agent.id for agent in desc] == [agent.id for agent in reversed(asc)]
    assert all(agent == expected[agent.id] for agent in asc)
    assert list(agents.list(after=asc[-1].id, order="asc")) == []
    assert list(agents.list(after=desc[-1].id, order="desc")) == []
    assert agents.list(limit=101).data == desc
    assert agents.list(limit=2**63 - 1).data == desc
    assert other.beta.agents.list().data == []
    for cursor in (asc[0].id, str(uuid.uuid4())):
        expect_error(NotFoundError, lambda: other.beta.agents.list(after=cursor))
    expect_error(AuthenticationError, lambda: invalid.beta.agents.list())
    expect_error(BadRequestError, lambda: agents.list(extra_headers={"OpenAI-Beta": ""}))
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        url = str(client.base_url).rstrip("/") + "/agents"
        headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
        first = raw.get(url, headers=headers, params={"limit": 2, "order": "asc"}).json()
        assert set(first) == {"object", "data", "has_more", "first_id", "last_id"}
        assert first["object"] == "list" and first["has_more"] is True
        assert first["first_id"] == asc[0].id and first["last_id"] == asc[1].id
        assert first["data"] == [raw.get(url + "/" + agent.id, headers=headers).json() for agent in asc[:2]]
        next_page = raw.get(url, headers=headers, params={"after": first["last_id"], "limit": 2, "order": "asc"}).json()
        assert [agent["id"] for agent in next_page["data"]] == [agent.id for agent in asc[2:4]]
        empty = raw.get(url, headers=headers, params={"after": asc[-1].id, "order": "asc"}).json()
        assert empty == {"object": "list", "data": [], "has_more": False, "first_id": None, "last_id": None}
        default = raw.get(url, headers=headers).json()
        assert len(default["data"]) == min(20, len(saved)) and default["has_more"] == (len(saved) > 20)
        assert [agent["id"] for agent in default["data"]] == [agent.id for agent in desc[:20]]
        for params in ({"limit": "0"}, {"limit": "-1"}, {"limit": "1.5"}, {"limit": "null"},
                       {"limit": ""}, {"limit": str(2**63)}, {"order": "newest"}, {"after": "invalid-id"},
                       {"tenant_id": "other"}, [("limit", "1"), ("limit", "2")]):
            response = raw.get(url, headers=headers, params=params)
            assert response.status_code == 400, (params, response.status_code)
            assert response.json()["error"]["type"] == "invalid_request_error"
        assert raw.get(url).status_code == 401
    print("Agent list: fixed SDK auto-pagination/raw HTTP, order/cursors, snapshots, isolation and local limit/envelope behavior passed; exact upstream defaults/caps/errors remain unverified.")
    return [agent.id for agent in asc]
