"""Fixtures for the team surfaces: members (+ invitations, join requests),
approvals (agent interactions) and scheduled tasks. Loaded by mock-api.py,
which injects WS / NOW / iso as module globals."""
from datetime import timedelta

WS = globals().get("WS", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
NOW = globals()["NOW"]
iso = globals()["iso"]


def ago(**kw):
    return iso(NOW - timedelta(**kw))


def ahead(**kw):
    return iso(NOW + timedelta(**kw))


# --- members -----------------------------------------------------------------

MEMBERS = [
    ("usr_fj", "owner", "fanjingluo@minimax.io", "范景洛", "active", 210),
    ("usr_yw", "admin", "yuwei.chen@minimax.io", "Yuwei Chen", "active", 168),
    ("usr_ml", "member", "marcus.lindqvist@minimax.io", "Marcus Lindqvist", "active", 41),
    ("usr_hz", "viewer", "hz.audit@minimax.io", "", "disabled", 12),
]

INVITATIONS = [
    {"id": "inv_01J8ZQ7K2M", "email": "priya.raman@minimax.io", "role": "member",
     "invited_by": "usr_fj", "invited_by_name": "范景洛",
     "invite_link": "http://127.0.0.1:5173/invite?token=9f3c1a7e2b",
     "expires_at": ahead(hours=61), "created_at": ago(hours=11)},
    {"id": "inv_01J8ZN4T8R", "email": "tomasz.k@minimax.io", "role": "viewer",
     "invited_by": "usr_yw", "invited_by_name": "Yuwei Chen",
     "invite_link": "", "expires_at": ahead(hours=7), "created_at": ago(days=2, hours=17)},
]

JOIN_REQUESTS = [
    {"id": "jr_01J8ZPX3", "workspace_id": WS, "user_id": "usr_al", "user_email": "alina.f@minimax.io",
     "user_name": "Alina Fischer", "request_reason": "接手 infra 值班，需要看 run 日志",
     "requested_at": ago(hours=3)},
]

# Same agent ids as fixtures/conversations.py so names resolve whichever
# fixture answers /agents first.
AGENTS = [
    ("9b1c2d3e-4f50-4a61-8b72-c83d94e5f601", "reviewer-bot"),
    ("9b1c2d3e-4f50-4a61-8b72-c83d94e5f602", "release-notes"),
    ("9b1c2d3e-4f50-4a61-8b72-c83d94e5f603", "docs-writer"),
]


def members(m, q):
    rows = []
    for uid, role, email, name, status, days in MEMBERS:
        rows.append({
            "id": f"wm_{uid}", "workspace_id": WS, "user_id": uid, "role": role,
            "user_email": email, "user_name": name, "user_status": status,
            "created_at": ago(days=days), "updated_at": ago(days=days),
        })
    return 200, {"workspace_id": WS, "members": rows}


def invitations(m, q):
    return 200, INVITATIONS


def join_requests(m, q):
    return 200, {"workspace_id": WS, "requests": JOIN_REQUESTS}


def user_search(m, q):
    needle = (q.get("q", [""])[0] or "").lower()
    users = [
        {"id": "usr_pr", "email": "priya.raman@minimax.io", "name": "Priya Raman", "avatar_url": "", "status": "active"},
        {"id": "usr_kt", "email": "kenta.oshiro@minimax.io", "name": "大城 健太", "avatar_url": "", "status": "active"},
        {"id": "usr_sb", "email": "s.bianchi@minimax.io", "name": "Sofia Bianchi", "avatar_url": "", "status": "active"},
    ]
    return 200, {"items": [u for u in users if needle in (u["name"] + u["email"]).lower()]}


def agents(m, q):
    return 200, {"agents": [
        {"id": aid, "workspace_id": WS, "name": name, "slug": name, "description": "",
         "connector_type": "agent_daemon", "status": "active",
         "config": {}, "visibility": "workspace", "created_at": ago(days=90), "updated_at": ago(days=1)}
        for aid, name in AGENTS
    ]}


# --- approvals (agent interactions) --------------------------------------------

def interaction(i, kind, status, created_min_ago, request, agent, conv, resolved=None, ttl_min=30):
    created = NOW - timedelta(minutes=created_min_ago)
    row = {
        "id": f"int_01J8Z{i:02d}QW7R{i:02d}", "workspace_id": WS, "conversation_id": conv,
        "agent_run_id": f"run_01J8Z{i:02d}KX2P9Q{i:02d}", "request_id": f"req-{i:03d}",
        "kind": kind, "status": status, "request": request, "response": {},
        "created_at": iso(created), "expires_at": iso(created + timedelta(minutes=ttl_min)),
        "updated_at": iso(created), "agent_name": agent,
        "conversation_title": "",
    }
    if resolved:
        row.update({"resolved_at": iso(created + timedelta(minutes=2)), "resolution_source": "web",
                    "resolved_actor": "user", "resolved_by": resolved,
                    "response": {"approved": status == "approved"}})
    return row


INTERACTIONS = [
    interaction(1, "permission", "pending", 3, {
        "request_id": "req-001", "resource": "Write production configuration", "action": "Modify files",
        "detail": "Agent wants to update deploy/production.yaml and restart the service.",
        "payload": {"command": "apply deployment configuration", "paths": ["deploy/production.yaml"], "risk": "high"},
    }, "reviewer-bot", "conv_01J8ZM3Q7K", ttl_min=6 * 60),
    interaction(2, "permission", "approved", 47, {
        "request_id": "req-002", "resource": "git push origin feat/split-routes", "action": "Run command",
        "payload": {"command": "git push origin feat/split-routes", "cwd": "~/dev/parsar", "risk": "medium"},
    }, "release-notes", "conv_01J8ZKX2P9", resolved="范景洛"),
    interaction(3, "permission", "denied", 130, {
        "request_id": "req-003", "resource": "DROP TABLE agent_runs_legacy", "action": "Run SQL",
        "payload": {"statement": "DROP TABLE agent_runs_legacy", "database": "parsar", "risk": "high"},
    }, "migrate-helper", "conv_01J8ZK9A1M", resolved="Yuwei Chen"),
    interaction(4, "user_choice", "cancelled", 1500, {
        "request_id": "req-004",
        "questions": [
            {"id": "q0", "header": "Deployment target", "question": "Which environment should the Agent deploy to?",
             "options": [{"label": "Staging", "description": "Deploy to the shared test environment."},
                         {"label": "Production", "description": "Deploy to the live customer environment."}],
             "multi_select": False, "is_other": False, "is_secret": False},
            {"id": "verification_steps", "header": "Verification", "question": "Which checks should run after deployment?",
             "options": [{"label": "Smoke tests", "description": "Run the critical user journey checks."},
                         {"label": "Full regression", "description": "Run the complete test suite."}],
             "multi_select": True, "is_other": False, "is_secret": False},
        ],
    }, "docs-writer", "conv_01J8ZJ3C5N", resolved="范景洛"),
]

GROUPS = {
    "pending": {"pending", "resolving"},
    "decided": {"approved", "denied", "answered"},
    "expired": {"cancelled", "expired"},
}


def interactions(m, q):
    group = q.get("status", ["pending"])[0]
    allowed = GROUPS.get(group, set())
    return 200, {"interactions": [r for r in INTERACTIONS if r["status"] in allowed]}


def resolve_interaction(m, q):
    for r in INTERACTIONS:
        if r["id"] == m.group(2):
            return 200, {"interaction": r, "applied": True, "already_resolved": False}
    return 404, {"error": "not_found"}


resolve_interaction.methods = ("POST",)


# --- scheduled tasks -----------------------------------------------------------

def task(i, name, agent_id, cron, tz, enabled, last_status, next_min, last_min, prompt, failures=0):
    created = NOW - timedelta(days=30 + i)
    return {
        "id": f"sch_01J8Z{i:02d}TSK{i:02d}", "agent_id": agent_id, "conversation_id": f"conv_sched_{i:02d}",
        "name": name, "prompt": prompt, "cron_expr": cron, "timezone": tz, "enabled": enabled,
        "feishu_chat_id": "", "feishu_chat_name": "",
        "next_run_at": ahead(minutes=next_min) if enabled and next_min is not None else None,
        "last_run_at": ago(minutes=last_min) if last_min is not None else None,
        "last_run_id": f"run_01J8Z{i:02d}KX2P9Q{i:02d}" if last_min is not None else "",
        "last_status": last_status, "consecutive_failures": failures, "created_by": "usr_fj",
        "created_at": iso(created), "updated_at": ago(hours=6),
    }


TASKS = [
    task(1, "每日晨会总结", "9b1c2d3e-4f50-4a61-8b72-c83d94e5f602", "0 9 * * 1-5", "Asia/Shanghai", True, "completed", 1393, 47,
         "总结昨天的提交，列出今天待办，发到 #infra。"),
    task(2, "Nightly migration dry-run", "9b1c2d3e-4f50-4a61-8b72-c83d94e5f601", "30 2 * * *", "UTC", True, "failed", 1003, 437,
         "Run `make migrate-dry-run` against staging and report drift.", failures=2),
    task(3, "Weekly changelog draft", "9b1c2d3e-4f50-4a61-8b72-c83d94e5f603", "0 17 * * 5", "Europe/London", False, "", None, None,
         "Draft the weekly changelog from merged PRs."),
]


def scheduled_tasks(m, q):
    offset = int(q.get("offset", ["0"])[0])
    limit = int(q.get("limit", ["20"])[0])
    return 200, {"scheduled_tasks": TASKS[offset:offset + limit], "total": len(TASKS), "limit": limit, "offset": offset}


ROUTES = [
    (r"^/api/v1/workspaces/([^/]+)/members$", members),
    (r"^/api/v1/workspaces/([^/]+)/invitations$", invitations),
    (r"^/api/v1/workspaces/([^/]+)/join-requests$", join_requests),
    (r"^/api/v1/users/search$", user_search),
    (r"^/api/v1/workspaces/([^/]+)/agents$", agents),
    (r"^/api/v1/workspaces/([^/]+)/interactions$", interactions),
    (r"^/api/v1/workspaces/([^/]+)/interactions/([^/]+)/resolve$", resolve_interaction),
    (r"^/api/v1/workspaces/([^/]+)/scheduled-tasks$", scheduled_tasks),
]
