import assert from "node:assert/strict";
import test from "node:test";
import { WorkspaceReads } from "../dist/workspace_reads.js";

const request = { type: "workspace_read", id: "read", path: "file", max_bytes: 4 };
function fixture(readFile) {
 const events=[],abort=new AbortController();let closed=false;
 const reads=new WorkspaceReads(async event=>{events.push(event);},abort);
 reads.bind({readFile,close(){closed=true;}},"/workspace");
 return {reads,events,abort,get closed(){return closed;}};
}
for (const [name,contents,truncated] of [["binary","AP+AAQ==",false],["empty","",false],["prefix","AP+AAQ==",true]]) {
 test(`native binary response: ${name}`,async()=>{
  const f=fixture(async(path,options)=>{assert.equal(path,"/workspace/file");assert.deepEqual(options,{maxBytes:4,encoding:"base64"});return {absPath:path,encoding:"base64",contents,...truncated&&{truncated}};});
  f.reads.submit(request);await f.reads.close();
  assert.deepEqual(f.events,[{type:"workspace_read",id:"read",data_base64:contents,truncated}]);assert.equal(f.abort.signal.aborted,false);
 });
}
test("bounds, admission and release retain the original native wait",async()=>{
 let settle;const f=fixture(()=>new Promise(resolve=>{settle=resolve;}));
 for(const fields of [{path:"../escape"},{path:"/absolute"},{path:"a//b"},{path:"a\\b"},{max_bytes:0},{max_bytes:1048577},{path:"a".repeat(8192)}]) f.reads.submit({...request,...fields});
 assert.ok(f.events.every(event=>event.error === "invalid"));f.events.length=0;
 f.reads.submit(request);f.reads.submit({...request,id:"busy"});assert.equal(f.events[0].error,"busy");
 let released=false;const release=f.reads.close().then(()=>{released=true;});await Promise.resolve();assert.equal(released,false);
 f.reads.submit({...request,id:"closed"});assert.equal(f.events[1].error,"unavailable");
 settle({absPath:"/workspace/file",encoding:"base64",contents:""});await release;assert.equal(f.events[2].data_base64,"");
});
for(const result of [null,{absPath:"/workspace/file",contents:"",encoding:"utf-8"},{absPath:"/other",contents:"",encoding:"base64"},
 {absPath:"/workspace/file",contents:"!",encoding:"base64"},{absPath:"/workspace/file",contents:"",encoding:"base64",truncated:true}]) {
 test("ambiguous native responses fence the owner",async()=>{
  const f=fixture(async()=>result);f.reads.submit(request);await f.reads.close();
  assert.deepEqual(f.events,[{type:"workspace_read",id:"read",error:"uncertain"}]);assert.equal(f.abort.signal.aborted,true);assert.equal(f.closed,true);
 });
}
