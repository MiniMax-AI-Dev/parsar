"""Static-bearer Credential metadata through the pinned SDK and real HTTP."""

import time
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, InternalServerError, NotFoundError


def verify_credential(body, vault_id, name, destination):
    assert set(body) == {"id", "auth", "created_at", "name", "object", "updated_at", "vault_id"}
    assert uuid.UUID(body["id"]).int != 0 and body["object"] == "vault.credential"
    assert body["vault_id"] == vault_id and body["name"] == name
    assert body["auth"] == {"type": "static_bearer", "mcp_server_url": destination}
    assert type(body["created_at"]) is int and type(body["updated_at"]) is int
    assert body["created_at"] == body["updated_at"] and body["created_at"] > 0


def verify_credentials(client, other, invalid, peer, saved_vaults, canary, expect_error):
    vaults, foreign_vault = saved_vaults
    vault, wrong_vault = vaults[:2]
    credentials = client.beta.agents.vaults.credentials
    base = str(client.base_url).rstrip("/") + "/vaults/"
    endpoint = base + vault.id + "/credentials"
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    destination = "https://example.invalid/mcp?tenant=alpha%2Fbeta"
    auth = {"type": "static_bearer", "mcp_server_url": destination, "token": " \t" + canary + "\n雪 "}
    request = {"name": " \tCredential 資源\u3000", "auth": auth}
    sessions_before = [list(api.beta.agents.sessions.list()) for api in (client, other)]
    saved = []

    def safe_body(response, status):
        assert response.status_code == status
        assert canary not in response.text, "Credential token appeared in an HTTP response"
        return response.json()

    with httpx2.Client(trust_env=False, timeout=10) as raw:
        response = credentials.with_raw_response.create(vault.id, **request)
        body, value = safe_body(response.http_response, 200), response.parse()
        verify_credential(body, vault.id, "Credential 資源", destination)
        assert value.to_dict() == body and abs(value.created_at - time.time()) < 10
        saved.append(value)

        # These successful writes exercise opaque strings, not a public token
        # round-trip. Byte preservation is verified by private Store tests.
        for name, token in (("🧪" * 64, canary + "x" * 1024), ("Empty opaque token", "")):
            response = raw.post(endpoint, headers=headers, json={"name": " " + name + "\n",
                                "auth": {**auth, "token": token}})
            body = safe_body(response, 200)
            verify_credential(body, vault.id, name, destination)
            saved.append(credentials.retrieve(body["id"], vault_id=vault.id))
            assert saved[-1].to_dict() == body
        user_value = peer.beta.agents.vaults.credentials.create(vault.id, name="Project user", auth=auth)
        verify_credential(user_value.to_dict(), vault.id, "Project user", destination)
        saved.append(user_value)
        foreign = other.beta.agents.vaults.credentials.create(foreign_vault.id, name="Foreign project", auth=auth)
        verify_credential(foreign.to_dict(), foreign_vault.id, "Foreign project", destination)

        for value in saved:
            assert credentials.retrieve(value.id, vault_id=vault.id) == value
            assert peer.beta.agents.vaults.credentials.retrieve(value.id, vault_id=vault.id) == value
            response = raw.get(endpoint + "/" + value.id, headers=headers)
            assert safe_body(response, 200) == value.to_dict()
            assert response.headers["content-type"].startswith("application/json")
            assert response.headers["cache-control"] == "no-store"

        invalid_requests = [
            {}, {"name": "missing auth"}, {"auth": auth},
            {**request, "name": None}, {**request, "name": 3}, {**request, "name": " \t\n"},
            {**request, "name": " " + "🧪" * 64 + "a "},
            {**request, "auth": None}, {**request, "auth": []},
            {**request, "auth": {**auth, "token": 3}},
            {**request, "auth": {"type": "mcp_oauth", "mcp_server_url": destination, "access_token": canary}},
            {**request, "metadata": {"unexpected": "field"}},
        ]
        for field in ("type", "mcp_server_url", "token"):
            invalid_requests.extend([{**request, "auth": {key: value for key, value in auth.items() if key != field}},
                                     {**request, "auth": {**auth, field: None}}])
        for url in ("http://example.invalid/mcp", "https://user:" + canary + "@example.invalid/mcp",
                    "https://example.invalid/mcp#fragment", "not-a-url"):
            invalid_requests.append({**request, "auth": {**auth, "mcp_server_url": url}})
        for body in invalid_requests:
            response = raw.post(endpoint, headers=headers, json=body)
            assert safe_body(response, 400)["error"]["type"] == "invalid_request_error"
        for body in ("null", "[]", "{} {}"):
            safe_body(raw.post(endpoint, headers=headers, content=body), 400)
        for override in ({"name": None}, {"auth": None}, {"auth": {**auth, "token": None}}):
            error = expect_error(BadRequestError, lambda: credentials.create(vault.id, **request, extra_body=override))
            safe_body(error.response, 400)

        for owner, credential_id in ((wrong_vault.id, saved[0].id), (foreign_vault.id, foreign.id),
                                     (vault.id, str(uuid.uuid4())), (vault.id, "invalid"),
                                     (vault.id, str(uuid.UUID(int=0))), ("invalid", saved[0].id),
                                     (str(uuid.UUID(int=0)), saved[0].id)):
            response = raw.get(base + owner + "/credentials/" + credential_id, headers=headers)
            assert safe_body(response, 404)["error"]["code"] == "not_found"
            assert saved[0].id not in response.text and foreign.id not in response.text
            error = expect_error(NotFoundError, lambda: credentials.retrieve(credential_id, vault_id=owner))
            safe_body(error.response, 404)
        for owner in (foreign_vault.id, str(uuid.uuid4()), "invalid", str(uuid.UUID(int=0))):
            response = raw.post(base + owner + "/credentials", headers=headers, json=request)
            assert safe_body(response, 404)["error"]["code"] == "not_found"
        expect_error(NotFoundError, lambda: other.beta.agents.vaults.credentials.retrieve(saved[0].id, vault_id=vault.id))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.credentials.create(vault.id, **request))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.credentials.retrieve(saved[0].id, vault_id=vault.id))
        for scope in ({"OpenAI-Project": "other-project"}, {"OpenAI-Organization": "other-organization"}):
            expect_error(AuthenticationError, lambda: credentials.create(vault.id, **request, extra_headers=scope))
            expect_error(AuthenticationError, lambda: credentials.retrieve(saved[0].id, vault_id=vault.id, extra_headers=scope))
        for method, url in (("POST", endpoint), ("GET", endpoint + "/" + saved[0].id)):
            body = {"json": request} if method == "POST" else {}
            safe_body(raw.request(method, url, **body), 401)
            response = raw.request(method, url, headers={"Authorization": headers["Authorization"]}, **body)
            assert safe_body(response, 400)["error"]["code"] == "invalid_beta_header"
        safe_body(raw.post(endpoint, headers=headers, params={"tenant_id": "other"}, json=request), 400)
        safe_body(raw.get(endpoint + "/" + saved[0].id, headers=headers, params={"include": "token"}), 400)

    assert [list(api.beta.agents.sessions.list()) for api in (client, other)] == sessions_before
    assert [client.beta.agents.vaults.retrieve(value.id) for value in vaults] == vaults
    assert other.beta.agents.vaults.retrieve(foreign_vault.id) == foreign_vault
    print("Static credentials: SDK/raw safe metadata, required fields, names, local HTTPS profile, project ownership and validation passed; no execution or token round-trip claim.")
    return saved, foreign


