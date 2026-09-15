"""Public text admission with controlled active Turns and offline preparation."""

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

    def message(text):
        return {"type": "agent.session.input.message", "input": [
            {"role": "user", "content": [{"type": "input_text", "text": text}]}]}

    with client() as api, httpx2.Client(trust_env=False, timeout=10, headers=headers) as raw:
        sessions = api.beta.agents.sessions
        endpoint = base + "/v1/agents/sessions/"

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
                response = raw.post(endpoint + session_id + "/events", json={"events": events},
                                    headers={"Idempotency-Key": key, "Authorization": "Bearer " + key_token})
                assert response.status_code == expected, (response.status_code, expected)
                if expected == 204:
                    assert response.content == b""

        def current(session_id):
            value = sessions.retrieve(session_id).to_dict()
            response = raw.get(endpoint + session_id)
            assert response.status_code == 200 and response.json() == value
            assert value["environment"]["type"] == "self_hosted"
            return value

        def waiting(session_id, events, key):
            try:
                raw.post(endpoint + session_id + "/events", json={"events": events},
                         headers={"Idempotency-Key": key}, timeout=0.8)
                raise AssertionError("offline input returned before preparation")
            except httpx2.ReadTimeout:
                pass

        phase = settings["phase"]
        if phase == "create":
            request = {"agent": {"model": "test-model", "instructions": "Controlled active text."},
                       "environment": {"type": "self_hosted", "workspace_directory": "/private-steering-workspace"}}
            main_session = sessions.create(**request)
            initial = sessions.create(**request, input="Keep initial pending input.")
            later, deleted = sessions.create(**request), sessions.create(**request)
            sessions.delete(deleted.id)
            result = {"id": main_session.id, "environment_id": main_session.environment.id,
                      "initial_id": initial.id, "later_id": later.id, "deleted_id": deleted.id,
                      "batch": [message("first active text"), message("second active text")],
                      "pending_event": message("Keep later pending input.")}
            result.update({key: str(uuid.uuid4()) for key in ("batch_key", "pending_key", "idle_key", "rollback_key")})
        else:
            result = settings["accepted"]
            session_id, batch, key = result["id"], result["batch"], result["batch_key"]
            before = current(session_id)
            assert before["environment"]["id"] == result["environment_id"]
            if phase == "active":
                barrier = threading.Barrier(4)

                def submit_once(index):
                    barrier.wait(timeout=10)
                    submit(session_id, batch, key, sdk=bool(index % 2))

                with ThreadPoolExecutor(max_workers=4) as workers:
                    list(workers.map(submit_once, range(4)))
                assert before["status"] == current(session_id)["status"] == "in_progress"
                turns = list(sessions.turns.list(session_id))
                assert len(turns) == 1 and turns[0].id == settings["turn_id"]
            elif phase == "reject":
                cancel = {"type": "agent.session.input.cancel"}
                for events, code in ((list(reversed(batch)), 409), ([message("changed")], 409),
                                      ([batch[0], {"type": "agent.session.input.message", "input": []}], 400),
                                      ([batch[0], cancel], 400)):
                    for sdk in (True, False):
                        submit(session_id, events, key, code, sdk)
                submit(session_id, batch, "foreign", 404, key_token=settings["foreign_token"])
                for sdk in (True, False):
                    submit(result["deleted_id"], batch, key, 404, sdk)
                    for pending_id in (result["initial_id"], result["later_id"]):
                        submit(pending_id, batch, key, 409, sdk)
                pending = current(result["later_id"])
                waiting(result["later_id"], [result["pending_event"]], result["pending_key"])
                assert current(result["later_id"]) == pending
                assert current(session_id) == before
            elif phase == "rollback":
                for sdk in (True, False):
                    submit(session_id, batch, result["rollback_key"], 500, sdk)
                assert current(session_id) == before
            elif phase == "idle":
                assert before["status"] == "idle"
                waiting(session_id, [message("New idle input.")], result["idle_key"])
                assert current(session_id)["status"] == "requires_action"
                for sdk in (True, False):
                    submit(session_id, batch, key, sdk=sdk)
                assert len(list(sessions.turns.list(session_id))) == 2
            else:
                assert before["status"] == ("idle" if phase == "terminal" else "in_progress")
                for sdk in (True, False):
                    submit(session_id, batch, key, sdk=sdk)
                assert current(session_id) == before
            listed = [item.to_dict() for item in sessions.items.list(session_id, order="asc")]
            response = raw.get(endpoint + session_id + "/items", params={"order": "asc"})
            assert response.status_code == 200 and response.json()["data"] == listed
            texts = ["".join(part["text"] for part in item["content"]) for item in listed if item["type"] == "message"]
            expected = ["Controlled original work.", "first active text", "second active text"]
            if phase in ("later", "idle"):
                expected.append("Controlled original work.")
            assert texts == expected
        print(json.dumps(result))


if __name__ == "__main__":
    main()
