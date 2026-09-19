import { build } from 'esbuild';
import { mkdirSync, cpSync, writeFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
const here = dirname(fileURLToPath(import.meta.url));
const source = process.env.MCODE_SOURCE;
if (!source) throw new Error('MCODE_SOURCE is required');
mkdirSync(resolve(here, 'dist'), { recursive: true });
const result = await build({ entryPoints: [resolve(here, 'sandbox-entry.ts')],
  outfile: resolve(here, 'dist/sandbox.mjs'), bundle: true, platform: 'node', format: 'esm',
  target: 'node22', nodePaths: [resolve(here, 'node_modules')], metafile: true,
  plugins: [{ name: 'sandbox-source', setup(b) { b.onResolve({ filter: /^@sandbox\// }, () =>
    ({ path: resolve(source, 'third_party/sandbox-runtime/src/sandbox/sandbox-manager.ts') })); } }],
  banner: { js: 'import { createRequire as srtCreateRequire } from "node:module"; const require = srtCreateRequire(import.meta.url);' },
  logLevel: 'info' });
cpSync(resolve(source, 'third_party/sandbox-runtime/vendor/seccomp'), resolve(here, 'dist/vendor/seccomp'), { recursive: true });
cpSync(resolve(source, 'third_party/sandbox-runtime/vendor/java-proxy-agent'), resolve(here, 'dist/vendor/java-proxy-agent'), { recursive: true });
writeFileSync(resolve(here, 'dist/sandbox-metafile.json'), JSON.stringify(result.metafile, null, 2) + '\n');
