"""HTTP MCP resource semantics; execution requires the separate real-provider run."""

from copy import deepcopy

from openai import BadRequestError, NotFoundError


def verify_mcp_configuration(client, other, expect_error):
    agents, sessions = client.beta.agents, client.beta.agents.sessions
    transport = {"type": "http", "server_url": "https://mcp.example.invalid/mcp"}
    tool = {"type": "mcp", "server_label": "tickets", "transport": transport,
            "connection_origin": "service"}
    recovered, saved = [], []
    for allow in ({}, {"allowed_tools": None}, {"allowed_tools": []},
                  {"allowed_tools": ["lookup_ticket"]}):
        declared = {**tool, **allow}
        response = agents.with_raw_response.create(model="requested-model", tools=[declared])
        resource, body = response.parse(), response.http_response.json()
        canonical = {**declared, "allowed_tools": allow.get("allowed_tools"),
                     "credential_id": None, "request_metadata": {}, "required": False,
                     "transport": {**transport, "headers": {}}}
        assert body["tools"] == [canonical]
        spec = {"agent_id": resource.id, "environment": {"type": "none"}}
        headers = {"Idempotency-Key": "mcp-snapshot-" + resource.id}
        response = sessions.with_raw_response.create(**spec, extra_headers=headers)
        session, body = response.parse(), response.http_response.json()
        assert body["agent"]["tools"] == [{**canonical, "transport": transport}]
        expect_error(NotFoundError, lambda: other.beta.agents.sessions.create(**spec))
        assert sessions.create(agent={"model": "requested-model", "tools": [declared]},
                               environment={"type": "none"}).agent.tools == session.agent.tools
        override = sessions.create(**spec, agent={"tools": []})
        assert override.agent.tools == []
        changed = agents.update(resource.id, tools=[])
        assert changed.tools == [] and sessions.retrieve(session.id) == session
        assert sessions.create(**spec, extra_headers=headers) == session
        recovered.extend([session, override])
        saved.append(changed)

    before = {item.id for item in sessions.list()}
    saved_before = {item.id for item in agents.list()}
    invalid = [{**tool, "connection_origin": value} for value in (None, "environment")]
    invalid += [{**tool, "required": True}, {**tool, "required": None},
                {**tool, "request_metadata": {"x": "y"}},
                {**tool, "allowed_tools": [None]}]
    for changes in ({"headers": {"Authorization": "synthetic-private"}},
                    {"authorization": "synthetic-private"},
                    {"server_url": "https://mcp.example.invalid/mcp?token=synthetic-private"}):
        invalid.append({**tool, "transport": {**transport, **changes}})
    omitted = deepcopy(tool)
    omitted.pop("connection_origin")
    invalid.append(omitted)
    for declaration in invalid:
        for operation in (
            lambda: agents.create(model="requested-model", tools=[declaration]),
            lambda: sessions.create(agent={"model": "requested-model", "tools": [declaration]},
                                    environment={"type": "none"}),
        ):
            error = expect_error(BadRequestError, operation)
            assert "synthetic-private" not in str(error.body)
    assert {item.id for item in sessions.list()} == before
    assert {item.id for item in agents.list()} == saved_before
    print("HTTP MCP: pinned saved/Session projections, null/empty allowlists, immutable snapshots and rejected writes passed; no native execution claimed.")
    return recovered, saved
