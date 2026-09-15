"""Vault create/retrieve through the pinned SDK and the built HTTP service."""

import time
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError


def verify_vault(body, name, metadata):
    assert set(body) == {"id", "object", "created_at", "name", "metadata"}
    assert uuid.UUID(body["id"]).int != 0 and body["object"] == "vault"
    assert type(body["created_at"]) is int and body["created_at"] > 0
    assert body["name"] == name and body["metadata"] == metadata
    assert all(isinstance(key, str) and isinstance(value, str) for key, value in body["metadata"].items())


def verify_vaults(client, other, invalid, peer, binding, expect_error):
    vaults = client.beta.agents.vaults
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    base = str(client.base_url).rstrip("/") + "/vaults"
    sessions_before = [list(api.beta.agents.sessions.list()) for api in (client, other)]
    saved = []
    # Vault metadata does not inherit the Session count/key/value limits.
    metadata = {str(index): "value" for index in range(17)} | {"k" * 65: "值" * 513, "empty": ""}
    cases = [({}, None, {}), ({"metadata": None}, None, {}),
             ({"name": " \tVault 資源\u3000", "metadata": metadata}, "Vault 資源", metadata),
             ({"name": " \t" + "🧪" * 64 + "\n"}, "🧪" * 64, {})]
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for request, name, expected_metadata in cases:
            response = vaults.with_raw_response.create(**request)
            body, value = response.http_response.json(), response.parse()
            assert response.status_code == 200
            verify_vault(body, name, expected_metadata)
            assert value.to_dict() == body and abs(value.created_at - time.time()) < 10
            assert vaults.retrieve(value.id) == value
            assert peer.beta.agents.vaults.retrieve(value.id) == value
            saved.append(value)

        response = raw.post(base, headers=headers, json={"name": "\nRaw Vault\t", "metadata": {}})
        assert response.status_code == 200
        verify_vault(response.json(), "Raw Vault", {})
        saved.append(vaults.retrieve(response.json()["id"]))
        assert saved[-1].to_dict() == response.json()

        # A user and a service account in one project share resource ownership.
        user_vault = peer.beta.agents.vaults.create(name="Created by project user")
        assert vaults.retrieve(user_vault.id) == user_vault
        saved.append(user_vault)
        foreign = other.beta.agents.vaults.create(name="Foreign project")
        assert other.beta.agents.vaults.retrieve(foreign.id) == foreign
        expect_error(NotFoundError, lambda: vaults.retrieve(foreign.id))
        expect_error(NotFoundError, lambda: other.beta.agents.vaults.retrieve(saved[0].id))

        for value in saved:
            response = raw.get(base + "/" + value.id, headers=headers)
            assert response.status_code == 200 and response.json() == value.to_dict()
            assert response.headers["content-type"].startswith("application/json")
            assert response.headers["cache-control"] == "no-store"

        invalid_requests = [
            {"name": None}, {"name": 3}, {"name": " \t\n"},
            {"name": " " + "🧪" * 64 + "a "},
            {"metadata": {"bad": None}}, {"metadata": {"bad": 1}},
            {"metadata": []}, {"metadata": "invalid"},
            {"metadata": {"large": "x" * (64 * 1024)}},
            {"tenant_id": str(uuid.uuid4())}, {"status": "active"},
        ]
        for request in invalid_requests:
            response = raw.post(base, headers=headers, json=request)
            assert response.status_code == 400
            assert response.json()["error"]["type"] == "invalid_request_error"
            expect_error(BadRequestError, lambda: vaults.create(extra_body=request))
        for content in ("null", "[]", "{} {}"):
            assert raw.post(base, headers=headers, content=content).status_code == 400

        for resource_id in (str(uuid.uuid4()), "invalid-vault", str(uuid.UUID(int=0)), foreign.id):
            response = raw.get(base + "/" + resource_id, headers=headers)
            assert response.status_code == 404
            assert response.json()["error"]["code"] == "not_found"
            assert resource_id not in response.text
            expect_error(NotFoundError, lambda: vaults.retrieve(resource_id))
        for scope in ({"OpenAI-Organization": "wrong-org"}, {"OpenAI-Project": "wrong-project"}):
            expect_error(AuthenticationError, lambda: vaults.retrieve(saved[0].id, extra_headers=scope))
            expect_error(AuthenticationError, lambda: vaults.create(extra_headers=scope))
        scope = {"OpenAI-Organization": binding["organization_id"], "OpenAI-Project": binding["project_id"]}
        assert vaults.retrieve(saved[0].id, extra_headers=scope) == saved[0]
        expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.create())
        expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.retrieve(saved[0].id))
        for suffix, method in (("", "POST"), ("/" + saved[0].id, "GET")):
            assert raw.request(method, base + suffix, json={} if method == "POST" else None).status_code == 401
            for beta in (None, "agents=v2"):
                request_headers = {"Authorization": headers["Authorization"]}
                if beta is not None:
                    request_headers["OpenAI-Beta"] = beta
                response = raw.request(method, base + suffix, headers=request_headers,
                                       json={} if method == "POST" else None)
                assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_beta_header"
        assert raw.post(base, headers=headers, params={"tenant_id": "other"}, json={}).status_code == 400
        assert raw.get(base + "/" + saved[0].id, headers=headers, params={"include": "credentials"}).status_code == 400
        alias = str(client.base_url).rstrip("/") + "/agents/vaults/" + saved[0].id
        assert raw.get(alias, headers=headers).status_code == 404

    assert [list(api.beta.agents.sessions.list()) for api in (client, other)] == sessions_before
    # Rejected-request no-write evidence belongs to the real PostgreSQL tests;
    # no Vault listing or private database query is substituted into this client.
    print("Vault resources: fixed SDK/raw create/retrieve, exact fields, UTF-8 name boundary, metadata, project user/service access and validation passed; credentials and full lifecycle remain gaps.")
    return saved, foreign


def verify_vault_recovery(client, other, peer, saved):
    values, foreign = saved
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    base = str(client.base_url).rstrip("/") + "/vaults"
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for value in values:
            assert client.beta.agents.vaults.retrieve(value.id) == value
            assert peer.beta.agents.vaults.retrieve(value.id) == value
            response = raw.get(base + "/" + value.id, headers=headers)
            assert response.status_code == 200 and response.json() == value.to_dict()
        assert other.beta.agents.vaults.retrieve(foreign.id) == foreign
    print("Vault resources: exact SDK/raw responses and shared-project reads survived the server restart.")
