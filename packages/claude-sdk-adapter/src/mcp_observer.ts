import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import { isDeepStrictEqual } from "node:util";
import type { ToolIdentity } from "./mcp.js";

type Observation = ToolIdentity & {
  kind: "mcp";
  status: "in_progress" | "completed" | "failed" | "incomplete";
  arguments: unknown;
  output: unknown;
  error: unknown;
};
export type MCPEvent = { type: "mcp_observation"; id: string; stage: "before" | "after"; observation: Observation };

export class MCPObserver {
  private readonly calls = new Map<string, Observation>();
  constructor(private readonly tools: Map<string, ToolIdentity>) {}

  consume(message: SDKMessage, sessionID: string): MCPEvent[] {
    if ((message.type !== "assistant" && message.type !== "user") || message.parent_tool_use_id !== null ||
        ("isSynthetic" in message && message.isSynthetic) || ("isReplay" in message && message.isReplay)) return [];
    if (message.session_id !== sessionID || !sessionID) throw new Error("invalid MCP session identity");
    const content = message.message.content;
    if (!Array.isArray(content)) return [];
    const events: MCPEvent[] = [];
    if (message.type === "assistant") {
      if (message.error) return [];
      for (const block of message.message.content) {
        if (block.type !== "tool_use") continue;
        const identity = this.tools.get(block.name);
        if (!identity) continue;
        if (!block.id) throw new Error("missing MCP call identity");
        const previous = this.calls.get(block.id);
        if (previous) {
          if (previous.name !== identity.name || previous.server !== identity.server ||
              !isDeepStrictEqual(previous.arguments, block.input)) throw new Error("conflicting MCP call identity");
          continue;
        }
        const observation: Observation = { ...identity, kind: "mcp", status: "in_progress", arguments: block.input, output: null, error: null };
        this.calls.set(block.id, observation);
        events.push({ type: "mcp_observation", id: block.id, stage: "before", observation: { ...observation } });
      }
    } else {
      const results = content.filter(block => block.type === "tool_result");
      for (const block of results) {
        const observation = this.calls.get(block.tool_use_id);
        if (!observation) continue;
        if (observation.status !== "in_progress") throw new Error("repeated MCP result");
        // This is native tool output, not a reconstructed original MCP envelope.
        const value = results.length === 1 && "tool_use_result" in message && message.tool_use_result !== undefined ?
          message.tool_use_result : block.content ?? null;
        observation.status = block.is_error ? "failed" : "completed";
        if (block.is_error) observation.error = value;
        else observation.output = value;
        events.push({ type: "mcp_observation", id: block.tool_use_id, stage: "after", observation: { ...observation } });
      }
    }
    return events;
  }

  assertComplete(): void {
    if ([...this.calls.values()].some(call => call.status === "in_progress")) throw new Error("unconfirmed MCP result");
  }

  close(): MCPEvent[] {
    const events: MCPEvent[] = [];
    for (const [id, observation] of this.calls) {
      if (observation.status !== "in_progress") continue;
      observation.status = "incomplete";
      events.push({ type: "mcp_observation", id, stage: "after", observation: { ...observation } });
    }
    return events;
  }
}
