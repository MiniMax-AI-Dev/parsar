import assert from "node:assert/strict";
import test from "node:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { createFunctionServer } from "../dist/functions.js";

const schema = {
  type: "object", additionalProperties: false,
  $defs: { row: { type: "object", properties: { id: { type: "string" } }, required: ["id"], additionalProperties: false } },
  properties: {
    rows: { type: "array", items: { $ref: "#/$defs/row" }, minItems: 1 },
    choice: { anyOf: [{ type: "string", enum: ["ready"] }, { type: "null" }] },
    mode: { oneOf: [{ const: "read" }, { const: "write" }] },
  },
  required: ["rows", "choice", "mode"],
};
const definition = () => ({ name: "lookup", description: "Synthetic lookup", inputSchema: structuredClone(schema) });
const call = (id, args = { rows: [{ id: "42" }], choice: null, mode: "read" }) => ({
  name: "lookup", arguments: args, _meta: { "claudecode/toolUseId": id },
});
async function connect(t, tools, invoke) {
  const server = createFunctionServer(tools, invoke);
  const client = new Client({ name: "verification", version: "1.0.0" });
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  t.after(async () => { await client.close(); await server.instance.close(); });
  await Promise.all([server.instance.connect(serverTransport), client.connect(clientTransport)]);
  return client;
}

test("official MCP client receives a stable lossless schema snapshot", { timeout: 5000 }, async t => {
  const tool = definition();
  tool._meta = { retained: "metadata" };
  const client = await connect(t, [tool], async () => ({ content: [] }));
  tool.inputSchema.properties.mode = { type: "number" };
  const listed = await client.listTools();
  assert.deepEqual(listed.tools[0], {
    ...definition(), _meta: { retained: "metadata", "anthropic/alwaysLoad": true },
  });
});

test("identical concurrent calls retain native identity and ordered success/error content", { timeout: 5000 }, async t => {
  const pending = new Map();
  let bothStarted;
  const started = new Promise(resolve => { bothStarted = resolve; });
  const client = await connect(t, [definition()], (request, signal) => new Promise(resolve => {
    assert.deepEqual(request.arguments, call("").arguments);
    assert.equal(signal.aborted, false);
    pending.set(request.id, resolve);
    if (pending.size === 2) bothStarted();
  }));
  const first = client.callTool(call("native-first"));
  const second = client.callTool(call("native-second"));
  await started;
  const error = { isError: true, content: [{ type: "text", text: "failed" }, { type: "text", text: "reason" }] };
  pending.get("native-second")(error);
  assert.deepEqual(await second, error);
  const success = { isError: false, content: [{ type: "text", text: "first" }, { type: "text", text: "second" }] };
  pending.get("native-first")(success);
  assert.deepEqual(await first, success);
});

test("missing identities and unknown functions never invoke the host", { timeout: 5000 }, async t => {
  let invoked = 0;
  const client = await connect(t, [definition()], async () => { invoked++; return { content: [] }; });
  for (const request of [{ name: "lookup" }, call(""), call(42), { ...call("native"), name: "unknown" }]) {
    await assert.rejects(client.callTool(request), /identity is missing|Unknown function/);
  }
  assert.equal(invoked, 0);
});

test("MCP cancellation reaches the individual host callback", { timeout: 5000 }, async t => {
  let onStarted, onAborted;
  const started = new Promise(resolve => { onStarted = resolve; });
  const aborted = new Promise(resolve => { onAborted = resolve; });
  const client = await connect(t, [definition()], async (request, signal) => {
    onStarted(request.id);
    await new Promise(resolve => {
      signal.addEventListener("abort", () => { onAborted(request.id); resolve(); }, { once: true });
    });
    return { content: [] };
  });
  const controller = new AbortController();
  const result = client.callTool(call("cancel-native"), undefined, { signal: controller.signal });
  const rejection = assert.rejects(result);
  assert.equal(await started, "cancel-native");
  controller.abort();
  await rejection;
  assert.equal(await aborted, "cancel-native");
});

test("ambiguous function registration is rejected", () => {
  const invoke = async () => ({ content: [] });
  assert.throws(() => createFunctionServer([definition(), definition()], invoke), /unique/);
  assert.throws(() => createFunctionServer([{ ...definition(), name: "" }], invoke), /nonempty/);
});
