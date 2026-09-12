import type { SDKResultMessage } from "@anthropic-ai/claude-agent-sdk";

export type NativeUsage = Pick<SDKResultMessage, "usage" | "modelUsage" | "total_cost_usd" | "subtype" | "is_error">;

// Keep the SDK scopes and estimate provenance intact. This is not public token accounting.
export function resultUsage(message: SDKResultMessage): NativeUsage {
  return structuredClone({ usage: message.usage, modelUsage: message.modelUsage,
    total_cost_usd: message.total_cost_usd, subtype: message.subtype, is_error: message.is_error });
}
