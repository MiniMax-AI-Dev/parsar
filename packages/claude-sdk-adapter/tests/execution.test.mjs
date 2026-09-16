import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

// Exercise execute() against controlled SDK event interleavings. The native child
// is real, but these cases are not live provider acceptance.
const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
const sdk = 'export async function getSessionInfo(){return {sessionId:"native"};} export function query(options){return globalThis.queryFixture(options);} export function startup(){throw new Error("Unexpected preparation");}';
registerHooks({ resolve(specifier, context, next) {
  if (specifier === "@anthropic-ai/claude-agent-sdk") return {url:"data:text/javascript,"+encodeURIComponent(sdk),shortCircuit:true};
  return next(specifier,context);
}});
const { execute } = await import(${JSON.stringify(new URL("../dist/adapter.js", import.meta.url).href)});
const { Inputs } = await import(${JSON.stringify(new URL("../dist/inputs.js", import.meta.url).href)});
const { FunctionBridge } = await import(${JSON.stringify(new URL("../dist/function_bridge.js", import.meta.url).href)});
const mode = process.argv[1];
const events = [];
const inputs = new Inputs("opening text");
const abort = new AbortController();
let invocation;
let functions;
const emit = async event => {
  events.push(event);
  if (event.type === "input_ready") inputs.submit({type:"steer",input_id:"extra",text:"later text"});
  if (mode === "later-function" && event.type === "usage" && !invocation) {
    // The SDK dispatches MCP controls independently while the output consumer is
    // yielding the previous native turn's result. This call belongs to the next turn.
    invocation = functions.invoke({id:"call-next",name:"lookup",arguments:{}},abort.signal);
    invocation.catch(() => {});
  }
};
functions = new FunctionBridge(emit);
const init = {type:"system",subtype:"init",session_id:"native",tools:mode === "later-function"?["mcp__functions__lookup"]:[],mcp_servers:mode === "later-function"?[{name:"functions",status:"connected"}]:[]};
const result = (uuid, ids, failed = false) => ({type:"result",uuid,session_id:"native",user_message_uuids:ids,
  subtype:failed?"error_during_execution":"success",is_error:failed,result:"later answer",
  usage:{input_tokens:2,output_tokens:1},modelUsage:{},total_cost_usd:0.01});
globalThis.queryFixture = ({prompt,options}) => {
  const child = options.spawnClaudeCodeProcess({command:process.execPath,args:["-e","process.stdin.resume();process.stdin.on('end',()=>process.exit(0));"],env:process.env,signal:abort.signal});
  return {
    close(){child.stdin.end();},
    async *[Symbol.asyncIterator](){
      const iterator = prompt[Symbol.asyncIterator]();
      const first = (await iterator.next()).value;
      yield init;
      const second = (await iterator.next()).value;
      if(mode === "error-receipt") { yield result("failed",[first.uuid,second.uuid],true); return; }
      yield result("first",[first.uuid]);
      assert.ok(invocation,"next turn's control request did not reach the pending ledger");
      yield init;
      functions.submit(JSON.stringify({type:"function_result",call_id:"call-next",delivery_id:"delivery",success:true,content:[{type:"input_text",text:"value"}]}));
      await invocation;
      yield {type:"user",session_id:"native",parent_tool_use_id:null,message:{role:"user",content:[{type:"tool_result",tool_use_id:"call-next",content:[{type:"text",text:"value"}],is_error:false}]}};
      yield result("second",[second.uuid]);
    },
  };
};
await execute({type:"start",prompt:"opening text",model:"fixture",system_prompt:"",cwd:process.cwd(),
  ...(mode === "later-function" ? {functions:[{name:"lookup",description:"fixture",parameters:{type:"object",properties:{}}}]} : {})},emit,abort,functions,inputs);
assert.equal(inputs.complete,true);
assert.equal(events.filter(e=>e.type === "input_applied" && e.input_id === "extra").length,1);
if(mode === "error-receipt") {
  assert.equal(events.at(-1).type,"error");
  assert.equal(events.at(-1).code,"execution_failed");
  assert.equal(events.filter(e=>e.type === "usage").length,1);
  assert.equal(events.some(e=>e.type === "result"),false);
} else {
  assert.equal(events.at(-1).type,"result");
  assert.equal(events.filter(e=>e.type === "function_applied").length,1);
  assert.equal(events.filter(e=>e.type === "usage").length,2);
  assert.equal(events.some(e=>e.type === "error"),false);
}
`;

for (const mode of ["error-receipt", "later-function"]) {
  test(`native result lifecycle: ${mode}`, { timeout: 15000 }, () => {
    const child = spawnSync(process.execPath, ["--input-type=module", "-e", fixture, mode], { encoding: "utf8", timeout: 10000 });
    assert.equal(child.status, 0, child.stderr || child.error?.message);
  });
}
