import type { Tool } from "@modelcontextprotocol/sdk/types.js";
import { isAbsolute } from "node:path";
import { parseHTTPServers, type HTTPServer } from "./mcp.js";
import { parseWorkspace, type Workspace } from "./workspace.js";

export type Start = {
  type: "start";
  prompt: string;
  model: string;
  system_prompt: string;
  cwd: string;
  resume?: string;
  require_history?: boolean;
  observe_messages?: boolean;
  functions?: { name: string; description: string; parameters: Tool["inputSchema"] }[];
  mcp_http_servers?: HTTPServer[];
  workspace?: Workspace;
};
export type Prepare = Omit<Start, "type" | "prompt" | "workspace"> & { type: "prepare"; workspace: Workspace };

export function parseRequest(line: string): Start | Prepare {
  const value: unknown = JSON.parse(line);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const request = value as Record<string, unknown>;
  const allowed = new Set(["type", "prompt", "model", "system_prompt", "cwd", "resume", "require_history", "observe_messages", "functions", "mcp_http_servers", "workspace"]);
  if (Object.keys(request).some(key => !allowed.has(key)) ||
      (request.type !== "start" && request.type !== "prepare") ||
      (request.type === "start" ? typeof request.prompt !== "string" || !request.prompt.trim() : "prompt" in request) ||
      typeof request.model !== "string" || !request.model.trim() ||
      typeof request.system_prompt !== "string" ||
      typeof request.cwd !== "string" || !isAbsolute(request.cwd) ||
      (request.observe_messages !== undefined && typeof request.observe_messages !== "boolean") ||
      (request.require_history !== undefined && typeof request.require_history !== "boolean") ||
      (request.resume !== undefined && (typeof request.resume !== "string" || !request.resume))) throw new Error("invalid_request");
  if (request.functions !== undefined && (!Array.isArray(request.functions) || request.functions.some(tool =>
      !tool || typeof tool.name !== "string" || !tool.name || typeof tool.description !== "string" ||
      !tool.parameters || tool.parameters.type !== "object"))) throw new Error("invalid_request");
  parseHTTPServers(request.mcp_http_servers);
  const workspace = parseWorkspace(request.workspace, request.cwd);
  if (request.require_history && !workspace) throw new Error("invalid_request");
  if ((workspace && ("functions" in request || "mcp_http_servers" in request)) ||
      (request.type === "prepare" && !workspace)) throw new Error("invalid_request");
  return request as Start | Prepare;
}

export function parseStart(line: string): Start {
  const request = parseRequest(line);
  if (request.type !== "start") throw new Error("invalid_request");
  return request;
}

export function preparedPrompt(value: unknown): string {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const request = value as Record<string, unknown>;
  if (request.type !== "start" || Object.keys(request).some(key => key !== "type" && key !== "prompt") ||
      typeof request.prompt !== "string" || !request.prompt.trim()) throw new Error("invalid_request");
  return request.prompt;
}
