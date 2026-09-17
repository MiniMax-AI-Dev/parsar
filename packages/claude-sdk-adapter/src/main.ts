import { WorkspaceReads } from "./workspace_reads.js";
import { Inputs } from "./inputs.js";
import { FunctionBridge } from "./function_bridge.js";
import { createInterface } from "node:readline";
import { execute, type Event } from "./adapter.js";
import { parseRequest, preparedPrompt } from "./request.js";

const abort = new AbortController();
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
const stop = () => abort.abort();
process.once("SIGTERM", stop);
process.once("SIGINT", stop);
lines.once("close", stop);
const emit = (event: Event): Promise<void> => new Promise((resolve, reject) => {
  process.stdout.write(JSON.stringify(event) + "\n", error => error ? reject(error) : resolve());
});
try {
  const input = lines[Symbol.asyncIterator]();
  const first = await input.next();
  if (first.done || Buffer.byteLength(first.value) > 1024 * 1024) throw new Error("invalid_request");
  const request = parseRequest(first.value);
  let phase = request.type === "prepare" ? "preparing" : "running";
  let invalid = false;
  const output = async (event: Event) => {
    if (event.type === "prepared") phase = "prepared";
    await emit(invalid && (event.type === "error" || event.type === "result") ? { type: "error", code: "invalid_request" } : event);
  };
  const functions = new FunctionBridge(output);
  const reads = new WorkspaceReads(output, abort);
  const prompts = new Inputs(request.type === "start" ? request.prompt : undefined);
  const incoming = (async () => {
    try {
      for await (const line of { [Symbol.asyncIterator]: () => input }) {
        if (Buffer.byteLength(line) > 1024 * 1024) throw new Error("Invalid input.");
        const value: unknown = JSON.parse(line);
        if (value && typeof value === "object" && "type" in value && value.type === "workspace_read") {
          reads.submit(value as Record<string, unknown>);
        } else if (request.type === "prepare" && phase !== "running") {
          if (phase !== "prepared" || abort.signal.aborted) throw new Error("invalid_request");
          prompts.release(preparedPrompt(value));
          phase = "running";
        } else if (value && typeof value === "object" && "type" in value && value.type === "steer") {
          for (const event of prompts.submit(value)) await output(event);
        } else functions.submit(line);
      }
    }
    catch { invalid = request.type === "prepare"; abort.abort(); }
  })();
  try { await execute(request, output, abort, functions, prompts, reads); }
  catch { await output({ type: "error", code: "execution_failed" }); }
  finally {
    prompts.close();
    functions.close();
    lines.removeListener("close", stop);
    lines.close();
    process.stdin.destroy();
    await incoming;
  }
} catch {
  await emit({ type: "error", code: "invalid_request" });
} finally {
  lines.removeListener("close", stop);
  lines.close();
  process.stdin.destroy();
  process.removeListener("SIGTERM", stop);
  process.removeListener("SIGINT", stop);
}
