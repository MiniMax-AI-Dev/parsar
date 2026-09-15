"""Pinned SDK/raw Credential deletion and retained absence after restart."""

import uuid

import httpx2
from openai import AuthenticationError, NotFoundError


def verify_credential_deletion(client, other, invalid, peer, canary, expect_error):
    vault = client.beta.agents.vaults.create(name="Credential deletion")
    foreign_vault = other.beta.agents.vaults.create(name="Foreign deletion scope")
    credentials = client.beta.agents.vaults.credentials
    auth = {"type": "static_bearer", "mcp_server_url": "https://example.invalid/delete", "token": canary}
    values = [credentials.create(vault.id, name=name, auth=auth)
              for name in ["SDK target", "HTTP target", "Keyless target", "Retained sibling"]]
    foreign = other.beta.agents.vaults.credentials.create(foreign_vault.id, name="Foreign", auth=auth)
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    endpoint = str(client.base_url).rstrip("/") + "/vaults/" + vault.id + "/credentials"
    target = values[0]
    expect_error(NotFoundError, lambda: other.beta.agents.vaults.credentials.delete(target.id, vault_id=vault.id))
    expect_error(NotFoundError, lambda: credentials.delete(target.id, vault_id=foreign_vault.id))
    expect_error(NotFoundError, lambda: credentials.delete(str(uuid.uuid4()), vault_id=vault.id))
    expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.credentials.delete(target.id, vault_id=vault.id))
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        url = endpoint + "/" + target.id
        assert raw.delete(url).status_code == 401
        response = raw.delete(url, headers={"Authorization": headers["Authorization"]})
        assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_beta_header"
        for kwargs in [{"params": {"include": "token"}}, {"content": b"{}"}]:
            assert raw.request("DELETE", url, headers=headers, **kwargs).status_code == 400
        assert credentials.retrieve(target.id, vault_id=vault.id) == target
        response = peer.beta.agents.vaults.credentials.with_raw_response.delete(target.id, vault_id=vault.id)
        expected = {"id": target.id, "deleted": True, "object": "vault.credential.deleted"}
        assert response.status_code == 200 and response.parse().to_dict() == expected
        assert response.http_response.json() == expected and canary not in response.http_response.text
        assert response.headers["cache-control"] == "no-store"
        response = raw.delete(endpoint + "/" + values[1].id, headers=headers)
        assert response.status_code == 200 and response.json() == {**expected, "id": values[1].id}
        assert canary not in response.text
        for value in values[:2]:
            url = endpoint + "/" + value.id
            for method in ["GET", "DELETE"]:
                response = raw.request(method, url, headers=headers)
                assert response.status_code == 404 and response.json()["error"]["code"] == "not_found"
            expect_error(NotFoundError, lambda: credentials.update(value.id, vault_id=vault.id,
                         auth={"type": "static_bearer", "token": canary}))
        for status in [None, ["active"], ["active", "archived"]]:
            args = {} if status is None else {"status": status}
            assert list(credentials.list(vault.id, order="asc", limit=1, **args)) == values[2:]
        assert list(credentials.list(vault.id, status=["archived"])) == []
    assert client.beta.agents.vaults.retrieve(vault.id) == vault
    assert other.beta.agents.vaults.credentials.retrieve(foreign.id, vault_id=foreign_vault.id) == foreign
    print("Credential deletion: SDK/raw confirmation, project/Vault scope, safe errors and retained sibling passed.")
    return values, foreign


def verify_credential_deletion_recovery(client, other, saved, expect_error):
    values, foreign = saved
    credentials = client.beta.agents.vaults.credentials
    for value in values[:2]:
        expect_error(NotFoundError, lambda: credentials.retrieve(value.id, vault_id=value.vault_id))
        expect_error(NotFoundError, lambda: credentials.delete(value.id, vault_id=value.vault_id))
    assert list(credentials.list(values[0].vault_id, order="asc", limit=1)) == values[2:]
    assert other.beta.agents.vaults.credentials.retrieve(foreign.id, vault_id=foreign.vault_id) == foreign
    print("Credential deletion: absent resources and unaffected siblings survived API restart.")


def verify_keyless_credential_deletion(client, saved, expect_error):
    values, _ = saved
    target, sibling = values[2:]
    credentials = client.beta.agents.vaults.credentials
    result = credentials.delete(target.id, vault_id=target.vault_id)
    assert result.to_dict() == {"id": target.id, "deleted": True, "object": "vault.credential.deleted"}
    expect_error(NotFoundError, lambda: credentials.retrieve(target.id, vault_id=target.vault_id))
    assert credentials.retrieve(sibling.id, vault_id=sibling.vault_id) == sibling
    assert list(credentials.list(target.vault_id)) == [sibling]
    print("Credential deletion: no storage key required; safe sibling metadata remains available.")
