import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parseStart } from "../dist/adapter.js";
import { parseWorkspace, WorkspaceProfile } from "../dist/workspace.js";

function fixture(t) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "parsar-workspace-")));
  const dirs = Object.fromEntries(["workspace", "home", "state", "scratch", "protected", "deps"].map(name => {
    const path = join(root, name);
    mkdirSync(path);
    return [name, path];
  }));
  const config = { home: dirs.home, state: dirs.state, scratch: dirs.scratch, protected_dirs: [dirs.protected],
    dependency_path: dirs.deps, env_names: ["ANTHROPIC_API_KEY", "HTTP_PROXY"] };
  const request = { type: "start", prompt: "fixture", model: "fixture", system_prompt: "", cwd: dirs.workspace, workspace: config };
  const previous = process.env;
  process.env = { HOME: dirs.home, CLAUDE_CONFIG_DIR: dirs.state, ANTHROPIC_API_KEY: "fixture-secret",
    HTTP_PROXY: "http://fixture-proxy", UNSELECTED_CANARY: "must-not-inherit", NODE_OPTIONS: "unsafe",
    CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "0", ANTHROPIC_AUTH_TOKEN: "not-selected" };
  t.after(() => { process.env = previous; rmSync(root, { recursive: true, force: true }); });
  return { root, dirs, config, request };
}

test("workspace is explicit, typed and rejects external MCP declarations", t => {
  const { config, request } = fixture(t);
  assert.deepEqual(parseStart(JSON.stringify(request)), request);
  for (const fields of [{ mcp_http_servers: [] }, { functions: null }, { mcp_http_servers: null }]) {
    assert.throws(() => parseStart(JSON.stringify({ ...request, ...fields })), /invalid_request/);
  }
  for (const workspace of [null, [], {}, { ...config, native: {} }, { ...config, env: {} },
    { ...config, env_names: ["ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"] },
    { ...config, env_names: ["NODE_OPTIONS"] }, { ...config, env_names: ["HOME"] }]) {
    assert.throws(() => parseStart(JSON.stringify({ ...request, workspace })), /invalid_request/);
  }
  const { workspace, ...ordinary } = request;
  assert.deepEqual(parseStart(JSON.stringify(ordinary)), ordinary);
  assert.equal(parseWorkspace(undefined, ordinary.cwd), undefined);
  assert.deepEqual(parseStart(JSON.stringify({ ...ordinary, functions: [], mcp_http_servers: [] })),
    { ...ordinary, functions: [], mcp_http_servers: [] });
});

test("workspace roots are existing canonical directories with no overlap or rule syntax", t => {
  const { root, dirs, config } = fixture(t);
  const alias = join(root, "alias");
  symlinkSync(dirs.home, alias);
  const child = join(dirs.workspace, "child");
  mkdirSync(child);
  const file = join(root, "file");
  writeFileSync(file, "fixture");
  for (const home of ["relative", "/", dirs.home + "/", dirs.home + "/../home", join(root, "missing"),
    dirs.workspace, child, root, alias, file, dirs.home + "*", dirs.home + "\n"]) {
    assert.throws(() => parseWorkspace({ ...config, home }, dirs.workspace), /invalid_request/);
  }
  assert.throws(() => parseWorkspace({ ...config, protected_dirs: [dirs.protected, dirs.protected] }, dirs.workspace), /invalid_request/);
  for (const path of ["", ":" + dirs.deps, dirs.deps + ":", "relative", dirs.home, child, root]) {
    assert.throws(() => parseWorkspace({ ...config, dependency_path: path }, dirs.workspace), /invalid_request/);
  }
  const depsAlias = join(root, "deps-alias");
  symlinkSync(dirs.deps, depsAlias);
  assert.equal(parseWorkspace({ ...config, dependency_path: depsAlias }, dirs.workspace).dependency_path, dirs.deps);
  assert.equal(new WorkspaceProfile(dirs.workspace, { ...config, dependency_path: depsAlias }).options.env.PATH, dirs.deps);
  for (const mutable of [dirs.workspace, dirs.scratch]) {
    const unsafeAlias = join(mutable, "deps-alias");
    symlinkSync(dirs.deps, unsafeAlias);
    assert.throws(() => new WorkspaceProfile(dirs.workspace, { ...config, dependency_path: unsafeAlias }), /invalid_request/);
  }
  assert.throws(() => parseWorkspace({ ...config, dependency_path: alias }, dirs.workspace), /invalid_request/);
});

