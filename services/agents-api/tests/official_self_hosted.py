"""Public self-hosted execution against a built standalone service and real harness."""

import importlib.metadata
import json
import os
from pathlib import Path
import shlex
import sys
import threading
import time
import uuid

sys.dont_write_bytecode = True

import httpx2
from openai import OpenAI
from official_environment_retrieve import verify_environment
from official_self_hosted_creation import assert_creation_retry, create_initial_session


def main():
    settings = json.load(sys.stdin)
    mode = settings.get("creation_mode", "empty_later")
    assert mode in ("empty_later", "ordinary_initial", "streamed_initial")
    initial_input = mode != "empty_later"
    base, token, foreign = (settings[name] for name in ("base", "token", "foreign_token"))
    directory = Path(settings["evidence"])
    os.umask(0o077)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"]
    assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]
    proof = {"scope": "public Session creation and input admission through a built standalone service; real remote execution", "creation_mode": mode,
             "sdk_version": distribution.version, "sdk_commit": pin["commit"], "snapshots": {}, "environment_reads": {}}
    observations = {"sdk": [], "raw": []}
    ready = {name: threading.Event() for name in observations}
    done = {name: threading.Event() for name in observations}
    failures = []
    lock = threading.Lock()
    session_id = None
    expected_environment = None

    def write_private(name, value):
        data = json.dumps(value, indent=2)
        assert token not in data and foreign not in data, "caller credential in public evidence"
        temporary = directory / (name + ".tmp")
        temporary.write_text(data)
        temporary.chmod(0o600)
        temporary.replace(directory / (name + ".json"))

    def client(key=token):
        return OpenAI(api_key=key, base_url=base + "/v1", max_retries=0,
                      _strict_response_validation=True,
                      http_client=httpx2.Client(trust_env=False, timeout=360))

    def wait_for(predicate, timeout, label):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            with lock:
                if failures:
                    raise AssertionError("public observer or input failed") from failures[0]
            value = predicate()
            if value:
                return value
            time.sleep(0.025)
        raise AssertionError(label + " timed out")

    def message(text):
        return {"type": "agent.session.input.message", "input": [
            {"role": "user", "content": [{"type": "input_text", "text": text}]}]}

    def prompt(phase):
        text = "Run the exact command `./placement.sh " + phase + "` once with the native shell. "
        text += "The tool command argument must be exactly the text inside the backticks: no wrapper, no appended echo, no separators, no error recovery. Exit 7 is intentional; preserve that native exit status and do not retry. Report stdout, stderr and the verification memory briefly."
        if phase == "first":
            return text + " The fictional festival name to remember is " + settings["memory"] + "."
        return text + " Recall the fictional festival name from the first Turn and read retained.txt."

    def check_session(value, status):
        assert value["id"] == session_id and value["object"] == "agent.session"
        assert value["environment"] == expected_environment
        assert value["status"] == status and value["error"] is None
        actions = [{"type": "environment_connection", "environment_id": expected_environment["id"]}]
        assert value["required_actions"] == (actions if status == "requires_action" else [])
        assert value["vault_ids"] == [] and isinstance(value["metadata"], dict)
        assert isinstance(value["created_at"], int) and isinstance(value["last_active_at"], int)

    def observe(name):
        completed = set()

        def accept(value):
            with lock:
                observations[name].append(value)
            kind = value["type"]
            assert kind not in ("error", "agent.session.failed", "agent.session.turn.failed", "agent.session.turn.cancelled")
            if kind == "agent.session.requires_action":
                check_session(value["session"], "requires_action")
            if kind == "agent.session.turn.completed":
                completed.add(value["turn"]["id"])
            return kind == "agent.session.idle" and len(completed) == 2

        try:
            if name == "sdk":
                with client() as api:
                    with api.beta.agents.sessions.events.stream(session_id, timeout=450) as stream:
                        ready[name].set()
                        for event in stream:
                            if accept(event.to_dict()):
                                return
            else:
                with httpx2.Client(trust_env=False, timeout=450) as raw:
                    with raw.stream("GET", base + "/v1/agents/sessions/" + session_id + "/events",
                                    headers={"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}) as response:
                        assert response.status_code == 200 and response.headers["content-type"] == "text/event-stream"
                        ready[name].set()
                        for line in response.iter_lines():
                            if line.startswith("data: ") and accept(json.loads(line[6:])):
                                return
            raise AssertionError("live stream ended before both Turns")
        except BaseException as error:
            with lock:
                failures.append(error)
        finally:
            done[name].set()

    try:
        with client() as api, httpx2.Client(
            base_url=base + "/v1", trust_env=False, timeout=360,
            headers={"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1", "Host": "untrusted.example"},
        ) as raw:
            sessions = api.beta.agents.sessions
            instructions = "Use the native shell for requested commands. Command verification requires the exact supplied command argument. Never append echo, separators, wrappers or error recovery. Exit 7 is intentional and must remain the tool's exit status; do not turn it into exit 0."
            agent = {"model": "MiniMax-M3", "instructions": instructions, "tools": []}
            environment = {"type": "self_hosted", "workspace_directory": settings["workspace_directory"]}
            creation = {"agent": agent, "environment": environment}

            def assert_empty(created):
                value = created.to_dict()
                assert value["status"] == "idle" and value["required_actions"] == [] and value["usage"] is None
                assert value["created_at"] == value["last_active_at"]
                assert value["environment"] == {**environment, "id": value["environment"]["id"],
                                                 "capability_directories": [], "remote_url": settings["remote_url"]}
                assert list(sessions.turns.list(created.id)) == [] and list(sessions.items.list(created.id)) == []
                return value

            with sessions.create(**creation, input=None, stream=True) as stream:
                created = next(stream)
                assert created.type == "agent.session.created"
                assert set(created.to_dict()) == {"type", "event_id", "session"}
                proof["streamed_empty_creation"] = assert_empty(created.session)
            for capability in (None, []):
                value = sessions.create(agent=agent, environment={**environment, "capability_directories": capability}, input=None)
                assert_empty(value)
            creation_key = str(uuid.uuid4())
            if initial_input:
                creation["input"] = prompt("first") if mode == "ordinary_initial" else message(prompt("first"))["input"]
                created = create_initial_session(sessions, creation, creation_key, mode, assert_empty, proof)
                initial = created.to_dict()
            else:
                created = sessions.create(**creation, extra_headers={"Idempotency-Key": creation_key})
                initial = assert_empty(created)
            session_id = created.id
            expected_environment = {**environment, "id": initial["environment"]["id"],
                                    "capability_directories": [], "remote_url": settings["remote_url"]}
            agent_id = initial["agent"]["id"]
            check_session(initial, "requires_action" if initial_input else "idle")
            assert sessions.create(**creation, extra_headers={"Idempotency-Key": creation_key}).id == session_id

            def snapshot(name, status):
                value = sessions.retrieve(session_id).to_dict()
                check_session(value, status)
                response = raw.get("/agents/sessions/" + session_id)
                assert response.status_code == 200 and response.json() == value
                assert [item.to_dict() for item in sessions.list(agent_id=agent_id, limit=1)] == [value]
                proof["snapshots"][name] = value
                return value

            def environment_snapshot(name, status=None):
                environment_id = expected_environment["id"]
                value = verify_environment(api.beta.agents.environments.retrieve(environment_id).to_dict(), environment_id, status)
                response = raw.get("/agents/environments/" + environment_id)
                assert response.status_code == 200 and response.headers["cache-control"] == "no-store"
                wire = verify_environment(response.json(), environment_id, status)
                proof["environment_reads"][name] = {"sdk": value, "raw": wire}

            snapshot("initial", "requires_action" if initial_input else "idle")
            environment_snapshot("initial", "pending")
            before = {value.id for value in sessions.list()}
            for change in ({"input": [{"role": "user", "content": [{"type": "input_image", "image_url": "https://example.invalid/image.png"}]}]}, {"agent": {**agent, "tools": [
                {"type": "function", "name": "pending", "description": "Unsupported pending action", "parameters": {"type": "object"}}]}},
                {"environment": {"type": "self_hosted", "workspace_directory": "relative"}}):
                reply = raw.post("/agents/sessions", json={**creation, **change})
                assert reply.status_code == 400 and reply.headers["content-type"].startswith("application/json")
            assert {value.id for value in sessions.list()} == before
            for events in ([{"type": "agent.session.input.cancel"}],
                           [message("Mixed input must not commit"), {"type": "agent.session.input.cancel"}],
                           [{"type": "agent.session.input.tool_result", "call_id": "unknown", "output": "unused"}]):
                assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": events}).status_code == 400
            assert list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
            for suffix in ("", "/events", "/turns", "/items"):
                assert raw.get("/agents/sessions/" + session_id + suffix,
                               headers={"Authorization": "Bearer " + foreign}).status_code == 404
            assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": [message("Foreign")]},
                            headers={"Authorization": "Bearer " + foreign}).status_code == 404
            assert raw.get("/agents/sessions", headers={"Authorization": "Bearer " + foreign}).json()["data"] == []
            proof["unsupported_profile_rejected_before_writes"] = True
            proof["tenant_isolation"] = True

            for name in observations:
                threading.Thread(target=observe, args=(name,), daemon=True).start()
            wait_for(lambda: all(value.is_set() for value in ready.values()), 25, "live subscriptions")
            first_event, first_key = message(prompt("first")), str(uuid.uuid4())
            submitted = threading.Event()
            submission = {}

            def submit_first():
                try:
                    with client() as submitting:
                        began = time.monotonic()
                        response = submitting.beta.agents.sessions.events.with_raw_response.create(
                            session_id, events=[first_event], idempotency_key=first_key)
                        assert response.status_code == 204 and response.parse() is None
                        submission["elapsed_seconds"] = time.monotonic() - began
                        submission["status"] = response.status_code
                        assert list(submitting.beta.agents.sessions.turns.list(session_id)), "204 before durable Turn admission"
                except BaseException as error:
                    with lock:
                        failures.append(error)
                finally:
                    submitted.set()

            if not initial_input:
                threading.Thread(target=submit_first, daemon=True).start()
                wait_for(lambda: all(any(item["type"] == "agent.session.requires_action" for item in values)
                                     for values in observations.values()), 25, "offline action")
            pending = snapshot("waiting", "requires_action")
            assert pending["usage"] is None
            if mode == "streamed_initial":
                assert proof["creation_events"][1]["session"] == pending
            began = time.monotonic()
            # Exceed the server's ordinary write timeout while no executor or daemon exists.
            while time.monotonic() - began < 32:
                if not initial_input:
                    assert not submitted.is_set(), "input returned before executor/native readiness"
                assert list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
                time.sleep(0.25)
            proof["offline_observation_seconds"] = time.monotonic() - began
            with lock:
                for values in observations.values():
                    if initial_input:
                        assert values == [], "GET events replayed initial creation activity"
                    else:
                        assert len(values) == 1 and values[0]["type"] == "agent.session.requires_action"
                        assert values[0]["session"] == pending
            write_private("waiting", {"session_id": session_id, "environment_id": expected_environment["id"],
                                      "remote_url": expected_environment["remote_url"], "creation_mode": mode})
            wait_for(lambda: (directory / "initial-connection-ready.json").exists(), 90, "executor connection before daemon startup")
            environment_snapshot("connected_before_execution", "connected")
            write_private("initial-connection-read", {"environment_id": expected_environment["id"]})
            if initial_input:
                proof["first_input"] = {"source": mode, "creation_response_elapsed_seconds": proof["creation_response_elapsed_seconds"]}
            else:
                wait_for(submitted.is_set, 150, "first input admission")
                assert submission["elapsed_seconds"] >= 32
                proof["first_input"] = submission

            def completed(count):
                turns = list(sessions.turns.list(session_id, order="asc"))
                assert len(turns) <= count
                assert not any(turn.status in ("failed", "cancelled") for turn in turns)
                return turns if len(turns) == count and all(turn.status == "completed" for turn in turns) else None

            first_turn = wait_for(lambda: completed(1), 150, "first real Turn")[0]
            snapshot("after_first", "idle")
            environment_snapshot("after_first")
            if initial_input:
                assert_creation_retry(sessions, raw, creation, creation_key, session_id)
            else:
                sessions.events.create(session_id, events=[first_event], idempotency_key=first_key)
            assert len(list(sessions.turns.list(session_id))) == 1
            write_private("first-completed", {"turn_id": first_turn.id})
            wait_for(lambda: (directory / "resume-ready.json").exists(), 45, "retained native binding and executor reconnection")
            environment_snapshot("before_resumed", "connected")
            second_event, second_key = message(prompt("resumed")), str(uuid.uuid4())
            reply = raw.post("/agents/sessions/" + session_id + "/events", json={"events": [second_event]},
                             headers={"Idempotency-Key": second_key})
            assert reply.status_code == 204 and reply.content == b""
            turns = wait_for(lambda: completed(2), 150, "second real Turn")
            wait_for(lambda: all(value.is_set() for value in done.values()), 30, "both completion streams")
            final = snapshot("final", "idle")
            environment_snapshot("after_remote_file_and_history")
            items = list(sessions.items.list(session_id, limit=100, order="asc"))
            proof["turns"], proof["items"] = [turn.to_dict() for turn in turns], [item.to_dict() for item in items]
            for turn, phase in zip(turns, ("first", "resumed")):
                assert sessions.turns.retrieve(turn.id, session_id=session_id) == turn
                group = [item.to_dict() for item in items if item.turn_id == turn.id]
                commands = [item for item in group if item["type"] == "command_execution"
                            and "remote-stdout:" + phase in item.get("output", "")
                            and "remote-stderr:" + phase in item.get("output", "")]
                assert len(commands) == 1
                command = commands[0]
                assert command["exit_code"] == 7 and command["cwd"] == settings["workspace_directory"]
                argv = shlex.split(command["command"])
                assert Path(argv[0]).name == "bash" and argv[1:] == ["-lc", "./placement.sh " + phase]
                answer = "\n".join(part.get("text", "") for item in group
                                   if item["type"] == "message" and item.get("role") == "assistant" for part in item["content"])
                assert settings["memory"] in answer and settings["instruction"] in answer
                assert "WRONG_LOCAL_INSTRUCTIONS" not in answer
            assert raw.get("/agents/sessions/" + session_id + "/turns", params={"order": "asc"}).json()["data"] == proof["turns"]
            assert raw.get("/agents/sessions/" + session_id + "/items", params={"order": "asc", "limit": 100}).json()["data"] == proof["items"]
            retries = [(second_event, second_key)] if initial_input else [(first_event, first_key), (second_event, second_key)]
            for event, key in retries:
                sessions.events.create(session_id, events=[event], idempotency_key=key)
                response = raw.post("/agents/sessions/" + session_id + "/events", json={"events": [message("Changed retry")]},
                                    headers={"Idempotency-Key": key})
                assert response.status_code == 409
            if initial_input:
                assert_creation_retry(sessions, raw, creation, creation_key, session_id)
            time.sleep(1)
            assert list(sessions.turns.list(session_id, order="asc")) == turns
            assert list(sessions.items.list(session_id, limit=100, order="asc")) == items
            proof["retries_preserved_turns_and_items"] = True

            for values in observations.values():
                ids = [value["event_id"] for value in values]
                assert len(ids) == len(set(ids))
                kinds = [value["type"] for value in values]
                connected = kinds.index("agent.session.environment.connected")
                cleared, admitted = kinds.index("agent.session.idle"), kinds.index("agent.session.turn.created")
                assert connected < cleared < admitted
                if initial_input:
                    assert "agent.session.created" not in kinds and "agent.session.requires_action" not in kinds
                else:
                    request = kinds.index("agent.session.requires_action")
                    assert request < connected and kinds.count("agent.session.requires_action") == 1
                    assert values[request]["session"] == pending
                assert set(values[cleared]) == {"type", "event_id", "session"}
                check_session(values[cleared]["session"], "idle")
                assert values[cleared]["session"]["usage"] is None
                turn_ids = [turn.id for turn in turns]
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.created"] == turn_ids
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.completed"] == turn_ids
                for event in values:
                    if event["type"].startswith("agent.session.environment."):
                        assert set(event) == {"type", "event_id", "session_id", "environment"}
                        assert event["environment"]["id"] == expected_environment["id"] and event["environment"]["error"] is None
            assert [value["event_id"] for value in observations["sdk"]] == [value["event_id"] for value in observations["raw"]]
            with client() as recovered:
                resource = recovered.beta.agents.sessions
                assert resource.retrieve(session_id).to_dict() == final
                assert list(resource.turns.list(session_id, order="asc")) == turns
                assert list(resource.items.list(session_id, limit=100, order="asc")) == items
                verify_environment(recovered.beta.agents.environments.retrieve(expected_environment["id"]).to_dict(), expected_environment["id"])
            proof["query_recovery"] = True
            proof["live_event_scope"] = "common GET subscription interval; creation events are recorded separately"
            proof["status"] = "public_creation_waiting_admission_and_real_remote_continuation_verified"
            print("Built service (" + mode + "): public self-hosted creation/wait, safe Environment GET, fixed SDK/raw SSE, two real remote Turns and retry recovery passed.", flush=True)
    finally:
        with lock:
            proof["sdk_events"], proof["raw_events"] = list(observations["sdk"]), list(observations["raw"])
        write_private("public-environment-proof", proof)


if __name__ == "__main__":
    main()
