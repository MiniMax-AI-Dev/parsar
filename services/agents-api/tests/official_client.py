"""Exercise the real service and PostgreSQL with the pinned official Python SDK."""

import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
from urllib.parse import parse_qs, urlsplit
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, ConflictError, NotFoundError, OpenAI


def main():
    root = Path(__file__).resolve().parents[3]
    pin = json.loads((root / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert source.get("vcs_info", {}).get("commit_id") == pin["commit"], "Install the pinned SDK commit first"
    dsn = os.environ["PARSAR_AGENTS_API_TEST_DATABASE_URL"]
    parts = urlsplit(dsn)
    database = parse_qs(parts.query).get("dbname", [parts.path.lstrip("/")])[0]
    assert parts.scheme in ("postgres", "postgresql") and database.startswith("parsar_agents_api_") and database.endswith("_tests"), "A dedicated execution test database is required"
    binary = os.environ["AGENTS_API_SERVER_BIN"]
    with socket.socket() as address:
        address.bind(("127.0.0.1", 0))
        port = address.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    tokens = [secrets.token_hex(32), secrets.token_hex(32)]
    bindings = [{"tenant_id": str(uuid.uuid4()), "token_sha256": hashlib.sha256(token.encode()).hexdigest()} for token in tokens]
    process = None
    with tempfile.TemporaryDirectory(prefix="agents-api-test-") as directory:
        keys = Path(directory) / "keys.json"
        keys.write_text(json.dumps(bindings))
        keys.chmod(0o600)
        env = dict(os.environ, AGENTS_API_DATABASE_URL=dsn, AGENTS_API_KEYS_FILE=str(keys), AGENTS_API_ADDR=f"127.0.0.1:{port}", AGENTS_API_ENGINE="codex")
        with (Path(directory) / "server.log").open("w+") as log:
            def start():
                child = subprocess.Popen([binary], env=env, stdout=log, stderr=log)
                try:
                    deadline = time.monotonic() + 20
                    with httpx2.Client(trust_env=False, timeout=1) as probe:
                        while time.monotonic() < deadline:
                            if child.poll() is not None:
                                raise AssertionError("Agents API exited during startup")
                            try:
                                if probe.get(base + "/healthz").status_code == 200:
                                    return child
                            except httpx2.TransportError:
                                pass
                            time.sleep(0.1)
                    raise AssertionError("Agents API did not become healthy")
                except BaseException:
                    child.terminate()
                    child.wait(timeout=15)
                    raise

            def client(token):
                return OpenAI(api_key=token, base_url=base + "/v1", max_retries=0, _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=10))

            def expect_error(error, operation):
                try:
                    operation()
                except error as result:
                    assert isinstance(result.body, dict) and result.body.get("code")
                else:
                    raise AssertionError(f"Expected {error.__name__}")

            try:
                process = start()
                with client(tokens[0]) as a, client(tokens[1]) as b, client("invalid-key") as invalid:
                    sessions = a.beta.agents.sessions
                    spec = {"agent": {"model": "requested-test-model", "instructions": "Keep the configuration."}, "environment": {"type": "none"}}
                    headers = {"Idempotency-Key": "same-key"}
                    first = sessions.create(**spec, metadata={"workspace": "untrusted-reference"}, extra_headers=headers)
                    assert first.object == "agent.session" and first.status == "idle"
                    assert first.agent.model == spec["agent"]["model"] and first.agent.instructions == spec["agent"]["instructions"]
                    assert first.environment.type == "none" and first.required_actions == [] and first.vault_ids == []
                    assert first.agent.tools == [] and first.agent.multi_agent.enabled is False
                    assert first.created_at == first.last_active_at and isinstance(first.created_at, int)
                    replay = sessions.create(**spec, metadata={"workspace": "untrusted-reference"}, extra_headers=headers)
                    assert replay == first
                    assert sessions.retrieve(first.id) == first
                    changed = {**spec, "agent": {**spec["agent"], "instructions": "Changed"}}
                    expect_error(ConflictError, lambda: sessions.create(**changed, metadata={"workspace": "untrusted-reference"}, extra_headers=headers))
                    others = [sessions.create(**spec) for _ in range(2)]
                    expected = {first.id, *(item.id for item in others)}
                    asc = list(sessions.list(limit=1, order="asc"))
                    desc = list(sessions.list(limit=2, order="desc"))
                    assert {item.id for item in asc} == expected
                    assert [item.id for item in asc] == list(reversed([item.id for item in desc]))
                    assert list(sessions.list(after=asc[-1].id, order="asc")) == []
                    other = b.beta.agents.sessions.create(**spec, extra_headers=headers)
                    assert other.id != first.id
                    assert [item.id for item in b.beta.agents.sessions.list()] == [other.id]
                    expect_error(NotFoundError, lambda: b.beta.agents.sessions.retrieve(first.id))
                    expect_error(NotFoundError, lambda: b.beta.agents.sessions.list(after=first.id))
                    expect_error(AuthenticationError, lambda: invalid.beta.agents.sessions.retrieve(first.id))
                    expect_error(BadRequestError, lambda: sessions.retrieve(first.id, extra_headers={"OpenAI-Beta": ""}))
                    expect_error(BadRequestError, lambda: sessions.create(**spec, input="Do work"))
                    expect_error(BadRequestError, lambda: sessions.create(**spec, stream=True))
                    expect_error(BadRequestError, lambda: sessions.create(agent=spec["agent"], environment={"type": "self_hosted", "workspace_directory": "/workspace"}))
                    expect_error(BadRequestError, lambda: sessions.create(**spec, extra_body={"tenant_id": bindings[1]["tenant_id"]}))
                    expect_error(BadRequestError, lambda: sessions.list(agent_id="unsupported-saved-agent"))
                    expect_error(BadRequestError, lambda: sessions.list(limit=0))
                    assert {item.id for item in sessions.list()} == expected
                    metadata = {str(i): "🧪" * 512 for i in range(16)}
                    large = sessions.create(**spec, metadata=metadata)
                    assert sessions.retrieve(large.id).metadata == metadata
                    process.terminate()
                    process.wait(timeout=15)
                    process = start()
                    assert sessions.retrieve(first.id) == first
                    assert sessions.create(**spec, metadata={"workspace": "untrusted-reference"}, extra_headers=headers) == first
                print("Official client: strict schemas, persistence/restart, retries, pagination, tenant isolation and explicit unsupported options passed.")
            finally:
                if process and process.poll() is None:
                    process.terminate()
                    process.wait(timeout=15)


if __name__ == "__main__":
    main()
