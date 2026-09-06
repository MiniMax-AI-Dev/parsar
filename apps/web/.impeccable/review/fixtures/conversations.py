# Fixtures for ?admin=conversations: agents, the conversation list scoped to
# an agent, one conversation + timeline (messages, a completed run and a
# running run with tool steps), one pending permission interaction with a
# deadline (the approval bar), and the run's SSE stream so the live trace
# replays its steps. Globals WS, NOW, iso are injected by mock-api.py.
import importlib.util
import json
import os
import re
import sys
import time
from datetime import datetime, timedelta, timezone
from http.server import ThreadingHTTPServer
from urllib.parse import urlparse

# Wins the /interactions and /agents routes over team.py / agents.py.
PRIORITY = 2


def ago(**kw):
    return iso(NOW - timedelta(**kw))


# The browser's clock, not the frozen NOW: the trace's elapsed counter and the
# approval countdown are computed against Date.now().
def real_ago(**kw):
    return iso(datetime.now(timezone.utc) - timedelta(**kw))


def real_in(**kw):
    return iso(datetime.now(timezone.utc) + timedelta(**kw))


AGENTS = [
    {"id": "9b1c2d3e-4f50-4a61-8b72-c83d94e5f601", "name": "reviewer-bot", "slug": "reviewer-bot",
     "description": "Reviews pull requests and leaves inline comments", "connector_type": "agent_daemon",
     "runtime": "local", "config": {"daemon_mode": "local"}},
    {"id": "9b1c2d3e-4f50-4a61-8b72-c83d94e5f602", "name": "release-notes", "slug": "release-notes",
     "description": "Drafts release notes from merged PRs", "connector_type": "agent_daemon",
     "runtime": "local", "config": {"daemon_mode": "local"}},
    {"id": "9b1c2d3e-4f50-4a61-8b72-c83d94e5f603", "name": "docs-writer", "slug": "docs-writer",
     "description": "Keeps the docs folder in sync with the code", "connector_type": "http-agent",
     "runtime": "local", "config": {}},
]
for a in AGENTS:
    a.update({"workspace_id": WS, "status": "active", "visibility": "workspace",
              "created_at": ago(days=12), "updated_at": ago(hours=3)})



def _sibling_agents():
    """agents.py (if present) loads before this file and wins /agents, so bind
    conversations to its agents rather than to the local stand-ins."""
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "agents.py")
    if not os.path.exists(path):
        return []
    spec = importlib.util.spec_from_file_location("fixture_agents_peek", path)
    mod = importlib.util.module_from_spec(spec)
    mod.WS, mod.NOW, mod.iso = WS, NOW, iso
    try:
        spec.loader.exec_module(mod)
        for pattern, handler in getattr(mod, "ROUTES", []):
            if pattern.endswith("/agents$"):
                _, body = handler(re.match(pattern, f"/api/v1/workspaces/{WS}/agents"), {})
                return [a for a in body.get("agents", []) if a.get("status") == "active"]
    except Exception:  # noqa: BLE001
        return []
    return []


_peer = _sibling_agents()
if _peer:
    AGENTS = _peer + [a for a in AGENTS if a["name"] not in {x.get("name") for x in _peer}]

R = AGENTS[0]["id"]
CONVS = [
    {"id": "5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a01", "title": "Review PR #257 fix/claude-home-workdir",
     "agent": R, "message_count": 3, "last": ago(minutes=9), "created": ago(minutes=38),
     "preview": "Add the missing test and push it to the branch."},
    {"id": "5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a02", "title": "Why does make check-web fail on CI?",
     "agent": R, "message_count": 10, "last": ago(hours=2), "created": ago(hours=3),
     "preview": "The typography plugin is missing from devDependencies."},
    {"id": "5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a03", "title": "对比 sandbox 与 local 两种运行模式的差异",
     "agent": R, "message_count": 2, "last": ago(days=1, hours=5), "created": ago(days=1, hours=5),
     "preview": "sandbox 模式在 Docker 中运行，local 模式直接使用开发机。"},
    {"id": "5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a04", "title": "Draft notes for v0.9.3",
     "agent": AGENTS[1]["id"] if len(AGENTS) > 1 else R, "message_count": 2, "last": ago(days=2), "created": ago(days=2),
     "preview": "Here is the draft grouped by area."},
]


