"""Verify public result admission against storage fixtures, not native execution."""

import importlib.metadata
import json
from pathlib import Path
import sys

import httpx2
from openai import APIStatusError, OpenAI

base, token, foreign, session, turn, other = sys.argv[1:]
pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
source = json.loads(importlib.metadata.distribution("openai").read_text("direct_url.json") or "{}")
assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]


def result(call, **values):
    return {"type": "agent.session.input.tool_result", "turn_id": turn, "call_id": call, **values}


def submit(api, events, key, expected=204, target=session):
    try:
        response = api.beta.agents.sessions.events.with_raw_response.create(
            target, events=events, extra_headers={"Idempotency-Key": key})
        assert response.status_code == expected == 204
        assert response.content == b""
    except APIStatusError as error:
        assert error.status_code == expected, (error.status_code, expected, error.message)
        assert error.body["code"] in {"not_found", "turn_conflict", "idempotency_conflict", "invalid_request"}


with OpenAI(api_key=token, base_url=base+"/v1", max_retries=0,
            _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=10)) as api:
    output = [{"type":"input_text","text":""}, {"type":"input_image","image_url":"data:image/png;base64,AA=="}, {"type":"input_text","text":"last"}]
    message = {"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"follow up"}]}]}
    batch = [result("a", success=False, error="failure", output=output), result("b", success=True, output=None, error=None), result("c", success=True), message, {"type":"agent.session.input.cancel"}]
    submit(api, [message, result("rollback", success=True), result("missing", success=True)], "rollback", 404)
    submit(api, [result("a", success=True)], "other-session", 404, other)
    submit(api, [message, result("rollback", success=None)], "malformed", 400)
    stranger = api.with_options(api_key=foreign)
    submit(stranger, [result("a", success=True)], "foreign", 404)
    submit(api, batch, "batch")
    submit(api, batch, "batch")
    print("saved", flush=True)
    assert sys.stdin.readline() == "terminal\n"
    submit(api, batch, "batch")
    submit(api, list(reversed(batch)), "batch", 409)
    submit(api, [result("late", success=True)], "late", 409)
    submit(api, [result("b", success=True)], "changed-null", 409)
