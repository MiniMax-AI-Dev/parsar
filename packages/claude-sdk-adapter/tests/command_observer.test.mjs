import assert from "node:assert/strict";
import test from "node:test";
import { CommandObserver } from "../dist/command_observer.js";

const assistant = (id, command = "printf 'native output\\n'", input = {}) => ({ type: "assistant", session_id: "session", parent_tool_use_id: null,
  message: { content: [{ type: "tool_use", id, name: "Bash", input: { command, ...input } }] } });
const user = (id, content, is_error = false) => ({ type: "user", session_id: "session", parent_tool_use_id: null,
  message: { content: [{ type: "tool_result", tool_use_id: id, content, is_error }] } });
const consume = (observer, message, session = "session", hasInput = true) => [...observer.consume(message, session, hasInput)];
const success = "OBS_SUCCESS_STDERR\nOBS_SUCCESS_STDOUT";
const failure = "Exit code 7\nOBS_FAILURE_STDERR\nOBS_FAILURE_STDOUT";

test("root Bash calls preserve native identity, exact command and observed success/failure text", () => {
  const observer = new CommandObserver();
  const command = "  printf 'OBS_SUCCESS_STDOUT\\n'; printf 'OBS_SUCCESS_STDERR\\n' >&2  ";
  const before = consume(observer, assistant("success", command));
  assert.deepEqual(before, [{ type: "command_observation", session_id: "session", id: "success", stage: "before",
    observation: { kind: "command", status: "in_progress", command } }]);
  consume(observer, assistant("failed", "printf 'OBS_FAILURE_STDOUT\\n'; printf 'OBS_FAILURE_STDERR\\n' >&2; exit 7"));
  const completed = consume(observer, { ...user("success", success),
    tool_use_result: { stdout: success, stderr: "", interrupted: false, isImage: false, noOutputExpected: false } })[0];
  const failed = consume(observer, { ...user("failed", failure, true), tool_use_result: "Error: " + failure })[0];
  assert.equal(completed.observation.output, success);
  assert.equal(completed.observation.status, "completed");
  assert.equal(failed.observation.output, failure);
  assert.equal(failed.observation.status, "failed");
  for (const event of [completed, failed]) {
    assert.deepEqual(Object.keys(event.observation).sort(), ["command", "kind", "output", "status"]);
  }
  observer.assertComplete();
  assert.deepEqual(observer.close(), []);
});

test("replayed, synthetic, child, non-Bash and assistant-error messages cannot create observations", () => {
  const observer = new CommandObserver();
  const read = assistant("read");
  read.message.content[0].name = "Read";
  for (const message of [{ ...assistant("a"), parent_tool_use_id: "parent" }, { ...assistant("a"), isReplay: true },
    { ...assistant("a"), isSynthetic: true }, { ...assistant("a"), error: "server_error" }, read,
    { type: "tool_progress", tool_use_id: "a", elapsed_time_seconds: 3 },
    { type: "command_lifecycle", uuid: "a", status: "completed" },
    { type: "stream_event", parent_tool_use_id: null, event: { type: "content_block_start", content_block: { type: "tool_use", id: "a", name: "Bash" } } }]) {
    assert.deepEqual(consume(observer, message), []);
  }
  assert.deepEqual(consume(observer, user("old", "old output")), []);
  consume(observer, assistant("a"));
  for (const fields of [{ isReplay: true }, { isSynthetic: true }, { parent_tool_use_id: "parent" }]) {
    assert.deepEqual(consume(observer, { ...user("a", "not observed"), ...fields }), []);
  }
  assert.throws(() => observer.assertComplete(), /unconfirmed/);
  assert.equal(observer.close()[0].observation.status, "incomplete");
});

test("input, Session and call identities are required without deriving them from configuration", () => {
  for (const [session, hasInput] of [["", true], ["other", true], ["session", false]]) {
    const observer = new CommandObserver();
    assert.throws(() => consume(observer, assistant("a"), session, hasInput), /session identity/);
    assert.deepEqual(observer.close(), []);
  }
  for (const [id, command] of [["", "pwd"], [7, "pwd"], ["a", ""], ["a", "  "], ["a", 7]]) {
    assert.throws(() => consume(new CommandObserver(), assistant(id, command)), /command call/);
  }
  const observer = new CommandObserver();
  consume(observer, assistant("a"));
  assert.throws(() => consume(observer, { ...user("a", "output"), session_id: "other" }), /session identity/);
  assert.throws(() => consume(observer, { ...assistant("b"), session_id: "other" }, "other"), /session identity/);
  assert.equal(observer.close()[0].id, "a");
});

