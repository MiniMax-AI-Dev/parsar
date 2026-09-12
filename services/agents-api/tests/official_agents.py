"""Reusable Agent resource checks against the fixed SDK and real HTTP service."""

import time
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, NotFoundError


def verify_agents(client, other, invalid, expect_error):
    agents = client.beta.agents
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    base = str(client.base_url).rstrip("/") + "/agents"
    saved = []
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        # These assertions cover known resource defaults; they do not assert a
        # model-derived reasoning default that the service cannot yet resolve.
        for values in ({}, {"name": None, "instructions": None, "metadata": None,
                            "multi_agent": None, "reasoning": None,
                            "service_tier": None, "text": None, "tools": None}):
            response = agents.with_raw_response.create(model=" caller-model ", **values)
            body, agent = response.http_response.json(), response.parse()
            assert set(body) == {"id", "object", "created_at", "updated_at", "metadata", "model", "name",
                                 "instructions", "multi_agent", "reasoning", "service_tier", "text", "tools"}
            assert body["object"] == "agent" and body["model"] == " caller-model "
            assert body["name"] is None and body["instructions"] is None and body["metadata"] == {}
            assert body["multi_agent"] == {"enabled": False, "max_concurrent_subagents": None}
            assert body["text"] == {"format": {"type": "text"}, "verbosity": "medium"}
            assert body["tools"] == []
            assert agent.created_at == agent.updated_at and abs(agent.created_at - time.time()) < 10
            assert agents.retrieve(agent.id) == agent
            saved.append(agent)
        schema = {"type": "object", "properties": {"number": {"const": 9007199254740993}}}
        tools = [{"type": "function", "name": "lookup", "description": "", "parameters": schema,
                  "defer_loading": True}, {"type": "tool_search"}, {"type": "programmatic_tool_calling"}]
        metadata = {"empty": "", "🧪" * 64: "值" * 512}
        request = {"model": "arbitrary-provider-model", "name": " ", "instructions": " preserve whitespace ",
                   "metadata": metadata, "multi_agent": {"enabled": True},
                   "reasoning": {"effort": "max", "summary": "detailed"}, "service_tier": "fast",
                   "text": {"format": {"type": "json_schema", "schema": schema}, "verbosity": "high"},
                   "tools": tools}
        response = agents.with_raw_response.create(**request)
        body, agent = response.http_response.json(), response.parse()
        for field in ("model", "name", "instructions", "metadata", "reasoning", "service_tier", "text"):
            assert body[field] == request[field], field
        assert body["multi_agent"] == {"enabled": True, "max_concurrent_subagents": 6}
        assert body["tools"] == [*tools[:2], {"type": "programmatic_tool_calling", "enabled": True}]
        assert raw.get(base + "/" + agent.id, headers=headers).json() == body
        assert agents.retrieve(agent.id) == agent
        saved.append(agent)
        for fields in [
            {"model": "", "name": "🧪" * 128, "instructions": "", "metadata": {}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": 4294967295}},
            {"multi_agent": {"enabled": False, "max_concurrent_subagents": 4}},
            {"text": {"format": None, "verbosity": None}},
            {"tools": [{"type": "function", "name": "", "description": "", "parameters": {}}]},
            {"tools": [{"type": "programmatic_tool_calling", "enabled": False}]},
        ]:
            item = agents.create(**{"model": "resource-model", **fields})
            assert agents.retrieve(item.id) == item
            saved.append(item)
        assert saved[-1].tools[0].enabled is False
        assert saved[-2].tools[0].defer_loading is False
        assert saved[-4].multi_agent.max_concurrent_subagents is None
        for tier in ("auto", "default", "flex", "priority", "fast"):
            item = agents.create(model="resource-model", service_tier=tier)
            assert item.service_tier == tier
            saved.append(item)
        for effort in ("none", "minimal", "low", "medium", "high", "xhigh", "max"):
            item = agents.create(model="resource-model", reasoning={"effort": effort})
            assert item.reasoning.effort == effort
            saved.append(item)

        invalid_fields = [
            {"model": None}, {"model": 4}, {"name": "x" * 129}, {"instructions": False},
            {"metadata": {"bad": None}}, {"metadata": {"x" * 65: "v"}},
            {"metadata": {str(i): "v" for i in range(17)}}, {"metadata": {"x": "v" * 513}},
            {"tenant_id": str(uuid.uuid4())}, {"reasoning": {"effort": "automatic"}},
            {"reasoning": {"summary": "never"}}, {"reasoning": {"unknown": 1}},
            {"service_tier": "premium"}, {"multi_agent": {}}, {"multi_agent": {"enabled": None}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": None}},
            {"multi_agent": {"enabled": False, "max_concurrent_subagents": 0}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": 1.5}},
            {"multi_agent": {"enabled": True, "max_concurrent_subagents": 4294967296}},
            {"text": {"format": {}}}, {"text": {"format": {"type": None}}},
            {"text": {"format": {"type": "text", "schema": {}}}},
            {"text": {"format": {"type": "json_schema", "schema": None}}},
            {"text": {"format": {"type": "json_schema", "schema": []}}},
            {"text": {"verbosity": "automatic"}}, {"text": {"unknown": True}},
            {"tools": [None]}, {"tools": [{"type": "function", "name": None}]},
            {"tools": [{"type": "function", "name": "x", "description": "", "parameters": None}]},
            {"tools": [{"type": "function", "name": "x", "description": "", "parameters": {}, "defer_loading": None}]},
            {"tools": [{"type": "programmatic_tool_calling", "enabled": None}]},
            {"tools": [{"type": "tool_search", "unknown": 1}]},
        ]
        for fields in invalid_fields:
            response = raw.post(base, headers=headers, json={"model": "resource-model", **fields})
            assert response.status_code == 400, (fields, response.status_code)
            assert response.json()["error"]["type"] == "invalid_request_error"
            expect_error(BadRequestError, lambda: agents.create(model="resource-model", extra_body=fields))
        for content in ("{}", "null", "[]", '{"model":"x"} {}'):
            assert raw.post(base, headers=headers, content=content).status_code == 400
        assert raw.post(base, headers=headers, content='{"model":"' + "x" * (1024 * 1024) + '"}').status_code == 413
        assert raw.post(base, headers=headers, params={"tenant_id": "other"}, json={"model": "x"}).status_code == 400
        assert raw.get(base + "/" + saved[0].id, headers=headers, params={"tenant_id": "other"}).status_code == 400
        for resource_id in (str(uuid.uuid4()), "unrecognized-agent", str(uuid.UUID(int=0))):
            expect_error(NotFoundError, lambda: agents.retrieve(resource_id))
        expect_error(NotFoundError, lambda: other.beta.agents.retrieve(saved[0].id))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.retrieve(saved[0].id))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.create(model="x"))
        expect_error(BadRequestError, lambda: agents.create(model="x", extra_headers={"OpenAI-Beta": ""}))
        assert raw.post(base, json={"model": "x"}).status_code == 401
        # Unsupported families are explicit gaps, not schema-conformance evidence.
        for tool in ({"type": "web_search"}, {"type": "mcp", "server_label": "x", "transport": {"type": "http", "server_url": "https://example.invalid"}}):
            expect_error(BadRequestError, lambda: agents.create(model="x", tools=[tool]))
    print("Reusable Agents: fixed SDK/raw HTTP create/retrieve, explicit configuration, known defaults, isolation and validation passed; model defaults/MCP/web_search/retry semantics remain gaps.")
    return saved
