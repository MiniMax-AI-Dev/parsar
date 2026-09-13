import { Inputs } from "./inputs.js";
import { FunctionBridge } from "./function_bridge.js";
import { createInterface } from "node:readline";
import { execute, parseStart, type Event } from "./adapter.js";

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
  const request = parseStart(first.value);
  const functions = new FunctionBridge(emit);
  const prompts = new Inputs(request.prompt);
  const incoming = (async () => {
    try {
      for await (const line of { [Symbol.asyncIterator]: () => input }) {
        if (Buffer.byteLength(line) > 1024 * 1024) throw new Error("Invalid input.");
        const value: unknown = JSON.parse(line);
        if (value && typeof value === "object" && "type" in value && value.type === "steer") {
          for (const event of prompts.submit(value)) await emit(event);
        } else functions.submit(line);
      }
    }
    catch { abort.abort(); }
  })();
  try { await execute(request, emit, abort, functions, prompts); }
  catch { await emit({ type: "error", code: "execution_failed" }); }
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