def verify_credential_recovery(client, other, peer, saved):
    values, foreign = saved
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    base = str(client.base_url).rstrip("/") + "/vaults/"
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for value in values:
            assert client.beta.agents.vaults.credentials.retrieve(value.id, vault_id=value.vault_id) == value
            assert peer.beta.agents.vaults.credentials.retrieve(value.id, vault_id=value.vault_id) == value
            response = raw.get(base + value.vault_id + "/credentials/" + value.id, headers=headers)
            assert response.status_code == 200 and response.json() == value.to_dict()
        assert other.beta.agents.vaults.credentials.retrieve(foreign.id, vault_id=foreign.vault_id) == foreign
    print("Static credentials: exact SDK/raw metadata and shared-project reads survived the service restart.")


def verify_credential_storage_disabled(client, value, canary, expect_error):
    credentials = client.beta.agents.vaults.credentials
    assert credentials.retrieve(value.id, vault_id=value.vault_id) == value
    request = {"name": "Disabled storage", "auth": {**value.auth.to_dict(), "token": canary}}
    error = expect_error(InternalServerError, lambda: credentials.create(value.vault_id, **request))
    assert error.status_code == 503 and error.body["code"] == "credential_storage_unavailable"
    assert canary not in error.response.text
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        response = raw.post(str(client.base_url).rstrip("/") + "/vaults/" + value.vault_id + "/credentials",
                            headers={"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"},
                            json=request)
        assert response.status_code == 503 and canary not in response.text
    vault = client.beta.agents.vaults.create(name="Non-secret resource without credential key")
    assert client.beta.agents.vaults.retrieve(vault.id) == vault
    print("Static credentials: absent key rejects SDK/raw writes while safe reads and Vault creation remain available.")
