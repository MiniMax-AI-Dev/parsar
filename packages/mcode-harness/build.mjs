import { build } from 'esbuild';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
const here = dirname(fileURLToPath(import.meta.url));
const source = process.env.MCODE_SOURCE;
if (!source) throw new Error('MCODE_SOURCE is required');
const revision = '33b259bbbeb1c16433390869938191d09bdb0680';
if (readFileSync(resolve(source, '.parsar-source-revision'), 'utf8').trim() !== revision)
  throw new Error('source revision mismatch');
const paths = JSON.parse(readFileSync(resolve(source, 'tsconfig.standalone.json'))).compilerOptions.paths;
const plugin = { name: 'pinned-native-source', setup(builder) {
  builder.onResolve({ filter: /^[^./]/ }, ({ path }) => {
    if (path === '@earendil-works/pi-coding-agent') return { path: resolve(here, 'native-pi-tools.ts') };
    if (path.startsWith('@native/')) return { path: resolve(source, 'packages/agent-tools/src/desktop', path.slice(8) + '.ts') };
    if (path.startsWith('@pi/')) return { path: resolve(source, 'third_party/pi-mono/packages/coding-agent/src', path.slice(4) + '.ts') };
    if (paths[path]) return { path: resolve(source, paths[path][0]) };
  });
}};
mkdirSync(resolve(here, 'dist'), { recursive: true });
const result = await build({ entryPoints: [resolve(here, 'worker.ts')], outfile: resolve(here, 'dist/worker.mjs'),
  bundle: true, platform: 'node', format: 'esm', target: 'node22', packages: 'external',
  plugins: [plugin], tsconfig: resolve(source, 'tsconfig.standalone.json'), metafile: true,
  banner: { js: 'import { createRequire as workerCreateRequire } from "node:module"; const require = workerCreateRequire(import.meta.url);' },
  logLevel: 'info' });
writeFileSync(resolve(here, 'dist/metafile.json'), JSON.stringify(result.metafile, null, 2) + '\n');
const external = [...new Set(Object.values(result.metafile.outputs).flatMap(o => o.imports)
  .filter(i => i.external && !i.path.startsWith('node:')).map(i => i.path))];
writeFileSync(resolve(here, 'dist/build-evidence.json'), JSON.stringify({ revision,
  sourceFiles: Object.keys(result.metafile.inputs).length, external }, null, 2) + '\n');
console.log(JSON.stringify({ sourceFiles: Object.keys(result.metafile.inputs).length, external }));