test("workspace environment copies only selected refs and fixed values", t => {
  const { dirs, config } = fixture(t);
  const options = new WorkspaceProfile(dirs.workspace, config).options;
  assert.deepEqual(options.env, {
    PATH: dirs.deps, HOME: dirs.home, TMPDIR: dirs.scratch, CLAUDE_CONFIG_DIR: dirs.state,
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1", DISABLE_TELEMETRY: "1", DISABLE_ERROR_REPORTING: "1",
    DISABLE_AUTOUPDATER: "1", CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "1",
    ANTHROPIC_API_KEY: "fixture-secret", HTTP_PROXY: "http://fixture-proxy",
  });
  assert.ok(options.sandbox.credentials.envVars.some(entry => entry.name === "ANTHROPIC_AUTH_TOKEN" && entry.mode === "deny"));
  assert.ok(options.sandbox.credentials.envVars.some(entry => entry.name === "HTTP_PROXY" && entry.mode === "deny"));
  delete process.env.ANTHROPIC_API_KEY;
  assert.throws(() => new WorkspaceProfile(dirs.workspace, config), /invalid_request/);
  process.env.ANTHROPIC_API_KEY = "fixture-secret";
  for (const key of ["HOME", "CLAUDE_CONFIG_DIR"]) {
    const previous = process.env[key];
    process.env[key] = "/incorrect";
    assert.throws(() => new WorkspaceProfile(dirs.workspace, config), /invalid_request/);
    process.env[key] = previous;
  }
  process.env.CLAUDE_CODE_PROJECT_DIR_NAME = "ambient-history-override";
  assert.throws(() => new WorkspaceProfile(dirs.workspace, config), /invalid_request/);
});

test("workspace native options keep the strict sandbox and exact native inventory", t => {
  const { dirs, config } = fixture(t);
  const profile = new WorkspaceProfile(dirs.workspace, config);
  const options = profile.options;
  assert.deepEqual(options.tools, ["Bash", "Read", "Edit"]);
  assert.deepEqual(options.allowedTools, []);
  assert.deepEqual(options.settingSources, []);
  assert.deepEqual(options.mcpServers, {});
  assert.equal(options.strictMcpConfig, true);
  assert.equal(options.permissionMode, "default");
  assert.equal(options.persistSession, true);
  assert.equal(options.sandbox.enabled, true);
  assert.equal(options.sandbox.failIfUnavailable, true);
  for (const key of ["autoAllowBashIfSandboxed", "allowUnsandboxedCommands", "enableWeakerNestedSandbox", "enableWeakerNetworkIsolation"]) {
    assert.equal(options.sandbox[key], false);
  }
  assert.deepEqual(options.sandbox.excludedCommands, []);
  assert.deepEqual(options.sandbox.filesystem, { disabled: false, allowWrite: [dirs.workspace, dirs.scratch],
    denyRead: [dirs.home, dirs.state, dirs.protected], denyWrite: [dirs.home, dirs.state, dirs.protected], allowRead: [] });
  assert.deepEqual(options.sandbox.network,
    { allowedDomains: [], strictAllowlist: true, allowAllUnixSockets: false, allowLocalBinding: false });
  assert.equal(options.settings.permissions.blockReadsOutsideWorkingDirectories, true);
  assert.equal(options.settings.permissions.disableBypassPermissionsMode, "disable");
  for (const path of [dirs.home, dirs.state, dirs.protected, "/proc", "/sys"]) {
    assert.ok(options.settings.permissions.deny.includes(`Read(/${path}/**)`));
    assert.ok(options.settings.permissions.deny.includes(`Edit(/${path}/**)`));
  }
  profile.verify(["Read", "Edit", "Bash"], []);
  for (const tools of [[], ["Bash", "Read", "Read"], ["Bash", "Read", "Write"], ["Bash", "Read", "Edit", "Agent"]]) {
    assert.throws(() => profile.verify(tools, []), /unexpected native workspace inventory/);
  }
  assert.throws(() => profile.verify(options.tools, [{ name: "untrusted", status: "connected" }]), /unexpected native workspace inventory/);
});

