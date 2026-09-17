import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { access, readFile, readdir, realpath } from "node:fs/promises";
import { join, relative, sep } from "node:path";

const root = await realpath(process.argv[2]);
async function checkLinks(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isSymbolicLink()) {
      const rel = relative(root, await realpath(path));
      assert(rel !== ".." && !rel.startsWith(".." + sep), "Runtime dependency escaped the exported directory");
    } else if (entry.isDirectory()) await checkLinks(path);
  }
}
await checkLinks(root);
await access(join(root, "pnpm-lock.yaml"));
const source = JSON.parse(await readFile(new URL("../packages/claude-sdk-adapter/package.json", import.meta.url), "utf8"));
const probe = spawnSync(process.execPath, [join(root, "dist/runtime_check.js"), join(root, "dist/main.js")], {
  encoding: "utf8", timeout: 15000, killSignal: "SIGKILL", maxBuffer: 64 * 1024, cwd: root,
});
assert.equal(probe.status, 0, "Exported runtime is unavailable");
const report = JSON.parse(probe.stdout);
assert.equal(report.type, "runtime_ready");
assert.equal(report.protocol, 1);
assert.deepEqual(report.features, ["mcp_http_tools", "mcp_http_bearer_auth", "workspace_tools", "workspace_prepare", "workspace_read", "workspace_command_observations"]);
assert.equal(report.sdk, source.dependencies["@anthropic-ai/claude-agent-sdk"]);
assert.equal(report.mcp, source.dependencies["@modelcontextprotocol/sdk"]);
console.log(`Verified exported SDK ${report.sdk}, MCP ${report.mcp}, ${report.native}`);
