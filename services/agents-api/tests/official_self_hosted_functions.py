"""Public function admission and reads using controlled calls, observations and receipts."""

from concurrent.futures import ThreadPoolExecutor
import importlib.metadata
import json
from pathlib import Path
import sys
import threading
import uuid

sys.dont_write_bytecode = True

import httpx2
from openai import APIStatusError, OpenAI


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source["vcs_info"]["commit_id"] == pin["commit"]
    base, token = settings["base"], settings["token"]
    headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}

    def client():
        return OpenAI(api_key=token, base_url=base + "/v1", max_retries=0,
                      _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=10))

    with client() as api, httpx2.Client(trust_env=False, timeout=10, headers=headers) as raw:
        sessions = api.beta.agents.sessions
        endpoint = base + "/v1/agents/sessions"

        def submit(session_id, events, key, expected=204, sdk=False, key_token=token):
            if sdk:
                with client() as caller:
                    try:
                        response = caller.beta.agents.sessions.events.with_raw_response.create(
                            session_id, events=events, idempotency_key=key)
                        assert response.status_code == expected == 204 and response.content == b""
                        assert response.parse() is None
                    except APIStatusError as error:
                        assert error.status_code == expected and expected != 204
            else:
                response = raw.post(endpoint + "/" + session_id + "/events", json={"events": events},
                                    headers={"Idempotency-Key": key, "Authorization": "Bearer " + key_token})
                assert response.status_code == expected, (response.status_code, expected)
                if expected == 204:
                    assert response.content == b""

        def current(session_id):
            value = sessions.retrieve(session_id).to_dict()
            response = raw.get(endpoint + "/" + session_id)
            assert response.status_code == 200 and response.json() == value
            assert value["environment"]["type"] == "self_hosted"
            return value

        phase = settings["phase"]
        if phase == "create":
            tool = {"type": "function", "name": "lookup_ticket", "description": "Look up a ticket.",
                    "parameters": {"type": "object", "properties": {"ticket": {"type": "string"}},
                                   "required": ["ticket"], "additionalProperties": False}}
            expected = {**tool, "defer_loading": False}
            agent = {"model": "test-model", "tools": [tool]}
            environment = {"type": "self_hosted", "workspace_directory": "/private-function-workspace"}
            key = str(uuid.uuid4())
            session = sessions.create(agent=agent, environment=environment, extra_headers={"Idempotency-Key": key})
            assert session.agent.tools[0].to_dict() == expected
            assert sessions.create(agent={**agent, "tools": [expected]}, environment=environment,
                                   extra_headers={"Idempotency-Key": key}).id == session.id
            saved = api.beta.agents.create(**agent)
            response = raw.post(endpoint, json={"agent_id": saved.id, "environment": environment})
            assert response.status_code == 200
            saved_session = response.json()
            assert saved_session["agent"]["id"] == saved.id and saved_session["agent"]["tools"] == [expected]
            api.beta.agents.update(saved.id, tools=[{**tool, "description": "Changed later."}])
            assert current(saved_session["id"]) == saved_session
            initial = sessions.create(agent=agent, environment=environment, input="Retain the initial function prompt.")
            later = sessions.create(agent=agent, environment=environment)
            for tools in ([{**tool, "defer_loading": True}], [{**tool, "parameters": None}], [tool, tool]):
                response = raw.post(endpoint, json={"agent": {**agent, "tools": tools}, "environment": environment})
                assert response.status_code == 400
            unsupported = api.beta.agents.create(**{**agent, "tools": [{**tool, "defer_loading": True}]})
            response = raw.post(endpoint, json={"agent_id": unsupported.id, "environment": environment})
            assert response.status_code == 400
            result = {"id": session.id, "saved_id": saved_session["id"], "initial_id": initial.id, "later_id": later.id,
                      "environment_id": session.environment.id, "tools": [expected], "result_key": str(uuid.uuid4())}
            unknown_result = {"type": "agent.session.input.tool_result", "turn_id": str(uuid.uuid4()),
                              "call_id": "unknown", "success": True}
            for sdk in (True, False):
                submit(session.id, [unknown_result], "idle-result", 404, sdk)
            assert current(session.id)["status"] == "idle"
            for session_id in (session.id, saved_session["id"], initial.id, later.id):
                assert list(sessions.turns.list(session_id)) == list(sessions.items.list(session_id)) == []
        else:
            result = settings["accepted"]
            session_id, turn = result["id"], settings["turn_id"]
            calls = settings["calls"]
            outputs = [{"success": False, "error": "controlled tool failure", "output": [
                {"type": "input_text", "text": "before"}, {"type": "input_text", "text": ""},
                {"type": "input_image", "image_url": "data:image/png;base64,AA=="},
                {"type": "input_text", "text": "after"}]}, {"success": True, "output": None, "error": None}, {"success": True}]
            batch = [{"type": "agent.session.input.tool_result", "turn_id": turn, "call_id": call, **output}
                     for call, output in zip(calls, outputs)]
            before = current(session_id)
            assert before["agent"]["tools"] == result["tools"]
            if phase in ("reject", "submit"):
                assert before["status"] == "requires_action"
                actions = before["required_actions"]
                assert {action["call_id"] for action in actions} == set(calls)
                assert all(action["type"] == "function_call" and action["turn_id"] == turn and
                           action["name"] == "lookup_ticket" and action["arguments"] == {"ticket": "42"} for action in actions)
            if phase == "reject":
                missing = {**batch[1], "call_id": "missing"}
                foreign_turn = {**batch[1], "turn_id": settings["other_turn"], "call_id": settings["other_call"]}
                cancel = {"type": "agent.session.input.cancel"}
                message = {"type": "agent.session.input.message", "input": [
                    {"role": "user", "content": [{"type": "input_text", "text": "Must roll back."}]}]}
                for events, status in (([batch[0], missing], 404), ([batch[0], foreign_turn], 404),
                                       ([batch[0], {**batch[0], "success": True}], 409),
                                       ([batch[0], cancel], 400), ([message, batch[0]], 400)):
                    for sdk in (True, False):
                        submit(session_id, events, "failed-batch", status, sdk)
                submit(result["saved_id"], batch, "other-session", 404)
                submit(session_id, batch, "foreign", 404, key_token=settings["foreign_token"])
                for pending_id in (result["initial_id"], result["later_id"]):
                    waiting = current(pending_id)
                    for sdk in (True, False):
                        submit(pending_id, batch, "pending-result", 409, sdk)
                    assert current(pending_id) == waiting and waiting["status"] == "requires_action"
                assert current(session_id) == before
            elif phase == "submit":
                barrier = threading.Barrier(4)

                def submit_once(index):
                    barrier.wait(timeout=10)
                    submit(session_id, batch, result["result_key"], sdk=bool(index % 2))

                with ThreadPoolExecutor(max_workers=4) as workers:
                    list(workers.map(submit_once, range(4)))
                assert current(session_id) == before
                assert not any(item.type == "function_call_output" for item in sessions.items.list(session_id))
                result = {**result, "batch": batch}
            else:
                assert sessions.turns.retrieve(turn, session_id=session_id).status == "completed"
                assert before["status"] == ("idle" if phase == "terminal" else "requires_action")
                for sdk in (True, False):
                    submit(session_id, batch, result["result_key"], sdk=sdk)
                    submit(session_id, list(reversed(batch)), result["result_key"], 409, sdk)
                    submit(session_id, [{**batch[1], "output": "changed"}], "changed-result", 409, sdk)
                assert current(session_id) == before
                if phase == "later":
                    assert len(before["required_actions"]) == 1
                    assert before["required_actions"][0]["turn_id"] == settings["next_turn"]
                    assert before["required_actions"][0]["call_id"] == settings["next_call"]
                listed = [item.to_dict() for item in sessions.items.list(session_id, order="asc")]
                response = raw.get(endpoint + "/" + session_id + "/items", params={"order": "asc"})
                assert response.status_code == 200 and response.json()["data"] == listed
                projected = [item for item in listed if item["type"] == "function_call_output"]
                assert [item["call_id"] for item in projected] == calls
                for item, output in zip(projected, outputs):
                    for field in ("output", "error"):
                        assert (field in item) == (field in output) and item.get(field) == output.get(field)
            environment = api.beta.agents.environments.retrieve(result["environment_id"]).to_dict()
            assert environment["type"] == "self_hosted" and environment["files"] == environment["plugins"] == environment["skills"] == []
        print(json.dumps(result))


if __name__ == "__main__":
    main()
