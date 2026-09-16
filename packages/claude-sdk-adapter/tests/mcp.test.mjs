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
    profile.verify([...expected.map(native), "mcp__functions__lookup"], [...statuses, { name: "functions", status: "connected" }]);
    assert.deepEqual([...profile.identities.keys()], expected.map(native));
    assert.equal(profile.allowed.includes("mcp__functions__lookup"), true);
    assert.deepEqual(profile.denied, selection?.length === 0 ? [native("*")] : []);
    assert.throws(() => profile.verify([...expected.map(native), "Bash"], statuses), /inventory/);
    assert.throws(() => profile.verify(expected.map(native), [...statuses, { name: "ambient", status: "connected" }]), /undeclared/);
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
