import type { HookCallback, McpServerConfig, McpServerStatus } from "@anthropic-ai/claude-agent-sdk";

export type HTTPServer = {
  server_label: string;
  server_url: string;
  allowed_tools: string[] | null;
};
export type ToolIdentity = { server: string; name: string };

function nativeToolName(server: string, tool: string): string {
  let name = tool.replace(/[^a-zA-Z0-9_-]/g, "_");
  if (tool.startsWith("claude.ai ")) name = name.replace(/_+/g, "_").replace(/^_|_$/g, "");
  return `mcp__${server}__${name}`;
}

export function parseHTTPServers(value: unknown): HTTPServer[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new Error("invalid_request");
  const labels = new Set<string>();
  for (const server of value) {
    if (!server || typeof server !== "object" ||
        Object.keys(server).some(key => !["server_label", "server_url", "allowed_tools"].includes(key)) ||
        typeof server.server_label !== "string" || !/^[a-zA-Z0-9_-]+$/.test(server.server_label) ||
        server.server_label === "functions" || labels.has(server.server_label) ||
        typeof server.server_url !== "string" ||
        (server.allowed_tools !== null && (!Array.isArray(server.allowed_tools) ||
          server.allowed_tools.some((name: unknown) => typeof name !== "string" || !/^[a-zA-Z0-9_.-]+$/.test(name))))) {
      throw new Error("invalid_request");
    }
    const url = new URL(server.server_url);
    if (!["http:", "https:"].includes(url.protocol) || !url.hostname || url.username || url.password ||
        server.server_url.includes("?") || server.server_url.includes("#")) throw new Error("invalid_request");
    labels.add(server.server_label);
  }
  return value;
}

export class MCPProfile {
  readonly servers: Record<string, McpServerConfig> = Object.create(null);
  readonly allowed: string[];
  readonly denied: string[] = [];
  readonly identities = new Map<string, ToolIdentity>();
  private sessionID = "";
  private admitted = false;
  private release!: (ready: boolean) => void;
  private readonly ready = new Promise<boolean>(resolve => { this.release = resolve; });

  readonly beforeTool: HookCallback = async (input, id, { signal }) => {
    let stopped!: () => void;
    const interrupted = new Promise<boolean>(resolve => {
      stopped = () => resolve(false);
      signal.addEventListener("abort", stopped, { once: true });
    });
    try {
      if (!signal.aborted && await Promise.race([this.ready, interrupted]) && !signal.aborted && this.admitted &&
          input.hook_event_name === "PreToolUse" && input.agent_id === undefined && input.session_id === this.sessionID &&
          (id === undefined || id === input.tool_use_id) &&
          (this.identities.has(input.tool_name) || this.functions.includes(input.tool_name))) return {};
      return { hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny",
        permissionDecisionReason: "Tool is outside the verified execution profile." } };
    } finally {
      signal.removeEventListener("abort", stopped);
    }
  };

  constructor(private readonly declarations: HTTPServer[], private readonly functions: string[]) {
    this.allowed = [...functions];
    for (const server of declarations) {
      const prefix = `mcp__${server.server_label}__`;
      // An explicit empty Authorization suppresses native OAuth and automatic auth.
      this.servers[server.server_label] = { type: "http", url: server.server_url, alwaysLoad: true, headers: { Authorization: "" } };
      if (server.allowed_tools === null) this.allowed.push(prefix + "*");
      else if (!server.allowed_tools.length) this.denied.push(prefix + "*");
      else this.allowed.push(...server.allowed_tools.map(name => nativeToolName(server.server_label, name)));
    }
  }

  verify(inventory: string[], statuses: McpServerStatus[], sessionID: string): void {
    this.admitted = false;
    const expected = new Map<string, ToolIdentity>();
    const declared = new Map(this.declarations.map(server => [server.server_label, server]));
    const seen = new Set<string>();
    const nativeNames = new Set(this.functions);
    for (const status of statuses) {
      if (seen.has(status.name)) throw new Error("duplicate native MCP server");
      seen.add(status.name);
      if (status.name === "functions" && this.functions.length) {
        if (status.status !== "connected") throw new Error("native function server unavailable");
        continue;
      }
      const server = declared.get(status.name);
      if (!server || status.status !== "connected") throw new Error("native MCP server unavailable or undeclared");
      for (const tool of status.tools ?? []) {
        const native = nativeToolName(server.server_label, tool.name);
        if (nativeNames.has(native)) throw new Error("ambiguous native MCP identity");
        nativeNames.add(native);
        if (server.allowed_tools !== null && !server.allowed_tools.includes(tool.name)) continue;
        expected.set(native, { server: server.server_label, name: tool.name });
      }
    }
    if (seen.size !== declared.size + (this.functions.length ? 1 : 0) ||
        inventory.length !== expected.size + this.functions.length || new Set(inventory).size !== inventory.length ||
        inventory.some(name => !expected.has(name) && !this.functions.includes(name))) {
      throw new Error("unexpected native MCP inventory");
    }
    this.identities.clear();
    for (const [name, identity] of expected) this.identities.set(name, identity);
    this.sessionID = sessionID;
    this.admitted = true;
    this.release(true);
  }

  close(): void { this.admitted = false; this.release(false); }
}
