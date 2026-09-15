"""Real public self-hosted active text, native application and cold continuation."""

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
    proof = {"case": "public_steering", "scope": "built service, real remote MiniMax, public active input and cold continuation",
             "sdk_version": distribution.version, "sdk_commit": pin["commit"], "snapshots": {},
             "limits": ["204 acknowledges durable admission; Go separately verifies actual native Accepted and cursor",
                        "Written only releases the bounded fixture gate; no crash recovery or OS-quiescence claim"]}
    observations = {"sdk": [], "raw": []}
    ready = {name: threading.Event() for name in observations}
    done = {name: threading.Event() for name in observations}
    failures, lock = [], threading.Lock()
    session_id = None

    def write_private(name, value):
        data = json.dumps(value, indent=2)
        assert token not in data and foreign not in data
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
                    raise AssertionError("public steering observer or input failed") from failures[0]
            value = predicate()
            if value:
                return value
            time.sleep(0.025)
        raise AssertionError(label + " timed out")

    def message(text):
        return {"type": "agent.session.input.message", "input": [
            {"role": "user", "content": [{"type": "input_text", "text": text}]}]}

    def observe(name):
        completed = set()

        def accept(value):
            with lock:
                observations[name].append(value)
            kind = value["type"]
            assert kind not in ("error", "agent.session.failed", "agent.session.turn.failed", "agent.session.turn.cancelled")
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
            raise AssertionError("live stream ended before both real Turns")
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
            instructions = "Use the native shell for requested commands. Command verification requires the exact supplied command argument. Never append echo, separators, wrappers or error recovery. Exit 7 is intentional and must remain the tool's exit status; do not turn it into exit 0. Keep waiting or polling a running command until it finishes. Integrate additional user text without restarting or cancelling that command."
            creation = {"agent": {"model": "MiniMax-M3", "instructions": instructions, "tools": []}, "environment": environment}
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
                assert value["environment"] == expected_environment and value["status"] == status and value["error"] is None
                response = raw.get("/agents/sessions/" + session_id)
                assert response.status_code == 200 and response.json() == value
                proof["snapshots"][name] = value
                return value

            def post_raw(event, key):
                response = raw.post("/agents/sessions/" + session_id + "/events", json={"events": [event]},
                                    headers={"Idempotency-Key": key})
                assert response.status_code == 204 and response.content == b""

            def running_command(turn_id, phase):
                values = [item.to_dict() for item in sessions.items.list(session_id, order="asc", limit=100)
                          if item.turn_id == turn_id and item.type == "command_execution" and item.status == "in_progress"
                          and shlex.split(item.command)[1:] == ["-lc", "./gate.sh " + phase]]
                if values:
                    assert len(values) == 1 and values[0]["cwd"] == settings["workspace_directory"]
                    return values[0]
                return None

            for name in observations:
                threading.Thread(target=observe, args=(name,), daemon=True).start()
            wait_for(lambda: all(signal.is_set() for signal in ready.values()), 25, "live subscriptions")
            exact = "The tool command argument must be exactly the text inside the backticks: no wrapper, no appended echo, no separators, no error recovery. Exit 7 is intentional; preserve that native exit status and do not retry."
            first_text = "Run the exact native shell command `./gate.sh first` once. It waits for the fixture before producing stdout/stderr and exit 7. Keep waiting or polling until it finishes; do not finish the Turn, release the gate yourself, cancel or restart it. Additional user text may arrive while it runs; include it and the command output in your final answer. " + exact
            first_event, first_key = message(first_text), str(uuid.uuid4())
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
            wait_for(lambda: sessions.retrieve(session_id).status == "requires_action", 25, "offline message reservation")
            pending = snapshot("offline", "requires_action")
            assert pending["required_actions"] == [{"type": "environment_connection", "environment_id": environment_id}]
            assert not submitted.is_set() and list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
            write_private("waiting", {"session_id": session_id, "environment_id": environment_id,
                                      "remote_url": settings["remote_url"], "creation_mode": "empty_later"})
            wait_for(lambda: (directory / "initial-connection-ready.json").exists(), 90, "executor connection")
            verify_environment(api.beta.agents.environments.retrieve(environment_id).to_dict(), environment_id, "connected")
            write_private("initial-connection-read", {"environment_id": environment_id})
            wait_for(submitted.is_set, 150, "initial input admission")
            first_id = list(sessions.turns.list(session_id))[0].id
            wait_for(lambda: (directory / "steer-ready.json").exists(), 150, "independent active remote command")
            proof["first_active_command"] = wait_for(lambda: running_command(first_id, "first"), 15, "public active command")
            assert sessions.turns.retrieve(first_id, session_id=session_id).status == "in_progress"
            value = "festival-" + uuid.uuid4().hex
            active_text = "The fictional festival name is " + value + ". Remember it without writing it to a file. Keep waiting for the current command without cancelling, restarting or changing it. Include this exact festival name and the command stdout/stderr in your final answer after the command finishes."
            assert value not in json.dumps(creation) and value not in first_text
            active_event, active_key = message(active_text), str(uuid.uuid4())
            response = sessions.events.with_raw_response.create(session_id, events=[active_event], idempotency_key=active_key)
            assert response.status_code == 204 and response.parse() is None
            post_raw(active_event, active_key)
            assert [turn.id for turn in sessions.turns.list(session_id)] == [first_id]
            assert sessions.turns.retrieve(first_id, session_id=session_id).status == "in_progress"

            def steered_items():
                return [item.to_dict() for item in sessions.items.list(session_id, order="asc", limit=100)
                        if item.type == "message" and item.role == "user" and value in json.dumps(item.to_dict()["content"])]

            active_items = steered_items()
            assert len(active_items) == 1 and active_items[0]["turn_id"] == first_id
            proof["active_submission"] = {"event": active_event, "key": active_key, "status": 204, "item": active_items[0]}
            write_private("steer-submitted", {"turn_id": first_id, "value": value})
            first_turn = wait_for(lambda: turn if (turn := sessions.turns.retrieve(first_id, session_id=session_id)).status == "completed" else None,
                                  150, "steered first Turn completion")
            first_items = list(sessions.items.list(session_id, order="asc", limit=100))
            post_raw(active_event, active_key)
            assert list(sessions.items.list(session_id, order="asc", limit=100)) == first_items
            snapshot("first_completed", "idle")
            write_private("first-completed", {"turn_id": first_id})
            wait_for(lambda: (directory / "resume-ready.json").exists(), 40, "retained native binding")
            second_text = "Recall the fictional festival name supplied by the additional user message during the first Turn. Read retained.txt in a separate native tool call. Then run the exact native shell command `./gate.sh resumed` once in a new tool call. Do not combine the file read with this command. The command waits for the fixture; keep waiting or polling until it finishes and do not release its gate yourself. Include the remembered festival, retained file contents and command stdout/stderr in the final answer. " + exact
            assert value not in second_text
            second_event, second_key = message(second_text), str(uuid.uuid4())
            post_raw(second_event, second_key)
            wait_for(lambda: (directory / "resumed-active.json").exists(), 150, "independent cold command activity")
            turns = list(sessions.turns.list(session_id, order="asc"))
            assert len(turns) == 2 and turns[0].status == "completed" and turns[1].status == "in_progress"
            second_id = turns[1].id
            proof["second_active_command"] = wait_for(lambda: running_command(second_id, "resumed"), 15, "public cold command")
            before = list(sessions.items.list(session_id, order="asc", limit=100))
            assert sessions.events.create(session_id, events=[active_event], idempotency_key=active_key) is None
            post_raw(active_event, active_key)
            assert list(sessions.items.list(session_id, order="asc", limit=100)) == before
            assert steered_items() == active_items and sessions.turns.retrieve(second_id, session_id=session_id).status == "in_progress"
            write_private("old-steer-retried", {"turn_id": second_id})
            second_turn = wait_for(lambda: turn if (turn := sessions.turns.retrieve(second_id, session_id=session_id)).status == "completed" else None,
                                   150, "cold Turn completion")
            wait_for(lambda: all(signal.is_set() for signal in done.values()), 30, "terminal live streams")
            final = snapshot("final", "idle")
            assert final["required_actions"] == []
            connected = wait_for(lambda: resource if (resource := api.beta.agents.environments.retrieve(environment_id, timeout=5)).status == "connected" else None,
                                 30, "executor reconnection after completion")
            proof["final_environment"] = verify_environment(connected.to_dict(), environment_id, "connected")
            turns, items = [first_turn, second_turn], list(sessions.items.list(session_id, order="asc", limit=100))
            for turn, phase in zip(turns, ("first", "resumed")):
                commands = [item for item in items if item.turn_id == turn.id and item.type == "command_execution"
                            and "remote-stdout:" + phase in (item.output or "") and "remote-stderr:" + phase in (item.output or "")]
                assert len(commands) == 1 and commands[0].exit_code == 7 and commands[0].cwd == settings["workspace_directory"]
                argv = shlex.split(commands[0].command)
                assert Path(argv[0]).name == "bash" and argv[1:] == ["-lc", "./gate.sh " + phase]
                answer = "\n".join(part.get("text", "") for item in items if item.turn_id == turn.id and item.type == "message"
                                   and item.role == "assistant" for part in item.to_dict()["content"])
                assert value in answer and settings["instruction"] in answer and "WRONG_LOCAL_INSTRUCTIONS" not in answer
                if phase == "resumed":
                    assert "remote-file-content" in answer
            assert steered_items() == active_items
            proof["turns"], proof["items"] = [turn.to_dict() for turn in turns], [item.to_dict() for item in items]
            assert raw.get("/agents/sessions/" + session_id + "/turns", params={"order": "asc"}).json()["data"] == proof["turns"]
            assert raw.get("/agents/sessions/" + session_id + "/items", params={"order": "asc", "limit": 100}).json()["data"] == proof["items"]
            for event, key in ((first_event, first_key), (active_event, active_key), (second_event, second_key)):
                post_raw(event, key)
            assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": [message("Changed active message")]}, headers={"Idempotency-Key": active_key}).status_code == 409
            assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": [active_event]}, headers={"Authorization": "Bearer " + foreign}).status_code == 404
            time.sleep(1)
            assert list(sessions.turns.list(session_id, order="asc")) == turns and list(sessions.items.list(session_id, order="asc", limit=100)) == items
            for events in observations.values():
                ids = [event["event_id"] for event in events]
                assert len(ids) == len(set(ids))
                assert [event["turn"]["id"] for event in events if event["type"] == "agent.session.turn.created"] == [turn.id for turn in turns]
                assert [event["turn"]["id"] for event in events if event["type"] == "agent.session.turn.completed"] == [turn.id for turn in turns]
                additions = [event for event in events if event["type"] == "agent.session.turn.item.added" and event["item"]["id"] == active_items[0]["id"]]
                assert len(additions) == 1 and additions[0]["turn_id"] == first_id
            raw_ids = [event["event_id"] for event in observations["raw"]]
            start = next(index for index, event in enumerate(observations["sdk"]) if event["event_id"] in raw_ids)
            common = observations["sdk"][start:]
            assert common == observations["raw"][raw_ids.index(common[0]["event_id"]):], "SDK/raw common live suffix differs"
            with client() as recovered:
                resource = recovered.beta.agents.sessions
                assert resource.retrieve(session_id).to_dict() == final
                assert list(resource.turns.list(session_id, order="asc")) == turns
                assert list(resource.items.list(session_id, order="asc", limit=100)) == items
            proof["common_live_events"] = len(common)
            proof["query_recovery"] = proof["old_input_did_not_retarget"] = True
            proof["status"] = "public_active_input_and_cold_continuation_verified"
            print("Built service: real self-hosted active input, exact retries, same Turn application, remote commands and cold history passed.", flush=True)
    finally:
        with lock:
            proof["sdk_events"], proof["raw_events"] = list(observations["sdk"]), list(observations["raw"])
        write_private("public-steering-proof", proof)


if __name__ == "__main__":
    main()
