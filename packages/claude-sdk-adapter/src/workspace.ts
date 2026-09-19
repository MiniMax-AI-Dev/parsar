import type { CanUseTool, HookCallback, Options } from "@anthropic-ai/claude-agent-sdk";
import { lstatSync, realpathSync, statSync } from "node:fs";
import { dirname, isAbsolute, join, resolve } from "node:path";

export type Workspace = {
  home: string;
  state: string;
  scratch: string;
  protected_dirs: string[];
  dependency_path: string;
  env_names: string[];
  network_access?: "enabled" | "disabled";
};

const environmentNames = new Set([
  "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
  "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
  "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
]);
const credentialNames = ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
  "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "GOOGLE_APPLICATION_CREDENTIALS"];
const nativeTools = ["Bash", "Read", "Edit"];
const denial = "Tool is outside the workspace execution profile.";
const invalidPath = /[\x00-\x1f\x7f\\:*?\[\]{}()]/;
const contains = (root: string, path: string) => path === root || path.startsWith(root + "/");

function directory(value: unknown, canonical: boolean): string {
  if (typeof value !== "string" || !isAbsolute(value) || value === "/" || invalidPath.test(value)) {
    throw new Error("invalid_request");
  }
  try {
    const actual = realpathSync(value);
    if (!statSync(actual).isDirectory() || (canonical && actual !== value)) throw new Error();
    return actual;
  } catch { throw new Error("invalid_request"); }
}

export function parseWorkspace(value: unknown, cwd: string): Workspace | undefined {
  if (value === undefined) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const config = value as Record<string, unknown>;
  if (Object.keys(config).some(key => !["home", "state", "scratch", "protected_dirs", "dependency_path", "env_names", "network_access"].includes(key)) ||
      (config.network_access !== undefined && config.network_access !== "enabled" && config.network_access !== "disabled") ||
      !Array.isArray(config.protected_dirs) || !Array.isArray(config.env_names) ||
      typeof config.dependency_path !== "string" || !config.dependency_path ||
      config.env_names.some(name => typeof name !== "string" || !environmentNames.has(name)) ||
      new Set(config.env_names).size !== config.env_names.length) throw new Error("invalid_request");
  const roots = [cwd, config.home, config.state, config.scratch, ...config.protected_dirs].map(path => directory(path, true));
  if (roots.some((root, index) => roots.some((other, otherIndex) => index !== otherIndex && contains(root, other)))) {
    throw new Error("invalid_request");
  }
  const dependencies = config.dependency_path.split(":").map(path => {
    const actual = directory(path, false);
    if (roots.some(root => contains(root, resolve(path)) || contains(resolve(path), root) ||
        contains(root, actual) || contains(actual, root))) throw new Error("invalid_request");
    return actual;
  });
  return { ...config, dependency_path: dependencies.join(":") } as Workspace;
}

export class WorkspaceProfile {
  readonly options: Options;

