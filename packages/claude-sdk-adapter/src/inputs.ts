import type { SDKMessage, SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { randomUUID } from "node:crypto";

export type InputEvent =
  | { type: "input_ready" | "input_closed"; session_id: string }
  | { type: "input_applied" | "input_rejected"; input_id: string };
type Input = { id?: string; applied: boolean; completed: boolean };

// This is an SDK input iterator and receipt ledger, never a model/tool loop.
export class Inputs implements AsyncIterable<SDKUserMessage> {
  private readonly submitted = new Map<string, Input>();
  private readonly ids = new Set<string>();
  private readonly queue: SDKUserMessage[] = [];
  private wake?: () => void;
  private ended = false;
  private sessionID = "";

  constructor(prompt: string) { this.enqueue(prompt); }

  start(sessionID: string): InputEvent[] {
    if (this.ended || !sessionID || this.sessionID && this.sessionID !== sessionID) throw new Error("Invalid input session identity.");
    // Native streaming queries initialize each native turn within the same Session.
    if (this.sessionID) return [];
    this.sessionID = sessionID;
    return [{ type: "input_ready", session_id: sessionID }];
  }

  submit(value: unknown): InputEvent[] {
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid input.");
    const input = value as Record<string, unknown>;
    if (input.type !== "steer" || Object.keys(input).some(key => !["type", "input_id", "text"].includes(key)) ||
        typeof input.input_id !== "string" || !input.input_id.trim() || input.input_id.length > 256 ||
        typeof input.text !== "string" || !input.text.trim()) throw new Error("Invalid input.");
    // The native consumed-UUID list has 64 slots, including the opening prompt.
    if (this.ended || !this.sessionID || this.submitted.size >= 64 || this.ids.has(input.input_id)) {
      return [{ type: "input_rejected", input_id: input.input_id }];
    }
    this.ids.add(input.input_id);
    this.enqueue(input.text, input.input_id);
    return [];
  }

  private enqueue(text: string, id?: string): void {
    const uuid = randomUUID();
    this.submitted.set(uuid, { id, applied: false, completed: false });
    this.queue.push({ type: "user", uuid, session_id: this.sessionID, parent_tool_use_id: null,
      message: { role: "user", content: text } });
    this.wake?.();
  }

  consume(message: SDKMessage): InputEvent[] {
    if (message.type !== "assistant" && message.type !== "stream_event" && message.type !== "result") return [];
    if (("parent_tool_use_id" in message && message.parent_tool_use_id !== null) ||
        ("isReplay" in message && message.isReplay) || ("isSynthetic" in message && message.isSynthetic)) return [];
    if (message.session_id !== this.sessionID || !this.sessionID) throw new Error("Invalid consumed-input session.");
    const uuids = message.user_message_uuids ?? (message.user_message_uuid ? [message.user_message_uuid] : []);
    const consumed = uuids.flatMap(uuid => this.submitted.has(uuid) ? [this.submitted.get(uuid)!] : []);
    if (message.type === "result" && !consumed.length) throw new Error("Unattributed native result.");
    const receipts: InputEvent[] = [];
    for (const input of consumed) {
      if (!input.applied && input.id) receipts.push({ type: "input_applied", input_id: input.id });
      input.applied = true;
      if (message.type === "result") input.completed = true;
    }
    if (message.type === "result" && this.complete) {
      this.close();
      // Close admission before releasing receipt waiters at the final result.
      receipts.unshift({ type: "input_closed", session_id: this.sessionID });
    }
    return receipts;
  }

  get complete(): boolean { return [...this.submitted.values()].every(input => input.completed); }

  close(): void { this.ended = true; this.wake?.(); }

  async *[Symbol.asyncIterator](): AsyncIterator<SDKUserMessage> {
    while (true) {
      if (this.queue.length) yield this.queue.shift()!;
      else if (this.ended) return;
      else await new Promise<void>(resolve => { this.wake = resolve; });
    }
  }
}
