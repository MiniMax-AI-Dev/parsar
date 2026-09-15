"""Public initial creation with an offline Worker and controlled deadline advancement."""

from concurrent.futures import ThreadPoolExecutor
import importlib.metadata
import json
from pathlib import Path
import sys
import threading
import time
import uuid

sys.dont_write_bytecode = True

import httpx2
from openai import OpenAI


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source["vcs_info"]["commit_id"] == pin["commit"]
    base, token = settings["base"], settings["token"]
    headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}

    def client(key=token):
        return OpenAI(api_key=key, base_url=base + "/v1", max_retries=0,
                      _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=10))

    def check(value, status, environment_id=None):
        assert value["object"] == "agent.session" and value["status"] == status and value["usage"] is None
        environment = value["environment"]
        assert environment["id"] == (environment_id or environment["id"])
        assert environment["type"] == "self_hosted" and environment["capability_directories"] == []
        assert environment["remote_url"] == settings["remote_url"]
        action = {"type": "environment_connection", "environment_id": environment["id"]}
        assert value["required_actions"] == ([action] if status == "requires_action" else [])
        assert value["error"] == ("The initial input timed out waiting for the environment connection." if status == "failed" else None)

    with client() as api, httpx2.Client(trust_env=False, timeout=10, headers=headers) as raw:
        sessions = api.beta.agents.sessions
        endpoint = base + "/v1/agents/sessions"

        def post(request, key, url=endpoint, key_token=token):
            return raw.post(url, json=request, headers={"Idempotency-Key": key, "Authorization": "Bearer " + key_token})

        def current(case, status="requires_action"):
            value = sessions.retrieve(case["id"]).to_dict()
            check(value, status, case["environment_id"])
            response = raw.get(endpoint + "/" + case["id"])
            assert response.status_code == 200 and response.json() == value
            assert list(sessions.turns.list(case["id"])) == [] and list(sessions.items.list(case["id"])) == []
            return value

        def retry(case, status="requires_action"):
            value = current(case, status)
            assert sessions.create(**case["request"], extra_headers={"Idempotency-Key": case["key"]}).to_dict() == value
            response = post(case["request"], case["key"])
            assert response.status_code == 200 and response.json() == value
            return value

        phase = settings.get("phase", "create")
        if phase == "create":
            environment = {"type": "self_hosted", "workspace_directory": "/private-initial-workspace"}
            inline = {"agent": {"model": "test-model", "instructions": "Retain the creation snapshot."}, "environment": environment}
            ordered = [{"role": "user", "content": [{"type": "input_text", "text": "first private input"}]},
                       {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "second private input"}]}]
            saved = api.beta.agents.create(model="test-model", instructions="Saved initial instructions.")
            cases = []
            for mode in ("concurrent", "saved", "sdk_stream", "raw_disconnect"):
                request = {**inline, "input": ordered if mode in ("saved", "sdk_stream") else "private initial string"}
                if mode == "saved":
                    request = {"agent_id": saved.id, "environment": environment, "input": ordered}
                key, began = str(uuid.uuid4()), time.monotonic()
                if mode == "concurrent":
                    def create_once(index):
                        if index == 0:
                            with client() as creator:
                                return creator.beta.agents.sessions.create(**request, extra_headers={"Idempotency-Key": key}).to_dict()
                        reply = post(request, key)
                        assert reply.status_code == 200
                        return reply.json()

                    with ThreadPoolExecutor(max_workers=4) as workers:
                        replies = list(workers.map(create_once, range(4)))
                    value = replies[0]
                    assert all(reply == value for reply in replies)
                elif mode == "sdk_stream":
                    with sessions.create(**request, stream=True, extra_headers={"Idempotency-Key": key}) as stream:
                        events = iter(stream)
                        created, waiting = next(events).to_dict(), next(events).to_dict()
                        assert created["type"] == "agent.session.created" and waiting["type"] == "agent.session.requires_action"
                        assert set(created) == set(waiting) == {"type", "event_id", "session"}
                        assert created["event_id"] != waiting["event_id"]
                        check(created["session"], "idle")
                        value = waiting["session"]
                        assert created["session"]["id"] == value["id"] and created["session"]["environment"] == value["environment"]
                        assert created["session"]["created_at"] == created["session"]["last_active_at"]
                elif mode == "raw_disconnect":
                    with raw.stream("POST", endpoint, json={**request, "stream": True}, headers={"Idempotency-Key": key}) as response:
                        assert response.status_code == 200 and response.headers["content-type"] == "text/event-stream"
                        created = next(json.loads(line[6:]) for line in response.iter_lines() if line.startswith("data: "))
                        assert created["type"] == "agent.session.created"
                        check(created["session"], "idle")
                    value = sessions.retrieve(created["session"]["id"]).to_dict()
                else:
                    value = sessions.create(**request, extra_headers={"Idempotency-Key": key}).to_dict()
                assert time.monotonic() - began < 8, "creation waited for offline execution"
                check(value, "requires_action")
                case = {"id": value["id"], "environment_id": value["environment"]["id"], "key": key, "request": request,
                        "snapshot": value, "texts": [message["content"][0]["text"] for message in ordered] if isinstance(request["input"], list) else [request["input"]]}
                cases.append(case)
                retry(case)
                assert post({**request, "input": "changed"}, key).status_code == 409
                assert post(request, key, key_token=settings["peer_token"]).status_code == 409
                assert raw.get(endpoint + "/" + value["id"], headers={"Authorization": "Bearer " + settings["peer_token"]}).status_code == 200
            api.beta.agents.update(saved.id, instructions="Changed after acceptance.")
            assert retry(cases[1]) == cases[1]["snapshot"]
            api.beta.agents.delete(saved.id)
            assert retry(cases[1]) == cases[1]["snapshot"]
            for case in cases:
                for suffix in ("", "/events", "/turns", "/items"):
                    assert raw.get(endpoint + "/" + case["id"] + suffix, headers={"Authorization": "Bearer " + settings["foreign_token"]}).status_code == 404
            foreign = post(cases[0]["request"], cases[0]["key"], key_token=settings["foreign_token"])
            assert foreign.status_code == 200 and foreign.json()["id"] != cases[0]["id"]
            assert raw.get(endpoint + "/" + foreign.json()["id"]).status_code == 404
            result = {"cases": cases}
        else:
            result = settings["accepted"]
            cases = result["cases"]
            if phase == "reopen":
                for case in cases:
                    assert retry(case) == case["snapshot"]
            elif phase == "expire":
                case = cases[0]
                observations, failures = {}, []
                ready = {name: threading.Event() for name in ("sdk", "raw", "creation_retry")}

                def observe(name):
                    try:
                        if name == "raw":
                            with httpx2.Client(trust_env=False, timeout=15) as http:
                                with http.stream("GET", endpoint + "/" + case["id"] + "/events", headers=headers) as response:
                                    assert response.status_code == 200
                                    ready[name].set()
                                    event = next(json.loads(line[6:]) for line in response.iter_lines() if line.startswith("data: "))
                        else:
                            with client() as observer:
                                events = observer.beta.agents.sessions
                                stream = (events.create(**case["request"], stream=True, extra_headers={"Idempotency-Key": case["key"]})
                                          if name == "creation_retry" else events.events.stream(case["id"]))
                                with stream:
                                    ready[name].set()
                                    event = next(iter(stream)).to_dict()
                        assert event["type"] == "agent.session.failed" and set(event) == {"type", "event_id", "session"}
                        check(event["session"], "failed", case["environment_id"])
                        observations[name] = event
                    except BaseException as error:
                        failures.append(error)

                workers = [threading.Thread(target=observe, args=(name,), daemon=True) for name in ready]
                for worker in workers:
                    worker.start()
                assert all(event.wait(10) for event in ready.values()), "live subscriptions did not open"
                # The Go control advances only this committed deadline; the real Worker performs expiry.
                assert raw.post(settings["expiry_control"]).status_code == 204
                for worker in workers:
                    worker.join(timeout=15)
                    assert not worker.is_alive(), "public initial failure observer timed out"
                if failures:
                    raise AssertionError("public initial failure observer failed") from failures[0]
                assert observations["sdk"] == observations["raw"] == observations["creation_retry"]
                assert retry(case, "failed") == observations["sdk"]["session"]
                assert api.beta.agents.environments.retrieve(case["environment_id"]).status == "pending"
                for retained in cases[1:]:
                    assert retry(retained) == retained["snapshot"]
                result["controlled_deadline_expiry"] = True
            elif phase == "unavailable":
                before = {session.id for session in sessions.list()}
                for target in (endpoint, settings["disabled_base"] + "/v1/agents/sessions"):
                    for streaming in (False, True):
                        response = post({**cases[0]["request"], "stream": streaming}, str(uuid.uuid4()), url=target)
                        assert response.status_code == 503 and response.json()["error"]["code"] == "execution_unavailable"
                    response = post(cases[1]["request"], cases[1]["key"], url=target)
                    assert response.status_code == 200 and response.json() == cases[1]["snapshot"]
                assert {session.id for session in sessions.list()} == before
            else:
                raise AssertionError("unknown test phase")
        print(json.dumps(result))


if __name__ == "__main__":
    main()
