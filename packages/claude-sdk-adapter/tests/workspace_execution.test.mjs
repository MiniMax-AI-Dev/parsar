import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

const fixture = `
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from "node:fs";
import { registerHooks } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
const sdk = 'export function getSessionInfo(...args){return globalThis.historyFixture(...args);} export function query(options){return globalThis.queryFixture(options);} export function startup(){throw new Error("Unexpected preparation");}';
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return {url:"data:text/javascript,"+encodeURIComponent(sdk),shortCircuit:true};
  return next(specifier,context);
}});
const { execute } = await import(${JSON.stringify(new URL("../dist/adapter.js", import.meta.url).href)});
const root = realpathSync(mkdtempSync(join(tmpdir(), "parsar-workspace-execute-")));
const dirs = Object.fromEntries(["workspace", "home", "state", "scratch", "deps"].map(name => {
  const path = join(root, name); mkdirSync(path); return [name, path];
}));
const mode = process.argv[1];
const workspace = { home: dirs.home, state: dirs.state, scratch: dirs.scratch,
  protected_dirs: [], dependency_path: dirs.deps, env_names: ["ANTHROPIC_API_KEY"] };
const request = { type: "start", prompt: "fixture", model: "fixture", system_prompt: "", cwd: dirs.workspace,
  workspace, ...(mode.startsWith("resume") ? { resume: "native" } : {}) };
process.env.HOME = dirs.home;
process.env.CLAUDE_CONFIG_DIR = dirs.state;
process.env.ANTHROPIC_API_KEY = "fixture-secret";
process.env.UNSELECTED_CANARY = "must-not-inherit";
delete process.env.CLAUDE_CODE_PROJECT_DIR_NAME;
const events = [], calls = [];
const abort = new AbortController();
globalThis.historyFixture = async (id, options) => {
  calls.push("history");
  assert.equal(id, "native");
  assert.deepEqual(options, { dir: dirs.workspace });
  assert.equal(process.env.HOME, dirs.home);
  assert.equal(process.env.CLAUDE_CONFIG_DIR, dirs.state);
  return mode === "resume-missing" ? undefined : { sessionId: "native" };
};
globalThis.queryFixture = ({prompt, options}) => {
  calls.push("query");
  assert.equal(options.env.UNSELECTED_CANARY, undefined);
  assert.equal(options.env.ANTHROPIC_API_KEY, "fixture-secret");
  assert.equal(options.env.HOME, dirs.home);
  assert.deepEqual(options.tools, ["Bash", "Read", "Edit"]);
  assert.equal(options.sandbox.failIfUnavailable, true);
  assert.equal(options.resume, request.resume);
  const child = options.spawnClaudeCodeProcess({ command: process.execPath, env: options.env, signal: abort.signal,
    args: ["-e", "const assert=require('node:assert/strict');assert.equal(process.env.UNSELECTED_CANARY,undefined);assert.equal(process.env.ANTHROPIC_API_KEY,'fixture-secret');process.stdin.resume();process.stdin.on('end',()=>process.exit(0));"] });
  return { close() { child.stdin.end(); }, async *[Symbol.asyncIterator]() {
    const first = (await prompt[Symbol.asyncIterator]().next()).value;
    yield { type: "system", subtype: "init", session_id: "native", mcp_servers: mode === "extra-mcp" ? [{name:"other",status:"connected"}] : [],
      tools: mode === "extra-tool" ? ["Bash", "Read", "Edit", "Agent"] : ["Bash", "Read", "Edit"] };
    yield { type: "result", uuid: "result", session_id: "native", user_message_uuids: [first.uuid],
      subtype: "success", is_error: false, result: "fixture", usage: {input_tokens:1,output_tokens:1}, modelUsage:{} };
  } };
};
try {
  if (mode === "resume-mismatch") {
    process.env.CLAUDE_CONFIG_DIR = dirs.home;
    await assert.rejects(execute(request, async e => events.push(e), abort), /invalid_request/);
    assert.deepEqual(calls, []);
  } else {
    await execute(request, async e => events.push(e), abort);
    if (mode === "resume-missing") {
      assert.deepEqual(calls, ["history"]);
      assert.deepEqual(events, [{type:"error",code:"history_unavailable"}]);
    } else {
      assert.deepEqual(calls, mode === "resume-existing" ? ["history", "query"] : ["query"]);
      if (mode.startsWith("extra")) {
        assert.deepEqual(events.at(-1), {type:"error",code:"execution_failed"});
        assert.equal(events.some(e => e.type === "input_ready"), false);
      } else assert.equal(events.at(-1).type, "result");
    }
  }
} finally { rmSync(root, {recursive:true,force:true}); }
`;

for (const mode of ["fresh", "resume-existing", "resume-missing", "resume-mismatch", "extra-tool", "extra-mcp"]) {
  test(`workspace execution boundary: ${mode}`, { timeout: 15000 }, () => {
    const child = spawnSync(process.execPath, ["--input-type=module", "-e", fixture, mode], { encoding: "utf8", timeout: 10000 });
    assert.equal(child.status, 0, child.stderr || child.error?.message);
  });
}
