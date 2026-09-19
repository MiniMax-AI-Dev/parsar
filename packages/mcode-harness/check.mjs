import '@modelcontextprotocol/sdk/server/index.js';
import { accessSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

if (process.platform !== 'linux' || process.arch !== 'x64') throw new Error('Linux x86_64 is required');
for (const file of ['bridge.mjs', 'launch.mjs', 'tool-executor.mjs', 'dist/worker.mjs',
  'dist/sandbox.mjs', 'dist/vendor/seccomp/x64/apply-seccomp']) accessSync(new URL(file, import.meta.url));
for (const command of ['bwrap', 'socat', 'rg']) execFileSync('which', [command], { stdio: 'ignore' });
const pin = JSON.parse(readFileSync(new URL('source.json', import.meta.url)));
const tools = JSON.parse(execFileSync(process.execPath,
  [fileURLToPath(new URL('dist/worker.mjs', import.meta.url)), '/workspace', '--describe'], { maxBuffer: 1024 * 1024 }));
if (tools.map(t => t.name).sort().join(',') !== 'bash,edit,glob,grep,read,write') throw new Error('Native tool inventory mismatch');
process.stdout.write(JSON.stringify({ protocol: 1, native: pin.version, source: pin.revision }) + '\n');
