"""Real public self-hosted functions through the pinned SDK's automatic handlers."""

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
    memory = "festival-" + uuid.uuid4().hex
    private_exception = "private-handler-exception-" + uuid.uuid4().hex
    proof = {"case": "public_functions", "scope": "built service, real remote MiniMax, official automatic function handlers",
             "sdk_version": distribution.version, "sdk_commit": pin["commit"], "snapshots": {},
             "limits": ["One function success and one SDK-mapped error; no image or complete tool-set claim",
                        "Cold continuation is tested; recovery of an interrupted native call is not"]}
    observations = {"sdk": [], "raw": []}
    ready = {name: threading.Event() for name in observations}
    done = {name: threading.Event() for name in observations}
    failures, submissions, input_responses, handler_calls, helper_events = [], [], [], [], [[], []]
    lock = threading.Lock()
    session_id = None

    def write_private(name, value):
        data = json.dumps(value, indent=2)
        assert token not in data and foreign not in data and private_exception not in data
        temporary = directory / (name + ".tmp")
        temporary.write_text(data)
        temporary.chmod(0o600)
        temporary.replace(directory / (name + ".json"))

    def capture_result(response):
        request = response.request
        if request.method == "POST" and request.url.path.endswith("/events"):
            events = json.loads(request.content).get("events", [])
            if events and all(event["type"] == "agent.session.input.tool_result" for event in events):
                assert len(events) == 1
                with lock:
                    submissions.append({"status": response.status_code, "key": request.headers["Idempotency-Key"], "event": events[0]})
            elif events and all(event["type"] == "agent.session.input.message" for event in events):
                with lock:
                    input_responses.append({"status": response.status_code, "key": request.headers["Idempotency-Key"]})

    def client(capture=False):
        return OpenAI(api_key=token, base_url=base + "/v1", max_retries=0, _strict_response_validation=True,
                      http_client=httpx2.Client(trust_env=False, timeout=360,
                                               event_hooks={"response": [capture_result]} if capture else None))

    def wait_for(predicate, timeout, label):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            with lock:
                if failures:
                    raise AssertionError("public function observer or handler failed") from failures[0]
            value = predicate()
            if value:
                return value
            time.sleep(0.025)
        raise AssertionError(label + " timed out")

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
            raise AssertionError("live stream ended before two function Turns")
        except BaseException as error:
            with lock:
                failures.append(error)
        finally:
            done[name].set()

    try:
        with client() as api, httpx2.Client(base_url=base + "/v1", trust_env=False, timeout=360,
                                          headers={"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}) as raw:
            sessions = api.beta.agents.sessions
            tool = {"type": "function", "name": "lookup_festival", "description": "Call once per requested phase to obtain the festival record.",
                    "parameters": {"type": "object", "properties": {"phase": {"type": "string", "enum": ["first", "resumed"]}},
                                   "required": ["phase"], "additionalProperties": False}}
            environment = {"type": "self_hosted", "workspace_directory": settings["workspace_directory"]}
            instructions = "Use lookup_festival exactly once when requested, with the requested phase. Use the native shell for exact requested commands. Exit 7 and a tool failure are intentional test outcomes: report them without retries, alternate tools or recovery."
            creation = {"agent": {"model": "MiniMax-M3", "instructions": instructions, "tools": [tool]}, "environment": environment}
            creation_key = str(uuid.uuid4())
            created = sessions.create(**creation, extra_headers={"Idempotency-Key": creation_key})
            session_id, environment_id = created.id, created.environment.id
            expected_environment = {**environment, "id": environment_id, "capability_directories": [], "remote_url": settings["remote_url"]}
            assert created.environment.to_dict() == expected_environment and created.agent.tools[0].to_dict() == {**tool, "defer_loading": False}
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

            def accepted_result(index):
                with lock:
                    successful = [entry for entry in submissions if entry["status"] == 204]
                assert len(successful) == index + 1
                assert successful[index]["key"] and successful[index]["event"]["success"] == (index == 0)
                return successful[index]

            def replay_result(submission):
                response = raw.post("/agents/sessions/" + session_id + "/events", json={"events": [submission["event"]]},
                                    headers={"Idempotency-Key": submission["key"]})
                assert response.status_code == 204 and response.content == b""

            def run_turn(index):
                phase = ("first", "resumed")[index]
                prompt = ("Call lookup_festival once with phase first. Remember its festival value without writing it to files. Then run the exact native shell command `./placement.sh first` once. Its exit 7 is intentional; preserve stdout/stderr and do not retry. Include the festival value and command output in your final answer."
                          if index == 0 else "Recall the festival value returned by lookup_festival in the first Turn. Call lookup_festival once with phase resumed; its failure is intentional, do not retry it. Read retained.txt, then run the exact native shell command `./placement.sh resumed` once with intentional exit 7. Include the remembered festival, retained file contents, exact tool error and command stdout/stderr in your final answer.")
                assert memory not in prompt
                with client(capture=True) as submitting:
                    resource = submitting.beta.agents.sessions

                    def lookup_festival(arguments):
                        try:
                            assert arguments == {"phase": phase}
                            handler_calls.append({"phase": phase, "arguments": arguments})
                            assert len(handler_calls) == index + 1, "function handler executed more than once"
                            call = [event["item"] for event in helper_events[index] if event["type"] == "agent.session.turn.item.added"
                                    and event["item"]["type"] == "function_call"][-1]

                            def pending_action():
                                current = resource.retrieve(session_id)
                                return current.to_dict() if current.status == "requires_action" and any(
                                    action.type == "function_call" and action.call_id == call["call_id"] and action.turn_id == call["turn_id"]
                                    for action in current.required_actions) else None

                            pending = wait_for(pending_action, 15, "persisted function action")
                            proof["snapshots"][phase + "_function_pending"] = pending
                            if index == 1:
                                before = [item.to_dict() for item in resource.items.list(session_id, order="asc", limit=100)]
                                replay_result(accepted_result(0))
                                assert pending_action() == pending, "old result changed the current pending action"
                                assert [item.to_dict() for item in resource.items.list(session_id, order="asc", limit=100)] == before
                                assert len(list(resource.turns.list(session_id))) == 2
                                proof["old_result_did_not_retarget"] = True
                        except BaseException as error:
                            with lock:
                                failures.append(error)
                            raise
                        if index == 1:
                            raise RuntimeError(private_exception)
                        return {"festival": memory}

                    with resource.stream(session_id, input=prompt, tool_handlers={"lookup_festival": lookup_festival},
                                         idempotency_key=input_keys[index], timeout=360) as stream:
                        for event in stream:
                            helper_events[index].append(event.to_dict())

            for name in observations:
                threading.Thread(target=observe, args=(name,), daemon=True).start()
            wait_for(lambda: all(signal.is_set() for signal in ready.values()), 25, "live subscriptions")
            input_keys = [str(uuid.uuid4()), str(uuid.uuid4())]
            first_done = threading.Event()

            def first_turn():
                try:
                    run_turn(0)
                except BaseException as error:
                    with lock:
                        failures.append(error)
                finally:
                    first_done.set()

            threading.Thread(target=first_turn, daemon=True).start()
            wait_for(lambda: sessions.retrieve(session_id).status == "requires_action", 25, "offline input reservation")
            pending = snapshot("offline", "requires_action")
            assert pending["required_actions"] == [{"type": "environment_connection", "environment_id": environment_id}]
            assert not first_done.is_set() and not handler_calls and not input_responses
            assert list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
            write_private("waiting", {"session_id": session_id, "environment_id": environment_id,
                                      "remote_url": settings["remote_url"], "creation_mode": "empty_later"})
            wait_for(lambda: (directory / "initial-connection-ready.json").exists(), 90, "executor connection")
            verify_environment(api.beta.agents.environments.retrieve(environment_id).to_dict(), environment_id, "connected")
            write_private("initial-connection-read", {"environment_id": environment_id})
            wait_for(first_done.is_set, 180, "first real function Turn")
            first_result = accepted_result(0)
            first_id = first_result["event"]["turn_id"]
            assert sessions.turns.retrieve(first_id, session_id=session_id).status == "completed"
            snapshot("first_completed", "idle")
            first_items = list(sessions.items.list(session_id, order="asc", limit=100))
            replay_result(first_result)
            assert list(sessions.items.list(session_id, order="asc", limit=100)) == first_items
            write_private("first-completed", {"turn_id": first_id, "call_id": first_result["event"]["call_id"]})
            wait_for(lambda: (directory / "resume-ready.json").exists(), 40, "retained native binding")
            run_turn(1)
            wait_for(lambda: all(signal.is_set() for signal in done.values()), 30, "terminal live streams")
            second_result = accepted_result(1)
            proof["accepted_results"] = [first_result, second_result]
            assert input_responses == [{"status": 204, "key": key} for key in input_keys]
            assert first_result["key"] != second_result["key"] and not ({first_result["key"], second_result["key"]} & set(input_keys))
            assert first_result["event"]["output"] == json.dumps({"festival": memory}, separators=(",", ":"))
            assert "error" not in first_result["event"] and second_result["event"]["error"] == "Tool handler failed."
            assert "output" not in second_result["event"]
            final = snapshot("final", "idle")
            assert final["required_actions"] == [] and proof["old_result_did_not_retarget"]
            # Native release precedes Turn completion; executor reconnection is asynchronous.
            connected = wait_for(lambda: value if (value := api.beta.agents.environments.retrieve(environment_id, timeout=5)).status == "connected" else None,
                                 30, "executor reconnection after completion")
            proof["final_environment"] = verify_environment(connected.to_dict(), environment_id, "connected")
            turns = list(sessions.turns.list(session_id, order="asc"))
            items = list(sessions.items.list(session_id, order="asc", limit=100))
            assert len(turns) == 2 and all(turn.status == "completed" for turn in turns)
            for index, phase in enumerate(("first", "resumed")):
                turn_id = turns[index].id
                events = helper_events[index]
                added = [event for event in events if event["type"] == "agent.session.turn.item.added"]
                calls = [event for event in added if event["item"]["type"] == "function_call"]
                results = [event for event in added if event["item"]["type"] == "function_call_output"]
                completed = [event for event in events if event["type"] == "agent.session.turn.completed"]
                assert len(calls) == len(results) == len(completed) == 1 and completed[0]["turn"]["id"] == turn_id
                assert calls[0]["item"]["call_id"] == results[0]["item"]["call_id"] == proof["accepted_results"][index]["event"]["call_id"]
                assert events.index(calls[0]) < events.index(results[0]) < events.index(completed[0]) < len(events) - 1
                assert events[-1]["type"] == "agent.session.idle" and results[0].get("output_index") is None
                assert not any(event["type"] == "agent.session.turn.item.done" and event["item"]["type"] == "function_call_output" for event in events)
                output = {field: results[0]["item"][field] for field in ("output", "error") if field in results[0]["item"]}
                expected = {field: proof["accepted_results"][index]["event"][field] for field in ("output", "error") if field in proof["accepted_results"][index]["event"]}
                assert output == expected
                commands = [item for item in items if item.turn_id == turn_id and item.type == "command_execution"
                            and "remote-stdout:" + phase in (item.output or "") and "remote-stderr:" + phase in (item.output or "")]
                assert len(commands) == 1 and commands[0].exit_code == 7 and commands[0].cwd == settings["workspace_directory"]
                argv = shlex.split(commands[0].command)
                assert Path(argv[0]).name == "bash" and argv[1:] == ["-lc", "./placement.sh " + phase]
                answer = "\n".join(part.get("text", "") for item in items if item.turn_id == turn_id and item.type == "message"
                                   and item.role == "assistant" for part in item.to_dict()["content"])
                assert memory in answer and settings["instruction"] in answer and "WRONG_LOCAL_INSTRUCTIONS" not in answer
                if index == 1:
                    assert "remote-file-content" in answer and "Tool handler failed." in answer
            proof["turns"], proof["items"] = [turn.to_dict() for turn in turns], [item.to_dict() for item in items]
            proof["calls"] = [entry["event"]["call_id"] for entry in proof["accepted_results"]]
            assert len(set(proof["calls"])) == 2 and len(handler_calls) == 2
            assert raw.get("/agents/sessions/" + session_id + "/turns", params={"order": "asc"}).json()["data"] == proof["turns"]
            assert raw.get("/agents/sessions/" + session_id + "/items", params={"order": "asc", "limit": 100}).json()["data"] == proof["items"]
            for submission in proof["accepted_results"]:
                replay_result(submission)
            changed = {**first_result["event"], "output": "changed"}
            assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": [changed]}, headers={"Idempotency-Key": first_result["key"]}).status_code == 409
            assert raw.post("/agents/sessions/" + session_id + "/events", json={"events": [first_result["event"]]}, headers={"Authorization": "Bearer " + foreign}).status_code == 404
            time.sleep(1)
            assert list(sessions.turns.list(session_id, order="asc")) == turns and list(sessions.items.list(session_id, order="asc", limit=100)) == items
            for values in observations.values():
                ids = [value["event_id"] for value in values]
                assert len(ids) == len(set(ids))
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.created"] == [turn.id for turn in turns]
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.completed"] == [turn.id for turn in turns]
            raw_ids = [value["event_id"] for value in observations["raw"]]
            start = next(index for index, value in enumerate(observations["sdk"]) if value["event_id"] in raw_ids)
            common = observations["sdk"][start:]
            assert common == observations["raw"][raw_ids.index(common[0]["event_id"]):], "SDK/raw common live suffix differs"
            with client() as recovered:
                resource = recovered.beta.agents.sessions
                assert resource.retrieve(session_id).to_dict() == final
                assert list(resource.turns.list(session_id, order="asc")) == turns
                assert list(resource.items.list(session_id, order="asc", limit=100)) == items
            proof["common_live_events"] = len(common)
            proof["query_recovery"] = proof["retries_preserved_turns_and_items"] = True
            proof["status"] = "public_functions_and_cold_continuation_verified"
            print("Built service: real self-hosted functions, SDK success/error mapping, cold history, remote commands and original-result retry passed.", flush=True)
    finally:
        with lock:
            proof["sdk_events"], proof["raw_events"] = list(observations["sdk"]), list(observations["raw"])
            proof["result_requests"] = list(submissions)
            proof["input_responses"] = list(input_responses)
        proof["helper_events"], proof["handler_calls"] = helper_events, handler_calls
        write_private("public-functions-proof", proof)


if __name__ == "__main__":
    main()
