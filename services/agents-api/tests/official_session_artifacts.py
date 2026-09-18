"""Pinned SDK and raw HTTP checks for known immutable Session output versions."""

from openai import NotFoundError


def verify_session_artifacts(client, foreign, http, session_id, environment_id, expected):
    resource = client.beta.agents.sessions.artifacts
    endpoint = str(client.base_url).rstrip("/") + "/agents/sessions/" + session_id + "/artifacts"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    wanted = {(turn, path): data for turn, files in expected.items() for path, data in files.items()}
    all_items = list(resource.list(session_id, limit=100, order="asc"))
    assert len(all_items) == len(wanted), "Missing or duplicate published output"
    assert {(item.turn_id, item.path) for item in all_items} == set(wanted), "Wrong output versions"
    fields = {"id", "created_at", "environment_id", "object", "path", "session_id", "size_bytes", "turn_id"}
    for item in all_items:
        metadata = item.to_dict()
        assert set(metadata) == fields, "Wrong public metadata or private storage fields leaked"
        assert item.object == "agent.session.artifact" and item.session_id == session_id
        assert item.environment_id == environment_id and type(item.created_at) is int and item.created_at > 0
        data = wanted[(item.turn_id, item.path)]
        assert item.size_bytes == len(data), "Wrong immutable size"
        assert resource.retrieve(item.id, session_id=session_id).to_dict() == metadata
        with resource.with_streaming_response.content(item.id, session_id=session_id) as response:
            assert response.read() == data, "SDK content differs from completed output"
        response = http.get(endpoint + "/" + item.id, headers=headers)
        assert response.status_code == 200 and response.json() == metadata
        response = http.get(endpoint + "/" + item.id + "/content", headers=headers)
        assert response.status_code == 200 and response.content == data
        assert response.headers["content-type"].startswith("application/octet-stream")

    ascending_ids = [item.id for item in all_items]
    for order in ("asc", "desc"):
        wanted_ids = ascending_ids if order == "asc" else list(reversed(ascending_ids))
        sdk = resource.list(session_id, environment_id=environment_id, limit=1, order=order)
        assert [item.id for item in sdk] == wanted_ids, "SDK cursor continuation changed order"
        params = {"environment_id": environment_id, "limit": 2, "order": order}
        seen = []
        for _ in range(len(wanted) + 1):
            response = http.get(endpoint, headers=headers, params=params)
            assert response.status_code == 200
            page = response.json()
            assert isinstance(page["data"], list) and type(page["has_more"]) is bool
            assert len(page["data"]) <= 2
            seen.extend(item["id"] for item in page["data"])
            if not page["has_more"]:
                break
            assert page["data"], "Empty page claims continuation"
            params["after"] = page["data"][-1]["id"]
        else:
            raise AssertionError("Artifact cursor did not terminate")
        assert seen == wanted_ids, "Raw HTTP pagination changed order or completeness"
    assert [item.id for item in resource.list(session_id)] == list(reversed(ascending_ids))
    assert [item.id for item in resource.list(session_id, after=None, environment_id=None, limit=None)] == list(reversed(ascending_ids))

    assert http.get(endpoint, headers={"Authorization": headers["Authorization"]}).status_code == 400
    assert http.get(endpoint, headers={"OpenAI-Beta": "agents=v1"}).status_code == 401
    for query in ("limit=0", "limit=101", "order=wrong", "environment_id=a&environment_id=b"):
        assert http.get(endpoint + "?" + query, headers=headers).status_code == 400
    assert http.post(endpoint, headers=headers, json={}).status_code == 405
    other = foreign.beta.agents.sessions.artifacts
    probes = [lambda: other.list(session_id)]
    for item in all_items:
        probes.extend((
            lambda item=item: other.retrieve(item.id, session_id=session_id),
            lambda item=item: other.content(item.id, session_id=session_id),
            lambda item=item: other.delete(item.id, session_id=session_id),
        ))
    for probe in probes:
        try:
            probe()
        except NotFoundError:
            pass
        else:
            raise AssertionError("Foreign tenant accessed published artifacts")
    return all_items
