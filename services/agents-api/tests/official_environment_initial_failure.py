"""Verify public pre-Turn failure reads/SSE after private initial Session setup."""

import importlib.metadata
import json
from pathlib import Path
import sys
import threading

sys.dont_write_bytecode = True

import httpx2
from openai import OpenAI


def main():
    settings = json.load(sys.stdin)
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source["vcs_info"]["commit_id"] == pin["commit"]
    base, session_id = settings["base"], settings["session_id"]
    headers = {"Authorization": "Bearer " + settings["token"], "OpenAI-Beta": "agents=v1"}
    observations, failures = {}, []

    def client():
        return OpenAI(api_key=settings["token"], base_url=base + "/v1", max_retries=0,
                      _strict_response_validation=True, http_client=httpx2.Client(trust_env=False, timeout=20))

    def check(value, status):
        assert value["id"] == session_id and value["object"] == "agent.session" and value["status"] == status
        assert value["environment"]["id"] == settings["environment_id"] and value["usage"] is None
        action = {"type": "environment_connection", "environment_id": settings["environment_id"]}
        assert value["required_actions"] == ([action] if status == "requires_action" else [])
        if status == "failed":
            assert value["error"] == "The initial input timed out waiting for the environment connection."
        else:
            assert value["error"] is None
        assert "private-input-marker" not in json.dumps(value)

    def observe(name):
        try:
            if name == "sdk":
                with client() as api, api.beta.agents.sessions.events.stream(session_id) as stream:
                    (Path(settings["directory"]) / "sdk-ready").touch()
                    event = next(iter(stream)).to_dict()
            else:
                with httpx2.Client(trust_env=False, timeout=20) as raw:
                    with raw.stream("GET", base + "/v1/agents/sessions/" + session_id + "/events", headers=headers) as response:
                        assert response.status_code == 200 and response.headers["content-type"] == "text/event-stream"
                        (Path(settings["directory"]) / "raw-ready").touch()
                        event = next(json.loads(line[6:]) for line in response.iter_lines() if line.startswith("data: "))
            assert set(event) == {"type", "event_id", "session"} and event["type"] == "agent.session.failed"
            check(event["session"], "failed")
            observations[name] = event
        except BaseException as error:
            failures.append(error)

    with client() as api, httpx2.Client(base_url=base + "/v1", trust_env=False, timeout=20, headers=headers) as raw:
        check(api.beta.agents.sessions.retrieve(session_id).to_dict(), "requires_action")
        check(raw.get("/agents/sessions/" + session_id).json(), "requires_action")
        workers = [threading.Thread(target=observe, args=(name,), daemon=True) for name in ("sdk", "raw")]
        for worker in workers:
            worker.start()
        for worker in workers:
            worker.join(timeout=30)
            assert not worker.is_alive(), "failure observer timed out"
        if failures:
            raise AssertionError("failure observer failed") from failures[0]
        assert observations["sdk"] == observations["raw"]
        current = api.beta.agents.sessions.retrieve(session_id).to_dict()
        check(current, "failed")
        response = raw.get("/agents/sessions/" + session_id)
        assert response.status_code == 200 and response.headers["cache-control"] == "no-store" and response.json() == current
        assert observations["sdk"]["session"] == current
        listed = list(api.beta.agents.sessions.list(agent_id=current["agent"]["id"]))
        assert len(listed) == 1 and listed[0].to_dict() == current
        assert list(api.beta.agents.sessions.turns.list(session_id)) == []
        assert list(api.beta.agents.sessions.items.list(session_id)) == []
        environment = api.beta.agents.environments.retrieve(settings["environment_id"])
        assert environment.status == "pending"
        for suffix in ("", "/events", "/turns", "/items"):
            assert raw.get("/agents/sessions/" + session_id + suffix, headers={"Authorization": "Bearer " + settings["foreign_token"]}).status_code == 404
    print(json.dumps({"status": "private_initial_failure_public_reads_and_live_events_verified", "session_id": session_id,
                      "event_id": observations["sdk"]["event_id"], "sdk_events": 1, "raw_events": 1, "turns": 0}))


if __name__ == "__main__":
    main()
