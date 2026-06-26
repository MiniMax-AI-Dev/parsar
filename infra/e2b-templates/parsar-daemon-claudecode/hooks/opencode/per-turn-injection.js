// OpenCode plugin — per-turn memory delta injector.
//
// Mirrors hooks/claude/user-prompt-submit.py: on each user message,
// read /tmp/parsar-cursor, ask `parsar inject incremental` for memories
// created after that point, advance the cursor, and return the delta.
//
// FAIL-OPEN: failures log to stderr + /tmp/parsar-hook-failures.log and
// return "" so the turn proceeds.
//
// Missing cursor → silent no-op (expected when SessionStart itself
// failed-open). Per-turn pulling the full history retroactively would
// defeat the point.

const { spawn } = require("node:child_process");
const { promises: fs } = require("node:fs");

const CURSOR_PATH = "/tmp/parsar-cursor";
const FAILURE_LOG = "/tmp/parsar-hook-failures.log";
const SUBPROCESS_TIMEOUT_MS = 5_000;
const HOOK_LABEL = "UserPromptSubmit";

// Same regex as the Python side so a malformed cursor (partial write
// from a crashed SessionStart) is caught before invoking parsar.
const RFC3339_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

function nowRfc3339() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

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

async function readCursor() {
  let raw;
  try {
    raw = (await fs.readFile(CURSOR_PATH, "utf-8")).trim();
  } catch (err) {
    if (err && err.code === "ENOENT") {
      return null;
    }
    await logFailure("read-cursor", String(err));
    return null;
  }
  if (!RFC3339_RE.test(raw)) {
    await logFailure("cursor-malformed", raw.slice(0, 120));
    return null;
  }
  return raw;
}

function runIncremental(since) {
  return new Promise((resolve, reject) => {
    let settled = false;
    const child = spawn("parsar", ["inject", "incremental", "--since", since], {
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
      finish(reject, `incremental-timeout: ${SUBPROCESS_TIMEOUT_MS}ms`);
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
        finish(reject, `incremental-parse: ${err && err.message}`);
      }
    });
  });
}

/** Returns the incremental text to prepend to this turn (possibly
 *  empty). Cursor is advanced BEFORE the fetch — same rationale as the
 *  Python hook: avoid double-pulling the same window if the call
 *  takes longer than the user's typing speed. */
async function injectPerTurn() {
  const since = await readCursor();
  if (since == null) {
    return "";
  }

  try {
    await fs.writeFile(CURSOR_PATH, nowRfc3339(), "utf-8");
  } catch (err) {
    await logFailure("write-cursor", String(err));
    // Worst case: the next turn re-pulls the same window. Still
    // better than failing the whole hook over a cursor write.
  }

  let bundle;
  try {
    bundle = await runIncremental(since);
  } catch (detail) {
    await logFailure("incremental-error", String(detail));
    return "";
  }

  const delta = bundle && bundle.incremental_memory;
  return typeof delta === "string" ? delta.trim() : "";
}

// Hook event names (onChatMessage / onUserPromptSubmit) are scaffold
// pending OpenCode contract confirmation.
module.exports = {
  default: async function parsarPerTurnInjectionPlugin(_ctx) {
    return {
      name: "parsar.per-turn-injection",
      onChatMessage: injectPerTurn,
      onUserPromptSubmit: injectPerTurn,
    };
  },
  injectPerTurn,
  __internal: { runIncremental, readCursor, nowRfc3339 },
};
