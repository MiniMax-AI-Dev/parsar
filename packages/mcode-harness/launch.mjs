import { SandboxManager } from './dist/sandbox.mjs';
import { spawn } from 'node:child_process';
import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
const here = dirname(fileURLToPath(import.meta.url));
const profile = JSON.parse(readFileSync(process.argv[2], 'utf8'));
if (profile.workspace !== process.argv[3] || profile.workspace !== '/workspace')
  throw new Error('Workspace profile does not match execution binding');
if (!['disabled','enabled'].includes(profile.network)) throw new Error('Invalid network policy');
const baseEnv = {PATH:'/usr/local/bin:/usr/bin:/bin',HOME:profile.scratch,TMPDIR:profile.scratch,LANG:'C.UTF-8'};
let child;
let cancelled=false;
const cancel=()=>{cancelled=true;child?.kill('SIGKILL');};
process.on('SIGTERM',cancel);process.on('SIGINT',cancel);
try {
 mkdirSync(profile.scratch,{recursive:true});
 await SandboxManager.initialize({
  network:{allowedDomains:[],deniedDomains:profile.network==='disabled'?['*']:[],allowAll:profile.network==='enabled'},
  filesystem:{denyRead:profile.protectedDirs,allowWrite:[profile.workspace,profile.scratch],denyWrite:[]},
  seccomp:{applyPath:join(here,'dist/vendor/seccomp/x64/apply-seccomp')},
 },undefined,false);
 const quote=s=>"'"+s.replaceAll("'","'\\''")+"'";
 const command=[process.execPath,join(here,'dist/worker.mjs'),profile.workspace].map(quote).join(' ');
 const wrapped=await SandboxManager.wrapWithSandbox(command,'/bin/bash',undefined,undefined,{baseEnv,sandboxTempDir:profile.scratch});
 if (cancelled) throw new Error('Cancelled before workspace tool start');
 child=spawn('/bin/bash',['-c','exec '+wrapped],{cwd:profile.workspace,env:baseEnv,stdio:['pipe','pipe','pipe']});
 process.stdin.pipe(child.stdin);child.stdout.pipe(process.stdout);child.stderr.pipe(process.stderr);
 child.stdin.on('error',()=>cancel());
 const status=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('close',resolve);});
 process.exitCode=cancelled?1:(status??1);
} catch(error) {process.stderr.write(String(error)+'\n');process.exitCode=1;}
finally {await SandboxManager.reset();}
