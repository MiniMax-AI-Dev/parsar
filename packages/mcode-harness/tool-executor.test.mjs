import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, mkdir, writeFile, rm, access } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { ToolExecutor } from './tool-executor.mjs';

async function fixture(t, body) {
  const root = join(homedir(), '.parsar', 'tests');
  await mkdir(root, { recursive: true });
  const dir = await mkdtemp(join(root, 'mcode-worker-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const script = join(dir, 'launcher.mjs');
  await writeFile(script, body);
  const executor = new ToolExecutor(dir, script);
  t.after(() => executor.close());
  return { dir, executor };
}

test('returns the original native result, including a native tool error', async t => {
  const { executor } = await fixture(t, `
    process.stdin.resume(); process.stdin.on('end', () => {
      console.log(JSON.stringify({tool_name:'read',text:'denied',content:[],isError:true}));
      process.exitCode=1;
    });`);
  assert.deepEqual(await executor.execute('read', { path: 'private' }), {
    tool_name: 'read', text: 'denied', content: [], isError: true,
  });
});

test('rejects a result attributed to a different native tool', async t => {
  const { executor } = await fixture(t, `
    process.stdin.resume(); process.stdin.on('end', () =>
      console.log(JSON.stringify({tool_name:'write',text:'wrong',content:[]})));`);
  await assert.rejects(executor.execute('read', {}), /Invalid native/);
});

for (const shutdown of ['cancel', 'transport close']) {
  test(`${shutdown} waits for worker exit and prevents late effects`, async t => {
    const { dir, executor } = await fixture(t, `
      import { writeFileSync } from 'node:fs';
      process.stdin.resume();
      process.stdin.on('end', () => {
        writeFileSync(process.argv[2]+'/ready','1');
        setTimeout(() => writeFileSync(process.argv[2]+'/late','1'),500);
      });`);
    const controller = new AbortController();
    const failed = assert.rejects(executor.execute('bash', {}, controller.signal));
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try { await access(join(dir, 'ready')); ready = true; break; }
      catch { await delay(10); }
    }
    assert.equal(ready, true, 'fixture worker started');
    if (shutdown === 'cancel') controller.abort();
    else await executor.close();
    await failed;
    await delay(550);
    await assert.rejects(access(join(dir, 'late')), { code: 'ENOENT' });
    await executor.close();
    await assert.rejects(executor.execute('read', {}), /transport is closed/);
  });
}
