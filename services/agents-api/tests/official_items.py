"""Validate canonical recovery Items through the pinned SDK and the real HTTP service."""
from openai import AuthenticationError, BadRequestError, NotFoundError


def verify_items(a, b, invalid, session, empty_session, turns, expect_error):
    items = a.beta.agents.sessions.items
    recovered = list(items.list(session, limit=3, order="asc"))
    assert len(recovered) == 32 and len({item.id for item in recovered}) == 32
    assert list(items.list(session, limit=5)) == list(reversed(recovered))
    assert list(items.list(session, after=recovered[-1].id, order="asc")) == []
    assert list(items.list(empty_session)) == []
    expect_error(NotFoundError, lambda: b.beta.agents.sessions.items.list(session))
    expect_error(NotFoundError, lambda: items.list(empty_session, after=recovered[0].id))
    expect_error(AuthenticationError, lambda: invalid.beta.agents.sessions.items.list(session))
    expect_error(BadRequestError, lambda: items.list(session, limit=101))
    assert "PRIVATE" not in repr(recovered) and "SECRET" not in repr(recovered)
    for i, turn in enumerate(turns):
        group = [item for item in recovered if item.turn_id == turn]
        assert len(group) == 8
        assert group[0].type == "message" and group[0].role == "user" and group[0].status == "completed"
        answer = next(item for item in group if item.type == "message" and item.role == "assistant")
        assert answer.output_text == ("final answer" if i < 2 else "partial answer")
        assert answer.status == ["completed", "completed", "incomplete", "in_progress"][i]
        command = next(item for item in group if item.type == "command_execution")
        assert command.status == "failed" and command.exit_code == 7 and command.output == "command failed"
        assert command.duration_ms == 8 and command.cwd == "/workspace"
        mcp = next(item for item in group if item.type == "mcp_call")
        assert mcp.status == "completed" and mcp.server_label == "reference" and mcp.name == "lookup"
        assert mcp.arguments["n"] == 9007199254740993 and mcp.output["structuredContent"]["n"] == 9007199254740993
        assert mcp.error is None
        call = next(item for item in group if item.type == "function_call" and item.name == "lookup")
        output = next(item for item in group if item.type == "function_call_output")
        assert output.call_id == call.call_id and output.id != call.id and group.index(call) < group.index(output)
        assert output.output[0].type == "input_text" and output.output[0].text == ""
        patch = next(item for item in group if item.type == "function_call" and item.name == "apply_patch")
        assert patch.arguments["changes"][0]["diff"] == "+example"
        search = next(item for item in group if item.type == "web_search_call")
        assert search.status == "completed" and search.action.query == "reference"
    print("Official Items client: native result shapes, partial/completed states, ordering, pagination and isolation passed.")
    return recovered
