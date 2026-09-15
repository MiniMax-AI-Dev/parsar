"""Credential discovery through the pinned SDK and HTTP with private status fixtures."""

import json
import os
from pathlib import Path
import subprocess
from urllib.parse import parse_qs, urlsplit
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError

from official_credentials import verify_credential


def verify_page(response, vault_id, expected, has_more, canary):
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("application/json")
    assert response.headers["cache-control"] == "no-store"
    assert canary not in response.text, "Credential token appeared in a list response"
    body = response.json()
    assert type(body["has_more"]) is bool
    assert body == {"object": "list", "data": [value.to_dict() for value in expected],
                    "has_more": has_more, "first_id": expected[0].id if expected else None,
                    "last_id": expected[-1].id if expected else None}
    for value in body["data"]:
        verify_credential(value, vault_id, value["name"], value["auth"]["mcp_server_url"])


def seed_archived_fixture(root, directory, tenant, vault_id, values):
    path = Path(directory) / "credential-list.json"
    path.write_text(json.dumps({"tenant": tenant, "vault": vault_id,
                                "credentials": [value.id for value in values]}))
    subprocess.run(["go", "run", "./services/agents-api/tests/fixtures"], cwd=root,
                   env=dict(os.environ, AGENTS_API_CREDENTIAL_LIST_FIXTURE=str(path)),
                   check=True, timeout=120)


def verify_credential_list(client, other, invalid, peer, binding, saved_vaults, saved_credentials,
                           listed_vaults, root, directory, canary, expect_error):
    vault = listed_vaults[1][0]
    empty_vault = saved_vaults[0][1]
    sibling, foreign = saved_credentials[0][0], saved_credentials[1]
    credentials = client.beta.agents.vaults.credentials
    destination = "https://discovery.example.invalid/mcp?scope=private%2Ffixture"
    expected = [credentials.create(vault.id, name=f"Discovered Credential {index}",
                auth={"type": "static_bearer", "mcp_server_url": destination, "token": canary})
                for index in range(105)]
    # Credential classification is independent of this already-archived Vault.
    assert list(credentials.list(vault.id, status="active", order="asc")) == expected
    assert list(credentials.list(vault.id, status="archived")) == []
    archived = expected[::3]
    archived_ids = {value.id for value in archived}
    active = [value for value in expected if value.id not in archived_ids]
    seed_archived_fixture(root, directory, binding["tenant_id"], vault.id, archived)
    descending = list(reversed(expected))

    def sdk_page(values, has_more, query, **request):
        response = credentials.with_raw_response.list(vault.id, **request)
        assert parse_qs(urlsplit(str(response.http_response.request.url)).query, keep_blank_values=True) == query
        verify_page(response.http_response, vault.id, values, has_more, canary)
        assert response.parse().data == values

    sdk_page(descending[:20], True, {})
    sdk_page(descending[:20], True, {}, limit=None)
    sdk_page(descending[:20], True, {}, status=[])
    for limit, size in ((0, 1), (-7, 1), (101, 100)):
        sdk_page(descending[:size], True, {"limit": [str(limit)]}, limit=limit)
    sdk_page(list(reversed(archived))[:20], True, {"status": ["archived"]}, status="archived")
    sdk_page(active[:20], True, {"status[]": ["active"], "order": ["asc"]},
             status=["active"], order="asc")
    sdk_page(descending[:20], True, {"status[]": ["active", "archived"]},
             status=["active", "archived"])
    sdk_page(expected[7:14], True, {"after": [expected[6].id], "limit": ["7"], "order": ["asc"]},
             after=expected[6].id, limit=7, order="asc")
    sdk_page(active[:7], True, {"after": [archived[0].id], "limit": ["7"],
                              "order": ["asc"], "status": ["active"]},
             after=archived[0].id, limit=7, order="asc", status="active")

    pages = list(credentials.list(vault.id, limit=100, order="asc").iter_pages())
    assert [len(page.data) for page in pages] == [100, 5]
    assert [value for page in pages for value in page.data] == expected
    assert pages[0].has_next_page() and not pages[-1].has_next_page()
    assert list(credentials.list(vault.id, limit=17)) == descending
    assert list(credentials.list(vault.id, status="archived", limit=7, order="asc")) == archived
    assert list(credentials.list(vault.id, status=["active"], limit=19, order="asc")) == active
    assert list(peer.beta.agents.vaults.credentials.list(vault.id, order="asc")) == expected
    assert list(other.beta.agents.vaults.credentials.list(foreign.vault_id)) == [foreign]
    assert list(credentials.list(empty_vault.id)) == []
    assert credentials.retrieve(archived[0].id, vault_id=vault.id) == archived[0]

    base = str(client.base_url).rstrip("/") + "/vaults/"
    endpoint = base + vault.id + "/credentials"
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}

    def safe_error(response, status):
        assert response.status_code == status
        assert canary not in response.text
        for value in (expected[0], sibling, foreign):
            assert all(part not in response.text for part in (value.id, value.name, value.vault_id,
                                                            value.auth.mcp_server_url))
        body = response.json()["error"]
        if status == 404:
            assert body["code"] == "not_found"
        elif status == 400:
            assert body["type"] == "invalid_request_error"
        return body

    with httpx2.Client(trust_env=False, timeout=10) as raw:
        verify_page(raw.get(endpoint, headers=headers), vault.id, descending[:20], True, canary)
        for limit, size in (("0", 1), ("-7", 1), ("101", 100)):
            verify_page(raw.get(endpoint, headers=headers, params={"limit": limit}),
                        vault.id, descending[:size], True, canary)
        for params, values in (({"status": "active"}, list(reversed(active))),
                               ([("status[]", "archived")], list(reversed(archived))),
                               ([("status[]", "active"), ("status[]", "archived")], descending)):
            verify_page(raw.get(endpoint, headers=headers, params=params), vault.id, values[:20], True, canary)
        for order, values in (("asc", expected), ("desc", descending)):
            params = {"order": order, "limit": "100"}
            first = raw.get(endpoint, headers=headers, params=params)
            verify_page(first, vault.id, values[:100], True, canary)
            verify_page(raw.get(endpoint, headers=headers, params=params | {"after": first.json()["last_id"]}),
                        vault.id, values[100:], False, canary)
            verify_page(raw.get(endpoint, headers=headers, params=params | {"after": values[-1].id}),
                        vault.id, [], False, canary)
            assert list(credentials.list(vault.id, after=values[-1].id, order=order)) == []
        verify_page(raw.get(base + empty_vault.id + "/credentials", headers=headers),
                    empty_vault.id, [], False, canary)
        verify_page(raw.get(base + foreign.vault_id + "/credentials",
                            headers=headers | {"Authorization": f"Bearer {other.api_key}"},
                            params={"status": "archived"}), foreign.vault_id, [], False, canary)
        for owner in (foreign.vault_id, str(uuid.uuid4()), "invalid-vault", str(uuid.UUID(int=0))):
            safe_error(raw.get(base + owner + "/credentials", headers=headers), 404)
            expect_error(NotFoundError, lambda: credentials.list(owner))
        for owner, cursor in ((vault.id, sibling.id), (vault.id, foreign.id),
                              (empty_vault.id, expected[0].id), (vault.id, str(uuid.uuid4()))):
            safe_error(raw.get(base + owner + "/credentials", headers=headers, params={"after": cursor}), 404)
            expect_error(NotFoundError, lambda: credentials.list(owner, after=cursor))
        invalid_queries = [
            {"after": "invalid-credential"}, {"status": "unknown"}, {"limit": "null"},
            {"order": "invalid"}, {"include": "token"}, {"tenant_id": "foreign"},
            [("status", "active"), ("status", "archived")],
            [("status", "active"), ("status[]", "archived")],
        ]
        for params in invalid_queries:
            safe_error(raw.get(endpoint, headers=headers, params=params), 400)
        for suffix in (vault.id, "invalid-vault"):
            safe_error(raw.get(base + suffix + "/credentials", params={"after": "invalid"}), 401)
        for beta in (None, "agents=v2"):
            auth = {"Authorization": headers["Authorization"]}
            if beta is not None:
                auth["OpenAI-Beta"] = beta
            assert safe_error(raw.get(endpoint, headers=auth), 400)["code"] == "invalid_beta_header"
        for scope in ({"OpenAI-Organization": "wrong-org"}, {"OpenAI-Project": "wrong-project"}):
            safe_error(raw.get(endpoint, headers=headers | scope), 401)
            expect_error(AuthenticationError, lambda: credentials.list(vault.id, extra_headers=scope))
    expect_error(NotFoundError, lambda: other.beta.agents.vaults.credentials.list(vault.id))
    expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.credentials.list(vault.id))
    expect_error(BadRequestError, lambda: credentials.list(vault.id, extra_headers={"OpenAI-Beta": ""}))
    print("Credential list: fixed SDK serialization, safe HTTP envelopes, >100-row pagination, independent stored status and project/Vault isolation passed; archived rows use a private SQL fixture.")
    return vault, expected, archived, empty_vault, foreign


