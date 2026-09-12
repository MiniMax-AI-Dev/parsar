import type { SpawnOptions } from "@anthropic-ai/claude-agent-sdk";
import { spawn } from "node:child_process";

export function spawnNative(options: SpawnOptions) {
  const child = spawn(options.command, options.args, {
    cwd: options.cwd, env: options.env, signal: options.signal,
    stdio: ["pipe", "pipe", "pipe"],
  });
  // Custom spawners bypass the SDK stderr reader. Drain without exporting diagnostics.
  child.stderr.resume();
  child.on("error", () => {});
  return child;
}
