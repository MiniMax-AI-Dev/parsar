import type { McpSdkServerConfigWithInstance } from "@anthropic-ai/claude-agent-sdk";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import {
  CallToolRequestSchema, ListToolsRequestSchema, McpError, ErrorCode,
  type Tool, type CallToolResult,
} from "@modelcontextprotocol/sdk/types.js";

export type FunctionCall = { id: string; name: string; arguments: Record<string, unknown> };
export type FunctionHandler = (call: FunctionCall, signal: AbortSignal) => Promise<CallToolResult>;

export function createFunctionServer(definitions: Tool[], invoke: FunctionHandler): McpSdkServerConfigWithInstance {
  const tools = structuredClone(definitions);
  const names = new Set<string>();
  for (const tool of tools) {
    if (!tool.name || names.has(tool.name)) throw new Error("Function names must be nonempty and unique.");
    names.add(tool.name);
  }
  const instance = new McpServer({ name: "functions", version: "1.0.0" }, { capabilities: { tools: {} } });
  instance.server.setRequestHandler(ListToolsRequestSchema, async () => ({
    tools: tools.map(tool => ({ ...tool, _meta: { ...tool._meta, "anthropic/alwaysLoad": true } })),
  }));
  instance.server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
    if (!names.has(request.params.name)) throw new McpError(ErrorCode.InvalidParams, "Unknown function.");
    // The pinned native harness supplies this identity independently of the MCP request ID.
    const id = request.params._meta?.["claudecode/toolUseId"];
    if (typeof id !== "string" || !id) throw new McpError(ErrorCode.InvalidParams, "Native function call identity is missing.");
    return invoke({ id, name: request.params.name, arguments: request.params.arguments ?? {} }, extra.signal);
  });
  return { type: "sdk", name: "functions", instance };
}
