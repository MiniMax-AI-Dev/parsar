"""Opt-in live check; stdin supplies engine, base and two tenants with session_id and token_file/token_env."""

import importlib.metadata
import json
import os
from pathlib import Path, PurePosixPath
import shlex
import sys
import time
import uuid

sys.dont_write_bytecode = True

import httpx2
from openai import OpenAI
from official_environment_files import verify_environment_files, verify_file_tenant_isolation


UNVERIFIED = [
    "Recursive traversal, directory entries, symlinks and missing paths are unspecified by the pinned SDK.",
    "The scope when path is omitted is not asserted.",
    "Default limit, changed-filter cursors and exact invalid-parameter errors are not asserted.",
    "The operator must qualify the real provider, native engine and isolated placement separately.",
    "This fixture uses existing Sessions; it does not qualify Session creation or executor installation.",
    "Generated fixture directories remain in the caller-owned workspaces for independent inspection.",
]


def caller_token(settings):
    assert ("token_file" in settings) != ("token_env" in settings), "Choose one private token source"
    if "token_file" in settings:
        location = Path(settings["token_file"]).expanduser()
        assert location.is_absolute() and location.stat().st_mode & 0o077 == 0, "Token file must be private"
        token = location.read_text().strip()
    else:
        token = os.environ[settings["token_env"]].strip()
    assert token, "Empty caller token"
    return token


def generate_files(client, session_id, label):
    sessions = client.beta.agents.sessions
    session = sessions.retrieve(session_id)
    assert session.status == "idle" and not session.required_actions, "An idle prepared Session is required"
    assert session.environment.type == "self_hosted", "A self-hosted Environment is required"
    environment_id = session.environment.id
    workspace = PurePosixPath(session.environment.workspace_directory)
    assert workspace.is_absolute(), "Absolute workspace required"
    assert client.beta.agents.environments.retrieve(environment_id).status == "connected", "Connect the Environment first"
    directory = str(workspace / ("files-list-" + label + "-" + uuid.uuid4().hex))
    contents = {"A.txt": "A\n", "a-b.txt": "three\n", "a.txt": "fourteen-bytes\n", "z.txt": "last\n"}
    expected = {directory + "/" + name: len(content.encode()) for name, content in contents.items()}
    sibling = directory + "-sibling"
    sibling_expected = {sibling + "/one.txt": 3, sibling + "/two.txt": 3}
    command = "mkdir -- " + shlex.quote(directory) + " " + shlex.quote(sibling)
    for name, content in contents.items():
        command += " && printf %s " + shlex.quote(content) + " > " + shlex.quote(directory + "/" + name)
    for name in ("one", "two"):
        command += " && printf %s " + name + " > " + shlex.quote(sibling + "/" + name + ".txt")
    prompt = "Use your native shell tool to execute the following command exactly once. Create no additional files in that directory. Do not delegate. Reply done only after the command succeeds.\n" + command
    before = {turn.id for turn in sessions.turns.list(session_id)}
    sessions.events.create(session_id, events=[{"type": "agent.session.input.message", "input": [
        {"role": "user", "content": [{"type": "input_text", "text": prompt}]}]}], idempotency_key=str(uuid.uuid4()))
    deadline = time.monotonic() + 240
    while time.monotonic() < deadline:
        turns = [turn for turn in sessions.turns.list(session_id) if turn.id not in before]
        assert len(turns) <= 1, "One input created multiple Turns"
        if turns:
            assert turns[0].status not in ("failed", "cancelled"), "File generation Turn failed"
            if turns[0].status == "completed" and sessions.retrieve(session_id).status == "idle":
                return {"session_id": session_id, "environment_id": environment_id, "turn_id": turns[0].id,
                        "directory": directory, "expected": expected,
                        "sibling_directory": sibling, "sibling_expected": sibling_expected}
        time.sleep(0.2)
    raise AssertionError("File generation Turn did not complete")


def main():
    settings = json.load(sys.stdin)
    assert settings["engine"] in ("codex", "claude_sdk"), "Select one qualified native engine"
    assert len(settings["tenants"]) == 2, "Two independent tenant Sessions are required"
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source.get("vcs_info", {}).get("commit_id") == pin["commit"], "Install the pinned SDK"
    tokens = [caller_token(tenant) for tenant in settings["tenants"]]
    assert tokens[0] != tokens[1], "Distinct tenant credentials required"
    base = settings["base"].rstrip("/") + "/v1"
    with httpx2.Client(trust_env=False, timeout=30) as http:
        clients = [OpenAI(api_key=token, base_url=base, max_retries=0, _strict_response_validation=True,
                          http_client=http) for token in tokens]
        generated = [generate_files(client, tenant["session_id"], str(index))
                     for index, (client, tenant) in enumerate(zip(clients, settings["tenants"]))]
        assert generated[0]["environment_id"] != generated[1]["environment_id"], "Distinct Environments required"
        for index, (client, fixture) in enumerate(zip(clients, generated)):
            before = [turn.to_dict() for turn in client.beta.agents.sessions.turns.list(fixture["session_id"])]
            fixture["list_checks"], page = verify_environment_files(
                client, http, fixture["environment_id"], fixture["directory"], fixture["expected"])
            fixture["sibling_checks"], _ = verify_environment_files(
                client, http, fixture["environment_id"], fixture["sibling_directory"], fixture["sibling_expected"])
            verify_file_tenant_isolation(client, clients[1 - index], http, fixture["environment_id"],
                                        fixture["directory"], page, fixture["expected"] | fixture["sibling_expected"])
            assert [turn.to_dict() for turn in client.beta.agents.sessions.turns.list(fixture["session_id"])] == before, "Files.list changed Turns"
            fixture["cross_tenant_denied"] = True
    proof = {"engine": settings["engine"], "sdk_version": distribution.version, "sdk_commit": pin["commit"],
             "scope": "Public input-generated flat files, SDK/raw listing, sorting, pagination and two-tenant isolation",
             "fixtures": generated, "unverified": UNVERIFIED}
    serialized = json.dumps(proof, indent=2)
    assert all(token not in serialized for token in tokens), "Credential in acceptance evidence"
    print(serialized)


if __name__ == "__main__":
    try:
        main()
    except Exception:
        raise SystemExit("Files.list acceptance failed; inspect the current stage privately (response bodies withheld).") from None
