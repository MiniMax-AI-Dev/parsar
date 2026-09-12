import assert from "node:assert/strict";
import { once } from "node:events";
import test from "node:test";
import { spawnNative } from "../dist/native.js";

test("native stderr cannot block stdout or process release", { timeout: 10000 }, async () => {
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), 3000);
  const child = spawnNative({
    command: process.execPath,
    args: ["-e", `process.stderr.write(Buffer.alloc(2 * 1024 * 1024, "x"), () => {
      process.stdout.write("released");
    });`],
    env: process.env,
    signal: abort.signal,
  });
  child.stdin.end();
  let output = "";
  child.stdout.on("data", data => { output += data; });
  try {
    const [code, signal] = await once(child, "close");
    assert.equal(code, 0);
    assert.equal(signal, null);
    assert.equal(output, "released");
  } finally {
    clearTimeout(timer);
    if (child.exitCode === null) child.kill("SIGKILL");
  }
});
