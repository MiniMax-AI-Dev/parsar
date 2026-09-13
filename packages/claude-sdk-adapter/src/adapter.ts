import { getSessionInfo, query, type McpServerConfig } from "@anthropic-ai/claude-agent-sdk";
import { Inputs, type InputEvent } from "./inputs.js";
import { resultUsage, type NativeUsage } from "./usage.js";
import { spawnNative } from "./native.js";
import { MessageObserver, type MessageEvent } from "./messages.js";
import { createFunctionServer } from "./functions.js";
import { FunctionBridge, type FunctionEvent } from "./function_bridge.js";
import type { Tool } from "@modelcontextprotocol/sdk/types.js";
import { isAbsolute } from "node:path";

export type Start = {
  type: "start";
  prompt: string;
  model: string;
  system_prompt: string;
  cwd: string;
  resume?: string;
  observe_messages?: boolean;
  functions?: { name: string; description: string; parameters: Tool["inputSchema"] }[];
};
export type Event =
  | MessageEvent
  | InputEvent
  | FunctionEvent
  | { type: "usage"; session_id: string; result_id: string; usage: NativeUsage }
  | { type: "delta"; delta: string }
  | { type: "result"; session_id: string; text: string }
  | { type: "error"; code: "invalid_request" | "history_unavailable" | "execution_failed" | "cancelled" };

export function parseStart(line: string): Start {
  const value: unknown = JSON.parse(line);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const request = value as Record<string, unknown>;
  const allowed = new Set(["type", "prompt", "model", "system_prompt", "cwd", "resume", "observe_messages", "functions"]);
  if (Object.keys(request).some(key => !allowed.has(key)) || request.type !== "start" ||
      typeof request.prompt !== "string" || !request.prompt.trim() ||
      typeof request.model !== "string" || !request.model.trim() ||
      typeof request.system_prompt !== "string" ||
      typeof request.cwd !== "string" || !isAbsolute(request.cwd) ||
      (request.observe_messages !== undefined && typeof request.observe_messages !== "boolean") ||
      (request.resume !== undefined && (typeof request.resume !== "string" || !request.resume))) {
    throw new Error("invalid_request");
  }
  if (request.functions !== undefined && (!Array.isArray(request.functions) || request.functions.some(tool =>
      !tool || typeof tool.name !== "string" || !tool.name || typeof tool.description !== "string" ||
      !tool.parameters || tool.parameters.type !== "object"))) throw new Error("invalid_request");
  return request as Start;
}

export async function execute(request: Start, emit: (event: Event) => Promise<void>, abort: AbortController, functions = new FunctionBridge(emit), inputs = new Inputs(request.prompt)): Promise<void> {
  if (request.resume && !await getSessionInfo(request.resume, { dir: request.cwd })) {
    await emit({ type: "error", code: "history_unavailable" });
    return;
  }
  const definitions = (request.functions ?? []).map(tool => ({ name: tool.name, description: tool.description, inputSchema: tool.parameters }));
  const names = definitions.map(tool => `mcp__functions__${tool.name}`);
  const mcpServers: Record<string, McpServerConfig> = definitions.length ? { functions: createFunctionServer(definitions, functions.invoke) } : {};
  const children: Promise<number | null>[] = [];
  let result: Extract<Event, { type: "result" }> | undefined;
  let nativeID = "";
  const resultIDs = new Set<string>();
  let failed = false;
  const messages = request.observe_messages ? new MessageObserver() : undefined;
  let stream: ReturnType<typeof query> | undefined;
  try {
    stream = query({
      prompt: inputs,
      options: {
        cwd: request.cwd,
        env: { ...process.env },
        model: request.model,
        systemPrompt: request.system_prompt,
        ...(request.resume ? { resume: request.resume } : {}),
        tools: [], mcpServers, allowedTools: names, strictMcpConfig: true, settingSources: [],
        persistSession: true, includePartialMessages: true, abortController: abort,
        canUseTool: async () => ({ behavior: "deny", message: "Tools are unavailable in this execution profile." }),
        spawnClaudeCodeProcess: options => {
          const child = spawnNative(options);
          children.push(new Promise(resolve => child.once("close", code => resolve(code))));
          return child;
        },
      },
    });
    for await (const message of stream) {
      await functions.consume(message, nativeID);
      if (messages) for (const event of messages.consume(message)) await emit(event);
      if (message.type === "system" && message.subtype === "init") {
        nativeID = message.session_id;
        if (!nativeID || (request.resume && nativeID !== request.resume) || message.tools.length !== names.length || message.tools.some(name => !names.includes(name)) ||
            message.mcp_servers.length !== (definitions.length ? 1 : 0) ||
            message.mcp_servers.some(server => server.name !== "functions" || server.status !== "connected")) {
          throw new Error("unexpected native configuration");
        }
        for (const event of inputs.start(nativeID)) await emit(event);
      } else if (!messages && message.type === "stream_event" && message.parent_tool_use_id === null &&
                 message.event.type === "content_block_delta" && message.event.delta.type === "text_delta") {
        await emit({ type: "delta", delta: message.event.delta.text });
      } else if (message.type === "result") {
        if (!message.uuid || resultIDs.has(message.uuid) || !nativeID || message.session_id !== nativeID) throw new Error("invalid native result identity");
        resultIDs.add(message.uuid);
        await emit({ type: "usage", session_id: nativeID, result_id: message.uuid, usage: resultUsage(message) });
        for (const event of inputs.consume(message)) await emit(event);
        if (message.subtype !== "success" || message.is_error) throw new Error("unsuccessful native result");
        result = { type: "result", session_id: nativeID, text: message.result };
      }
      if (message.type !== "result") for (const event of inputs.consume(message)) await emit(event);
    }
    functions.assertComplete();
  } catch {
    failed = true;
  } finally {
    inputs.close();
    functions.close();
    stream?.close();
    const exits = await Promise.all(children);
    if (!exits.length || exits.some(code => code !== 0)) failed = true;
  }
  if (abort.signal.aborted) await emit({ type: "error", code: "cancelled" });
  else if (failed || !result || !inputs.complete) await emit({ type: "error", code: "execution_failed" });
  else await emit(result);
}
