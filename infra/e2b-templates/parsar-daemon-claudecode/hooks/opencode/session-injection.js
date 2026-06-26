// OpenCode plugin — SessionStart spec+memory injector.
//
// Mirrors hooks/claude/session-start.py for OpenCode: on the first
// chat.message of a session, run `parsar inject snapshot`, prepend the
// spec/memory bundle as system context, and drop /tmp/parsar-cursor so
// per-turn-injection.js knows the delta baseline.
//
// FAIL-OPEN: any failure logs to stderr + /tmp/parsar-hook-failures.log
// and returns "" so the session boots whether or not parsar succeeded.
//
// SCAFFOLD: OpenCode's plugin lifecycle hook names aren't pinned —
// see the default export at the bottom; integration step decides
// which event the runtime emits.

const { spawn } = require("node:child_process");
const { promises: fs } = require("node:fs");

const CURSOR_PATH = "/tmp/parsar-cursor";
const FAILURE_LOG = "/tmp/parsar-hook-failures.log";
const SUBPROCESS_TIMEOUT_MS = 5_000;
const HOOK_LABEL = "SessionStart";

/** UTC RFC3339 with trailing Z — matches parsar CLI / server format. */
function nowRfc3339() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** Best-effort log: never throws. Failure-to-log is silently swallowed
 *  because the alternative would crash the hook on disk-full sandboxes. */
async function logFailure(stage, detail) {
  const line = `[${nowRfc3339()}] ${HOOK_LABEL} ${stage}: ${detail}\n`;
  try {
    process.stderr.write(line);
  } catch (_e) {
    /* drop */
  }
  try {
    await fs.appendFile(FAILURE_LOG, line, "utf-8");
  } catch (_e) {
    /* drop */
  }
}

/** Promise-wrapped `parsar inject snapshot` with a hard timeout. Resolves
 *  the parsed JSON bundle; rejects with a stage/detail string callers
 *  convert to fail-open. */
function runSnapshot() {
  return new Promise((resolve, reject) => {
    let settled = false;
    const child = spawn("parsar", ["inject", "snapshot"], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdoutChunks = [];
    const stderrChunks = [];
    const finish = (fn, arg) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      fn(arg);
    };
    const timer = setTimeout(() => {
      try {
        child.kill("SIGKILL");
      } catch (_e) {
        /* drop */
      }
      finish(reject, `snapshot-timeout: ${SUBPROCESS_TIMEOUT_MS}ms`);
    }, SUBPROCESS_TIMEOUT_MS);
    child.stdout.on("data", (b) => stdoutChunks.push(b));
    child.stderr.on("data", (b) => stderrChunks.push(b));
    child.on("error", (err) => {
      finish(reject, `parsar-spawn-error: ${err && err.message}`);
    });
    child.on("close", (code) => {
      if (code !== 0) {
        const errText = Buffer.concat(stderrChunks).toString("utf-8").trim();
        finish(reject, `parsar-exit ${code}: ${errText.slice(0, 500)}`);
        return;
      }
      try {
        const text = Buffer.concat(stdoutChunks).toString("utf-8");
        finish(resolve, JSON.parse(text));
      } catch (err) {
        finish(reject, `snapshot-parse: ${err && err.message}`);
      }
    });
  });
}

/** Stitch the three blocks in the same order as the Claude hook so the
 *  agent sees identical context across platforms. */
function renderContext(bundle) {
  const parts = [];
  for (const key of ["spec_block", "memory_block", "memory_write_guide"]) {
    const value = bundle && bundle[key];
    if (typeof value === "string" && value.trim()) {
      parts.push(value.trim());
    }
  }
  return parts.join("\n\n");
}

/** Returns a context string (possibly empty) and writes the cursor
 *  file as a side effect. Callers treat the return value as opaque
 *  text to prepend to the system prompt. */
async function injectSessionStart() {
  const cursorTs = nowRfc3339();
  // Write cursor first: even if the snapshot fetch fails, the per-turn
  // hook has a sane baseline and won't retroactively dump history.
  try {
    await fs.writeFile(CURSOR_PATH, cursorTs, "utf-8");
  } catch (err) {
    await logFailure("write-cursor", String(err));
  }

  let bundle;
  try {
    bundle = await runSnapshot();
  } catch (detail) {
    await logFailure("snapshot-error", String(detail));
    return "";
  }
  return renderContext(bundle);
}

// OpenCode plugin convention: export a factory as default. The hook
// name guesses (onChatStart/onSessionStart) are scaffold pending
// OpenCode contract confirmation.
module.exports = {
  default: async function parsarSessionInjectionPlugin(_ctx) {
    return {
      name: "parsar.session-injection",
      onChatStart: injectSessionStart,
      onSessionStart: injectSessionStart,
    };
  },
  // Exported for direct call by the connector / AGENTS.md generator
  // that wants to reuse this rendering without booting a real plugin.
  injectSessionStart,
  renderContext,
  __internal: { runSnapshot, nowRfc3339 },
};
