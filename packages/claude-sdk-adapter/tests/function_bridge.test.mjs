import assert from "node:assert/strict";
import test from "node:test";
import { FunctionBridge } from "../dist/function_bridge.js";

const call = id => ({ id, name: "lookup", arguments: { ids: ["same"] } });
const result = (id, success = true) => ({ type: "function_result", call_id: id, delivery_id: `delivery-${id}`, success,
  content: [{ type: "input_text", text: `first-${id}` }, { type: "input_text", text: `second-${id}` }] });
const native = (value, content) => ({ type: "user", parent_tool_use_id: null, session_id: "native",
  message: { role: "user", content: [{ type: "tool_result", tool_use_id: value.call_id,
    is_error: !value.success, content: content ?? (value.success ? value.content.map(part => ({ type: "text", text: part.text })) : value.content.map(part => part.text).join("\n")) }] } });

test("identical calls accept reversed results but require matching live native responses", { timeout: 5000 }, async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const first = bridge.invoke(call("a"), new AbortController().signal);
  const second = bridge.invoke(call("b"), new AbortController().signal);
  for (const value of [result("b", false), result("a")]) bridge.submit(JSON.stringify(value));
  const [a, b] = await Promise.all([first, second]);
  assert.deepEqual(a.content.map(part => part.text), ["first-a", "second-a"]);
  assert.equal(b.isError, true);
  assert.deepEqual(events.map(event => event.type), ["function_call", "function_call"]);
  assert.throws(() => bridge.assertComplete(), /unconfirmed/);
  await bridge.consume({ ...native(result("a")), isReplay: true }, "native");
  await bridge.consume(native(result("a")), "other-session");
  await bridge.consume({ ...native(result("a")), parent_tool_use_id: "child" }, "native");
  assert.equal(events.length, 2);
  await assert.rejects(bridge.consume(native(result("a"), [{ type: "text", text: "wrong" }]), "native"), /differs/);
  await bridge.consume(native(result("b", false)), "native");
  await bridge.consume(native(result("a")), "native");
  assert.deepEqual(events.slice(2), [
    { type: "function_applied", call_id: "b", delivery_id: "delivery-b" },
    { type: "function_applied", call_id: "a", delivery_id: "delivery-a" },
  ]);
  bridge.assertComplete();
  await assert.rejects(bridge.invoke(call("a"), new AbortController().signal), /Repeated/);
});

test("invalid results preserve pending calls, cancellation and close never acknowledge application", { timeout: 5000 }, async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const abort = new AbortController();
  const cancelled = bridge.invoke(call("a"), abort.signal);
  const stopped = bridge.invoke(call("b"), new AbortController().signal);
  const cancelCheck = assert.rejects(cancelled, /aborted/);
  const stopCheck = assert.rejects(stopped, /ended/);
  for (const bad of [null, { ...result("a"), success: null }, { ...result("a"), content: null },
    { ...result("a"), content: [{ type: "input_image", image_url: "https://example.invalid/image" }] }, result("unknown")]) {
    assert.throws(() => bridge.submit(JSON.stringify(bad)));
  }
  abort.abort();
  bridge.close();
  await Promise.all([cancelCheck, stopCheck]);
  await bridge.consume(native(result("a")), "native");
  assert.deepEqual(events.map(event => event.type), ["function_call", "function_call"]);
});

test("an aborted submitted result never becomes a native application receipt", { timeout: 5000 }, async () => {
  const events = [];
  const bridge = new FunctionBridge(async event => { events.push(event); });
  const abort = new AbortController();
  const waiting = bridge.invoke(call("a"), abort.signal);
  bridge.submit(JSON.stringify(result("a", false)));
  await waiting;
  abort.abort();
  await bridge.consume(native(result("a", false)), "native");
  assert.deepEqual(events.map(event => event.type), ["function_call"]);
});
