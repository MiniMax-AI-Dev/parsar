import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parseRequest, preparedPrompt } from "../dist/request.js";

// Exercise the packaged entrypoint and real child ownership with a controlled SDK.
// Native dependency enforcement and model effects need separate native acceptance.
const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
const mode = process.argv[1];
const sdk = 'export function startup(args){return globalThis.startupFixture(args);} export function query(){throw new Error("Direct query used for preparation");} export async function getSessionInfo(){process.send({kind:"history"});return process.argv[1] === "missing-history" ? undefined : {sessionId:"native"};}';
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return {url:"data:text/javascript,"+encodeURIComponent(sdk),shortCircuit:true};
  return next(specifier,context);
}});
globalThis.startupFixture = async ({options, initializeTimeoutMs}) => {
  assert.equal(initializeTimeoutMs,15000);
  assert.equal(options.model,"fixed-model");
  assert.equal(options.env.UNSELECTED_CANARY,undefined);
  assert.equal(options.env.HOME,process.env.HOME);
  assert.deepEqual(options.tools,["Bash","Read","Edit"]);
  assert.equal(options.sandbox.failIfUnavailable,true);
  assert.equal(options.hooks.PreToolUse.length,1);
  const child = options.spawnClaudeCodeProcess({command:process.execPath,env:options.env,signal:options.abortController.signal,
    args:["-e","process.stdin.resume();process.stdin.on('end',()=>process.exit(0));"]});
  process.send({kind:"spawn",pid:child.pid});
  const closed = new Promise(resolve=>child.once("close",()=>{process.send({kind:"native_close"});resolve();}));
  const close = () => child.stdin.end();
  await new Promise(resolve=>setTimeout(resolve,mode === "slow-startup" ? 200 : 30));
  if(mode === "startup-error") {close();await closed;throw new Error("Private native diagnostics");}
  let consumed=false;
  return {
    close(){if(!consumed)close();},
    query(prompt){
      assert.equal(consumed,false);consumed=true;
      if(mode === "native-exit")setTimeout(()=>child.kill("SIGTERM"),100);
      return {
        close,
        async readFile(path,{maxBytes,encoding}) {
          assert.equal(encoding,"base64");
          assert.equal(path,options.cwd+"/binary");
          process.send({kind:"read",path,maxBytes});
          await new Promise(resolve=>setTimeout(resolve,80));
          return {absPath:path,encoding,contents:Buffer.from([0,255,128,1]).toString("base64")};
        },
        async initializationResult(){return {hooks_applied:mode !== "missing-hooks"};},
        async *[Symbol.asyncIterator](){
          const command={type:"assistant",session_id:"native",parent_tool_use_id:null,
            message:{content:[{type:"tool_use",id:"command",name:"Bash",input:{command:"printf 'prepared'"}}]}};
          if(mode === "command-before-input")yield command;
          const iterator=prompt[Symbol.asyncIterator]();
          const first=await Promise.race([iterator.next(),closed.then(()=>({done:true}))]);
          if(first.done)return;
          process.send({kind:"input",text:first.value.message.content});
          yield {type:"system",subtype:"init",session_id:mode === "resume-mismatch"?"other":"native",mcp_servers:[],
            tools:mode === "bad-inventory"?["Bash","Read","Edit","Agent"]:["Bash","Read","Edit"]};
          const ids=[first.value.uuid];
          if(mode === "commands") {
            yield command;
            yield {type:"user",session_id:"native",parent_tool_use_id:null,
              message:{content:[{type:"tool_result",tool_use_id:"command",content:"prepared",is_error:false}]}};
          }
          if(mode === "steer") {
            const second=await Promise.race([iterator.next(),closed.then(()=>({done:true}))]);
            if(second.done)return;
            ids.push(second.value.uuid);
          }
          await new Promise(resolve=>setTimeout(resolve,80));
          if(options.abortController.signal.aborted)return;
          yield {type:"result",uuid:"result",session_id:"native",user_message_uuids:ids,subtype:"success",is_error:false,
            result:"answer",usage:{input_tokens:1,output_tokens:1},modelUsage:{}};
        }
      };
    }
  };
};
await import(${JSON.stringify(new URL("../dist/main.js", import.meta.url).href)});
process.disconnect();
`;

function placement() {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "parsar-prepare-")));
  const dirs = Object.fromEntries(["workspace", "home", "state", "scratch", "deps"].map(name => {
    const path = join(root, name); mkdirSync(path); return [name, path];
  }));
  return { root, request: { type: "prepare", model: "fixed-model", system_prompt: "", cwd: dirs.workspace,
    workspace: { home: dirs.home, state: dirs.state, scratch: dirs.scratch, dependency_path: dirs.deps, protected_dirs: [], env_names: [] } } };
}

test("prepare validates a workspace-only immutable configuration and prompt-only Start", () => {
  const { root, request } = placement();
  try {
    assert.deepEqual(parseRequest(JSON.stringify(request)), request);
    assert.deepEqual(parseRequest(JSON.stringify({ ...request, functions: [] })), { ...request, functions: [] });
    for (const fields of [{ prompt: "" }, { prompt: "input" }, { workspace: undefined },
      { mcp_http_servers: [] }, { env: {} }, { resume: "" }, { type: "prepared" }]) {
      assert.throws(() => parseRequest(JSON.stringify({ ...request, ...fields })), /invalid_request/);
    }
    assert.equal(preparedPrompt({ type: "start", prompt: "first" }), "first");
    for (const value of [{ type: "start", prompt: "" }, { type: "start", prompt: "first", model: "other" },
      { type: "start", prompt: "first", resume: "other" }, { type: "start", prompt: "first", workspace: request.workspace },
      { type: "steer", text: "first" }, null]) assert.throws(() => preparedPrompt(value), /invalid_request/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

async function launch(t, mode) {
  const { root, request } = placement();
  const env = { ...process.env, HOME: request.workspace.home, CLAUDE_CONFIG_DIR: request.workspace.state, UNSELECTED_CANARY: "must-not-inherit" };
  delete env.CLAUDE_CODE_PROJECT_DIR_NAME;
  const child = spawn(process.execPath, ["--input-type=module", "-e", fixture, mode], { env, stdio: ["pipe", "pipe", "pipe", "ipc"] });
  const events = [], observations = [];
  let stderr = "", buffer = "", notify = () => {};
  child.stdout.setEncoding("utf8"); child.stderr.setEncoding("utf8");
  child.stdout.on("data", value => {
    buffer += value;
    while (buffer.includes("\n")) {
      const end = buffer.indexOf("\n"); events.push(JSON.parse(buffer.slice(0, end))); buffer = buffer.slice(end + 1);
    }
    notify();
  });
  child.stderr.on("data", value => { stderr += value; });
  child.on("message", value => { observations.push(value); notify(); });
  const closed = new Promise(resolve => child.once("close", (code, signal) => resolve({ code, signal })));
  t.after(async () => { child.kill("SIGKILL"); await closed; rmSync(root, { recursive: true, force: true }); });
  const wait = async predicate => {
    if (predicate()) return;
    let timer;
    try {
      await new Promise((resolve, reject) => {
        notify = () => { if (predicate()) resolve(); };
        timer = setTimeout(() => reject(new Error("Bridge observation timeout: " + stderr)), 5000);
      });
    } finally { clearTimeout(timer); notify = () => {}; }
  };
  const send = value => child.stdin.write(JSON.stringify(value) + "\n");
  const finish = async code => {
    await wait(() => events.some(event => event.type === "result" || event.type === "error"));
    assert.deepEqual(await closed, { code: 0, signal: null }, stderr);
    assert.equal(events.filter(event => event.type === "error" || event.type === "result").length, 1);
    if (code) assert.deepEqual(events.at(-1), { type: "error", code });
    else assert.equal(events.at(-1).type, "result");
    assert.equal(observations.filter(value => value.kind === "native_close").length, observations.filter(value => value.kind === "spawn").length);
  };
  if (["missing-history", "resume", "resume-mismatch"].includes(mode)) request.resume = "native";
  return { child, request, events, observations, send, wait, finish };
}

for (const mode of ["release", "resume", "steer", "commands", "unused", "owner-cancel", "native-exit", "duplicate", "replacement", "early-steer", "bad-inventory", "resume-mismatch"]) {
  test(`prepared entrypoint lifecycle: ${mode}`, { timeout: 10000 }, async t => {
    const bridge = await launch(t, mode);
    const { child, request, events, observations, send, wait, finish } = bridge;
    send(request);
    await wait(() => events.some(event => event.type === "prepared"));
    await new Promise(resolve => setTimeout(resolve, 30));
    assert.deepEqual(events, [{ type: "prepared" }]);
    assert.equal(observations.filter(value => value.kind === "spawn").length, 1);
    assert.equal(observations.some(value => value.kind === "input"), false);
    if (mode === "unused") { child.stdin.end(); await finish("cancelled"); }
    else if (mode === "owner-cancel") { child.kill("SIGTERM"); await finish("cancelled"); }
    else if (mode === "native-exit") await finish("execution_failed");
    else if (mode === "replacement") { send({ type: "start", prompt: "first", model: "changed" }); await finish("invalid_request"); }
    else if (mode === "early-steer") { send({ type: "steer", input_id: "early", text: "first" }); await finish("invalid_request"); }
    else {
      send({ type: "start", prompt: "first" });
      if (mode === "duplicate") { send({ type: "start", prompt: "second" }); await finish("invalid_request"); }
      else if (["bad-inventory", "resume-mismatch"].includes(mode)) {
        await finish("execution_failed");
        assert.equal(events.some(event => event.type === "input_ready"), false);
      } else {
        await wait(() => events.some(event => event.type === "input_ready"));
        if (mode === "steer") send({ type: "steer", input_id: "extra", text: "second" });
        await finish();
        assert.deepEqual(observations.filter(value => value.kind === "input"), [{ kind: "input", text: "first" }]);
        if (mode === "steer") assert.ok(events.some(event => event.type === "input_applied" && event.input_id === "extra"));
        if (mode === "resume") assert.equal(observations[0].kind, "history");
        if (mode === "commands") assert.deepEqual(events.filter(event=>event.type === "command_observation").map(event=>
          [event.session_id,event.id,event.stage,event.observation.output]),
          [["native","command","before",undefined],["native","command","after","prepared"]]);
      }
    }
  });
}

test("preparation cannot observe commands before actual input", { timeout: 10000 }, async t => {
  const { request, events, observations, send, finish } = await launch(t, "command-before-input");
  send(request);
  await finish("execution_failed");
  assert.equal(events.some(event=>event.type === "command_observation"),false);
  assert.equal(observations.some(value=>value.kind === "input"),false);
});

for (const mode of ["missing-history", "missing-hooks", "startup-error", "early-start", "cancel-startup"]) {
  test(`preparation rejects before receipt: ${mode}`, { timeout: 10000 }, async t => {
    const { child, request, events, observations, send, wait, finish } = await launch(t, mode === "cancel-startup" ? "slow-startup" : mode);
    send(request);
    if (mode === "early-start") send({ type: "start", prompt: "too early" });
    if (mode === "cancel-startup") { await wait(() => observations.some(value => value.kind === "spawn")); child.kill("SIGTERM"); }
    await finish(mode === "missing-history" ? "history_unavailable" : mode === "early-start" ? "invalid_request" : mode === "cancel-startup" ? "cancelled" : "execution_failed");
    assert.equal(events.some(event => event.type === "prepared"), false);
    assert.equal(observations.some(value => value.kind === "input"), false);
    if (mode === "missing-history") assert.equal(observations.some(value => value.kind === "spawn"), false);
  });
}


test("prepared native reader stays on the same query across Start", { timeout: 10000 }, async t => {
  const { request, events, observations, send, wait, finish } = await launch(t, "release");
  send(request); await wait(() => events.some(event => event.type === "prepared"));
  send({type:"workspace_read",id:"before",path:"binary",max_bytes:4});
  await wait(()=>observations.some(value=>value.kind === "read"));
  send({type:"start",prompt:"first"});
  await wait(()=>events.some(event=>event.type === "workspace_read"));
  assert.deepEqual(events.find(event=>event.type === "workspace_read"),{type:"workspace_read",id:"before",data_base64:"AP+AAQ==",truncated:false});
  await finish();
  assert.equal(observations.filter(value=>value.kind === "spawn").length,1);
});