def conv_row(c):
    return {
        "id": c["id"], "workspace_id": WS, "surface": "web", "form": "thread", "title": c["title"],
        "status": "active", "metadata": {"primary_agent_id": c["agent"]}, "primary_agent_id": c["agent"],
        "primary_agent_name": next(a["name"] for a in AGENTS if a["id"] == c["agent"]),
        "created_at": c["created"], "updated_at": c["last"],
        "message_count": c["message_count"], "last_message_at": c["last"],
        "last_message_preview": c["preview"], "last_message_sender_type": "agent",
    }


C1 = CONVS[0]["id"]
RUN_OK = "run_01J8ZC0NV1K7Q2"
RUN_PENDING = "run_01J8ZC0NV1K7Q3"
MSGS = [
    {"id": "msg_01J8ZC01", "sender_type": "user", "kind": "message", "created_at": ago(minutes=38),
     "content": "Review PR #257 (fix/claude-home-workdir). Focus on the HOME fallback and whether the daemon still works when the workdir is unset."},
    {"id": "msg_01J8ZC02", "sender_type": "agent", "kind": "message", "created_at": ago(minutes=31),
     "content": "I read the diff and ran the daemon tests.\n\nThe fallback to `$HOME/.parsar` is correct, but `resolveWorkdir` returns an empty string when `HOME` is also unset, and the caller then calls `os.Chdir(\"\")`. That path has no test.\n\nLeft three comments on the PR: one blocking (the empty-string case), two nits about log wording."},
    {"id": "msg_01J8ZC03", "sender_type": "user", "kind": "message", "created_at": ago(minutes=9),
     "content": "Add the missing test and push it to the branch."},
]
for m in MSGS:
    m.update({"conversation_id": C1, "content_format": "markdown"})

# A longer thread for the turn rail: five user turns, each answered.
C2 = CONVS[1]["id"]
C2_TURNS = [
    ("Why does make check-web fail on CI? It passes locally.",
     "CI runs `pnpm install --frozen-lockfile`; the lockfile is behind package.json, so the install step exits 1 before tsc runs."),
    ("Which package is out of sync?",
     "`@tailwindcss/typography` was added to package.json in 853b744 but `pnpm-lock.yaml` was not regenerated."),
    ("为什么本地不会失败？",
     "本地的 `pnpm install` 没有加 `--frozen-lockfile`，会静默更新 lockfile；CI 上则直接报错。"),
    ("Fix it and open a PR.",
     "Regenerated the lockfile and opened #266 (chore: sync pnpm-lock for typography plugin). CI is green."),
    ("Also add the typography plugin to devDependencies instead of dependencies.",
     "Moved it: the plugin is only used at build time. Pushed to the same branch; #266 updated."),
]
C2_MSGS = []
for i, (q, a) in enumerate(C2_TURNS):
    base = 170 - i * 30
    C2_MSGS.append({"id": f"msg_01J8ZD{i:02d}u", "conversation_id": C2, "sender_type": "user", "kind": "message",
                    "content_format": "markdown", "content": q, "created_at": ago(minutes=base)})
    C2_MSGS.append({"id": f"msg_01J8ZD{i:02d}a", "conversation_id": C2, "sender_type": "agent", "kind": "message",
                    "content_format": "markdown", "content": a, "created_at": ago(minutes=base - 4)})
C2_RUNS = [
    {"id": f"run_01J8ZD{i:02d}", "status": "completed", "agent_name": "reviewer-bot", "agent_slug": "reviewer-bot",
     "connector_type": "agent_daemon", "trigger_message_id": f"msg_01J8ZD{i:02d}u", "output_message_id": f"msg_01J8ZD{i:02d}a",
     "created_at": ago(minutes=170 - i * 30), "started_at": ago(minutes=170 - i * 30), "finished_at": ago(minutes=166 - i * 30),
     "steps": [
         {"tool_call_id": f"tc_d{i}_1", "name": "bash", "status": "completed", "occurred_at": ago(minutes=169 - i * 30),
          "args": {"command": "gh run view --log-failed"}, "result": {"exit_code": 0}},
         {"tool_call_id": f"tc_d{i}_2", "name": "read", "status": "completed", "occurred_at": ago(minutes=168 - i * 30),
          "args": {"file_path": "apps/web/package.json"}, "result": {"bytes": 1810}},
     ]}
    for i in range(len(C2_TURNS))
]

