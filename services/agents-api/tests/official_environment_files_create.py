"""Opt-in inline upload acceptance for a preconfigured local Environment.

Private Environment provisioning and subsequent real-model execution belong to
the invoking native fixture. This does not prove public hosted Session admission.
"""

import base64
import hashlib
import importlib.metadata
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True

import httpx2
from openai import OpenAI
from official_environment_files import verify_environment_files, verify_file_tenant_isolation
from official_environment_files_native import caller_token


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source.get("vcs_info", {}).get("commit_id") == pin["commit"], "Install the pinned SDK"
    token, foreign_token = (caller_token(value) for value in settings["callers"])
    assert token != foreign_token
    environment = settings["environment_id"]
    base = settings["base"].rstrip("/") + "/v1"
    endpoint = base + "/agents/environments/" + environment + "/files"
    directory = "/workspace/uploads"
    cases = {
        "empty.bin": b"",
        "binary.bin": bytes(range(256)),
        "chunked.bin": bytes(range(256)) * 8193,
        "large.bin": b"x" * (50 << 20),
        "model-input.txt": settings["model_input"].encode(),
    }
    expected, receipts = {}, []
    with httpx2.Client(trust_env=False, timeout=215) as http:
        client = OpenAI(api_key=token, base_url=base, max_retries=0, _strict_response_validation=True, http_client=http)
        foreign = OpenAI(api_key=foreign_token, base_url=base, max_retries=0, _strict_response_validation=True, http_client=http)
        headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
        for index, (name, content) in enumerate(cases.items()):
            path = directory + "/" + name
            body = {"type": "inline", "data": base64.b64encode(content).decode(), "path": path}
            if index % 2:
                response = http.post(endpoint, headers=headers, json=body)
                assert response.status_code == 200, "Raw inline upload failed"
                receipt = response.json()
            else:
                receipt = client.beta.agents.environments.files.create(environment, **body).to_dict()
            assert receipt == {"environment_id": environment, "object": "agent.environment.file", "path": path, "size_bytes": len(content)}, "Wrong upload metadata"
            expected[path] = len(content)
            receipts.append({"path": path, "size": len(content), "sha256": hashlib.sha256(content).hexdigest()})
        pages, continuation = verify_environment_files(client, http, environment, directory, expected)
        verify_file_tenant_isolation(client, foreign, http, environment, directory, continuation, list(expected))
        target = directory + "/model-input.txt"
        for body in (
            {"type": "inline", "data": "?", "path": target},
            {"type": "inline", "data": "", "path": "/workspace/../escape"},
            {"type": "inline", "data": "", "path": "/environment/staging/canary"},
            {"type": "inline", "data": "", "path": "/workspace/stage-link/canary"},
            {"type": "file_id", "file_id": "unimplemented", "path": target},
        ):
            response = http.post(endpoint, headers=headers, json=body)
            assert response.status_code == 400 and "error" in response.json(), "Invalid/unsupported upload accepted"
        response = http.post(endpoint, headers={**headers, "Authorization": "Bearer " + foreign_token},
                             json={"type": "inline", "data": "", "path": target})
        assert response.status_code == 404, "Foreign tenant upload accepted"
        assert token not in response.text and foreign_token not in response.text, "Credential leaked in error"
    print(json.dumps({"sdk": pin["sdk_version"], "commit": pin["commit"], "uploads": receipts, "listing": pages,
                      "limits": ["Private Environment setup", "file_id unimplemented", "Overwrite metadata/error parity unverified", "Real-model consumption verified by invoking fixture"]}))


if __name__ == "__main__":
    main()
