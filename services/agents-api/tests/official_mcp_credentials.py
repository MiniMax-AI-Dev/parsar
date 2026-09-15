"""Public MCP Vault admission; actual authenticated execution has a separate fixture."""

from copy import deepcopy
import json
import uuid

import httpx2
from openai import BadRequestError, ConflictError, NotFoundError

from official_session_creation_stream import event_data


def verify_mcp_credentials(client, other, peer, canary, expect_error):
    sessions, vaults = client.beta.agents.sessions, client.beta.agents.vaults
    url = "https://mcp.example.invalid/credential-admission"
    anonymous_url = "https://anonymous.example.invalid/mcp"
    attached = [vaults.create(name="MCP attachment " + str(index)) for index in range(2)]
    unattached = vaults.create(name="Unattached MCP credential")
    foreign = other.beta.agents.vaults.create(name="Foreign MCP credential")
    ids = [value.id for value in attached]

    def credential(api, vault, destination, name):
        value = api.beta.agents.vaults.credentials.create(
            vault.id, name=name,
            auth={"type": "static_bearer", "mcp_server_url": destination, "token": canary})
        assert canary not in value.model_dump_json()
        return value

    chosen = credential(client, attached[0], url, "First selected credential")
    alternate = credential(client, attached[1], url + "/other", "Other destination")
    outside = credential(client, unattached, url, "Not attached")
    foreign_credential = credential(other, foreign, url, "Not owned")
    tool = {"type": "mcp", "server_label": "private", "connection_origin": "service",
            "transport": {"type": "http", "server_url": url}, "allowed_tools": ["remember"]}
    anonymous = {**tool, "server_label": "anonymous",
                 "transport": {"type": "http", "server_url": anonymous_url}}
    inline = {"agent": {"model": "requested-model", "tools": [tool, anonymous]},
              "environment": {"type": "none"}, "vault_ids": ids}
    saved_sessions, saved_agents, retries = [], [], []

    def verify_session(value, expected_ids, expected_credential):
        body = value.to_dict()
        assert body["vault_ids"] == expected_ids
        assert body["agent"]["tools"][0]["credential_id"] == expected_credential
        assert "headers" not in body["agent"]["tools"][0]["transport"]
        assert canary not in json.dumps(body) and "mcp_credentials" not in body
        assert value.status == "idle"
        assert list(sessions.turns.list(value.id)) == []
        assert list(sessions.items.list(value.id)) == []
        assert sessions.retrieve(value.id) == value
        assert peer.beta.agents.sessions.retrieve(value.id) == value
        expect_error(NotFoundError, lambda: other.beta.agents.sessions.retrieve(value.id))
        saved_sessions.append(value)

    # Omission and null retain the declared public field while resolving the same
    # unique private selection; explicit selection is preserved publicly.
    for declaration in (tool, {**tool, "credential_id": None}, {**tool, "credential_id": chosen.id}):
        request = deepcopy(inline)
        request["agent"]["tools"][0] = declaration
        key = {"Idempotency-Key": "mcp-vault-" + str(uuid.uuid4())}
        response = sessions.with_raw_response.create(**request, extra_headers=key)
        value = response.parse()
        assert response.http_response.json() == value.to_dict()
        verify_session(value, ids, declaration.get("credential_id"))
        retries.append((request, key, value))

    saved = client.beta.agents.create(model="requested-model", tools=[tool, anonymous])
    saved_spec = {"agent_id": saved.id, "environment": {"type": "none"}, "vault_ids": ids}
    saved_key = {"Idempotency-Key": "mcp-vault-saved-" + saved.id}
    value = sessions.create(**saved_spec, extra_headers=saved_key)
    saved_session = value
    verify_session(value, ids, None)
    retries.append((saved_spec, saved_key, value))
    assert value.agent.id == saved.id

    # Stream only the first public creation event; the ordinary fixture's SDK
    # response-validation hook eagerly reads JSON and is unsuitable for live SSE.
    base = str(client.base_url).rstrip("/")
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        stream_key = {"Idempotency-Key": "mcp-vault-stream-" + str(uuid.uuid4())}
        with raw.stream("POST", base + "/agents/sessions", headers={**headers, **stream_key},
                        json={**inline, "stream": True}) as response:
            assert response.status_code == 200
            assert response.headers["content-type"].startswith("text/event-stream")
            event = event_data(response.iter_lines())
            assert event["type"] == "agent.session.created"
            assert canary not in json.dumps(event) and "mcp_credentials" not in event["session"]
        streamed = sessions.retrieve(event["session"]["id"])
        assert streamed.to_dict() == event["session"]
        verify_session(streamed, ids, None)
        retries.append((inline, stream_key, streamed))

        # Vault defaults retain anonymous MCP and never implicitly search other
        # project Vaults. A no-match attachment is also still anonymous.
        for changes in ({}, {"vault_ids": None}, {"vault_ids": []}, {"vault_ids": [attached[1].id]}):
            request = {key: item for key, item in inline.items() if key != "vault_ids"}
            request.update(changes)
            value = sessions.create(**request)
            verify_session(value, changes.get("vault_ids") or [], None)

        # Same-project users can attach the same Vaults; role names do not add a
        # product-specific approval or permission boundary to the independent API.
        value = peer.beta.agents.sessions.create(**inline)
        verify_session(value, ids, None)

        second = credential(client, attached[1], url, "Second matching credential")
        explicit = deepcopy(inline)
        explicit["agent"]["tools"][0]["credential_id"] = second.id
        value = sessions.create(**explicit)
        verify_session(value, ids, second.id)
        for request, key, original in retries:
            assert sessions.create(**request, extra_headers=key) == original
            assert list(sessions.turns.list(original.id)) == []
        expect_error(BadRequestError, lambda: sessions.create(**inline))

        changed = client.beta.agents.update(saved.id, tools=[])
        saved_agents.append(changed)
        assert sessions.create(**saved_spec, extra_headers=saved_key) == saved_session
        override = sessions.create(**saved_spec, agent={"tools": [{**tool, "credential_id": second.id}]})
        verify_session(override, ids, second.id)

        # Saved schemas can retain references without obtaining execution access.
        referenced = client.beta.agents.create(model="requested-model", tools=[{**tool, "credential_id": outside.id}])
        saved_agents.append(referenced)
        expect_error(NotFoundError, lambda: sessions.create(agent_id=referenced.id,
                     environment={"type": "none"}, vault_ids=ids))

        before = {item.id for item in sessions.list()}
        foreign_before = {item.id for item in other.beta.agents.sessions.list()}
        rejected = []
        for reference in (chosen.id, outside.id, foreign_credential.id, alternate.id, str(uuid.uuid4())):
            request = deepcopy(inline)
            request["agent"]["tools"][0]["credential_id"] = reference
            if reference == chosen.id:
                request["vault_ids"] = [attached[1].id]
            rejected.append((request, 404))
        rejected.extend([({**explicit, "vault_ids": [foreign.id]}, 404),
                         ({**explicit, "vault_ids": [str(uuid.uuid4())]}, 404),
                         (inline, 400)])
        for invalid in ("invalid", 3, [None], [3], {}):
            rejected.append(({**explicit, "vault_ids": invalid}, 400))
        for request, status in rejected:
            for initial in ({}, {"input": "Must not be admitted", "stream": True}):
                response = raw.post(base + "/agents/sessions", headers=headers, json={**request, **initial})
                assert response.status_code == status
                assert response.headers["content-type"].startswith("application/json")
                assert canary not in response.text and foreign.id not in response.text
        assert {item.id for item in sessions.list()} == before
        assert {item.id for item in other.beta.agents.sessions.list()} == foreign_before
        for field in ({"vault_ids": [attached[0].id]},
                      {"agent": {"model": "requested-model", "tools": [{**tool, "credential_id": second.id}]}}):
            request, key, _ = retries[0]
            expect_error(ConflictError, lambda: sessions.create(**{**request, **field}, extra_headers=key))
        public = raw.get(base + "/agents/sessions", headers=headers).json()
        assert canary not in json.dumps(public) and "mcp_credentials" not in json.dumps(public)

    print("Public MCP Vault admission: explicit/unique selection, owner/destination isolation, safe snapshots, creation streams and stable retries passed; native execution is checked separately.")
    return saved_sessions, saved_agents, retries


def verify_mcp_credential_recovery(client, state):
    sessions, agents, retries = state
    assert [client.beta.agents.sessions.retrieve(value.id) for value in sessions] == sessions
    assert [client.beta.agents.retrieve(value.id) for value in agents] == agents
    for request, key, value in retries:
        assert client.beta.agents.sessions.create(**request, extra_headers=key) == value
