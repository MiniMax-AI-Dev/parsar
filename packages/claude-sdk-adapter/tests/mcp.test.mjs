import assert from "node:assert/strict";
import test from "node:test";
import { parseStart } from "../dist/adapter.js";
import { MCPProfile } from "../dist/mcp.js";
import { MCPObserver } from "../dist/mcp_observer.js";

const declaration = allowed_tools => ({ server_label: "fixture", server_url: "https://example.invalid/mcp", allowed_tools });
const native = name => `mcp__fixture__${name}`;
const statuses = [{ name: "fixture", status: "connected", tools: [{ name: "echo" }, { name: "fail" }] }];
const assistant = (id, name = native("echo"), input = { value: 7 }) => ({ type: "assistant", session_id: "session", parent_tool_use_id: null,
  message: { content: [{ type: "tool_use", id, name, input }] } });
const user = (id, content, is_error = false) => ({ type: "user", session_id: "session", parent_tool_use_id: null,
  message: { content: [{ type: "tool_result", tool_use_id: id, content, is_error }] } });

test("native selection composes unrestricted, selected and empty servers with host functions", () => {
  for (const selection of [null, ["echo"], []]) {
    const profile = new MCPProfile([declaration(selection)], ["mcp__functions__lookup"]);
    const expected = selection === null ? ["echo", "fail"] : selection;
    profile.verify([...expected.map(native), "mcp__functions__lookup"], [...statuses, { name: "functions", status: "connected" }], "session");
    assert.deepEqual([...profile.identities.keys()], expected.map(native));
    assert.equal(profile.allowed.includes("mcp__functions__lookup"), true);
    assert.deepEqual(profile.denied, selection?.length === 0 ? [native("*")] : []);
    assert.throws(() => profile.verify([...expected.map(native), "Bash"], statuses, "session"), /inventory/);
    assert.throws(() => profile.verify(expected.map(native), [...statuses, { name: "ambient", status: "connected" }], "session"), /undeclared/);
  }
});

test("invalid remote declarations and wildcard injection fail at the bridge boundary", () => {
  const start = { type: "start", prompt: "hello", model: "model", system_prompt: "", cwd: "/tmp" };
  for (const value of [null, {}, [declaration(["*"])], [{ ...declaration(null), server_label: "functions" }],
    [{ ...declaration(null), server_url: "https://user:secret@example.invalid/mcp" }],
    [{ ...declaration(null), server_url: "https://example.invalid/mcp?" }],
    [{ ...declaration(null), required: true }], [declaration(null), declaration([])]]) {
    assert.throws(() => parseStart(JSON.stringify({ ...start, mcp_http_servers: value })));
  }
  assert.deepEqual(parseStart(JSON.stringify({ ...start, mcp_http_servers: [declaration(null), { ...declaration([]), server_label: "empty" }] })).mcp_http_servers,
    [declaration(null), { ...declaration([]), server_label: "empty" }]);
});

test("native tool spelling preserves original identity and rejects ambiguous aliases", () => {
  for (const tools of [null, ["echo.v1"]]) {
    const profile = new MCPProfile([declaration(tools)], []);
    profile.verify([native("echo_v1")], [{ ...statuses[0], tools: [{ name: "echo.v1" }] }], "session");
    assert.deepEqual(profile.identities.get(native("echo_v1")), { server: "fixture", name: "echo.v1" });
    if (tools) assert.deepEqual(profile.allowed, [native("echo_v1")]);
    assert.throws(() => profile.verify([native("echo_v1")], [{ ...statuses[0], tools: [{ name: "echo.v1" }, { name: "echo_v1" }] }], "session"), /ambiguous/);
  }
});

