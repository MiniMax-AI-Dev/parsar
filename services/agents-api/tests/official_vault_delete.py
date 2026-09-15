"""Pinned Vault deletion, child removal, scoped retries and keyless recovery."""

import uuid

import httpx2
from openai import AuthenticationError, NotFoundError


def verify_vault_deletion(client, other, invalid, peer, canary, expect_error):
    vaults = client.beta.agents.vaults
    values = [vaults.create(name=name) for name in ["Cascade target", "Empty HTTP target", "Keyless target", "Retained Vault"]]
    foreign = other.beta.agents.vaults.create(name="Foreign Vault deletion")
    auth = {"type": "static_bearer", "mcp_server_url": "https://example.invalid/vault-delete", "token": canary}
    children = [vaults.credentials.create(values[0].id, name=str(i), auth=auth) for i in range(3)]
    keyless = vaults.credentials.create(values[2].id, name="Keyless child", auth=auth)
    retained = vaults.credentials.create(values[3].id, name="Retained child", auth=auth)
    foreign_child = other.beta.agents.vaults.credentials.create(foreign.id, name="Foreign child", auth=auth)
    spec = {"agent": {"model": "requested-model"}, "environment": {"type": "none"}, "vault_ids": [values[0].id, values[3].id]}
    headers = {"Idempotency-Key": "vault-delete-" + str(uuid.uuid4())}
    session = client.beta.agents.sessions.create(**spec, extra_headers=headers)
    target = values[0]
    expect_error(NotFoundError, lambda: other.beta.agents.vaults.delete(target.id))
    expect_error(NotFoundError, lambda: vaults.delete(foreign.id))
    expect_error(NotFoundError, lambda: vaults.delete(str(uuid.uuid4())))
    expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.delete(target.id))
    auth_headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    endpoint = str(client.base_url).rstrip("/") + "/vaults/"
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        assert raw.delete(endpoint + target.id).status_code == 401
        response = raw.delete(endpoint + target.id, headers={"Authorization": auth_headers["Authorization"]})
        assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_beta_header"
        for kwargs in [{"params": {"include": "credentials"}}, {"content": b"{}"}]:
            assert raw.request("DELETE", endpoint + target.id, headers=auth_headers, **kwargs).status_code == 400
        assert vaults.retrieve(target.id) == target
        assert list(vaults.credentials.list(target.id, order="asc")) == children
        response = peer.beta.agents.vaults.with_raw_response.delete(target.id)
        expected = {"id": target.id, "deleted": True, "object": "vault.deleted"}
        assert response.status_code == 200 and response.parse().to_dict() == expected
        assert response.http_response.json() == expected and canary not in response.http_response.text
        assert response.headers["cache-control"] == "no-store"
        response = raw.delete(endpoint + values[1].id, headers=auth_headers)
        assert response.status_code == 200 and response.json() == {**expected, "id": values[1].id}
        for value in values[:2]:
            for method in ["GET", "DELETE"]:
                response = raw.request(method, endpoint + value.id, headers=auth_headers)
                assert response.status_code == 404 and response.json()["error"]["code"] == "not_found"
            response = raw.get(endpoint + value.id + "/credentials", headers=auth_headers)
            assert response.status_code == 404 and response.json()["error"]["code"] == "not_found"
            response = raw.post(endpoint + value.id + "/credentials", headers=auth_headers, json={"name": "late", "auth": auth})
            assert response.status_code == 404 and canary not in response.text
            expect_error(NotFoundError, lambda: vaults.credentials.list(value.id))
            expect_error(NotFoundError, lambda: vaults.credentials.create(value.id, name="late", auth=auth))
        for child in children:
            for operation in [vaults.credentials.retrieve, vaults.credentials.delete]:
                expect_error(NotFoundError, lambda: operation(child.id, vault_id=target.id))
            expect_error(NotFoundError, lambda: vaults.credentials.update(child.id, vault_id=target.id,
                         auth={"type": "static_bearer", "token": canary}))
            response = raw.get(endpoint + target.id + "/credentials/" + child.id, headers=auth_headers)
            assert response.status_code == 404
        for status in [None, ["active"], ["active", "archived"]]:
            args = {} if status is None else {"status": status}
            ids = {v.id for v in values}
            assert [v for v in vaults.list(order="asc", limit=2, **args) if v.id in ids] == values[2:]
        expect_error(NotFoundError, lambda: client.beta.agents.sessions.create(**spec))
        response = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions", headers=auth_headers, json=spec)
        assert response.status_code == 404
    assert client.beta.agents.sessions.create(**spec, extra_headers=headers) == session
    assert client.beta.agents.sessions.retrieve(session.id) == session
    assert vaults.credentials.retrieve(retained.id, vault_id=retained.vault_id) == retained
    assert other.beta.agents.vaults.retrieve(foreign.id) == foreign
    assert other.beta.agents.vaults.credentials.retrieve(foreign_child.id, vault_id=foreign.id) == foreign_child
    print("Vault deletion: SDK/raw confirmation, cascade visibility, scope and retained Session identity passed.")
    return values, keyless, retained, foreign, foreign_child, spec, headers, session


def verify_vault_deletion_recovery(client, other, saved, expect_error):
    values, keyless, retained, foreign, foreign_child, spec, headers, session = saved
    vaults = client.beta.agents.vaults
    for target in values[:2]:
        expect_error(NotFoundError, lambda: vaults.retrieve(target.id))
        expect_error(NotFoundError, lambda: vaults.delete(target.id))
        expect_error(NotFoundError, lambda: vaults.credentials.list(target.id))
    for value in values[2:]:
        assert vaults.retrieve(value.id) == value
    for child in [keyless, retained]:
        assert vaults.credentials.retrieve(child.id, vault_id=child.vault_id) == child
    assert other.beta.agents.vaults.retrieve(foreign.id) == foreign
    assert other.beta.agents.vaults.credentials.retrieve(foreign_child.id, vault_id=foreign.id) == foreign_child
    assert client.beta.agents.sessions.retrieve(session.id) == session
    assert client.beta.agents.sessions.create(**spec, extra_headers=headers) == session
    expect_error(NotFoundError, lambda: client.beta.agents.sessions.create(**spec))
    print("Vault deletion: absent parents/children, unaffected resources and Session retry survived API restart.")


def verify_keyless_vault_deletion(client, saved, expect_error):
    values, child, retained, *_ = saved
    vaults, target = client.beta.agents.vaults, values[2]
    assert vaults.delete(target.id).to_dict() == {"id": target.id, "deleted": True, "object": "vault.deleted"}
    expect_error(NotFoundError, lambda: vaults.retrieve(target.id))
    expect_error(NotFoundError, lambda: vaults.credentials.retrieve(child.id, vault_id=target.id))
    expect_error(NotFoundError, lambda: vaults.credentials.list(target.id))
    assert vaults.retrieve(values[3].id) == values[3]
    assert vaults.credentials.retrieve(retained.id, vault_id=retained.vault_id) == retained
    print("Vault deletion: keyless cascade removed stored children while another Vault remained usable.")
