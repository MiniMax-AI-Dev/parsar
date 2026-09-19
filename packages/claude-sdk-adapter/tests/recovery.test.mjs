import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

// Controlled history inventories test recovery admission, not native execution.
const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
const mode=process.argv[1];
const source='export async function listSessions(o){return globalThis.inventory(o);} export async function getSessionInfo(id,o){return globalThis.info(id,o);} export async function getSessionMessages(id,o){return globalThis.messages(id,o);}';
registerHooks({resolve(s,c,next){return s==='@anthropic-ai/claude-agent-sdk'?{url:'data:text/javascript,'+encodeURIComponent(source),shortCircuit:true}:next(s,c);}});
let reads=0;
globalThis.inventory=o=>{assert.deepEqual(o,{dir:'/workspace',includeWorktrees:false,limit:2});return mode==='empty'?[]:mode==='ambiguous'?[{sessionId:'a',cwd:'/workspace'},{sessionId:'b',cwd:'/workspace'}]:[{sessionId:'a',cwd:mode==='foreign'?'/other':'/workspace'}];};
globalThis.info=(id,o)=>{reads++;assert.equal(id,'a');assert.deepEqual(o,{dir:'/workspace'});return mode==='missing'?undefined:{sessionId:'a',cwd:mode==='changed'?'/other':'/workspace'};};
globalThis.messages=(id,o)=>{assert.equal(id,'a');assert.deepEqual(o,{dir:'/workspace',limit:1});return mode==='metadata-only'?[]:[{type:'user'}];};
const {recoverSession}=await import('./dist/recovery.js');
assert.equal(await recoverSession('/workspace'),mode==='valid'?'a':undefined);
if(['empty','ambiguous','foreign'].includes(mode))assert.equal(reads,0);
`;
for (const mode of ["empty", "ambiguous", "foreign", "missing", "changed", "metadata-only", "valid"]) {
  test(`recovery inventory: ${mode}`, () => {
    const child=spawnSync(process.execPath,["--input-type=module","-e",fixture,mode],{encoding:"utf8",timeout:5000});
    assert.equal(child.status,0,child.stderr||child.error?.message);
  });
}