test("workspace permissions and pre-tool hook reject outside paths and unsafe Bash flags", async t => {
  const { dirs, config } = fixture(t);
  writeFileSync(join(dirs.workspace, "file.txt"), "fixture");
  symlinkSync(dirs.protected, join(dirs.workspace, "escape"));
  symlinkSync(join(dirs.protected, "missing"), join(dirs.workspace, "dangling"));
  symlinkSync(join(dirs.workspace, "file.txt"), join(dirs.workspace, "inside"));
  const profile = new WorkspaceProfile(dirs.workspace, config);
  const options = { signal: new AbortController().signal, toolUseID: "tool", requestId: "request" };
  const hook = async (name, input) => profile.beforeTool({ hook_event_name: "PreToolUse", session_id: "native", cwd: dirs.workspace,
    transcript_path: join(dirs.state, "session"), tool_name: name, tool_input: input, tool_use_id: "tool" }, "tool", options);
  for (const [name, input] of [["Bash", { command: "printf value" }], ["Read", { file_path: "file.txt" }],
    ["Read", { file_path: "inside" }], ["Edit", { file_path: "new/file.txt", old_string: "", new_string: "value" }]]) {
    const permission = await profile.canUseTool(name, input, options);
    assert.equal(permission.behavior, "allow");
    if (name === "Bash") assert.deepEqual(await hook(name, input), {});
    else {
      assert.equal(permission.updatedInput.file_path, join(dirs.workspace, input.file_path));
      assert.deepEqual((await hook(name, input)).hookSpecificOutput.updatedInput, permission.updatedInput);
    }
  }
  for (const [name, input] of [["Bash", { command: "true", run_in_background: true }],
    ["Bash", { command: "true", dangerouslyDisableSandbox: true }], ["Bash", { command: "true", run_in_background: "false" }],
    ["Write", { file_path: "file.txt" }], ["Read", { file_path: "../protected/value" }],
    ["Read", { file_path: join(dirs.home, "credentials") }], ["Edit", { file_path: "escape/new" }],
    ["Read", { file_path: "dangling" }], ["Edit", { file_path: "dangling/new" }]]) {
    assert.equal((await profile.canUseTool(name, input, options)).behavior, "deny");
    assert.equal((await hook(name, input)).hookSpecificOutput.permissionDecision, "deny");
  }
  assert.equal((await profile.canUseTool("Bash", { command: "true" }, { ...options, agentID: "child" })).behavior, "deny");
  assert.equal((await profile.canUseTool("Bash", { command: "true" }, { ...options, signal: AbortSignal.abort() })).behavior, "deny");
});

test("dedicated Runtime carries an explicit native network policy", t => {
  const { dirs, config, request } = fixture(t);
  for (const network_access of ["enabled", "disabled"]) {
    const workspace = { ...config, network_access };
    const parsed = parseStart(JSON.stringify({ ...request, workspace, require_history: true }));
    assert.equal(parsed.require_history, true);
    const options = new WorkspaceProfile(dirs.workspace, workspace).options;
    assert.deepEqual(options.sandbox.network.allowedDomains, network_access === "enabled" ? ["*"] : []);
    assert.equal(options.sandbox.enableWeakerNestedSandbox, false);
    assert.equal(options.sandbox.allowUnsandboxedCommands, false);
  }
  assert.throws(() => parseStart(JSON.stringify({ ...request, workspace: { ...config, network_access: "restricted" } })), /invalid_request/);
  const { workspace, ...none } = request;
  assert.throws(() => parseStart(JSON.stringify({ ...none, require_history: true })), /invalid_request/);
});