test("native pre-tool admission waits for verified inventory and denies unsafe or cancelled setup", async () => {
  const input = { hook_event_name: "PreToolUse", session_id: "session", tool_name: native("echo"), tool_use_id: "call", tool_input: {} };
  const signal = new AbortController().signal;
  const p = new MCPProfile([declaration(["echo"])], []);
  assert.deepEqual(p.servers.fixture.headers, { Authorization: "" });
  let released = false;
  const pending = p.beforeTool(input, "call", { signal }).then(value => { released = true; return value; });
  await Promise.resolve();
  assert.equal(released, false);
  p.verify([native("echo")], statuses, "session");
  assert.deepEqual(await pending, {});
  for (const invalid of [{ ...input, session_id: "other" }, { ...input, agent_id: "child" }, { ...input, tool_name: native("fail") }]) {
    assert.equal((await p.beforeTool(invalid, "call", { signal })).hookSpecificOutput.permissionDecision, "deny");
  }
  p.close();
  assert.equal((await p.beforeTool(input, "call", { signal })).hookSpecificOutput.permissionDecision, "deny");
  const rejected = new MCPProfile([declaration(["echo_v1"])], []);
  const waiting = rejected.beforeTool({ ...input, tool_name: native("echo_v1") }, "call", { signal });
  // Native status may retain only the first of two normalized aliases.
  assert.throws(() => rejected.verify([native("echo_v1")], [{ ...statuses[0], tools: [{ name: "echo.v1" }] }], "session"), /inventory/);
  rejected.close();
  assert.equal((await waiting).hookSpecificOutput.permissionDecision, "deny");
  const controller = new AbortController();
  const stopped = new MCPProfile([declaration(null)], []).beforeTool(input, "call", { signal: controller.signal });
  controller.abort();
  assert.equal((await stopped).hookSpecificOutput.permissionDecision, "deny");
});

function observer() { return new MCPObserver(new Map([[native("echo"), { server: "fixture", name: "echo" }]])); }

test("parallel calls preserve actual native JSON, identity, nulls and error representation", () => {
  const o = observer();
  assert.equal(o.consume(assistant("a"), "session")[0].observation.status, "in_progress");
  o.consume(assistant("b"), "session");
  assert.deepEqual(o.consume(assistant("a"), "session"), []);
  const failed = { ...user("b", "MCP error", true), tool_use_result: "Error: MCP error" };
  assert.equal(o.consume(failed, "session")[0].observation.error, "Error: MCP error");
  const originalNative = { content: '{"value":7}', structuredContent: { value: 7 } };
  const completed = o.consume({ ...user("a", originalNative.content), tool_use_result: originalNative }, "session")[0];
  assert.deepEqual(completed.observation.output, originalNative);
  assert.equal(completed.observation.error, null);
  o.assertComplete();
  assert.deepEqual(o.close(), []);
  assert.throws(() => o.consume(failed, "session"), /repeated/);
});

test("root ownership and exact correlation exclude replay, functions and nested work", () => {
  const o = observer();
  for (const message of [{ ...assistant("a"), parent_tool_use_id: "parent" }, { ...assistant("a"), isReplay: true },
    { ...assistant("a"), isSynthetic: true }, assistant("host", "mcp__functions__lookup")]) {
    assert.deepEqual(o.consume(message, "session"), []);
  }
  assert.throws(() => o.consume(assistant("a"), "other"), /session/);
  o.consume(assistant("a"), "session");
  assert.throws(() => o.consume(assistant("a", native("echo"), { value: 8 }), "session"), /conflicting/);
  assert.deepEqual(o.consume(user("unrelated", "text"), "session"), []);
  assert.throws(() => o.assertComplete(), /unconfirmed/);
  const events = o.close();
  assert.equal(events.length, 1);
  assert.deepEqual(events[0].observation, { kind: "mcp", name: "echo", server: "fixture", arguments: { value: 7 }, status: "incomplete", output: null, error: null });
});

test("batched results use per-call content, never duplicate a whole-message native result", () => {
  const o = observer();
  o.consume(assistant("a"), "session");
  o.consume(assistant("b"), "session");
  const first = user("a", []), second = user("b", "failed", true);
  first.message.content.push(...second.message.content);
  first.tool_use_result = { unassignable: true };
  const events = o.consume(first, "session");
  assert.deepEqual(events.map(e => [e.id, e.observation.output, e.observation.error]), [["a", [], null], ["b", null, "failed"]]);
});
