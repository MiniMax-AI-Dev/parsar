"""Public Agent update main flows against fixed SDK and real PostgreSQL."""

import concurrent.futures
import sys
import uuid

import httpx2
from openai import OpenAI


def main():
    base, token, foreign, restarted = sys.argv[1:]
    with httpx2.Client(trust_env=False, timeout=10) as http:
        client = OpenAI(api_key=token, base_url=base + "/v1", http_client=http,
                        max_retries=0, _strict_response_validation=True)
        agents, sessions = client.beta.agents, client.beta.agents.sessions
        headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
        tool = {"type": "function", "name": "lookup", "description": "Read a value.",
                "parameters": {"type": "object", "properties": {"value": {"const": 9007199254740993}}}}
        original = agents.create(model="original-model", name="Original", instructions="Keep original.",
                                 metadata={"old": "value"}, tools=[tool])
        endpoint = base + "/v1/agents/" + original.id
        spec = {"agent_id": original.id, "environment": {"type": "none"}}
        retry = {"Idempotency-Key": "before-agent-update"}
        old = sessions.create(**spec, extra_headers=retry)
        updated = agents.update(original.id, instructions="Use updated instructions.",
                                name="更新", model=" updated-model ", metadata={"new": "value"})
        assert updated.id == original.id and updated.created_at == original.created_at
        assert updated.updated_at >= original.updated_at
        assert updated.model == " updated-model " and updated.name == "更新"
        assert updated.instructions == "Use updated instructions." and updated.metadata == {"new": "value"}
        assert updated.tools == original.tools and updated.text == original.text
        assert updated.reasoning == original.reasoning and updated.multi_agent == original.multi_agent
        assert agents.retrieve(original.id) == updated
        assert next(a for a in agents.list() if a.id == original.id) == updated
        assert sessions.retrieve(old.id) == old and sessions.create(**spec, extra_headers=retry) == old
        fresh = sessions.create(**spec)
        assert fresh.id != old.id and fresh.agent.instructions == updated.instructions
        assert fresh.agent.model == updated.model
        assert [t.to_dict() for t in fresh.agent.tools] == [t.to_dict() for t in updated.tools]
        assert fresh.metadata == {} and old.agent.instructions == original.instructions
        # A request without fields is a local no-op, including its update timestamp.
        assert agents.update(original.id) == updated
        for body in (None, [], {"model": None}, {"model": 3}, {"name": "x" * 129},
                     {"metadata": {"bad": None}}, {"text": {"unexpected": True}},
                     {"metadata": {"replace": "no"}, "instructions": False},
                     {"updated_at": 1}, {"tools": [{"type": "unknown"}]}):
            response = http.post(endpoint, headers=headers, content="null" if body is None else None,
                                 json=body if body is not None else None)
            assert response.status_code == 400, (body, response.status_code, response.text)
            assert agents.retrieve(original.id) == updated
        for target in (original.id, str(uuid.uuid4()), "invalid", str(uuid.UUID(int=0))):
            response = http.post(base + "/v1/agents/" + target,
                                 headers=headers | {"Authorization": "Bearer " + foreign}, json={"name": "foreign"})
            assert response.status_code == 404
        assert http.post(endpoint + "?unknown=1", headers=headers, json={}).status_code == 400
        assert http.post(endpoint, headers={"Authorization": "Bearer " + token}, json={}).status_code == 400
        assert http.post(endpoint, headers={"OpenAI-Beta": "agents=v1"}, json={}).status_code == 401
        assert agents.retrieve(original.id) == updated

        configured = agents.update(original.id, multi_agent={"enabled": True, "max_concurrent_subagents": 3},
                                   reasoning={"effort": "high", "summary": "auto"}, service_tier="priority",
                                   text={"verbosity": "high", "format": {"type": "json_schema", "schema": {"const": 9007199254740993}}})
        assert configured.multi_agent.max_concurrent_subagents == 3
        assert configured.text.format.to_dict()["schema"]["const"] == 9007199254740993
        assert http.get(endpoint, headers=headers).json()["tools"][0]["parameters"]["properties"]["value"]["const"] == 9007199254740993
        # Local nested replacement/default policy remains explicitly subject to hosted comparison.
        replaced = agents.update(original.id, text={"format": {"type": "text"}}, reasoning={"summary": "detailed"},
                                 metadata={"only": "this"}, tools=[])
        assert replaced.text.verbosity == "medium" and replaced.reasoning.effort is None
        assert replaced.reasoning.summary == "detailed" and replaced.metadata == {"only": "this"}
        assert replaced.tools == [] and replaced.multi_agent == configured.multi_agent
        cleared = agents.update(original.id, name=None, instructions=None, metadata=None, multi_agent=None,
                                reasoning=None, service_tier=None, text=None, tools=None)
        assert cleared.name is None and cleared.instructions is None and cleared.metadata == {}
        assert not cleared.multi_agent.enabled and cleared.multi_agent.max_concurrent_subagents is None
        assert cleared.reasoning.effort is None and cleared.reasoning.summary is None
        assert cleared.service_tier == "auto" and cleared.text.verbosity == "medium" and cleared.tools == []
        assert cleared.model == updated.model
        assert agents.update(original.id, metadata={}).metadata == {}

        # Different fields written concurrently must all survive; each metadata map is a replacement.
        changes = [{"name": "concurrent-name"}, {"instructions": "concurrent-instructions"},
                   {"model": "concurrent-model"}, {"metadata": {"concurrent": "metadata"}},
                   {"text": {"verbosity": "high"}}]
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(changes)) as pool:
            results = list(pool.map(lambda patch: http.post(endpoint, headers=headers, json=patch), changes))
        assert all(r.status_code == 200 for r in results)
        current = agents.retrieve(original.id)
        assert current.name == "concurrent-name" and current.instructions == "concurrent-instructions"
        assert current.model == "concurrent-model" and current.metadata == {"concurrent": "metadata"}
        assert current.text.verbosity == "high" and current.tools == []
        recovered = OpenAI(api_key=token, base_url=restarted + "/v1", http_client=http,
                           max_retries=0, _strict_response_validation=True)
        assert recovered.beta.agents.retrieve(original.id) == current
        assert recovered.beta.agents.sessions.retrieve(old.id) == old
        assert recovered.beta.agents.sessions.create(**spec, extra_headers=retry) == old
    print("Agent update: SDK/raw HTTP, omission/null/replacement, isolation, concurrent fields, old/new Sessions and recreated handler/store passed.")


if __name__ == "__main__":
    main()
