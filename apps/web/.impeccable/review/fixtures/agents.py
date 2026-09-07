"""Fixtures for the Agents ledger and the agent detail page.

Endpoints (see src/lib/api-agents.ts, api-models.ts, api-capabilities.ts):
  GET /api/v1/workspaces/{ws}/agents              -> { agents: [...] }
  GET /api/v1/workspaces/{ws}/agents/{id}         -> AgentDetail
  GET /api/v1/workspaces/{ws}/agents/{id}/metrics -> AgentMetrics
  GET /api/v1/workspaces/{ws}/agents/{id}/capabilities -> { installed, available }
  GET /api/v1/workspaces/{ws}/models              -> { models: [...] }
  GET /api/v1/workspaces/{ws}/capabilities        -> { capabilities: [...] }
The mock injects WS, NOW and iso as module globals.
"""
from datetime import timedelta

WS = globals().get("WS", "ws")
NOW = globals()["NOW"]
iso = globals()["iso"]

MODELS = [
    {"id": "mdl_claude_opus", "slug": "claude-opus-5", "name": "Claude Opus 5", "provider_type": "anthropic",
     "adapter": "anthropic", "base_url": "https://api.anthropic.com", "model_key": "claude-opus-5",
     "credential_mode": "credential_ref", "credential_kind_code": "anthropic_api_key", "status": "active",
     "config": {"protocols": ["anthropic"]}, "created_at": iso(NOW - timedelta(days=40)), "updated_at": iso(NOW - timedelta(days=2))},
    {"id": "mdl_gpt5_codex", "slug": "gpt-5-codex", "name": "GPT-5 Codex", "provider_type": "openai",
     "adapter": "openai", "base_url": "https://api.openai.com/v1", "model_key": "gpt-5-codex",
     "credential_mode": "credential_ref", "credential_kind_code": "openai_api_key", "status": "active",
     "config": {"protocols": ["openai_responses"]}, "created_at": iso(NOW - timedelta(days=30)), "updated_at": iso(NOW - timedelta(days=5))},
    {"id": "mdl_minimax_m2", "slug": "minimax-m2", "name": "MiniMax M2", "provider_type": "minimax",
     "adapter": "openai", "base_url": "https://api.minimax.io/v1", "model_key": "MiniMax-M2",
     "credential_mode": "inline_secret", "secret_id": "sec_m2", "status": "active",
     "config": {"protocols": ["openai_chat", "anthropic"]}, "created_at": iso(NOW - timedelta(days=12)), "updated_at": iso(NOW - timedelta(days=1))},
]


def agent(i, name, slug, desc, status, engine, connector, mode, model, enabled_min_ago, **extra):
    row = {
        "id": f"agt_01J8Z{i:02d}AGNT{i:02d}", "workspace_id": WS, "name": name, "slug": slug, "description": desc,
        "connector_type": connector, "status": status, "visibility": extra.pop("visibility", "workspace"),
        "config": {"agent_kind": engine, "daemon_mode": mode, "default_model_id": model,
                   "work_dir": extra.pop("work_dir", "/workspace"),
                   "system_prompt": extra.pop("system_prompt", ""),
                   **({"mode": "plan"} if engine == "codex" and extra.pop("plan", False) else {})},
        "created_by_user_id": "usr_fj", "created_by_name": "fanjingluo",
        "enabled_at": iso(NOW - timedelta(minutes=enabled_min_ago)) if enabled_min_ago is not None else None,
    }
    row.update(extra)
    return row


AGENTS = [
    agent(1, "reviewer-bot", "reviewer-bot", "Reviews pull requests and leaves inline comments · 审查 PR 并留下行内评论",
          "active", "claude_code", "agent_daemon", "local", "mdl_claude_opus", 4 * 60,
          runtime="local", runtime_id="rt_01J8ZR", runtime_name="mbp-fanjingluo", runtime_kind="agent_daemon", runtime_liveness="online",
          system_prompt="You are a meticulous code reviewer. Prefer small, verifiable suggestions.\n\n你是一名严谨的代码评审者。"),
    agent(2, "release-notes", "release-notes", "Drafts release notes from merged PRs", "active", "codex", "agent_daemon", "sandbox",
          "mdl_gpt5_codex", 26 * 60, runtime="sandbox", sandbox_external_id="sbx_2b81f0c9", sandbox_status="running", plan=True),
    agent(3, "migrate-helper", "migrate-helper", "Runs schema migrations through the internal HTTP agent", "error", "claude_code", "http", "local",
          "mdl_minimax_m2", 3 * 24 * 60, visibility="tenant"),
    agent(4, "docs-writer", "docs-writer", "Keeps docs/ in sync with the OpenAPI spec", "disabled", "claude_code", "agent_daemon", "local",
          "mdl_claude_opus", None, runtime="local", runtime_id="rt_01J8ZQ", runtime_name="ci-runner-03", runtime_kind="agent_daemon", runtime_liveness="offline"),
    agent(5, "triage", "triage", "Labels new issues and pings the owning team", "active", "codex", "agent_daemon", "sandbox",
          "mdl_gpt5_codex", 9 * 24 * 60, visibility="public"),
]

