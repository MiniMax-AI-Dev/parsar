import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

// Run the production entrypoint: its explicitly supplied Inputs must also be held.
const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
const mode=process.argv[1];
const sdk='export function startup(args){return globalThis.startup(args);} export function query(args){return globalThis.direct(args);} export async function getSessionInfo(){return process.argv[1]==="missing-history"?undefined:{sessionId:"native"};}';
registerHooks({resolve(s,c,next){return s==="@anthropic-ai/claude-agent-sdk"?{url:"data:text/javascript,"+encodeURIComponent(sdk),shortCircuit:true}:next(s,c);}});
function create(options){
 assert.deepEqual(options.tools,[]);assert.equal(options.strictMcpConfig,true);assert.equal(options.hooks.PreToolUse.length,1);
 assert.equal(options.mcpServers.fixture.alwaysLoad,true);
 const child=options.spawnClaudeCodeProcess({command:process.execPath,args:["-e","process.stdin.resume();process.stdin.on('end',()=>process.exit(0));"],env:options.env,signal:options.abortController.signal});
 process.send({kind:"spawn"});
 let iterator,pending,readiness=false;
 const status=()=>[{name:"fixture",status:"connected",tools:[{name:"echo"}]},{name:"optional",status:"connected",tools:[]}];
 return {close(){child.stdin.end();},query(inputs){
  iterator=inputs[Symbol.asyncIterator]();pending=iterator.next();
  let yielded=false;pending.then(v=>{if(!v.done)yielded=true;});
  return {close(){child.stdin.end();},async initializationResult(){await new Promise(r=>setTimeout(r,20));assert.equal(yielded,false);return {hooks_applied:mode!=="missing-hooks"};},
   async mcpServerStatus(){
    if(!readiness && mode!=="optional"){
     await new Promise(r=>setTimeout(r,30));assert.equal(yielded,false);process.send({kind:"checked_before_input"});
     if(mode==="cancelled")options.abortController.abort();
     readiness=true;
     if(mode==="missing")return [];
     if(mode==="duplicate")return [...status(),status()[0]];
     if(mode==="pending"||mode==="failed")return [{name:"fixture",status:mode}];
    }
    return status();
   },
   async *[Symbol.asyncIterator](){
    const first=await pending;if(first.done)return;
    assert.ok(readiness||mode==="optional");process.send({kind:"input",text:first.value.message.content});
    yield {type:"system",subtype:"init",session_id:mode==="wrong-history"?"foreign":"native",tools:["mcp__fixture__echo"],mcp_servers:[]};
    yield {type:"result",uuid:"result",session_id:"native",user_message_uuids:[first.value.uuid],subtype:"success",is_error:false,result:"done",usage:{input_tokens:1,output_tokens:1},modelUsage:{}};
   }
  };
 }};
}
globalThis.startup=async({options})=>{process.send({kind:"warm"});return create(options);};
globalThis.direct=()=>{throw new Error("MCP must use the shared startup path");};
await import(${JSON.stringify(new URL("../dist/main.js", import.meta.url).href)});
process.disconnect();
`;

for (const mode of ["connected", "pending", "failed", "missing", "duplicate", "missing-hooks", "cancelled", "resume", "wrong-history", "missing-history", "optional"]) {
  test(`required MCP entrypoint holds input through readiness: ${mode}`, async () => {
    const cwd = mkdtempSync(join(tmpdir(), "parsar-required-"));
    const child = spawn(process.execPath, ["--input-type=module", "-e", fixture, mode], { stdio: ["pipe", "pipe", "pipe", "ipc"] });
    const observations = [];
    let stdout = "", stderr = "";
    child.stdout.on("data", b => { stdout += b; });
    child.stderr.on("data", b => { stderr += b; });
    child.on("message", value => observations.push(value));
    const closed = new Promise(resolve => child.once("close", (code, signal) => resolve({ code, signal })));
    const timer = setTimeout(() => child.kill("SIGKILL"), 8000);
    try {
      child.stdin.write(JSON.stringify({ type: "start", model: "fixed", prompt: "one input", system_prompt: "", cwd,
        ...(mode.includes("history") || mode === "resume" ? { resume: "native" } : {}),
        mcp_http_servers: [{ server_label: "fixture", server_url: "https://example.invalid/mcp", allowed_tools: ["echo"], required: mode !== "optional" },
          { server_label: "optional", server_url: "https://optional.invalid/mcp", allowed_tools: [], required: false }] }) + "\n");
      const exit = await closed;
      assert.equal(exit.signal, null, stderr);
      assert.equal(exit.code, 0, stderr);
      const events = stdout.trim().split("\n").map(JSON.parse);
      const success = ["connected", "resume", "optional"].includes(mode);
      assert.equal(events.at(-1).type, success ? "result" : "error");
      assert.equal(observations.filter(v => v.kind === "input").length, success || mode === "wrong-history" ? 1 : 0);
      assert.equal(events.some(e => e.type === "prepared"), false);
      assert.equal(events.some(e => e.type === "input_ready"), success);
      if (mode === "missing-history") assert.equal(observations.length, 0);
      if (mode === "cancelled") assert.equal(events.at(-1).code, "cancelled");
    } finally {
      clearTimeout(timer);child.kill("SIGKILL");await closed;rmSync(cwd, { recursive: true, force: true });
    }
  });
}
