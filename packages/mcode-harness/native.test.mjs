import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { isAbsolute, join, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

const profile = process.env.PARSAR_MCODE_NATIVE_PROFILE;
const artifact = process.env.PARSAR_MCODE_NATIVE_ARTIFACT;

test('packaged native tools use writable scratch and retain large output', {
  skip: !profile || !artifact,
  timeout: 30_000,
}, async t => {
  assert.ok(isAbsolute(profile) && isAbsolute(artifact));
  const config = JSON.parse(await readFile(profile, 'utf8'));
  const { ToolExecutor } = await import(pathToFileURL(join(artifact, 'tool-executor.mjs')));
  const executor = new ToolExecutor(profile);
  t.after(() => executor.close());
  const temporary = await executor.execute('bash', {
    command: 'node -p "require(\'os\').tmpdir()"; f=$(mktemp) && printf TEMP_OK > "$f" && cat "$f" && rm "$f"',
  });
  assert.notEqual(temporary.isError, true);
  assert.ok(temporary.text.includes(config.scratch));
  assert.ok(temporary.text.includes('TEMP_OK'));
  const output = await executor.execute('bash', {
    command: 'python3 -c "print((\'native-spill-line\' + chr(10)) * 16000, end=\'\')"',
  });
  assert.notEqual(output.isError, true);
  const full = output.details?.fullOutputPath;
  assert.equal(typeof full, 'string');
  assert.ok(full.startsWith(config.scratch + sep));
  assert.equal(await readFile(full, 'utf8'), 'native-spill-line\n'.repeat(16000));
  const read = await executor.execute('read', { path: full, offset: 1, limit: 1 });
  assert.notEqual(read.isError, true);
  assert.ok(read.text.includes('native-spill-line'));
});