STEPS_OK = [
    {"tool_call_id": "tc_1", "name": "bash", "status": "completed", "occurred_at": ago(minutes=36),
     "args": {"command": "gh pr diff 257 --color=never"}, "result": {"exit_code": 0}},
    {"tool_call_id": "tc_2", "name": "read", "status": "completed", "occurred_at": ago(minutes=35),
     "args": {"file_path": "internal/daemon/workdir.go"}, "result": {"bytes": 2410}},
    {"tool_call_id": "tc_3", "name": "bash", "status": "completed", "occurred_at": ago(minutes=33),
     "args": {"command": "go test ./internal/daemon/... -run Workdir -count=1"}, "result": {"exit_code": 0}},
    {"tool_call_id": "tc_4", "name": "grep", "status": "completed", "occurred_at": ago(minutes=32),
     "args": {"pattern": "os.Chdir", "path": "internal/"}, "result": {"matches": 3}},
]
RUN_STARTED = real_ago(minutes=3, seconds=12)
STEPS_PENDING = [
    {"tool_call_id": "tc_5", "name": "read", "status": "completed", "occurred_at": real_ago(minutes=3, seconds=5),
     "args": {"file_path": "internal/daemon/workdir.go"}, "result": {"bytes": 2410}},
    {"tool_call_id": "tc_6", "name": "edit", "status": "completed", "occurred_at": real_ago(minutes=2, seconds=40),
     "args": {"file_path": "internal/daemon/workdir_test.go", "old_string": "", "new_string": "func TestResolveWorkdir_NoHome(t *testing.T) {"},
     "result": {"ok": True}},
    {"tool_call_id": "tc_7", "name": "bash", "status": "completed", "occurred_at": real_ago(minutes=2, seconds=10),
     "args": {"command": "go test ./internal/daemon/... -run TestResolveWorkdir -count=1"},
     "result": {"exit_code": 0, "stdout": "ok  \tparsar/internal/daemon\t0.412s"}},
    {"tool_call_id": "tc_8", "name": "bash", "status": "completed", "occurred_at": real_ago(minutes=1, seconds=30),
     "args": {"command": "git add internal/daemon/workdir_test.go && git commit -m 'daemon: test workdir fallback without HOME'"},
     "result": {"exit_code": 0}},
    {"tool_call_id": "tc_9", "name": "bash", "status": "running", "occurred_at": real_ago(seconds=42),
     "args": {"command": "git push origin fix/claude-home-workdir"}},
]
RUNS = [
    {"id": RUN_OK, "status": "completed", "agent_name": "reviewer-bot", "agent_slug": "reviewer-bot",
     "connector_type": "agent_daemon", "trigger_message_id": "msg_01J8ZC01", "output_message_id": "msg_01J8ZC02",
     "steps": STEPS_OK, "created_at": ago(minutes=38), "started_at": ago(minutes=37), "finished_at": ago(minutes=31)},
    # Running: the page opens the run's SSE stream, served below.
    {"id": RUN_PENDING, "status": "running", "agent_name": "reviewer-bot",
     "agent_slug": "reviewer-bot", "connector_type": "agent_daemon", "trigger_message_id": "msg_01J8ZC03",
     "steps": STEPS_PENDING, "created_at": ago(minutes=9), "started_at": RUN_STARTED},
]

INTERACTION = {
    "id": "int_01J8ZC0NV1", "workspace_id": WS, "conversation_id": C1, "agent_run_id": RUN_PENDING,
    "request_id": "req_7f3a9c", "kind": "permission", "status": "pending",
    "request": {"resource": "git push origin fix/claude-home-workdir", "action": "bash",
                "detail": "Push one commit (workdir_test.go) to the protected branch.",
                "payload": {"tool": "bash", "command": "git push origin fix/claude-home-workdir",
                            "cwd": "~/dev/parsar", "risk": "medium"}},
    "response": {}, "created_at": real_ago(seconds=42), "expires_at": real_in(minutes=4, seconds=18),
    "updated_at": real_ago(seconds=42), "agent_name": "reviewer-bot", "conversation_title": CONVS[0]["title"],
}


# --- SSE: the running run's stream -------------------------------------------
# mock-api.py answers JSON only and serves one request at a time. A fixture
# cannot register a streaming handler, so this one swaps in a threading server
# whose handler answers the stream path itself: it replays the run's tool
# events (before/after), announces the pending permission, then holds the
# connection open with keep-alive comments so the page stays "streaming".
STREAM_RE = re.compile(r"^/api/v1/conversations/([^/]+)/runs/([^/]+)/stream$")
STREAM_HOLD_S = 600


