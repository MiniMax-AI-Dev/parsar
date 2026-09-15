"""Public token replacement without changing Credential or Session identity."""

import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError


def verify_credential_rotation(client, other, invalid, peer, saved_vaults, saved_credentials, canary, expect_error):
    vaults, foreign_vault = saved_vaults
    vault, wrong_vault = vaults[:2]
    untouched, foreign = saved_credentials
    credentials = client.beta.agents.vaults.credentials
    destination = "https://rotation.example.invalid/mcp"
    original = credentials.create(vault.id, name="Stable rotation identity", auth={
        "type": "static_bearer", "mcp_server_url": destination, "token": canary + "initial"})
    stable = {key: value for key, value in original.to_dict().items() if key != "updated_at"}
    endpoint = str(client.base_url).rstrip("/") + "/vaults/" + vault.id + "/credentials/" + original.id
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    replacement = {"auth": {"type": "static_bearer", "token": canary + "replacement"}}
    sessions = []
    for selected in (None, original.id):
        request = {"agent": {"model": "requested-model", "tools": [{
            "type": "mcp", "server_label": "rotating", "credential_id": selected,
            "connection_origin": "service", "allowed_tools": [],
            "transport": {"type": "http", "server_url": destination}}]},
            "environment": {"type": "none"}, "vault_ids": [vault.id]}
        key = {"Idempotency-Key": "credential-rotation-" + str(uuid.uuid4())}
        sessions.append((request, key, client.beta.agents.sessions.create(**request, extra_headers=key)))

    def safe(response, expected):
        assert response.status_code == expected
        assert canary not in response.text, "Credential replacement leaked into the public response"
        assert response.headers["cache-control"] == "no-store"
        return response.json()

    def metadata(response, previous):
        body = safe(response, 200)
        assert set(body) == set(original.to_dict())
        assert {key: value for key, value in body.items() if key != "updated_at"} == stable
        assert type(body["updated_at"]) is int and body["updated_at"] >= previous.updated_at
        current = credentials.retrieve(original.id, vault_id=vault.id)
        assert current.to_dict() == body
        assert peer.beta.agents.vaults.credentials.retrieve(original.id, vault_id=vault.id) == current
        return current

    with httpx2.Client(trust_env=False, timeout=10) as raw:
        response = credentials.with_raw_response.update(original.id, vault_id=vault.id, **replacement)
        current = metadata(response.http_response, original)
        assert response.parse() == current
        # The public boundary accepts opaque values; private Store tests verify
        # their bytes. The final value also supplies a log-scan canary.
        for token in ("", " \t" + canary + "\n雪 ", canary + "final"):
            response = raw.post(endpoint, headers=headers, json={"auth": {"type": "static_bearer", "token": token}})
            current = metadata(response, current)
        response = peer.beta.agents.vaults.credentials.with_raw_response.update(
            original.id, vault_id=vault.id, auth={"type": "static_bearer", "token": canary + "peer"})
        current = metadata(response.http_response, current)
        assert response.parse() == current

        invalid_bodies = [
            {}, {"auth": None}, {"auth": []}, {"auth": {}},
            {"auth": {"type": "static_bearer"}}, {"auth": {"token": canary}},
            {"auth": {"type": None, "token": canary}},
            {"auth": {"type": 3, "token": canary}},
            {"auth": {"type": "static_bearer", "token": None}},
            {"auth": {"type": "static_bearer", "token": 3}},
            {"auth": {"type": "mcp_oauth", "access_token": canary}},
            {**replacement, "name": "Unexpected rename"},
            {**replacement, "metadata": {}},
            {"auth": {**replacement["auth"], "mcp_server_url": destination + "/other"}},
        ]
        for body in invalid_bodies:
            safe(raw.post(endpoint, headers=headers, json=body), 400)
        for body in ("null", "[]", "{} {}"):
            safe(raw.post(endpoint, headers=headers, content=body), 400)
        for override in ({"auth": None}, {"auth": {"type": "static_bearer", "token": None}}):
            error = expect_error(BadRequestError, lambda: credentials.update(
                original.id, vault_id=vault.id, **replacement, extra_body=override))
            safe(error.response, 400)

        base = str(client.base_url).rstrip("/") + "/vaults/"
        for owner, credential_id in ((wrong_vault.id, original.id), (foreign_vault.id, foreign.id),
                                     (vault.id, str(uuid.uuid4())), (vault.id, "invalid"),
                                     (vault.id, str(uuid.UUID(int=0))), ("invalid", original.id),
                                     (str(uuid.UUID(int=0)), original.id)):
            response = raw.post(base + owner + "/credentials/" + credential_id, headers=headers, json=replacement)
            assert safe(response, 404)["error"]["code"] == "not_found"
            assert original.id not in response.text and foreign.id not in response.text
            error = expect_error(NotFoundError, lambda: credentials.update(credential_id, vault_id=owner, **replacement))
            safe(error.response, 404)
        expect_error(NotFoundError, lambda: other.beta.agents.vaults.credentials.update(
            original.id, vault_id=vault.id, **replacement))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.credentials.update(
            original.id, vault_id=vault.id, **replacement))
        safe(raw.post(endpoint, json=replacement), 401)
        assert safe(raw.post(endpoint, headers={"Authorization": headers["Authorization"]},
                             json=replacement), 400)["error"]["code"] == "invalid_beta_header"
        for scope in ({"OpenAI-Project": "other-project"}, {"OpenAI-Organization": "other-organization"}):
            expect_error(AuthenticationError, lambda: credentials.update(
                original.id, vault_id=vault.id, **replacement, extra_headers=scope))
        safe(raw.post(endpoint, headers=headers, params={"include": "token"}, json=replacement), 400)
        assert safe(raw.get(endpoint, headers=headers), 200) == current.to_dict()

    assert credentials.retrieve(original.id, vault_id=vault.id) == current
    assert [credentials.retrieve(value.id, vault_id=value.vault_id) for value in untouched] == untouched
    assert other.beta.agents.vaults.credentials.retrieve(foreign.id, vault_id=foreign.vault_id) == foreign
    assert [client.beta.agents.vaults.retrieve(value.id) for value in vaults] == vaults
    state = current, sessions
    verify_rotation_recovery(client, peer, state)
    print("Credential rotation: SDK/raw opaque replacement, stable metadata and bindings, owner scope and rejection non-mutation passed.")
    return state


def verify_rotation_recovery(client, peer, state):
    credential, sessions = state
    assert client.beta.agents.vaults.credentials.retrieve(credential.id, vault_id=credential.vault_id) == credential
    assert peer.beta.agents.vaults.credentials.retrieve(credential.id, vault_id=credential.vault_id) == credential
    for request, key, original in sessions:
        assert client.beta.agents.sessions.retrieve(original.id) == original
        assert client.beta.agents.sessions.create(**request, extra_headers=key) == original
        assert list(client.beta.agents.sessions.turns.list(original.id)) == []
        assert list(client.beta.agents.sessions.items.list(original.id)) == []
