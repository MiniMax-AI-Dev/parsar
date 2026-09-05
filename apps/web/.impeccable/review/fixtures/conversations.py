# Fixtures for ?admin=conversations: agents, the conversation list scoped to
# an agent, one conversation + timeline (messages, runs with tool steps), and
# one pending permission interaction so the approval card renders in-thread.
# Globals WS, NOW, iso are injected by mock-api.py.
import importlib.util
import os
import re
from datetime import timedelta  # noqa: F401  (NOW is a datetime)


def ago(**kw):
    return iso(NOW - timedelta(**kw))


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
     "agent": R, "message_count": 4, "last": ago(minutes=4), "created": ago(minutes=38),
     "preview": "Left three comments; the workdir fallback needs a test."},
    {"id": "5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a02", "title": "Why does make check-web fail on CI?",
     "agent": R, "message_count": 6, "last": ago(hours=2), "created": ago(hours=3),
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
    {"id": "msg_01J8ZC04", "sender_type": "agent", "kind": "message", "created_at": ago(minutes=4),
     "content": "Test added in `internal/daemon/workdir_test.go` (`TestResolveWorkdir_NoHome`). It fails on the current branch and passes with a one-line guard. Pushing needs your approval since the branch is protected."},
]
for m in MSGS:
    m.update({"conversation_id": C1, "content_format": "markdown"})

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
STEPS_PENDING = [
    {"tool_call_id": "tc_5", "name": "edit", "status": "completed", "occurred_at": ago(minutes=7),
     "args": {"file_path": "internal/daemon/workdir_test.go"}, "result": {"ok": True}},
    {"tool_call_id": "tc_6", "name": "bash", "status": "running", "occurred_at": ago(minutes=4),
     "args": {"command": "git push origin fix/claude-home-workdir"}},
]
RUNS = [
    {"id": RUN_OK, "status": "completed", "agent_name": "reviewer-bot", "agent_slug": "reviewer-bot",
     "connector_type": "agent_daemon", "trigger_message_id": "msg_01J8ZC01", "output_message_id": "msg_01J8ZC02",
     "steps": STEPS_OK, "created_at": ago(minutes=38), "started_at": ago(minutes=37), "finished_at": ago(minutes=31)},
    # Queued, not running: a running run would open an SSE EventSource that
    # this JSON-only mock cannot answer (the browser would reconnect forever).
    {"id": RUN_PENDING, "status": "queued", "queue_position": 1, "agent_name": "reviewer-bot",
     "agent_slug": "reviewer-bot", "connector_type": "agent_daemon", "trigger_message_id": "msg_01J8ZC03",
     "output_message_id": "msg_01J8ZC04", "steps": STEPS_PENDING, "created_at": ago(minutes=9)},
]

INTERACTION = {
    "id": "int_01J8ZC0NV1", "workspace_id": WS, "conversation_id": C1, "agent_run_id": RUN_PENDING,
    "request_id": "req_7f3a9c", "kind": "permission", "status": "pending",
    "request": {"resource": "git push origin fix/claude-home-workdir", "action": "bash",
                "detail": "Push one commit (workdir_test.go) to the protected branch.",
                "payload": {"tool": "bash", "command": "git push origin fix/claude-home-workdir",
                            "cwd": "~/dev/parsar", "risk": "medium"}},
    "response": {}, "created_at": ago(minutes=4), "expires_at": iso(NOW + timedelta(minutes=26)),
    "updated_at": ago(minutes=4), "agent_name": "reviewer-bot", "conversation_title": CONVS[0]["title"],
}


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


def interactions(m, q):
    status = q.get("status", ["pending"])[0]
    return 200, {"interactions": [INTERACTION] if status == "pending" else []}


ROUTES = [
    (r"^/api/v1/workspaces/([^/]+)/agents$", agents),
    (r"^/api/v1/workspaces/([^/]+)/conversations$", conversations),
    (r"^/api/v1/conversations/([^/]+)$", conversation),
    (r"^/api/v1/conversations/([^/]+)/timeline$", timeline),
    (r"^/api/v1/workspaces/([^/]+)/interactions$", interactions),
]
