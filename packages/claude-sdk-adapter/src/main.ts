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
  const first = await lines[Symbol.asyncIterator]().next();
  if (first.done || Buffer.byteLength(first.value) > 1024 * 1024) throw new Error("invalid_request");
  const request = parseStart(first.value);
  try { await execute(request, emit, abort); }
  catch { await emit({ type: "error", code: "execution_failed" }); }
} catch {
  await emit({ type: "error", code: "invalid_request" });
} finally {
  lines.removeListener("close", stop);
  lines.close();
  process.stdin.destroy();
  process.removeListener("SIGTERM", stop);
  process.removeListener("SIGINT", stop);
}