test("a native identity cannot be reused across Bash and another tool", () => {
  for (const firstBash of [false, true]) {
    const observer = new CommandObserver();
    const bash = assistant("shared");
    const read = assistant("shared");
    read.message.content[0].name = "Read";
    consume(observer, firstBash ? bash : read);
    assert.throws(() => consume(observer, firstBash ? read : bash), /conflicting/);
    assert.equal(observer.close().length, firstBash ? 1 : 0);
  }
});

test("identical calls and results are observed once while conflicting identities cannot replace them", () => {
  const observer = new CommandObserver();
  const call = assistant("a", "pwd", { timeout: 10 });
  consume(observer, call);
  assert.deepEqual(consume(observer, call), []);
  assert.throws(() => consume(observer, assistant("a", "pwd", { timeout: 20 })), /conflicting/);
  const result = user("a", "output");
  consume(observer, result);
  assert.deepEqual(consume(observer, result), []);
  assert.deepEqual(consume(observer, call), []);
  assert.throws(() => consume(observer, user("a", "different")), /conflicting/);
  assert.throws(() => consume(observer, user("a", "output", true)), /conflicting/);
  assert.deepEqual(observer.close(), []);
});

test("missing and ambiguous outputs remain absent while per-call results stay separate", () => {
  const observer = new CommandObserver();
  for (const id of ["empty", "missing", "rich", "a", "b", "single"]) consume(observer, assistant(id));
  assert.equal(consume(observer, user("empty", ""))[0].observation.output, "");
  assert.equal("output" in consume(observer, user("missing", undefined))[0].observation, false);
  assert.equal("output" in consume(observer, user("rich", [{ type: "text", text: "one" }, { type: "text", text: "two" }]))[0].observation, false);
  const batch = user("a", "first");
  batch.message.content.push(...user("b", "second", true).message.content);
  batch.tool_use_result = { stdout: "unassignable", stderr: "", interrupted: true };
  assert.deepEqual(consume(observer, batch).map(event => [event.id, event.observation.status, event.observation.output]),
    [["a", "completed", "first"], ["b", "failed", "second"]]);
  assert.equal(consume(observer, user("single", [{ type: "text", text: "exact" }]))[0].observation.output, "exact");
  observer.assertComplete();
});

test("trustworthy interruption retains output and shutdown leaves unfinished calls incomplete", () => {
  const observer = new CommandObserver();
  consume(observer, assistant("interrupted"));
  const interrupted = consume(observer, { ...user("interrupted", "partial", true),
    tool_use_result: { stdout: "partial", stderr: "", interrupted: true } })[0];
  assert.equal(interrupted.observation.status, "incomplete");
  assert.equal(interrupted.observation.output, "partial");
  consume(observer, assistant("unfinished"));
  const closed = observer.close();
  assert.deepEqual(closed.map(event => [event.id, event.observation.status, "output" in event.observation]), [["unfinished", "incomplete", false]]);
  assert.deepEqual(observer.close(), []);
  assert.throws(() => consume(observer, user("unfinished", "too late")), /closure/);
});

test("unexpected background and malformed interruption evidence cannot become completion", () => {
  for (const native of [{ stdout: "", stderr: "", interrupted: false, backgroundTaskId: "task" },
    { stdout: "", stderr: "", interrupted: false, timedOutAfterMs: 10 }, { interrupted: true },
    { stdout: "", stderr: "", interrupted: "true" }]) {
    const observer = new CommandObserver();
    consume(observer, assistant("a"));
    assert.throws(() => consume(observer, { ...user("a", "native text"), tool_use_result: native }));
    assert.equal(observer.close()[0].observation.status, "incomplete");
  }
});

test("a later invalid block does not hide the earlier observed call or invent an unmatched closure", () => {
  const observer = new CommandObserver();
  const message = assistant("a");
  message.message.content.push(...assistant("bad", "").message.content);
  const iterator = observer.consume(message, "session", true);
  assert.equal(iterator.next().value.id, "a");
  assert.throws(() => iterator.next(), /command call/);
  assert.deepEqual(observer.close().map(event => event.id), ["a"]);
});
