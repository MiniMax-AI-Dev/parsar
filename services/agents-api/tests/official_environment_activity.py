"""Accept public reads/SSE for privately provisioned, caller-connected native work."""

import importlib.metadata
import json
import os
from pathlib import Path
import sys
import threading
import time

sys.dont_write_bytecode = True

import httpx2
from openai import NotFoundError, OpenAI


def main():
    settings = json.load(sys.stdin)
    base, token, foreign = (settings[key] for key in ("base", "token", "foreign_token"))
    session_id, agent_id = settings["session_id"], settings["agent_id"]
    directory = Path(settings["evidence"])
    os.umask(0o077)
    root = Path(__file__).resolve().parents[3]
    pin = json.loads((root / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"]
    assert source.get("vcs_info", {}).get("commit_id") == pin["commit"]
    expected_environment = {
        "id": settings["environment_id"], "type": "self_hosted", "capability_directories": [],
        "remote_url": settings["remote_url"], "workspace_directory": settings["workspace_directory"],
    }
    action = {"type": "environment_connection", "environment_id": expected_environment["id"]}
    proof = {"scope": "private Session provisioning/input reservation; public read/SSE acceptance; public create/input remain gated",
             "sdk_commit": pin["commit"], "sdk_version": distribution.version, "snapshots": {}}
    observations = {"sdk": [], "raw": []}
    ready = {name: threading.Event() for name in observations}
    waiting = {name: threading.Event() for name in observations}
    done = {name: threading.Event() for name in observations}
    failures = []
    lock = threading.Lock()

    def write_private(name, value):
        text = json.dumps(value, indent=2)
        assert token not in text and foreign not in text, "caller credential appeared in public evidence"
        temporary = directory / (name + ".tmp")
        temporary.write_text(text)
        temporary.chmod(0o600)
        temporary.replace(directory / (name + ".json"))

    def client(key):
        return OpenAI(api_key=key, base_url=base + "/v1", max_retries=0,
                      _strict_response_validation=True,
                      http_client=httpx2.Client(trust_env=False, timeout=30))

    def check_session(value, status):
        assert value["id"] == session_id and value["agent"]["id"] == agent_id
        assert value["object"] == "agent.session" and value["environment"] == expected_environment
        assert value["status"] == status and value["error"] is None
        assert value["required_actions"] == ([action] if status == "requires_action" else [])
        assert value["vault_ids"] == [] and isinstance(value["metadata"], dict)
        assert isinstance(value["last_active_at"], int) and isinstance(value["created_at"], int)

    def await_observers(signals, timeout, label):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            with lock:
                if failures:
                    raise AssertionError("public event observer failed") from failures[0]
            if all(signal.is_set() for signal in signals.values()):
                return
            time.sleep(0.025)
        raise AssertionError(label + " timed out")

    def observe(name):
        completed = set()

        def accept(value):
            with lock:
                observations[name].append(value)
            kind = value["type"]
            assert kind != "error" and kind not in ("agent.session.failed", "agent.session.turn.failed", "agent.session.turn.cancelled")
            if kind == "agent.session.requires_action":
                check_session(value["session"], "requires_action")
                waiting[name].set()
            if kind == "agent.session.turn.completed":
                completed.add(value["turn"]["id"])
            return kind == "agent.session.idle" and len(completed) == 2

        try:
            if name == "sdk":
                with client(token) as api:
                    with api.beta.agents.sessions.events.stream(session_id, timeout=450) as stream:
                        ready[name].set()
                        for event in stream:
                            if accept(event.to_dict()):
                                return
            else:
                with httpx2.Client(trust_env=False, timeout=450) as raw:
                    with raw.stream("GET", base + "/v1/agents/sessions/" + session_id + "/events",
                                    headers={"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}) as response:
                        assert response.status_code == 200
                        assert response.headers["content-type"] == "text/event-stream"
                        ready[name].set()
                        for line in response.iter_lines():
                            if line.startswith("data: ") and accept(json.loads(line[6:])):
                                return
            raise AssertionError("live stream ended before both native Turns completed")
        except BaseException as error:
            with lock:
                failures.append(error)
        finally:
            done[name].set()

    try:
        with client(token) as api, client(foreign) as stranger, httpx2.Client(
            base_url=base + "/v1", trust_env=False, timeout=15,
            headers={"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1", "Host": "untrusted.example"},
        ) as raw:
            sessions = api.beta.agents.sessions

            def snapshot(name, status):
                retrieved = sessions.retrieve(session_id)
                check_session(retrieved.to_dict(), status)
                listed = list(sessions.list(agent_id=agent_id, limit=1))
                assert listed == [retrieved]
                response = raw.get("/agents/sessions/" + session_id)
                assert response.status_code == 200
                value = response.json()
                check_session(value, status)
                page = raw.get("/agents/sessions", params={"agent_id": agent_id, "limit": 1})
                assert page.status_code == 200 and page.json() == {"data": [value], "has_more": False}
                proof["snapshots"][name] = value
                return value

            initial = snapshot("initial", "idle")
            assert initial["created_at"] == initial["last_active_at"] and initial["usage"] is None
            assert list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
            assert list(stranger.beta.agents.sessions.list(agent_id=agent_id)) == []
            for operation in (
                lambda: stranger.beta.agents.sessions.retrieve(session_id),
                lambda: stranger.beta.agents.sessions.events.stream(session_id),
                lambda: stranger.beta.agents.sessions.turns.list(session_id),
                lambda: stranger.beta.agents.sessions.items.list(session_id),
            ):
                try:
                    operation()
                except NotFoundError:
                    pass
                else:
                    raise AssertionError("foreign tenant accessed the target Session")
            for suffix in ("", "/events", "/turns", "/items"):
                assert raw.get("/agents/sessions/" + session_id + suffix,
                               headers={"Authorization": "Bearer " + foreign}).status_code == 404
            create = raw.post("/agents/sessions", json={"agent": {"model": "MiniMax-M3"},
                              "environment": {"type": "self_hosted", "workspace_directory": settings["workspace_directory"]}})
            assert create.status_code == 400
            submit = raw.post("/agents/sessions/" + session_id + "/events", json={"events": [{
                "type": "agent.session.input.message", "input": [{"role": "user", "content": [
                    {"type": "input_text", "text": "Public admission must remain disabled."}]}]}]})
            assert submit.status_code == 400
            proof["tenant_isolation"] = True
            proof["public_create_and_input_rejected"] = True

            for name in observations:
                threading.Thread(target=observe, args=(name,), daemon=True).start()
            await_observers(ready, 25, "SDK and raw live subscriptions")
            write_private("ready", {})
            await_observers(waiting, 25, "pre-Turn connection action")
            pending = snapshot("waiting", "requires_action")
            assert pending["usage"] is None
            assert list(sessions.turns.list(session_id)) == [] and list(sessions.items.list(session_id)) == []
            assert raw.get("/agents/sessions/" + session_id + "/turns").json()["data"] == []
            assert raw.get("/agents/sessions/" + session_id + "/items").json()["data"] == []
            with lock:
                for values in observations.values():
                    assert len(values) == 1 and values[0]["type"] == "agent.session.requires_action"
                    assert values[0]["session"] == pending
                    assert set(values[0]) == {"type", "event_id", "session"}
            write_private("waiting", {"remote_url": pending["environment"]["remote_url"],
                                      "environment_id": pending["environment"]["id"]})
            await_observers(done, 420, "first and resumed native Turns")

            final = snapshot("final", "idle")
            turns = list(sessions.turns.list(session_id, order="asc"))
            assert len(turns) == 2 and all(turn.status == "completed" for turn in turns)
            turn_ids = [turn.id for turn in turns]
            items = list(sessions.items.list(session_id, limit=100, order="asc"))
            proof["turns"] = [turn.to_dict() for turn in turns]
            proof["items"] = [item.to_dict() for item in items]
            for turn, phase in zip(turns, ("first", "resumed")):
                assert sessions.turns.retrieve(turn.id, session_id=session_id) == turn
                group = [item.to_dict() for item in items if item.turn_id == turn.id]
                commands = [item for item in group if item["type"] == "command_execution"
                            and "remote-stdout:" + phase in item.get("output", "")
                            and "remote-stderr:" + phase in item.get("output", "")]
                assert len(commands) == 1
                command = commands[0]
                assert command["exit_code"] == 7 and command["cwd"] == settings["workspace_directory"]
                assert "remote-stdout:" + phase in command["output"] and "remote-stderr:" + phase in command["output"]
                text = "\n".join(part.get("text", "") for item in group
                                 if item["type"] == "message" and item.get("role") == "assistant"
                                 for part in item["content"])
                assert settings["memory"] in text and settings["instruction"] in text
                assert "WRONG_LOCAL_INSTRUCTIONS" not in text
            assert raw.get("/agents/sessions/" + session_id + "/turns", params={"order": "asc"}).json()["data"] == proof["turns"]
            assert raw.get("/agents/sessions/" + session_id + "/items", params={"order": "asc", "limit": 100}).json()["data"] == proof["items"]

            for values in observations.values():
                ids = [event["event_id"] for event in values]
                assert len(ids) == len(set(ids))
                types = [event["type"] for event in values]
                request = types.index("agent.session.requires_action")
                connected = types.index("agent.session.environment.connected")
                cleared = types.index("agent.session.idle")
                first_turn = types.index("agent.session.turn.created")
                assert request < connected < cleared < first_turn
                assert values[request]["session"] == pending
                clear = values[cleared]
                assert set(clear) == {"type", "event_id", "session"}
                check_session(clear["session"], "idle")
                assert clear["session"]["usage"] is None
                assert clear["session"]["last_active_at"] == pending["last_active_at"]
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.created"] == turn_ids
                assert [value["turn"]["id"] for value in values if value["type"] == "agent.session.turn.completed"] == turn_ids
                check_session(values[-1]["session"], "idle")
                for event in values:
                    if event["type"].startswith("agent.session.environment."):
                        assert set(event) == {"type", "event_id", "session_id", "environment"}
                        assert event["environment"]["id"] == expected_environment["id"]
                        assert event["environment"]["error"] is None
            assert [event["event_id"] for event in observations["sdk"]] == [event["event_id"] for event in observations["raw"]]
            with client(token) as recovered:
                restored = recovered.beta.agents.sessions
                assert restored.retrieve(session_id).to_dict() == final
                assert list(restored.turns.list(session_id, order="asc")) == turns
                assert list(restored.items.list(session_id, limit=100, order="asc")) == items
            proof["client_reconnect_recovery"] = True
            proof["pre_turn_action_cleared_before_turn"] = True
            proof["status"] = "public_read_sse_and_remote_first_resumed_verified"
            print("Pinned SDK/raw HTTP/live SSE and remote first/resumed recovery passed.", flush=True)
    finally:
        with lock:
            proof["sdk_events"] = list(observations["sdk"])
            proof["raw_events"] = list(observations["raw"])
        write_private("public-environment-proof", proof)


if __name__ == "__main__":
    main()