test("workspace functions retain native sandbox and exact tool authority", async t => {
  const { dirs, config, request } = fixture(t);
  const functions = [{ name: "lookup", description: "Lookup", parameters: { type: "object" } }];
  assert.deepEqual(parseStart(JSON.stringify({ ...request, functions })).functions, functions);
  const profile = new WorkspaceProfile(dirs.workspace, config, ["mcp__functions__lookup"]);
  profile.verify(["Bash", "Read", "Edit", "mcp__functions__lookup"], [{ name: "functions", status: "connected" }]);
  for (const servers of [[], [{ name: "functions", status: "failed" }], [{ name: "external", status: "connected" }]]) {
    assert.throws(() => profile.verify(["Bash", "Read", "Edit", "mcp__functions__lookup"], servers));
  }
  assert.throws(() => profile.verify(["Bash", "Read", "Edit", "mcp__functions__unknown"], [{ name: "functions", status: "connected" }]));
  const controller = new AbortController();
  const input = { id: "fixture" };
  const options = { signal: controller.signal };
  assert.deepEqual(await profile.canUseTool("mcp__functions__lookup", input, options), { behavior: "allow", updatedInput: input });
  assert.equal((await profile.canUseTool("mcp__functions__unknown", input, options)).behavior, "deny");
  assert.equal((await profile.canUseTool("mcp__functions__lookup", input, { ...options, agentID: "child" })).behavior, "deny");
  const event = { hook_event_name: "PreToolUse", tool_name: "mcp__functions__lookup", tool_input: input, tool_use_id: "call" };
  assert.deepEqual(await profile.beforeTool(event, "call", options), {});
  assert.equal((await profile.beforeTool({ ...event, tool_name: "mcp__external__lookup" }, "call", options)).hookSpecificOutput.permissionDecision, "deny");
  assert.equal((await profile.canUseTool("Bash", { command: "true", dangerouslyDisableSandbox: true }, options)).behavior, "deny");
  assert.equal((await profile.canUseTool("Read", { file_path: dirs.state + "/history" }, options)).behavior, "deny");
  assert.equal(profile.options.sandbox.failIfUnavailable, true);
  assert.equal(profile.options.sandbox.allowUnsandboxedCommands, false);
  controller.abort();
  assert.equal((await profile.canUseTool("mcp__functions__lookup", input, options)).behavior, "deny");
});

test("initialized user env is applied inside native Bash, never SDK spawn env", async t => {
  const { dirs, config } = fixture(t);
  const profile = new WorkspaceProfile(dirs.workspace, { ...config, tool_environment: true });
  assert.equal(profile.options.env.PYTHONPATH, undefined);
  assert.equal(profile.options.env.PATH, dirs.deps);
  assert.ok(profile.options.sandbox.filesystem.allowWrite.includes("/environment/packages"));
  const input = { hook_event_name: "PreToolUse", tool_name: "Bash", tool_use_id: "tool",
    tool_input: { command: "printf '%s' 'quoted value'", timeout: 1000 } };
  const result = await profile.beforeTool(input, "tool", { signal: new AbortController().signal });
  assert.equal(result.hookSpecificOutput.updatedInput.timeout, 1000);
  assert.equal(result.hookSpecificOutput.updatedInput.command,
    ". /environment/initialization/tool-env.sh && eval -- 'printf '\\''%s'\\'' '\\''quoted value'\\'''" );
  const denied = await profile.beforeTool({ ...input, tool_input: { command: "id", dangerouslyDisableSandbox: true } }, "tool", { signal: new AbortController().signal });
  assert.equal(denied.hookSpecificOutput.permissionDecision, "deny");
});

test("installed system tools use the full native shell prefix and existing scratch", t => {
  const { dirs, config } = fixture(t);
  assert.throws(() => new WorkspaceProfile(dirs.workspace, { ...config, system_packages: true }), /invalid_request/);
  const profile = new WorkspaceProfile(dirs.workspace, { ...config, tool_environment: true, system_packages: true });
  assert.equal(profile.options.env.CLAUDE_CODE_SHELL_PREFIX, "/usr/local/bin/agents-api-tool-root");
  assert.equal(profile.options.env.PARSAR_RUNTIME_TOOL_SCRATCH, dirs.scratch);
  assert.equal(profile.options.env.TMPDIR, dirs.scratch);
  assert.ok(profile.options.sandbox.filesystem.denyWrite.includes("/environment/packages/system"));
  assert.equal(profile.options.env.PYTHONPATH, undefined);
  assert.equal(profile.options.sandbox.failIfUnavailable, true);
});
