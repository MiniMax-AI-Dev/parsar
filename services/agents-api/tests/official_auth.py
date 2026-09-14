"""Exercise caller scope through the pinned SDK, raw HTTP and service restart."""

import json
import subprocess
import uuid

import httpx2 as httpx
from openai import AuthenticationError


def verify_caller_principals(client, base, binding, token, rotated, peer, session):
    scope = {"organization": binding["organization_id"], "project": binding["project_id"]}
    for key in (token, rotated, peer):
        with client(key, **scope) as scoped:
            assert scoped.beta.agents.sessions.retrieve(session.id) == session
            assert session.id in {item.id for item in scoped.beta.agents.sessions.list()}
    for mismatch in ({**scope, "organization": "wrong-org"}, {**scope, "project": "wrong-project"}):
        with client(token, **mismatch) as invalid:
            try:
                invalid.beta.agents.sessions.retrieve(session.id)
            except AuthenticationError as error:
                assert error.body["code"] == "invalid_api_key"
            else:
                raise AssertionError("Untrusted scope header was accepted")
    headers = [("Authorization", "Bearer " + token), ("OpenAI-Beta", "agents=v1")]
    path = base + "/v1/agents/sessions/" + session.id
    with httpx.Client(trust_env=False, timeout=10) as raw:
        for extra in ([('OpenAI-Project', binding['project_id'])] * 2,
                      [('OpenAI-Organization', binding['organization_id']), ('OpenAI-Organization', 'other')],
                      [('Authorization', 'Bearer ' + peer)]):
            response = raw.get(path, headers=headers + extra)
            assert response.status_code == 401 and response.json()["error"]["code"] == "invalid_api_key"
        response = raw.get(path, headers=headers + [("X-Tenant-ID", str(uuid.uuid4())), ("X-User-ID", "forged")])
        assert response.status_code == 200 and response.json()["id"] == session.id


def verify_scope_bootstrap(binary, env, keys_file, bindings, log):
    # The previous process must be stopped before checking persisted configuration.
    for name in ("organization_id", "project_id", "tenant_id"):
        changed = [{**binding, name: str(uuid.uuid4())} if binding["tenant_id"] == bindings[0]["tenant_id"] else binding
                   for binding in bindings]
        # Keep same-project caller entries internally consistent, so PostgreSQL is the authority.
        replacement = str(uuid.uuid4())
        for old, new in zip(bindings, changed):
            if old["tenant_id"] == bindings[0]["tenant_id"]:
                new[name] = replacement
        keys_file.write_text(json.dumps(changed))
        process = subprocess.Popen([binary], env=env, stdout=log, stderr=log)
        try:
            assert process.wait(timeout=20) != 0, "Persisted scope remapping started successfully"
        finally:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=15)
            keys_file.write_text(json.dumps(bindings))
