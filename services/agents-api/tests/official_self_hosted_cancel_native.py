"""Real public self-hosted cancellation and cold continuation against a built service."""

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


def main():
    settings = json.load(sys.stdin)
    base, token, foreign = (settings[name] for name in ("base", "token", "foreign_token"))
    directory = Path(settings["evidence"])
    os.umask(0o077)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source.get("vcs_info", {}).get("commit_id") == pin["commit"]
    proof = {"scope": "built service; public self_hosted long-command cancellation and real cold continuation",
             "case": "public_cancellation", "sdk_version": distribution.version, "sdk_commit": pin["commit"],
             "snapshots": {}, "limits": ["204 is durable admission, not process exit",
             "OS observations are recorded separately by the native fixture; no general quiescence guarantee",
             "Cancellation preserves observed output and usage; complete native final usage remains unverified"]}
    observations = {"sdk": [], "raw": []}
    ready = {name: threading.Event() for name in observations}
    done = {name: threading.Event() for name in observations}
    failures, lock = [], threading.Lock()
    session_id = None

    def write_private(name, value):
        data = json.dumps(value, indent=2)
        assert token not in data and foreign not in data, "caller credential in public evidence"
        temporary = directory / (name + ".tmp")
        temporary.write_text(data)
        temporary.chmod(0o600)
        temporary.replace(directory / (name + ".json"))

    def client():
        return OpenAI(api_key=token, base_url=base + "/v1", max_retries=0, _strict_response_validation=True,
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

    def observe(name):
        terminal = {}

        def accept(value):
            with lock:
                observations[name].append(value)
            kind = value["type"]
            assert kind not in ("error", "agent.session.failed", "agent.session.turn.failed")
            if kind in ("agent.session.turn.cancelled", "agent.session.turn.completed"):
                terminal[value["turn"]["id"]] = kind
            return kind == "agent.session.idle" and len(terminal) == 2

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
            raise AssertionError("live stream ended before cancelled and resumed Turns")
        except BaseException as error:
            with lock:
                failures.append(error)
        finally:
            done[name].set()

    try:
        with client() as api, httpx2.Client(base_url=base + "/v1", trust_env=False, timeout=360,
                                          headers={"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}) as raw:
            sessions = api.beta.agents.sessions
            environment = {"type": "self_hosted", "workspace_directory": settings["workspace_directory"]}
            instructions = "Use the native shell for exact requested commands. Do not add wrappers, separators or recovery. Wait or poll a running command; never finish the Turn while it is still running."
            creation = {"agent": {"model": settings.get("model") or "MiniMax-M3", "instructions": instructions, "tools": []}, "environment": environment}
            creation_key = str(uuid.uuid4())
            created = sessions.create(**creation, extra_headers={"Idempotency-Key": creation_key})
            session_id, environment_id = created.id, created.environment.id
            expected_environment = {**environment, "id": environment_id, "capability_directories": [], "remote_url": settings["remote_url"]}
            assert created.environment.to_dict() == expected_environment
            assert created.status == "idle" and created.required_actions == [] and created.usage is None
            assert list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
            assert sessions.create(**creation, extra_headers={"Idempotency-Key": creation_key}).id == session_id
            verify_environment(api.beta.agents.environments.retrieve(environment_id).to_dict(), environment_id, "pending")

            def snapshot(name, status):
                value = sessions.retrieve(session_id).to_dict()
                assert value["id"] == session_id and value["environment"] == expected_environment and value["status"] == status
                assert value["error"] is None
                response = raw.get("/agents/sessions/" + session_id)
                assert response.status_code == 200 and response.json() == value
                proof["snapshots"][name] = value
                return value

            for name in observations:
                threading.Thread(target=observe, args=(name,), daemon=True).start()
            wait_for(lambda: all(signal.is_set() for signal in ready.values()), 25, "live subscriptions")
            first_command = "./placement.sh first"
            first_event = message("First run the exact command `" + first_command + "` once with the native shell. Its exit 7 is intentional; preserve stdout/stderr and do not retry. Then run the exact command `./long.sh cancel` once as a separate native shell call. Keep waiting or polling, and do not finish while that long command is running. Remember the fictional festival name " + settings["memory"] + ".")
            first_key, cancel_key = str(uuid.uuid4()), str(uuid.uuid4())
            cancel_events = [{"type": "agent.session.input.cancel"}]
            submitted = threading.Event()

            def submit_first():
                try:
                    with client() as submitting:
                        response = submitting.beta.agents.sessions.events.with_raw_response.create(session_id, events=[first_event], idempotency_key=first_key)
                        assert response.status_code == 204 and response.parse() is None
                        assert list(submitting.beta.agents.sessions.turns.list(session_id)), "204 before durable admission"
                except BaseException as error:
                    with lock:
                        failures.append(error)
                finally:
                    submitted.set()

            threading.Thread(target=submit_first, daemon=True).start()
            wait_for(lambda: sessions.retrieve(session_id).status == "requires_action", 25, "pending public input")
            pending = snapshot("pending", "requires_action")
            assert pending["required_actions"] == [{"type": "environment_connection", "environment_id": environment_id}]
            assert not submitted.is_set() and list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
            write_private("waiting", {"session_id": session_id, "environment_id": environment_id,
                                      "remote_url": settings["remote_url"], "creation_mode": "empty_later"})
            wait_for(lambda: (directory / "initial-connection-ready.json").exists(), 90, "executor connection")
            verify_environment(api.beta.agents.environments.retrieve(environment_id).to_dict(), environment_id, "connected")
            write_private("initial-connection-read", {"environment_id": environment_id})
            wait_for(submitted.is_set, 150, "first input admission")
            first_turn = list(sessions.turns.list(session_id))[0]

            def command_items(turn_id, phase):
                return [item.to_dict() for item in sessions.items.list(session_id, limit=100, order="asc")
                        if item.turn_id == turn_id and item.type == "command_execution"
                        and "remote-stdout:" + phase in (item.output or "") and "remote-stderr:" + phase in (item.output or "")]

            partial = wait_for(lambda: command_items(first_turn.id, "first"), 120, "public partial command output")
            assert len(partial) == 1
            assert partial[0]["cwd"] == settings["workspace_directory"] and partial[0]["exit_code"] == 7
            argv = shlex.split(partial[0]["command"])
            assert Path(argv[0]).name == "bash" and argv[1:] == ["-lc", first_command]
            wait_for(lambda: (directory / "cancel-ready.json").exists(), 30, "independent long-command activity")
            def running_command():
                return [item.to_dict() for item in sessions.items.list(session_id, limit=100, order="asc")
                        if item.turn_id == first_turn.id and item.type == "command_execution"
                        and shlex.split(item.command)[1:] == ["-lc", "./long.sh cancel"]]
            running = wait_for(running_command, 15, "public long-command identity")
            assert len(running) == 1 and running[0]["status"] == "in_progress"
            assert running[0]["cwd"] == settings["workspace_directory"]
            proof["running_command_before_cancel"] = running[0]
            assert sessions.turns.retrieve(first_turn.id, session_id=session_id).status == "in_progress"
            proof["partial_before_cancel"] = partial
            proof["partial_output_scope"] = "Completed first command within the still-active Turn; running-command output deltas are not asserted"
            write_private("cancel-requested", {"turn_id": first_turn.id, "request_started_unix": str(time.time())})
            began = time.monotonic()
            response = sessions.events.with_raw_response.create(session_id, events=cancel_events, idempotency_key=cancel_key)
            assert response.status_code == 204 and response.parse() is None
            proof["cancel_response"] = {"status": 204, "elapsed_seconds": time.monotonic() - began}
            first_turn = wait_for(lambda: (turn if (turn := sessions.turns.retrieve(first_turn.id, session_id=session_id)).status == "cancelled" else None), 45, "cancelled public Turn")
            cancelled_output = command_items(first_turn.id, "first")
            assert len(cancelled_output) == 1 and cancelled_output[0]["id"] == partial[0]["id"]
            assert partial[0]["output"] in cancelled_output[0]["output"]
            proof["retained_cancelled_output"] = cancelled_output
            snapshot("cancelled", "idle")
            write_private("first-cancelled", {"turn_id": first_turn.id})
            wait_for(lambda: (directory / "resume-ready.json").exists(), 90, "native exit measurement and retained binding")
            second_event = message("Run the exact command `./resume.sh` once with the native shell. It waits for the test gate, then executes the resumed verification with intentional exit 7; preserve that exit and do not retry. Keep waiting while it runs. Read retained.txt and recall the fictional festival name from the first Turn. Include the remembered name and command stdout/stderr in the final answer.")
            second_key = str(uuid.uuid4())
            response = raw.post("/agents/sessions/" + session_id + "/events", json={"events": [second_event]}, headers={"Idempotency-Key": second_key})
            assert response.status_code == 204 and response.content == b""
            wait_for(lambda: (directory / "resumed-active.json").exists(), 150, "actual resumed command activity")
            turns = list(sessions.turns.list(session_id, order="asc"))
            assert len(turns) == 2 and turns[0].status == "cancelled" and turns[1].status == "in_progress"
            second_id = turns[1].id
            sessions.events.create(session_id, events=cancel_events, idempotency_key=cancel_key)
            response = raw.post("/agents/sessions/" + session_id + "/events", json={"events": cancel_events}, headers={"Idempotency-Key": cancel_key})
            assert response.status_code == 204 and response.content == b""
            time.sleep(1)
            assert sessions.turns.retrieve(second_id, session_id=session_id).status == "in_progress"
            write_private("old-cancel-retried", {"turn_id": second_id})
            second_turn = wait_for(lambda: (turn if (turn := sessions.turns.retrieve(second_id, session_id=session_id)).status == "completed" else None), 150, "uncancelled resumed Turn")
            wait_for(lambda: all(signal.is_set() for signal in done.values()), 30, "terminal live streams")
            final = snapshot("final", "idle")
            turns = [first_turn, second_turn]
            items = list(sessions.items.list(session_id, limit=100, order="asc"))
            resumed = command_items(second_id, "resumed")
            assert len(resumed) == 1 and resumed[0]["exit_code"] == 7 and resumed[0]["cwd"] == settings["workspace_directory"]
            argv = shlex.split(resumed[0]["command"])
            assert Path(argv[0]).name == "bash" and argv[1:] == ["-lc", "./resume.sh"]
            answer = "\n".join(part.get("text", "") for item in items if item.turn_id == second_id and item.type == "message"
                               and item.role == "assistant" for part in item.to_dict()["content"])
            assert settings["memory"] in answer and settings["instruction"] in answer and "WRONG_LOCAL_INSTRUCTIONS" not in answer
            proof["turns"], proof["items"] = [turn.to_dict() for turn in turns], [item.to_dict() for item in items]
            assert raw.get("/agents/sessions/" + session_id + "/turns", params={"order": "asc"}).json()["data"] == proof["turns"]
            assert raw.get("/agents/sessions/" + session_id + "/items", params={"order": "asc", "limit": 100}).json()["data"] == proof["items"]
            for event, key in ((first_event, first_key), (second_event, second_key)):
                sessions.events.create(session_id, events=[event], idempotency_key=key)
            assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": [message("Changed cancel key")]}, headers={"Idempotency-Key": cancel_key}).status_code == 409
            assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": cancel_events}, headers={"Authorization": "Bearer " + foreign}).status_code == 404
            time.sleep(1)
            assert list(sessions.turns.list(session_id, order="asc")) == turns and list(sessions.items.list(session_id, limit=100, order="asc")) == items
            for values in observations.values():
                ids = [value["event_id"] for value in values]
                assert len(ids) == len(set(ids))
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.created"] == [turn.id for turn in turns]
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.cancelled"] == [first_turn.id]
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.completed"] == [second_id]
            assert observations["sdk"] == observations["raw"], "SDK/raw common live event payloads differ"
            with client() as recovered:
                resource = recovered.beta.agents.sessions
                assert resource.retrieve(session_id).to_dict() == final
                assert list(resource.turns.list(session_id, order="asc")) == turns
                assert list(resource.items.list(session_id, limit=100, order="asc")) == items
            proof["query_recovery"] = proof["old_cancel_did_not_retarget"] = proof["retries_preserved_turns_and_items"] = True
            proof["status"] = "public_cancelled_turn_output_and_cold_continuation_verified"
            print("Built service: real public self-hosted cancellation, retained output/history, cold continuation and old-cancel retry passed.", flush=True)
    finally:
        with lock:
            proof["sdk_events"], proof["raw_events"] = list(observations["sdk"]), list(observations["raw"])
        write_private("public-cancellation-proof", proof)


if __name__ == "__main__":
    main()
