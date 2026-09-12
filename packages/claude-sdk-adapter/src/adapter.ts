import { getSessionInfo, query } from "@anthropic-ai/claude-agent-sdk";
import { spawnNative } from "./native.js";
import { isAbsolute } from "node:path";

export type Start = {
  type: "start";
  prompt: string;
  model: string;
  system_prompt: string;
  cwd: string;
  resume?: string;
};
export type Event =
  | { type: "delta"; delta: string }
  | { type: "result"; session_id: string; text: string }
  | { type: "error"; code: "invalid_request" | "history_unavailable" | "execution_failed" | "cancelled" };

export function parseStart(line: string): Start {
  const value: unknown = JSON.parse(line);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const request = value as Record<string, unknown>;
  const allowed = new Set(["type", "prompt", "model", "system_prompt", "cwd", "resume"]);
  if (Object.keys(request).some(key => !allowed.has(key)) || request.type !== "start" ||
      typeof request.prompt !== "string" || !request.prompt.trim() ||
      typeof request.model !== "string" || !request.model.trim() ||
      typeof request.system_prompt !== "string" ||
      typeof request.cwd !== "string" || !isAbsolute(request.cwd) ||
      (request.resume !== undefined && (typeof request.resume !== "string" || !request.resume))) {
    throw new Error("invalid_request");
  }
  return request as Start;
}

export async function execute(request: Start, emit: (event: Event) => Promise<void>, abort: AbortController): Promise<void> {
  if (request.resume && !await getSessionInfo(request.resume, { dir: request.cwd })) {
    await emit({ type: "error", code: "history_unavailable" });
    return;
  }
  const children: Promise<number | null>[] = [];
  let result: Extract<Event, { type: "result" }> | undefined;
  let nativeID = "";
  let failed = false;
  let stream: ReturnType<typeof query> | undefined;
  try {
    stream = query({
      prompt: request.prompt,
      options: {
        cwd: request.cwd,
        env: { ...process.env },
        model: request.model,
        systemPrompt: request.system_prompt,
        ...(request.resume ? { resume: request.resume } : {}),
        tools: [], mcpServers: {}, strictMcpConfig: true, settingSources: [],
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
      if (message.type === "system" && message.subtype === "init") {
        nativeID = message.session_id;
        if (!nativeID || (request.resume && nativeID !== request.resume) || message.tools.length || message.mcp_servers.length) {
          throw new Error("unexpected native configuration");
        }
      } else if (message.type === "stream_event" && message.parent_tool_use_id === null &&
                 message.event.type === "content_block_delta" && message.event.delta.type === "text_delta") {
        await emit({ type: "delta", delta: message.event.delta.text });
      } else if (message.type === "result") {
        if (result || message.subtype !== "success" || message.is_error || !nativeID || message.session_id !== nativeID) {
          throw new Error("unsuccessful native result");
        }
        result = { type: "result", session_id: nativeID, text: message.result };
      }
    }
  } catch {
    failed = true;
  } finally {
    stream?.close();
    const exits = await Promise.all(children);
    if (!exits.length || exits.some(code => code !== 0)) failed = true;
  }
  if (abort.signal.aborted) await emit({ type: "error", code: "cancelled" });
  else if (failed || !result) await emit({ type: "error", code: "execution_failed" });
  else await emit(result);
}
