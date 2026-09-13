import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { access, readFile, readdir, realpath } from "node:fs/promises";
import { constants } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, relative, sep } from "node:path";

const root = await realpath(process.argv[2]);
const inside = path => {
  const rel = relative(root, path);
  assert(rel !== ".." && !rel.startsWith(".." + sep), "Runtime dependency escaped the exported directory");
};
async function checkLinks(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isSymbolicLink()) inside(await realpath(path));
    else if (entry.isDirectory()) await checkLinks(path);
  }
}
await checkLinks(root);
await access(join(root, "pnpm-lock.yaml"));
const source = JSON.parse(await readFile(new URL("../packages/claude-sdk-adapter/package.json", import.meta.url), "utf8"));
const require = createRequire(join(root, "package.json"));
const sdkDir = dirname(require.resolve("@anthropic-ai/claude-agent-sdk"));
inside(await realpath(sdkDir));
const sdk = JSON.parse(await readFile(join(sdkDir, "package.json"), "utf8"));
assert.equal(sdk.version, source.dependencies[sdk.name]);
const mcpPath = join(root, "node_modules/@modelcontextprotocol/sdk/package.json");
inside(await realpath(mcpPath));
const mcp = JSON.parse(await readFile(mcpPath, "utf8"));
assert.equal(mcp.version, source.dependencies[mcp.name]);
// Native packages are optional dependencies of the pinned SDK, resolved from it.
const libc = process.platform === "linux" && !process.report.getReport().header.glibcVersionRuntime ? "-musl" : "";
const nativeName = `${sdk.name}-${process.platform}-${process.arch}${libc}`;
const sdkRequire = createRequire(join(sdkDir, "package.json"));
const nativePath = sdkRequire.resolve(`${nativeName}/package.json`);
inside(await realpath(nativePath));
const native = JSON.parse(await readFile(nativePath, "utf8"));
assert.equal(native.version, sdk.version);
const binary = join(dirname(nativePath), process.platform === "win32" ? "claude.exe" : "claude");
await access(binary, constants.X_OK);
const version = spawnSync(binary, ["--version"], { encoding: "utf8", timeout: 5000, cwd: root });
assert.equal(version.status, 0, "Exported native executable did not start on this platform");
// EOF exercises the real SDK/MCP import graph without starting a model request.
const smoke = spawnSync(process.execPath, [join(root, "dist/main.js")], { input: "", encoding: "utf8", timeout: 5000, cwd: root });
assert.equal(smoke.status, 0, "Exported bridge did not start");
assert.deepEqual(JSON.parse(smoke.stdout), { type: "error", code: "invalid_request" });
console.log(`Verified exported SDK ${sdk.version}, MCP ${mcp.version}, ${version.stdout.trim()}`);
