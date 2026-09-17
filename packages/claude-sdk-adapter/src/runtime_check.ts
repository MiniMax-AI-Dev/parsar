import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { constants } from "node:fs";
import { access, readFile, realpath } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

// This companion checks the exact bridge used by execution, without a model query.
try {
  assert(Number(process.versions.node.split(".")[0]) >= 20, "unsupported_node");
  const root = await realpath(fileURLToPath(new URL("..", import.meta.url)));
  const inside = async (path: string) => {
    const resolved = await realpath(path);
    const rel = relative(root, resolved);
    assert(rel !== ".." && !rel.startsWith(".." + sep), "external_dependency");
    return resolved;
  };
  const manifest = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
  const require = createRequire(join(root, "package.json"));
  const sdkDir = dirname(await inside(require.resolve("@anthropic-ai/claude-agent-sdk")));
  const sdk = JSON.parse(await readFile(join(sdkDir, "package.json"), "utf8"));
  const mcp = JSON.parse(await readFile(await inside(join(root, "node_modules/@modelcontextprotocol/sdk/package.json")), "utf8"));
  // pnpm deploy annotates pinned versions with resolved peer suffixes.
  assert.equal(sdk.version, manifest.dependencies["@anthropic-ai/claude-agent-sdk"].split("(")[0]);
  assert.equal(mcp.version, manifest.dependencies["@modelcontextprotocol/sdk"].split("(")[0]);
  const libc = process.platform === "linux" && !(process.report?.getReport() as { header: { glibcVersionRuntime?: string } }).header.glibcVersionRuntime ? "-musl" : "";
  const nativeName = `@anthropic-ai/claude-agent-sdk-${process.platform}-${process.arch}${libc}`;
  const sdkRequire = createRequire(join(sdkDir, "package.json"));
  const nativePath = await inside(sdkRequire.resolve(`${nativeName}/package.json`));
  const native = JSON.parse(await readFile(nativePath, "utf8"));
  assert.equal(native.version, sdk.version);
  const binary = await inside(join(dirname(nativePath), process.platform === "win32" ? "claude.exe" : "claude"));
  await access(binary, constants.X_OK);
  const options = { encoding: "utf8" as const, timeout: 5000, killSignal: "SIGKILL" as const, maxBuffer: 64 * 1024, cwd: root };
  const version = spawnSync(binary, ["--version"], options);
  assert.equal(version.error, undefined, "native_unavailable");
  assert.equal(version.status, 0, "native_unavailable");
  assert.match(sdk.claudeCodeVersion, /^\d+\.\d+\.\d+$/);
  const nativeVersion = `${sdk.claudeCodeVersion} (Claude Code)`;
  assert.equal(version.stdout.trim(), nativeVersion, "unexpected_native_version");
  const entrypoint = await inside(process.argv[2] ?? join(root, "dist/main.js"));
  assert.equal(entrypoint, await realpath(join(root, "dist/main.js")), "unexpected_entrypoint");
  const smoke = spawnSync(process.execPath, [entrypoint], { ...options, input: "" });
  assert.equal(smoke.error, undefined, "bridge_unavailable");
  assert.equal(smoke.status, 0, "bridge_unavailable");
  assert.deepEqual(JSON.parse(smoke.stdout), { type: "error", code: "invalid_request" });
  process.stdout.write(JSON.stringify({ type: "runtime_ready", protocol: 1, features: ["mcp_http_tools", "mcp_http_bearer_auth", "workspace_tools", "workspace_prepare", "workspace_read", "workspace_command_observations"], node: process.versions.node, sdk: sdk.version, mcp: mcp.version, native: nativeVersion }) + "\n");
} catch {
  // Native diagnostics can include operator environment; never forward them.
  process.stdout.write(JSON.stringify({ type: "runtime_unavailable" }) + "\n");
  process.exitCode = 1;
}
