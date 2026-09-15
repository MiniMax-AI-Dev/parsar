"""Vault discovery through the fixed SDK and HTTP, with synthetic archived rows."""

import json
import os
from pathlib import Path
import subprocess
from urllib.parse import parse_qs, urlsplit

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError

from official_vaults import verify_vault


def verify_page(response, expected, has_more):
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("application/json")
    assert response.headers["cache-control"] == "no-store"
    body = response.json()
    assert type(body["has_more"]) is bool
    assert body == {"object": "list", "data": [value.to_dict() for value in expected],
                    "has_more": has_more, "first_id": expected[0].id if expected else None,
                    "last_id": expected[-1].id if expected else None}
    for value in body["data"]:
        verify_vault(value, value["name"], value["metadata"])


def seed_archived_fixture(root, directory, tenant, values):
    # Only this private SQL fixture assigns status; no public archive operation exists.
    path = Path(directory) / "vault-list.json"
    path.write_text(json.dumps({"tenant": tenant, "vaults": [value.id for value in values]}))
    subprocess.run(["go", "run", "./services/agents-api/tests/fixtures"], cwd=root,
                   env=dict(os.environ, AGENTS_API_VAULT_LIST_FIXTURE=str(path)),
                   check=True, timeout=120)


def verify_vault_list(client, other, invalid, peer, binding, saved, root, directory, expect_error):
    vaults = client.beta.agents.vaults
    original, foreign = saved
    created = [vaults.create(name=f"Discovery {index}", metadata={"sequence": str(index)})
               for index in range(105)]
    expected = original + created
    archived = created[::3]
    archived_ids = {value.id for value in archived}
    active = [value for value in expected if value.id not in archived_ids]
    seed_archived_fixture(root, directory, binding["tenant_id"], archived)
    descending = list(reversed(expected))

    def sdk_page(values, has_more, query, **request):
        response = vaults.with_raw_response.list(**request)
        assert parse_qs(urlsplit(str(response.http_response.request.url)).query, keep_blank_values=True) == query
        verify_page(response.http_response, values, has_more)
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

    pages = list(vaults.list(limit=100, order="asc").iter_pages())
    assert [len(page.data) for page in pages] == [100, len(expected) - 100]
    assert [value for page in pages for value in page.data] == expected
    assert pages[0].has_next_page() and not pages[-1].has_next_page()
    assert list(vaults.list(limit=17)) == descending
    assert list(vaults.list(status="archived", limit=7, order="asc")) == archived
    assert list(vaults.list(status=["active"], limit=19, order="asc")) == active
    assert list(peer.beta.agents.vaults.list(order="asc")) == expected
    assert list(other.beta.agents.vaults.list()) == [foreign]
    assert other.beta.agents.vaults.list(status="archived").data == []
    assert vaults.retrieve(archived[0].id) == archived[0]

    endpoint = str(client.base_url).rstrip("/") + "/vaults"
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        verify_page(raw.get(endpoint, headers=headers), descending[:20], True)
        for limit, size in (("0", 1), ("-7", 1), ("101", 100)):
            verify_page(raw.get(endpoint, headers=headers, params={"limit": limit}),
                        descending[:size], True)
        for params, values in (({"status": "active"}, list(reversed(active))),
                               ([("status[]", "archived")], list(reversed(archived))),
                               ([("status[]", "active"), ("status[]", "archived")], descending)):
            verify_page(raw.get(endpoint, headers=headers, params=params), values[:20], True)
        for order, last in (("asc", expected[-1]), ("desc", expected[0])):
            params = {"after": last.id, "order": order}
            verify_page(raw.get(endpoint, headers=headers, params=params), [], False)
            assert list(vaults.list(**params)) == []
        first = raw.get(endpoint, headers=headers, params={"order": "asc", "limit": "100"})
        verify_page(first, expected[:100], True)
        verify_page(raw.get(endpoint, headers=headers,
                            params={"order": "asc", "limit": "100", "after": first.json()["last_id"]}),
                    expected[100:], False)
        foreign_headers = headers | {"Authorization": f"Bearer {other.api_key}"}
        verify_page(raw.get(endpoint, headers=foreign_headers, params={"status": "archived"}), [], False)
        response = raw.get(endpoint, headers=headers, params={"after": foreign.id})
        assert response.status_code == 404 and response.json()["error"]["code"] == "not_found"
        assert foreign.id not in response.text
        for params in ({"after": "invalid-vault"}, {"status": "unknown"}, {"limit": "null"}):
            response = raw.get(endpoint, headers=headers, params=params)
            assert response.status_code == 400
            assert response.json()["error"]["type"] == "invalid_request_error"
        assert raw.get(endpoint).status_code == 401
        for beta in (None, "agents=v2"):
            auth = {"Authorization": headers["Authorization"]}
            if beta is not None:
                auth["OpenAI-Beta"] = beta
            response = raw.get(endpoint, headers=auth)
            assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_beta_header"
        for scope in ({"OpenAI-Organization": "wrong-org"}, {"OpenAI-Project": "wrong-project"}):
            assert raw.get(endpoint, headers=headers | scope).status_code == 401
            expect_error(AuthenticationError, lambda: vaults.list(extra_headers=scope))
    expect_error(NotFoundError, lambda: vaults.list(after=foreign.id))
    expect_error(NotFoundError, lambda: other.beta.agents.vaults.list(after=expected[0].id))
    expect_error(AuthenticationError, lambda: invalid.beta.agents.vaults.list())
    expect_error(BadRequestError, lambda: vaults.list(extra_headers={"OpenAI-Beta": ""}))
    print("Vault list: fixed SDK query serialization, >100-row auto-pagination, exact HTTP envelopes, clamped limits, stored status filtering and project isolation passed; archived rows use a private SQL fixture.")
    return expected, archived


def verify_vault_list_recovery(client, other, peer, saved):
    expected, archived = saved
    known = {value.id for value in expected}
    for caller in (client, peer):
        discovered = list(caller.beta.agents.vaults.list(limit=100, order="asc"))
        assert [value for value in discovered if value.id in known] == expected
        assert list(caller.beta.agents.vaults.list(status=["archived"], limit=7, order="asc")) == archived
        for value in (expected[0], archived[0], expected[-1]):
            assert caller.beta.agents.vaults.retrieve(value.id) == value
    assert known.isdisjoint(value.id for value in other.beta.agents.vaults.list())
    endpoint = str(client.base_url).rstrip("/") + "/vaults"
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        verify_page(raw.get(endpoint, headers=headers,
                            params={"status[]": "archived", "order": "asc", "limit": "100"}),
                    archived, False)
    print("Vault list: public creation/discovery/retrieval and synthetic stored status reads survived the API restart.")