  constructor(private readonly cwd: string, config: Workspace) {
    config = parseWorkspace(config, cwd)!;
    // SDK history lookup reads the bridge environment, independently of query.env.
    if (process.env.HOME !== config.home || process.env.CLAUDE_CONFIG_DIR !== config.state ||
        process.env.CLAUDE_CODE_PROJECT_DIR_NAME !== undefined) throw new Error("invalid_request");
    const env: Record<string, string> = {
      PATH: config.dependency_path, HOME: config.home, TMPDIR: config.scratch, CLAUDE_CONFIG_DIR: config.state,
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1", DISABLE_TELEMETRY: "1", DISABLE_ERROR_REPORTING: "1",
      DISABLE_AUTOUPDATER: "1", CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "1",
    };
    for (const name of config.env_names) {
      const value = process.env[name];
      if (value === undefined) throw new Error("invalid_request");
      env[name] = value;
    }
    const protectedRoots = [config.home, config.state, ...config.protected_dirs];
    this.options = {
      env, tools: [...nativeTools], allowedTools: [], mcpServers: {}, strictMcpConfig: true,
      settingSources: [], permissionMode: "default", persistSession: true,
      settings: {
        permissions: {
          blockReadsOutsideWorkingDirectories: true, disableBypassPermissionsMode: "disable",
          deny: [...protectedRoots, "/proc", "/sys"].flatMap(path => [
            `Read(/${path})`, `Read(/${path}/**)`, `Edit(/${path})`, `Edit(/${path}/**)`,
          ]),
        },
      },
      sandbox: {
        enabled: true, failIfUnavailable: true, autoAllowBashIfSandboxed: false, allowUnsandboxedCommands: false,
        excludedCommands: [], enableWeakerNestedSandbox: false, enableWeakerNetworkIsolation: false,
        filesystem: { disabled: false, allowWrite: [cwd, config.scratch], denyRead: protectedRoots,
          denyWrite: protectedRoots, allowRead: [] },
        credentials: {
          envVars: [...new Set([...credentialNames, ...config.env_names])].map(name => ({ name, mode: "deny" })),
          files: protectedRoots.map(path => ({ path, mode: "deny" })),
        },
        network: { allowedDomains: config.network_access === "enabled" ? ["*"] : [], strictAllowlist: true, allowAllUnixSockets: false, allowLocalBinding: false },
      },
      canUseTool: this.canUseTool,
      hooks: { PreToolUse: [{ hooks: [this.beforeTool] }] },
    };
  }

  verify(tools: string[], servers: { name: string; status: string }[]): void {
    if (servers.length || tools.length !== nativeTools.length || new Set(tools).size !== tools.length ||
        tools.some(name => !nativeTools.includes(name))) throw new Error("unexpected native workspace inventory");
  }

  readonly canUseTool: CanUseTool = async (name, input, { signal, agentID }) => {
    if (!signal.aborted && agentID === undefined && this.permits(name, input)) {
      return { behavior: "allow", updatedInput: this.absoluteInput(name, input) };
    }
    return { behavior: "deny", message: denial };
  };

  readonly beforeTool: HookCallback = async (input, id, { signal }) => {
    if (!signal.aborted && input.hook_event_name === "PreToolUse" && input.agent_id === undefined &&
        (id === undefined || id === input.tool_use_id) && this.permits(input.tool_name, input.tool_input)) {
      return input.tool_name === "Bash" ? {} : { hookSpecificOutput: { hookEventName: "PreToolUse",
        updatedInput: this.absoluteInput(input.tool_name, input.tool_input as Record<string, unknown>) } };
    }
    return { hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: denial } };
  };

  private absoluteInput(name: string, input: Record<string, unknown>): Record<string, unknown> {
    return name === "Bash" ? input : { ...input, file_path: resolve(this.cwd, input.file_path as string) };
  }

  private permits(name: string, value: unknown): boolean {
    if (!value || typeof value !== "object" || Array.isArray(value)) return false;
    const input = value as Record<string, unknown>;
    if (name === "Bash") return typeof input.command === "string" && !!input.command.trim() &&
      (input.run_in_background === undefined || input.run_in_background === false) &&
      (input.dangerouslyDisableSandbox === undefined || input.dangerouslyDisableSandbox === false);
    if ((name !== "Read" && name !== "Edit") || typeof input.file_path !== "string" || !input.file_path ||
        /[\x00-\x1f]/.test(input.file_path)) return false;
    const path = resolve(this.cwd, input.file_path);
    if (!contains(this.cwd, path)) return false;
    let existing = path;
    const missing: string[] = [];
    try {
      while (true) {
        try { lstatSync(existing); break; }
        catch (error) {
          if ((error as NodeJS.ErrnoException).code !== "ENOENT" || existing === this.cwd) return false;
          missing.unshift(existing.slice(dirname(existing).length + 1));
          existing = dirname(existing);
        }
      }
      return contains(this.cwd, join(realpathSync(existing), ...missing));
    } catch { return false; }
  }
}
