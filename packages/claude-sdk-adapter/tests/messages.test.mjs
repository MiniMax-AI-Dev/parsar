import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import test from "node:test";
import { MessageObserver } from "../dist/messages.js";
import { parseStart } from "../dist/adapter.js";

const stream = (event, parent = null) => ({
  type: "stream_event", uuid: randomUUID(), session_id: "session", parent_tool_use_id: parent, event,
});
const start = id => stream({ type: "message_start", message: { id } });
const block = (index, text = "") => stream({ type: "content_block_start", index, content_block: { type: "text", text } });
const delta = (index, text) => stream({ type: "content_block_delta", index, delta: { type: "text_delta", text } });
const blockStop = index => stream({ type: "content_block_stop", index });
const stop = () => stream({ type: "message_stop" });
const snapshot = (id, text, parent = null) => ({
  type: "assistant", uuid: randomUUID(), session_id: "session", parent_tool_use_id: parent,
  message: { id, content: [{ type: "text", text }] },
});

test("multiple native messages retain identity, incremental fragments and authoritative block snapshots", () => {
  const observer = new MessageObserver();
  const output = [];
  const consume = message => output.push(...observer.consume(message));
  consume(start("native-1"));
  consume(block(0));
  consume(delta(0, "go "));
  consume(delta(0, "go "));
  consume(snapshot("native-1", "GO go "));
  consume(blockStop(0));
  assert.equal(output.filter(event => event.message?.status === "completed").length, 0);
  consume(block(1));
  consume(delta(1, "尾"));
  consume(snapshot("native-1", "尾"));
  consume(blockStop(1));
  consume(stop());
  consume(start("native-2"));
  consume(block(0, "next"));
  consume(snapshot("native-2", "next"));
  consume(blockStop(0));
  consume(stop());
  assert.deepEqual(output, [
    { type: "output_message", message: { id: "native-1", status: "in_progress" } },
    { type: "delta", item_id: "native-1", delta: "go " },
    { type: "delta", item_id: "native-1", delta: "go " },
    { type: "delta", item_id: "native-1", delta: "尾" },
    { type: "output_message", message: { id: "native-1", status: "completed", text: "GO go 尾" } },
    { type: "output_message", message: { id: "native-2", status: "in_progress" } },
    { type: "delta", item_id: "native-2", delta: "next" },
    { type: "output_message", message: { id: "native-2", status: "completed", text: "next" } },
  ]);
});

test("thinking, tools and nested assistant output do not create text messages", () => {
  const observer = new MessageObserver();
  for (const type of ["thinking", "tool_use"]) {
    assert.deepEqual(observer.consume(start(type)), []);
    assert.deepEqual(observer.consume(stream({ type: "content_block_start", index: 0, content_block: { type } })), []);
    assert.deepEqual(observer.consume({ ...snapshot(type, ""), message: { id: type, content: [{ type }] } }), []);
    assert.deepEqual(observer.consume(blockStop(0)), []);
    assert.deepEqual(observer.consume(stop()), []);
  }
  assert.deepEqual(observer.consume(stream({ type: "message_start", message: { id: "nested" } }, "tool-parent")), []);
  assert.deepEqual(observer.consume(snapshot("nested", "hidden", "tool-parent")), []);
  observer.consume(start("empty"));
  assert.deepEqual(observer.consume(block(0)), [
    { type: "output_message", message: { id: "empty", status: "in_progress" } },
  ]);
  observer.consume(blockStop(0));
  assert.deepEqual(observer.consume(stop()), [
    { type: "output_message", message: { id: "empty", status: "completed", text: "" } },
  ]);
});

test("interrupted or failed messages never become completed from a result or error snapshot", () => {
  const observer = new MessageObserver();
  const output = [start("partial"), block(0), delta(0, "unfinished"),
    { type: "result", subtype: "error_during_execution" },
  ].flatMap(message => observer.consume(message));
  assert.deepEqual(output, [
    { type: "output_message", message: { id: "partial", status: "in_progress" } },
    { type: "delta", item_id: "partial", delta: "unfinished" },
  ]);
  assert.deepEqual(observer.consume({ ...snapshot("partial", "provider error"), error: "server_error" }), []);
  assert.deepEqual(observer.consume(stop()), []);
});

test("unmatched text cannot acquire an invented identity", () => {
  const observer = new MessageObserver();
  assert.throws(() => observer.consume(delta(0, "missing")), /start is missing/);
  observer.consume(start("native"));
  observer.consume(block(0));
  assert.throws(() => observer.consume(snapshot("other", "wrong")), /unmatched/);
});

test("observation opt-in is an optional boolean", () => {
  const request = { type: "start", prompt: "hello", model: "model", system_prompt: "", cwd: "/tmp" };
  assert.equal(parseStart(JSON.stringify(request)).observe_messages, undefined);
  assert.equal(parseStart(JSON.stringify({ ...request, observe_messages: true })).observe_messages, true);
  assert.throws(() => parseStart(JSON.stringify({ ...request, observe_messages: "true" })), /invalid_request/);
});
