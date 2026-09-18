"""Project-scoped Files discovery through the pinned SDK and raw HTTP."""

from urllib.parse import parse_qs, urlsplit

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError


def verify_page(response, expected, has_more):
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("application/json")
    assert response.headers["cache-control"] == "no-store"
    body = response.json()
    assert body == {
        "object": "list",
        "data": [value.to_dict() for value in expected],
        "has_more": has_more,
        "first_id": expected[0].id if expected else None,
        "last_id": expected[-1].id if expected else None,
    }


def verify_source_file_list(client, other, invalid, peer, expect_error):
    files = client.files
    expected = [
        files.create(file=(f"source-{index}.txt", f"value-{index}".encode()), purpose="user_data")
        for index in range(105)
    ]
    foreign = other.files.create(file=("foreign.txt", b"foreign"), purpose="user_data")
    descending = list(reversed(expected))

    def sdk_page(values, has_more, query, **request):
        response = files.with_raw_response.list(**request)
        assert parse_qs(urlsplit(str(response.http_response.request.url)).query, keep_blank_values=True) == query
        verify_page(response.http_response, values, has_more)
        assert response.parse().data == values

    sdk_page(descending, False, {})
    sdk_page(expected[7:14], True, {"after": [expected[6].id], "limit": ["7"], "order": ["asc"], "purpose": ["user_data"]},
             after=expected[6].id, limit=7, order="asc", purpose="user_data")
    pages = list(files.list(limit=100, order="asc").iter_pages())
    assert [len(page.data) for page in pages] == [100, 5]
    assert [value for page in pages for value in page.data] == expected
    assert pages[0].has_next_page() and not pages[-1].has_next_page()
    assert list(files.list(limit=17, order="desc")) == descending
    assert files.list(purpose="batch").data == []
    assert list(peer.files.list(order="asc")) == expected
    assert list(other.files.list()) == [foreign]

    endpoint = str(client.base_url).rstrip("/") + "/files"
    headers = {"Authorization": f"Bearer {client.api_key}"}
    with httpx2.Client(trust_env=False, timeout=20) as raw:
        verify_page(raw.get(endpoint, headers=headers), descending, False)
        first = raw.get(endpoint, headers=headers, params={"order": "asc", "limit": "100"})
        verify_page(first, expected[:100], True)
        verify_page(raw.get(endpoint, headers=headers, params={"order": "asc", "limit": "100", "after": first.json()["last_id"]}), expected[100:], False)
        verify_page(raw.get(endpoint, headers=headers, params={"purpose": "batch"}), [], False)
        response = raw.get(endpoint, headers=headers, params={"after": foreign.id})
        assert response.status_code == 404 and response.json()["error"]["code"] == "not_found"
        assert foreign.id not in response.text
        for query in ("limit=0", "limit=10001", "limit=null", "order=invalid", "after=a&after=b", "purpose=a&purpose=b", "unknown=x"):
            response = raw.get(endpoint + "?" + query, headers=headers)
            assert response.status_code == 400
            assert response.json()["error"]["type"] == "invalid_request_error"
        response = raw.get(endpoint, headers=headers, params={"after": "not-a-file"})
        assert response.status_code == 404
        assert response.json()["error"]["code"] == "not_found"
        assert response.json()["error"]["type"] == "invalid_request_error"
        assert raw.get(endpoint).status_code == 401
        for scope in ({"OpenAI-Organization": "wrong-org"}, {"OpenAI-Project": "wrong-project"}):
            assert raw.get(endpoint, headers=headers | scope).status_code == 401
            expect_error(AuthenticationError, lambda scope=scope: files.list(extra_headers=scope))

    deleted_cursor = files.create(file=("deleted-cursor.txt", b"gone"), purpose="user_data")
    files.delete(deleted_cursor.id)
    expect_error(NotFoundError, lambda: files.list(after=deleted_cursor.id))
    expect_error(NotFoundError, lambda: files.list(after=foreign.id))
    expect_error(NotFoundError, lambda: other.files.list(after=expected[0].id))
    expect_error(AuthenticationError, lambda: invalid.files.list())
    expect_error(BadRequestError, lambda: files.list(limit=0))
    expect_error(BadRequestError, lambda: files.list(limit=10001))
    print("Source Files list: fixed SDK and raw HTTP pagination, filters, exact envelopes, restart-ready storage and project isolation passed.")
    return expected, foreign


def verify_source_file_list_recovery(client, other, peer, saved):
    expected, foreign = saved
    assert list(client.files.list(order="asc")) == expected
    assert list(peer.files.list(limit=19, order="desc")) == list(reversed(expected))
    assert list(other.files.list()) == [foreign]
    for value in expected:
        receipt = client.files.delete(value.id)
        assert receipt.id == value.id and receipt.object == "file" and receipt.deleted is True
    receipt = other.files.delete(foreign.id)
    assert receipt.id == foreign.id and receipt.deleted is True
    assert client.files.list().data == [] and other.files.list().data == []
    print("Source Files list: discovery survived API restart and project-owned cleanup returned empty envelopes.")
