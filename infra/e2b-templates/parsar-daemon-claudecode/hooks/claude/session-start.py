#!/usr/bin/env python3
"""Claude Code SessionStart hook — fetch the spec+memory snapshot from the
parsar server via `parsar inject snapshot` and emit it as additionalContext.

Contract (Claude Code hook protocol):
  - stdin: JSON event payload (we ignore the body but must drain it so
    Claude's pipe doesn't EPIPE on the producer side)
  - stdout: a single JSON object
      {"hookSpecificOutput": {
         "hookEventName": "SessionStart",
         "additionalContext": "<spec>...</spec>\\n\\n<memory>...</memory>..."
      }}
  - exit code: ALWAYS 0. Per plan §9 risk #1 the hook is fail-open: any
    error (network down, parsar CLI missing, server 5xx) → empty
    additionalContext + audit log + warning to stderr. The session must
    boot whether or not we managed to inject. Exiting non-zero would put
    Claude in "hook error" state and surface a scary banner to the user
    for what is, by design, a soft dependency.

Side effects:
  - Writes /tmp/parsar-cursor (UTC RFC3339 timestamp) so the per-turn hook
    knows the "since" point for incremental fetches. If the snapshot
    itself failed we still write the cursor so the next per-turn doesn't
    pull a giant retroactive delta.
  - Appends to /tmp/parsar-hook-failures.log on any error path. Operators
    can `parsar-daemon` / e2b exec to inspect this; sandbox is short-lived
    so a flat file is enough (no log rotation needed).
"""

from __future__ import annotations

import json
import subprocess
import sys
import traceback
from datetime import datetime, timezone
from pathlib import Path

CURSOR_PATH = Path("/tmp/parsar-cursor")
FAILURE_LOG = Path("/tmp/parsar-hook-failures.log")
SUBPROCESS_TIMEOUT_S = 5
HOOK_EVENT = "SessionStart"


def now_rfc3339() -> str:
    """UTC RFC3339 with 'Z' suffix — matches what the parsar CLI expects on
    `--since` and what the server emits in created_at fields."""
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def log_failure(stage: str, detail: str) -> None:
    """Best-effort: stderr (Claude surfaces this in hook logs) + a flat
    file inside the sandbox. Never raises — if even the log write fails
    we have to give up silently rather than crash the hook."""
    line = f"[{now_rfc3339()}] {HOOK_EVENT} {stage}: {detail}\n"
    try:
        sys.stderr.write(line)
    except Exception:
        pass
    try:
        with FAILURE_LOG.open("a", encoding="utf-8") as fh:
            fh.write(line)
    except Exception:
        pass


def write_cursor(ts: str) -> None:
    """Write the cursor file. Failure here is non-fatal: per-turn will
    just skip the incremental fetch if it can't read the cursor, which
    is the same fail-open posture."""
    try:
        CURSOR_PATH.write_text(ts, encoding="utf-8")
    except Exception as exc:  # noqa: BLE001
        log_failure("write-cursor", repr(exc))


def fetch_snapshot() -> dict:
    """Run `parsar inject snapshot` and return the parsed JSON.
    Raises on timeout, non-zero exit, or malformed JSON — the caller
    converts those to fail-open empty context."""
    proc = subprocess.run(
        ["parsar", "inject", "snapshot"],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        timeout=SUBPROCESS_TIMEOUT_S,
        check=False,
    )
    if proc.returncode != 0:
        # parsar CLI prints structured errors to stderr; include the first
        # ~500 chars so the audit log is greppable.
        raise RuntimeError(
            f"parsar exit {proc.returncode}: {proc.stderr.strip()[:500]}"
        )
    return json.loads(proc.stdout)


def render_context(bundle: dict) -> str:
    """Concatenate the three SessionStart blocks with blank-line
    separators. Order matches the server's prompts.go intent: spec first
    (project rules), then memory (recall), then the write guide
    (instructions to the agent on what to save back)."""
    parts = []
    for key in ("spec_block", "memory_block", "memory_write_guide"):
        value = bundle.get(key)
        if isinstance(value, str) and value.strip():
            parts.append(value.strip())
    return "\n\n".join(parts)


def emit(additional_context: str) -> None:
    """Write the Claude-Code hook output JSON to stdout. Single line so
    Claude's parser doesn't get confused by trailing whitespace."""
    payload = {
        "hookSpecificOutput": {
            "hookEventName": HOOK_EVENT,
            "additionalContext": additional_context,
        }
    }
    json.dump(payload, sys.stdout, ensure_ascii=False)
    sys.stdout.write("\n")
    sys.stdout.flush()


def main() -> int:
    # Drain stdin so the hook event JSON doesn't get re-buffered to the
    # next process in the pipe. We never look at the body — the snapshot
    # is workspace-scoped via runtime credentials, not event-scoped.
    try:
        sys.stdin.read()
    except Exception:
        pass

    # Always advance the cursor first; if the snapshot fetch fails the
    # next per-turn still has a sane baseline (no giant retroactive
    # delta on first user message).
    cursor_ts = now_rfc3339()
    write_cursor(cursor_ts)

    try:
        bundle = fetch_snapshot()
    except subprocess.TimeoutExpired:
        log_failure("snapshot-timeout", f"timed out after {SUBPROCESS_TIMEOUT_S}s")
        emit("")
        return 0
    except FileNotFoundError as exc:
        # `parsar` not on PATH — image build broken. Log loudly so build
        # smoke tests can catch it, but still let the session boot.
        log_failure("parsar-not-found", repr(exc))
        emit("")
        return 0
    except Exception as exc:  # noqa: BLE001
        log_failure("snapshot-error", f"{exc!r}\n{traceback.format_exc()}")
        emit("")
        return 0

    context = render_context(bundle)
    emit(context)
    return 0


if __name__ == "__main__":
    sys.exit(main())