def verify_credential_list_recovery(client, other, peer, saved, canary, phase="API restart"):
    vault, expected, archived, empty_vault, foreign = saved
    for caller in (client, peer):
        credentials = caller.beta.agents.vaults.credentials
        assert list(credentials.list(vault.id, limit=100, order="asc")) == expected
        assert list(credentials.list(vault.id, status=["archived"], limit=7, order="asc")) == archived
        assert list(credentials.list(empty_vault.id)) == []
        for value in (expected[0], expected[1], expected[-1]):
            assert credentials.retrieve(value.id, vault_id=vault.id) == value
    assert list(other.beta.agents.vaults.credentials.list(foreign.vault_id)) == [foreign]
    endpoint = str(client.base_url).rstrip("/") + "/vaults/" + vault.id + "/credentials"
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for params, values, has_more in (({"order": "asc", "limit": "100"}, expected[:100], True),
                                         ({"order": "asc", "after": expected[99].id}, expected[100:], False),
                                         ({"status[]": "archived", "order": "asc", "limit": "100"}, archived, False)):
            verify_page(raw.get(endpoint, headers=headers, params=params), vault.id, values, has_more, canary)
        response = raw.get(endpoint, headers=headers | {"Authorization": f"Bearer {other.api_key}"})
        assert response.status_code == 404 and response.json()["error"]["code"] == "not_found"
        assert canary not in response.text and expected[0].id not in response.text
    print(f"Credential list: exact metadata, stored status, discovery/retrieval and project isolation survived {phase}.")
