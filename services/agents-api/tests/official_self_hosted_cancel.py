"""Public cancellation admission with an offline Worker and controlled active Turns."""

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
    cancel = {"type": "agent.session.input.cancel"}
    batch = [cancel, cancel]
    message = {"type": "agent.session.input.message", "input": [
        {"role": "user", "content": [{"type": "input_text", "text": "Must not be admitted."}]}]}

    def client():
        return OpenAI(api_key=token, base_url=base + "/v1", max_retries=0,
                      _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=10))

    with client() as api, httpx2.Client(trust_env=False, timeout=10, headers=headers) as raw:
        sessions = api.beta.agents.sessions
        endpoint = base + "/v1/agents/sessions/"

        def sdk_submit(session_id, key, events=batch, expected=204):
            with client() as caller:
                try:
                    response = caller.beta.agents.sessions.events.with_raw_response.create(
                        session_id, events=events, idempotency_key=key)
                    assert response.status_code == expected == 204 and response.content == b""
                    assert response.parse() is None
                except APIStatusError as error:
                    assert error.status_code == expected and expected != 204

        def raw_submit(session_id, key, events=batch, expected=204, key_token=token):
            response = raw.post(endpoint + session_id + "/events", json={"events": events},
                                headers={"Idempotency-Key": key, "Authorization": "Bearer " + key_token})
            assert response.status_code == expected, (response.status_code, expected)
            if expected == 204:
                assert response.content == b"" and response.headers["cache-control"] == "no-store"

        def concurrent(session_id, key):
            barrier = threading.Barrier(4)

            def submit(index):
                barrier.wait(timeout=10)
                (sdk_submit if index % 2 else raw_submit)(session_id, key)

            with ThreadPoolExecutor(max_workers=4) as workers:
                list(workers.map(submit, range(4)))

        def current(session_id):
            value = sessions.retrieve(session_id).to_dict()
            response = raw.get(endpoint + session_id)
            assert response.status_code == 200 and response.json() == value
            assert value["environment"]["type"] == "self_hosted"
            return value

        phase = settings["phase"]
        if phase == "create":
            request = {"agent": {"model": "test-model", "instructions": "Controlled cancellation admission."},
                       "environment": {"type": "self_hosted", "workspace_directory": "/private-cancel-workspace"}}
            main_session = sessions.create(**request)
            initial = sessions.create(**request, input="Keep this pending initial input.")
            later = sessions.create(**request)
            deleted = sessions.create(**request)
            sessions.delete(deleted.id)
            result = {"id": main_session.id, "environment_id": main_session.environment.id,
                      "initial_id": initial.id, "later_id": later.id, "deleted_id": deleted.id,
                      "idle_key": str(uuid.uuid4()), "active_key": str(uuid.uuid4())}
            before = current(main_session.id)
            concurrent(main_session.id, result["idle_key"])
            sdk_submit(main_session.id, result["idle_key"])
            raw_submit(main_session.id, result["idle_key"])
            assert current(main_session.id) == before and before["status"] == "idle"
            assert list(sessions.turns.list(main_session.id)) == list(sessions.items.list(main_session.id)) == []
        else:
            result = settings["accepted"]
            session_id, turn_id = result["id"], settings["turn_id"]
            before = current(session_id)
            assert before["status"] == "in_progress" and before["environment"]["id"] == result["environment_id"]
            turn = sessions.turns.retrieve(turn_id, session_id=session_id).to_dict()
            items = [item.to_dict() for item in sessions.items.list(session_id)]
            assert turn["status"] == "in_progress"
            if phase == "active":
                concurrent(session_id, result["active_key"])
                assert any("Retained partial output." in json.dumps(item) for item in items)
            else:
                keys = [result["idle_key"]] if phase == "idle_replay" else [result["idle_key"], result["active_key"]]
                for key in keys:
                    sdk_submit(session_id, key)
                    raw_submit(session_id, key)
                assert current(session_id) == before
            assert sessions.turns.retrieve(turn_id, session_id=session_id).to_dict() == turn
            assert raw.get(endpoint + session_id + "/turns/" + turn_id).json() == turn
            assert [item.to_dict() for item in sessions.items.list(session_id)] == items
            sdk_submit(session_id, result["idle_key"], [cancel], 409)
            raw_submit(session_id, result["idle_key"], [cancel], 409)
            for pending_id in (result["initial_id"], result["later_id"]):
                pending = current(pending_id)
                assert pending["status"] == "requires_action"
                sdk_submit(pending_id, "rejected-pending-cancel", expected=409)
                raw_submit(pending_id, "rejected-pending-cancel", expected=409)
                assert current(pending_id) == pending
                assert list(sessions.turns.list(pending_id)) == list(sessions.items.list(pending_id)) == []
            for events in ([message, cancel], [cancel, message]):
                sdk_submit(session_id, str(uuid.uuid4()), events, 400)
                raw_submit(session_id, str(uuid.uuid4()), events, 400)
            missing_function = [{"type": "agent.session.input.tool_result", "turn_id": turn_id,
                                 "call_id": "unknown-function", "success": True, "output": "not admitted"}]
            sdk_submit(session_id, str(uuid.uuid4()), missing_function, 404)
            raw_submit(session_id, str(uuid.uuid4()), missing_function, 404)
            raw_submit(session_id, "foreign-cancel", expected=404, key_token=settings["foreign_token"])
            for missing in (result["deleted_id"], str(uuid.uuid4())):
                sdk_submit(missing, "missing-cancel", expected=404)
                raw_submit(missing, "missing-cancel", expected=404)
        print(json.dumps(result))


if __name__ == "__main__":
    main()