def _stream_frames():
    frames = []
    for s in STEPS_PENDING:
        frames.append(("tool", {"tool": {"id": s["tool_call_id"], "name": s["name"], "stage": "before", "args": s["args"]}}))
        if s["status"] == "completed":
            frames.append(("tool", {"tool": {"id": s["tool_call_id"], "name": s["name"], "stage": "after", "result": s.get("result")}}))
    frames.append(("permission", {"permission": {"id": INTERACTION["request_id"], "tool": "bash",
                                                 "title": INTERACTION["request"]["resource"],
                                                 "payload": INTERACTION["request"]["payload"]}}))
    return frames


def _serve_stream(handler):
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Cache-Control", "no-cache")
    handler.send_header("X-Accel-Buffering", "no")
    handler.end_headers()
    try:
        for event, data in _stream_frames():
            handler.wfile.write(f"event: {event}\ndata: {json.dumps(data)}\n\n".encode())
            handler.wfile.flush()
        deadline = time.time() + STREAM_HOLD_S
        while time.time() < deadline:
            time.sleep(15)
            handler.wfile.write(b": keep-alive\n\n")
            handler.wfile.flush()
    except (BrokenPipeError, ConnectionResetError, OSError):
        pass


class _StreamingServer(ThreadingHTTPServer):
    daemon_threads = True

    def finish_request(self, request, client_address):
        base = self.RequestHandlerClass
        if not getattr(base, "_streams", False):
            class Handler(base):
                _streams = True

                def do_GET(self):
                    if STREAM_RE.match(urlparse(self.path).path):
                        return _serve_stream(self)
                    return super().do_GET()

            self.RequestHandlerClass = Handler
        return super().finish_request(request, client_address)


_main = sys.modules.get("__main__")
if _main is not None and getattr(_main, "HTTPServer", None) is not None:
    _main.HTTPServer = _StreamingServer


def agents(m, q):
    return 200, {"agents": AGENTS}


def conversations(m, q):
    agent = q.get("agent_id", [""])[0]
    rows = [conv_row(c) for c in CONVS if not agent or c["agent"] == agent]
    return 200, {"conversations": rows}


def conversation(m, q):
    for c in CONVS:
        if c["id"] == m.group(1):
            return 200, conv_row(c)
    return 404, {"error": "not_found"}


def timeline(m, q):
    cid = m.group(1)
    if cid == C1:
        return 200, {"conversation_id": cid, "messages": MSGS, "agent_runs": RUNS}
    if cid == C2:
        return 200, {"conversation_id": cid, "messages": C2_MSGS, "agent_runs": C2_RUNS}
    c = next((x for x in CONVS if x["id"] == cid), None)
    if not c:
        return 404, {"error": "not_found"}
    msgs = [
        {"id": f"{cid}-m1", "conversation_id": cid, "sender_type": "user", "kind": "message",
         "content": c["title"], "created_at": c["created"]},
        {"id": f"{cid}-m2", "conversation_id": cid, "sender_type": "agent", "kind": "message",
         "content": c["preview"], "created_at": c["last"]},
    ]
    return 200, {"conversation_id": cid, "messages": msgs, "agent_runs": []}


RESOLVED = set()


def interactions(m, q):
    status = q.get("status", ["pending"])[0]
    ids = {INTERACTION.get("request_id"), INTERACTION.get("id")}
    pending = [] if RESOLVED & ids else [INTERACTION]
    return 200, {"interactions": pending if status == "pending" else []}


def resolve(m, q):
    # POST .../interactions/{id}/resolve: the bar's decision removes the
    # pending item so the composer (with its stop button) comes back.
    RESOLVED.add(m.group(2))
    return 200, {"ok": True}


resolve.methods = ("POST",)


ROUTES = [
    (r"^/api/v1/workspaces/([^/]+)/agents$", agents),
    (r"^/api/v1/workspaces/([^/]+)/conversations$", conversations),
    (r"^/api/v1/conversations/([^/]+)$", conversation),
    (r"^/api/v1/conversations/([^/]+)/timeline$", timeline),
    (r"^/api/v1/workspaces/([^/]+)/interactions$", interactions),
    (r"^/api/v1/workspaces/([^/]+)/interactions/([^/]+)/resolve$", resolve),
]
