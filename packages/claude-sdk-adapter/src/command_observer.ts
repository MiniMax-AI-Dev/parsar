import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import { isDeepStrictEqual } from "node:util";

type Observation = {
  kind: "command";
  status: "in_progress" | "completed" | "failed" | "incomplete";
  command: string;
  output?: string;
};
type Call = { observation: Observation; input: unknown; result?: unknown };
export type CommandEvent = {
  type: "command_observation"; session_id: string; id: string;
  stage: "before" | "after"; observation: Observation;
};

export class CommandObserver {
  private readonly calls = new Map<string, Call>();
  private readonly otherCalls = new Set<string>();
  private sessionID = "";

  *consume(message: SDKMessage, sessionID: string, hasInput: boolean): Generator<CommandEvent, void> {
    if ((message.type !== "assistant" && message.type !== "user") || message.parent_tool_use_id !== null ||
        ("isSynthetic" in message && message.isSynthetic) || ("isReplay" in message && message.isReplay)) return;
    if (!hasInput || !sessionID || message.session_id !== sessionID || this.sessionID && this.sessionID !== sessionID) {
      throw new Error("invalid command session identity");
    }
    this.sessionID = sessionID;
    const content = message.message.content;
    if (!Array.isArray(content)) return;
    if (message.type === "assistant") {
      if (message.error) return;
      for (const block of message.message.content) {
        if (block.type !== "tool_use") continue;
        if (block.name !== "Bash") {
          if (this.calls.has(block.id)) throw new Error("conflicting command call identity");
          this.otherCalls.add(block.id);
          continue;
        }
        const input = block.input;
        if (typeof block.id !== "string" || !block.id || !input || typeof input !== "object" || Array.isArray(input) ||
            !("command" in input) || typeof input.command !== "string" || !input.command.trim()) {
          throw new Error("invalid native command call");
        }
        if (this.otherCalls.has(block.id)) throw new Error("conflicting command call identity");
        const previous = this.calls.get(block.id);
        if (previous) {
          if (!isDeepStrictEqual(previous.input, input)) throw new Error("conflicting command call identity");
          continue;
        }
        const observation: Observation = { kind: "command", status: "in_progress", command: input.command };
        this.calls.set(block.id, { observation, input });
        yield this.event(block.id, "before", observation);
      }
    } else {
      const results = content.filter(block => block.type === "tool_result");
      for (const block of results) {
        const call = this.calls.get(block.tool_use_id);
        if (!call) continue;
        if (block.is_error !== undefined && typeof block.is_error !== "boolean") throw new Error("invalid command result status");
        const native = results.length === 1 ? message.tool_use_result : undefined;
        const result = { content: block.content, is_error: !!block.is_error, native };
        if (call.result !== undefined) {
          if (!isDeepStrictEqual(call.result, result)) throw new Error("conflicting command result");
          continue;
        }
        if (call.observation.status !== "in_progress") throw new Error("command result followed closure");
        let interrupted = false;
        if (native && typeof native === "object" && !Array.isArray(native)) {
          if ("backgroundTaskId" in native || "timedOutAfterMs" in native ||
              ("backgroundedByUser" in native && native.backgroundedByUser === true)) {
            throw new Error("unexpected background command");
          }
          if ("interrupted" in native) {
            if (typeof native.interrupted !== "boolean" || !("stdout" in native) || typeof native.stdout !== "string" ||
                !("stderr" in native) || typeof native.stderr !== "string") throw new Error("invalid command interruption evidence");
            interrupted = native.interrupted;
          }
        }
        const observation: Observation = { ...call.observation,
          status: interrupted ? "incomplete" : block.is_error ? "failed" : "completed" };
        // Preserve native per-call text; separate streams do not establish interleaving.
        if (typeof block.content === "string") observation.output = block.content;
        else if (Array.isArray(block.content) && block.content.length === 1 && block.content[0]?.type === "text" &&
                 typeof block.content[0].text === "string") {
          observation.output = block.content[0].text;
        }
        call.observation = observation;
        call.result = result;
        yield this.event(block.tool_use_id, "after", observation);
      }
    }
  }

  assertComplete(): void {
    if ([...this.calls.values()].some(call => call.observation.status === "in_progress")) throw new Error("unconfirmed command result");
  }

  close(): CommandEvent[] {
    const events: CommandEvent[] = [];
    for (const [id, call] of this.calls) {
      if (call.observation.status !== "in_progress") continue;
      call.observation = { ...call.observation, status: "incomplete" };
      events.push(this.event(id, "after", call.observation));
    }
    return events;
  }

  private event(id: string, stage: "before" | "after", observation: Observation): CommandEvent {
    return { type: "command_observation", session_id: this.sessionID, id, stage, observation: { ...observation } };
  }
}
