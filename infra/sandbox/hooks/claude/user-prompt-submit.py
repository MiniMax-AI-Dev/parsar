#!/usr/bin/env python3
"""Claude Code UserPromptSubmit hook — fetch only the memory delta
(memories written since the cursor stored at SessionStart) and emit it
as additionalContext.

Contract (Claude Code hook protocol):
  - stdin: JSON event payload — drained but unused (snapshot scope is
    determined server-side from the runtime credential, not the event)
  - stdout: single JSON object
      {"hookSpecificOutput": {
         "hookEventName": "UserPromptSubmit",
         "additionalContext": "..."
      }}
  - exit code: ALWAYS 0. Per plan §9 risk #1, fail-open semantics:
    cursor missing / network error / parsar failure → empty additionalContext
    + audit log. Better to miss a few minutes of incremental memory than
    to break the user's prompt with a hook error banner.

Cursor protocol (paired with session-start.py):
  - SessionStart writes /tmp/parsar-cursor with the start timestamp
  - This hook reads it, asks `parsar inject incremental --since <ts>` for
    the delta, then OVERWRITES the cursor with "now" so the next turn
    only sees newer rows. If the file is missing (e.g. SessionStart
    hook itself failed) we skip the fetch — emitting an empty context
    is safer than asking for the whole history retroactively.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
import traceback
from datetime import datetime, timezone
from pathlib import Path

CURSOR_PATH = Path("/tmp/parsar-cursor")
FAILURE_LOG = Path("/tmp/parsar-hook-failures.log")
SUBPROCESS_TIMEOUT_S = 5
HOOK_EVENT = "UserPromptSubmit"

# Cheap RFC3339 guard — `parsar inject incremental --since` rejects non-
# conforming strings, but parsing here lets us short-circuit with a
# clear log line if the cursor file is corrupt (e.g. partial write).
RFC3339_RE = re.compile(
    r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$"
)


def now_rfc3339() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def log_failure(stage: str, detail: str) -> None:
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


def read_cursor() -> str | None:
    """Return the cursor timestamp, or None if the file is absent /
    unreadable / malformed. None means 'skip this turn's incremental
    fetch' — the user's prompt still goes through, just without delta."""
    try:
        raw = CURSOR_PATH.read_text(encoding="utf-8").strip()
    except FileNotFoundError:
        # SessionStart probably never fired or already failed-open. Not
        # an error worth flagging on every turn — log once at debug
        # equivalent (we just no-op).
        return None
    except Exception as exc:  # noqa: BLE001
        log_failure("read-cursor", repr(exc))
        return None
    if not RFC3339_RE.match(raw):
        log_failure("cursor-malformed", repr(raw[:120]))
        return None
    return raw


def write_cursor(ts: str) -> None:
    try:
        CURSOR_PATH.write_text(ts, encoding="utf-8")
    except Exception as exc:  # noqa: BLE001
        log_failure("write-cursor", repr(exc))


def fetch_incremental(since: str) -> dict:
    proc = subprocess.run(
        ["parsar", "inject", "incremental", "--since", since],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        timeout=SUBPROCESS_TIMEOUT_S,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"parsar exit {proc.returncode}: {proc.stderr.strip()[:500]}"
        )
    return json.loads(proc.stdout)


def emit(additional_context: str) -> None:
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
    # Drain stdin for the same reason as session-start.py.
    try:
        sys.stdin.read()
    except Exception:
        pass

    since = read_cursor()
    if since is None:
        # No cursor → no incremental. SessionStart will write one on the
        # next session; this turn just runs without delta injection.
        emit("")
        return 0

    # Advance the cursor BEFORE the fetch so a slow server doesn't cause
    # us to re-pull the same window twice if the user hits enter again
    # mid-fetch. Worst case: we miss a memory written in the
    # microseconds between read and write — acceptable.
    write_cursor(now_rfc3339())

    try:
        bundle = fetch_incremental(since)
    except subprocess.TimeoutExpired:
        log_failure("incremental-timeout", f"timed out after {SUBPROCESS_TIMEOUT_S}s")
        emit("")
        return 0
    except FileNotFoundError as exc:
        log_failure("parsar-not-found", repr(exc))
        emit("")
        return 0
    except Exception as exc:  # noqa: BLE001
        log_failure("incremental-error", f"{exc!r}\n{traceback.format_exc()}")
        emit("")
        return 0

    # The server returns the full Injection struct on the incremental
    # endpoint too, but only IncrementalMemory is meant to be appended
    # mid-session (spec/memory snapshots are already in the agent's
    # context from SessionStart). Empty string → empty additionalContext
    # → Claude treats this as a no-op turn-modifier.
    delta = bundle.get("incremental_memory")
    if not isinstance(delta, str):
        delta = ""
    emit(delta.strip())
    return 0


if __name__ == "__main__":
    sys.exit(main())