CAPABILITIES = [
    {"id": "cap_github", "workspace_id": WS, "type": "mcp", "name": "GitHub MCP", "description": "Repositories, pull requests, issues",
     "visibility": "workspace", "status": "active", "required_credentials": [{"kind": "github_token", "required": True}],
     "latest_version_id": "capv_gh_3", "latest_version": "1.4.0", "latest_version_created_at": iso(NOW - timedelta(days=3)),
     "creator_id": "usr_fj", "created_at": iso(NOW - timedelta(days=60)), "updated_at": iso(NOW - timedelta(days=3))},
    {"id": "cap_review", "workspace_id": WS, "type": "skill", "name": "review-checklist", "description": "House style for PR reviews",
     "visibility": "workspace", "status": "active", "required_credentials": [],
     "latest_version_id": "capv_rv_2", "latest_version": "0.3.1", "latest_version_created_at": iso(NOW - timedelta(days=10)),
     "creator_id": "usr_fj", "created_at": iso(NOW - timedelta(days=20)), "updated_at": iso(NOW - timedelta(days=10))},
    {"id": "cap_sentry", "workspace_id": "ws_other", "source_workspace_id": "ws_other", "source_workspace_name": "MiniMax · Platform",
     "from_marketplace": True, "type": "mcp", "name": "Sentry MCP", "description": "Issues and traces from Sentry",
     "visibility": "public", "status": "active", "required_credentials": [{"kind": "sentry_token", "required": True}],
     "latest_version_id": "capv_se_5", "latest_version": "2.0.0", "latest_version_created_at": iso(NOW - timedelta(days=1)),
     "creator_id": "usr_x", "created_at": iso(NOW - timedelta(days=90)), "updated_at": iso(NOW - timedelta(days=1))},
]


def detail(a):
    d = dict(a)
    d.update({
        "created_at": iso(NOW - timedelta(days=41)),
        "updated_at": iso(NOW - timedelta(hours=6)),
        "profile": {"system_prompt": a["config"].get("system_prompt", "")},
    })
    return d


def list_agents(m, q):
    return 200, {"agents": AGENTS}


def agent_detail(m, q):
    for a in AGENTS:
        if a["id"] == m.group(2):
            return 200, detail(a)
    return 404, {"error": "not_found"}


def metrics(m, q):
    return 200, {"window_days": 30, "completed_count": 128, "failed_count": 7, "success_rate": 0.948, "avg_duration_ms": 184_300}


def agent_capabilities(m, q):
    installed = [
        {"id": "acap_1", "agent_id": m.group(2), "capability_id": "cap_github", "capability_version_id": "capv_gh_3", "version": "1.4.0",
         "enabled": True, "configuration": {"credential_bindings": {"github_token": {"source": "personal"}}}, "pinning_mode": "latest",
         "created_at": iso(NOW - timedelta(days=3)), "updated_at": iso(NOW - timedelta(days=3)), "capability": CAPABILITIES[0]},
        {"id": "acap_2", "agent_id": m.group(2), "capability_id": "cap_sentry", "workspace_id": "ws_other", "source_workspace_name": "MiniMax · Platform",
         "capability_version_id": "capv_se_4", "version": "1.9.2", "enabled": True, "configuration": {}, "pinning_mode": "pinned",
         "created_at": iso(NOW - timedelta(days=12)), "updated_at": iso(NOW - timedelta(days=12)), "capability": CAPABILITIES[2]},
        {"id": "acap_b", "agent_id": m.group(2), "capability_id": "builtin_web", "capability_version_id": "", "enabled": True, "configuration": {},
         "built_in": True, "builtin_key": "web_search", "created_at": iso(NOW), "updated_at": iso(NOW),
         "capability": {"id": "builtin_web", "workspace_id": WS, "type": "mcp", "name": "Web search", "description": "Runtime built-in", "status": "active",
                        "builtin_key": "web_search", "creator_id": "", "created_at": iso(NOW), "updated_at": iso(NOW)}},
    ]
    return 200, {"workspace_id": WS, "agent_id": m.group(2), "installed": installed, "available": [CAPABILITIES[1]]}


def versions(m, q):
    cap = m.group(2)
    if cap == "cap_github":
        rows = [("capv_gh_3", "1.4.0", 3), ("capv_gh_2", "1.3.2", 20), ("capv_gh_1", "1.0.0", 58)]
    elif cap == "cap_sentry":
        rows = [("capv_se_5", "2.0.0", 1), ("capv_se_4", "1.9.2", 30)]
    else:
        rows = [("capv_rv_2", "0.3.1", 10), ("capv_rv_1", "0.1.0", 20)]
    return 200, {"versions": [{"id": i, "capability_id": cap, "version": v, "created_at": iso(NOW - timedelta(days=d))} for i, v, d in rows]}


ROUTES = [
    (r"^/api/v1/workspaces/([^/]+)/agents$", list_agents),
    (r"^/api/v1/workspaces/([^/]+)/agents/([^/]+)$", agent_detail),
    (r"^/api/v1/workspaces/([^/]+)/agents/([^/]+)/metrics$", metrics),
    (r"^/api/v1/workspaces/([^/]+)/agents/([^/]+)/capabilities$", agent_capabilities),
    (r"^/api/v1/workspaces/([^/]+)/capabilities/([^/]+)/versions$", versions),
    (r"^/api/v1/workspaces/([^/]+)/capabilities$", lambda m, q: (200, {"capabilities": CAPABILITIES[:2]})),
    (r"^/api/v1/workspaces/([^/]+)/models$", lambda m, q: (200, {"models": MODELS})),
    (r"^/api/v1/workspaces/([^/]+)/secrets$", lambda m, q: (200, {"secrets": []})),
    (r"^/api/v1/me/credentials$", lambda m, q: (200, {"credentials": [{"id": "cred_gh", "kind": "github_token", "display_name": "GitHub (fanjingluo)", "status": "active"}]})),
    (r"^/api/v1/capabilities/marketplace$", lambda m, q: (200, {"capabilities": [], "items": []})),
]
