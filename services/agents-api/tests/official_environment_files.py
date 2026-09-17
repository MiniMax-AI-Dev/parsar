"""Pinned Files.list checks for a known, unchanged directory of regular files."""

from pathlib import PurePosixPath

from openai import NotFoundError


def verify_file_page(value, environment_id, limit):
    assert isinstance(value, dict) and isinstance(value.get("data"), list), "Invalid file page"
    assert len(value["data"]) <= limit, "File page exceeds the requested limit"
    assert value.get("has_more") is None or type(value["has_more"]) is bool, "Invalid has_more"
    assert value.get("next") is None or isinstance(value["next"], str), "Invalid next token"
    for item in value["data"]:
        assert isinstance(item, dict), "Invalid file entry"
        assert item.get("environment_id") == environment_id, "Wrong file Environment"
        assert item.get("object") == "agent.environment.file", "Wrong file object"
        assert isinstance(item.get("path"), str) and item["path"].startswith("/"), "Invalid file path"
        assert type(item.get("size_bytes")) is int and item["size_bytes"] >= 0, "Invalid file size"
    more = value.get("has_more") is not False and bool(value.get("next"))
    assert value.get("has_more") is not True or more, "Missing continuation token"
    assert not more or value["data"], "Empty page cannot continue through the pinned SDK"
    return more


def verify_environment_files(client, http, environment_id, directory, expected):
    assert PurePosixPath(directory).is_absolute() and expected, "Expected directory and files required"
    assert all(str(PurePosixPath(path).parent) == directory for path in expected), "Use a flat fixture directory"
    resource = client.beta.agents.environments.files
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    summary, continuation = [], None
    for order, limit in (("asc", 1), ("desc", 2), (None, 2)):
        params = {"path": directory, "limit": limit}
        if order is not None:
            params["order"] = order
        wanted = sorted(expected, key=lambda path: PurePosixPath(path).parts, reverse=order != "asc")
        found, tokens, page_sizes = [], set(), []
        for _ in range(len(expected) + 1):
            response = http.get(endpoint, headers=headers, params=params)
            assert response.status_code == 200, "Raw Files.list failed"
            assert response.headers.get("content-type", "").startswith("application/json"), "Wrong file page content type"
            value = response.json()
            more = verify_file_page(value, environment_id, limit)
            found.extend(value["data"])
            page_sizes.append(len(value["data"]))
            if not more:
                break
            token = value["next"]
            assert token not in tokens, "Files.list repeated a continuation token"
            tokens.add(token)
            if order == "asc" and continuation is None:
                continuation = token
            params["page"] = token
        else:
            raise AssertionError("Files.list did not terminate")
        assert [item["path"] for item in found] == wanted, "Raw file order, filtering or completeness differs"
        assert {item["path"]: item["size_bytes"] for item in found} == expected, "Raw file sizes differ"

        params.pop("page", None)
        page = resource.list(environment_id, **params)
        sdk_files, sdk_tokens = [], set()
        for _ in range(len(expected) + 1):
            more = verify_file_page(page.to_dict(), environment_id, limit)
            sdk_files.extend(item.to_dict() for item in page.data)
            assert page.has_next_page() == more, "SDK continuation disagrees with the wire page"
            if not more:
                break
            assert page.next not in sdk_tokens, "SDK repeated a continuation token"
            sdk_tokens.add(page.next)
            page = page.get_next_page()
        else:
            raise AssertionError("SDK Files.list did not terminate")
        assert sdk_files == found, "SDK file pages differ from raw HTTP"
        summary.append({"order": order or "default", "limit": limit, "page_sizes": page_sizes,
                        "files": found})
    assert continuation, "The fixture must exercise continuation"
    return summary, continuation


def verify_file_tenant_isolation(client, other, http, environment_id, directory, page, private_paths):
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    for params in ({"path": directory, "limit": 1, "order": "asc"},
                   {"path": directory, "limit": 1, "order": "asc", "page": page}):
        response = http.get(endpoint, params=params, headers={
            "Authorization": "Bearer " + other.api_key, "OpenAI-Beta": "agents=v1"})
        assert response.status_code == 404, "Foreign tenant can access Files.list"
        assert isinstance(response.json().get("error"), dict), "Missing safe error envelope"
        assert all(secret not in response.text for secret in (
            client.api_key, other.api_key, environment_id, *private_paths)), "Foreign response exposes private data"
        try:
            other.beta.agents.environments.files.list(environment_id, **params)
        except NotFoundError:
            pass
        else:
            raise AssertionError("Foreign tenant can access SDK Files.list")
