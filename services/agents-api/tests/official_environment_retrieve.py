"""Public Environment retrieval with the pinned SDK and real PostgreSQL."""

import importlib.metadata
import json
from pathlib import Path
import sys
import uuid

sys.dont_write_bytecode = True

import httpx2
from openai import NotFoundError, OpenAI


def verify_environment(value, environment_id, status=None):
    assert set(value) == {"id", "object", "type", "status", "files", "plugins", "skills"}
    assert value["id"] == environment_id and value["object"] == "agent.environment"
    assert value["type"] == "self_hosted"
    assert value["status"] in {"pending", "connected", "disconnected", "expired", "failed"}
    if status is not None:
        assert value["status"] == status
    assert value["files"] == [] and value["plugins"] == [] and value["skills"] == []
    return value


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"]
    assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]
    base, token = settings["base"], settings["token"]
    headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as http:
        def client(key):
            return OpenAI(api_key=key, base_url=base + "/v1", http_client=http,
                          max_retries=0, _strict_response_validation=True)

        api, peer, foreign = (client(settings[name]) for name in ("token", "peer_token", "foreign_token"))
        if settings.get("phase") == "reopened":
            result = {key: settings[key] for key in ("environment_id", "deleted_environment_id", "foreign_environment_id")}
        else:
            creation = {"agent": {"model": "test-model"}, "environment": {
                "type": "self_hosted", "workspace_directory": "/private-workspace-" + str(uuid.uuid4())}}
            session = api.beta.agents.sessions.create(**creation)
            removed = api.beta.agents.sessions.create(**creation)
            other = foreign.beta.agents.sessions.create(**creation)
            result = {"environment_id": session.environment.id, "deleted_environment_id": removed.environment.id,
                      "foreign_environment_id": other.environment.id}
            verify_environment(api.beta.agents.environments.retrieve(removed.environment.id).to_dict(), removed.environment.id, "pending")
            api.beta.agents.sessions.delete(removed.id)
            assert list(api.beta.agents.sessions.turns.list(session.id)) == []
            assert list(api.beta.agents.sessions.items.list(session.id)) == []

        environment_id = result["environment_id"]
        endpoint = base + "/v1/agents/environments/" + environment_id
        expected = verify_environment(api.beta.agents.environments.retrieve(environment_id).to_dict(), environment_id, "pending")
        assert peer.beta.agents.environments.retrieve(environment_id).to_dict() == expected
        raw = api.beta.agents.environments.with_raw_response.retrieve(environment_id.upper())
        assert raw.status_code == 200 and raw.http_response.json() == expected and raw.parse().to_dict() == expected
        for key in (token, settings["peer_token"]):
            response = http.get(endpoint, headers=headers | {"Authorization": "Bearer " + key})
            assert response.status_code == 200 and response.json() == expected
            assert response.headers["content-type"].startswith("application/json")
            assert response.headers["cache-control"] == "no-store"

        def rejected(url, status, code, request_headers=headers, method="GET"):
            response = http.request(method, url, headers=request_headers)
            assert response.status_code == status
            body = response.json()
            assert set(body) == {"error"} and body["error"]["code"] == code
            assert body["error"]["type"] == ("authentication_error" if status == 401 else "invalid_request_error")
            for private in (token, settings["peer_token"], settings["foreign_token"], settings["executor_token"], environment_id):
                assert private not in response.text

        for missing in (result["deleted_environment_id"], result["foreign_environment_id"], str(uuid.uuid4())):
            rejected(base + "/v1/agents/environments/" + missing, 404, "not_found")
            try:
                api.beta.agents.environments.retrieve(missing)
            except NotFoundError:
                pass
            else:
                raise AssertionError("absent Environment was exposed through the SDK")
        for malformed in ("invalid", str(uuid.UUID(int=0))):
            rejected(base + "/v1/agents/environments/" + malformed, 400, "invalid_request")
        rejected(endpoint, 404, "not_found", headers | {"Authorization": "Bearer " + settings["foreign_token"]})
        for authorization in (None, "Bearer invalid", "Bearer " + settings["executor_token"]):
            request_headers = {"OpenAI-Beta": "agents=v1"}
            if authorization is not None:
                request_headers["Authorization"] = authorization
            rejected(endpoint, 401, "invalid_api_key", request_headers)
        for beta in (None, "agents=v2"):
            request_headers = {"Authorization": "Bearer " + token}
            if beta is not None:
                request_headers["OpenAI-Beta"] = beta
            rejected(endpoint, 400, "invalid_beta_header", request_headers)
        rejected(endpoint + "?include=files", 400, "unsupported_parameter")
        for method in ("POST", "PATCH", "DELETE"):
            rejected(endpoint, 405, "unsupported_operation", method=method)
        assert api.beta.agents.environments.retrieve(environment_id).to_dict() == expected
        verify_environment(foreign.beta.agents.environments.retrieve(result["foreign_environment_id"]).to_dict(), result["foreign_environment_id"], "pending")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
