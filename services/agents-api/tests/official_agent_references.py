"""Saved-Agent Session references through the pinned SDK and real database."""

import uuid

import httpx2
from openai import BadRequestError, ConflictError, NotFoundError


def verify_agent_references(client, other, expect_error):
    agents, sessions = client.beta.agents, client.beta.agents.sessions
    tool = {"type": "function", "name": "lookup", "description": "Return a value.",
            "parameters": {"type": "object", "properties": {"value": {"const": 9007199254740993}}}}
    resource = agents.create(model=" requested-model ", name="Reusable configuration",
                             instructions="Saved instructions.", metadata={"business": "not-session-metadata"},
                             text={"verbosity": "high"}, tools=[tool])
    spec = {"agent_id": resource.id, "environment": {"type": "none"}}
    headers = {"Idempotency-Key": "saved-agent-reference"}
    first = sessions.create(**spec, extra_headers=headers)
    expected = resource.to_dict(mode="json")
    for field in ("object", "metadata", "created_at", "updated_at"):
        expected.pop(field)
    assert first.agent.to_dict(mode="json") == expected
    assert first.metadata == {} and first.id != resource.id
    assert first.agent.id == resource.id
    assert sessions.create(**spec, extra_headers=headers) == first
    sibling = sessions.create(**spec)
    assert sibling.id != first.id and sibling.agent == first.agent
    recovered = [first, sibling]

    # Whole-field replacement: an object containing only format resets verbosity
    # to its known default instead of merging the saved high value.
    for override in ({}, {"model": " override-model "}, {"instructions": ""},
                     {"instructions": None}, {"tools": None}, {"tools": []},
                     {"tools": [dict(tool, name="replacement")]},
                     {"text": {"format": {"type": "text"}}}, {"text": None}):
        item = sessions.create(**spec, agent=override)
        assert item.agent.id == resource.id and item.agent.name == resource.name
        assert item.agent.model == override.get("model", resource.model)
        assert item.agent.instructions == override.get("instructions", resource.instructions)
        if "text" in override:
            assert item.agent.text.verbosity == "medium"
        else:
            assert item.agent.text == resource.text
        if "tools" in override:
            assert [t.name for t in item.agent.tools] == [t["name"] for t in (override["tools"] or [])]
        else:
            assert [t.to_dict(mode="json") for t in item.agent.tools] == [t.to_dict(mode="json") for t in resource.tools]
        recovered.append(item)
    assert agents.retrieve(resource.id) == resource
    assert sessions.retrieve(first.id) == first
    expect_error(ConflictError, lambda: sessions.create(**spec, agent={"instructions": "Changed"}, extra_headers=headers))
    same_config = agents.create(model=resource.model, name=resource.name, instructions=resource.instructions,
                                text={"verbosity": "high"}, tools=[tool])
    expect_error(ConflictError, lambda: sessions.create(agent_id=same_config.id, environment={"type": "none"}, extra_headers=headers))
    expect_error(NotFoundError, lambda: other.beta.agents.sessions.create(**spec))
    for missing in (str(uuid.uuid4()), "not-an-agent", str(uuid.UUID(int=0))):
        expect_error(NotFoundError, lambda: sessions.create(agent_id=missing, environment={"type": "none"}))

    # Configuration storage is broader than execution. Never silently drop an
    # unsupported saved option, but admit a supported whole-field replacement.
    for field, value, replacement in (
        ("reasoning", {"effort": "high"}, None),
        ("reasoning", {"summary": "auto"}, {}),
        ("service_tier", "fast", "auto"),
        ("multi_agent", {"enabled": True}, {"enabled": False}),
        ("text", {"format": {"type": "json_schema", "schema": {"type": "object"}}}, {"verbosity": "medium"}),
        ("tools", [dict(tool, defer_loading=True)], None),
        ("tools", [{"type": "tool_search"}], []),
        ("tools", [{"type": "programmatic_tool_calling", "enabled": False}], []),
    ):
        unsupported = agents.create(model="model", **{field: value})
        reference = {"agent_id": unsupported.id, "environment": {"type": "none"}}
        expect_error(BadRequestError, lambda: sessions.create(**reference))
        recovered.append(sessions.create(**reference, agent={field: replacement}))
        expect_error(BadRequestError, lambda: sessions.create(agent={"model": "model", field: value}, environment={"type": "none"}))
        assert agents.retrieve(unsupported.id) == unsupported

    base = str(client.base_url).rstrip("/") + "/agents/sessions"
    auth = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        body = raw.post(base, headers=auth, json=spec)
        assert body.status_code == 200 and body.json()["agent"] == expected
        recovered.append(sessions.retrieve(body.json()["id"]))
        before = {item.id for item in sessions.list()}
        for override in (None, [], {"model": None}, {"model": 1}, {"model": ""},
                         {"name": "not-a-session-override"}, {"reasoning": {"unknown": True}},
                         {"tools": [{"type": "function", "name": "x", "description": "", "parameters": {}, "unknown": True}]},
                         {"text": {"format": {"type": "text", "unknown": True}}}):
            response = raw.post(base, headers=auth, json={**spec, "agent": override})
            assert response.status_code == 400, (override, response.status_code)
        assert {item.id for item in sessions.list()} == before, "rejected configuration created a Session"
    # Metadata updates must not change the creation identity or copied config.
    updated = sessions.update(first.id, metadata={"current": "value"})
    assert sessions.create(**spec, extra_headers=headers) == updated
    recovered[0] = updated
    print("Saved-Agent Session references: SDK/raw HTTP inheritance, whole-field replacement, isolation, immutable snapshots and retries passed; unsupported execution settings rejected.")
    return recovered, (spec, headers, updated)
