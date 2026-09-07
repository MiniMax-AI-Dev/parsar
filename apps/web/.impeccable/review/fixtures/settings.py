"""Fixtures for the settings group: usage, audit, credentials (org + personal),
connections and the settings/auth-provider reads.

Loaded by mock-api.py; `WS`, `NOW`, `iso` are injected as globals.
"""
from datetime import timedelta

BASE = f"/api/v1/workspaces/{WS}"  # noqa: F821 (injected)


def ago(**kw):
    return iso(NOW - timedelta(**kw))  # noqa: F821 (injected)


# --- Usage --------------------------------------------------------------------

RUN_IDS = [f"run_01J8Z{i:02d}KX2P9Q{i:02d}" for i in range(1, 11)]

USAGE = []


def usage(i, run, provider, model, inp, out, cost, min_ago):
    USAGE.append({
        "id": f"usg_01J8ZU{i:02d}", "workspace_id": WS, "agent_run_id": run,  # noqa: F821
        "provider": provider, "model": model, "input_tokens": inp, "output_tokens": out,
        "cost_usd": cost, "created_at": ago(minutes=min_ago),
    })


usage(1, RUN_IDS[0], "anthropic", "claude-opus-5", 48210, 6120, 1.1834, 3)
usage(2, RUN_IDS[1], "anthropic", "claude-opus-5", 30877, 4402, 0.7930, 27)
usage(3, RUN_IDS[1], "anthropic", "claude-sonnet-5", 12044, 1910, 0.0651, 28)
usage(4, RUN_IDS[2], "anthropic", "claude-opus-5", 91320, 11208, 2.2290, 64)
usage(5, RUN_IDS[3], "openai", "gpt-5.4", 22910, 3311, 0.3199, 120)
usage(6, RUN_IDS[5], "anthropic", "claude-sonnet-5", 8402, 1276, 0.0447, 180)
usage(7, None, "openai", "gpt-5.4-mini", 1450, 220, 0.0011, 240)
usage(8, RUN_IDS[7], "anthropic", "claude-opus-5", 63118, 7015, 1.4980, 1499)
usage(9, RUN_IDS[9], "minimax", "MiniMax-M2.5", 15200, 2810, 0.0212, 1699)


def usage_route(m, q):
    return 200, {"usage_logs": USAGE}


# --- Audit --------------------------------------------------------------------

AUDIT = [
    {"id": 6021, "occurred_at": ago(minutes=2), "source": "runtime", "event_type": "agent_run.started",
     "actor_type": "agent", "actor_id": "agt_reviewer-bot", "target_type": "agent_run", "target_id": RUN_IDS[0],
     "workspace_id": WS, "payload": {"conversation_id": "conv_01J8ZM3Q7K", "connector_type": "agent_daemon", "runtime_id": "rt_01J8ZR"}},  # noqa: F821
    {"id": 6020, "occurred_at": ago(minutes=9), "source": "approval", "event_type": "permission_request.approved",
     "actor_type": "user", "actor_id": "usr_fj", "target_type": "permission_request", "target_id": "perm_01J8ZLQ2",
     "workspace_id": WS, "payload": {"agent_run_id": RUN_IDS[0], "action": "bash", "decision": "allow_once"}},  # noqa: F821
    {"id": 6019, "occurred_at": ago(minutes=41), "source": "admin", "event_type": "secret.created",
     "actor_type": "user", "actor_id": "usr_fj", "target_type": "secret", "target_id": "sec_01J8ZK7E2B",
     "workspace_id": WS, "payload": {"kind": "runtime", "provider": "e2b", "name": "E2B sandbox key"}},  # noqa: F821
    {"id": 6018, "occurred_at": ago(hours=2, minutes=3), "source": "runtime", "event_type": "agent_run.failed",
     "actor_type": "system", "target_type": "agent_run", "target_id": RUN_IDS[3],
     "workspace_id": WS, "payload": {"error": "migration 0042: relation \"agent_runs\" already exists", "source": "daemon"}},  # noqa: F821
    {"id": 6017, "occurred_at": ago(hours=5), "source": "admin", "event_type": "workspace_member.role_changed",
     "actor_type": "user", "actor_id": "usr_wcy", "target_type": "workspace_member", "target_id": "mem_01J8ZH9F",
     "workspace_id": WS, "payload": {"from": "member", "to": "admin", "user_email": "wcy@minimax.io"}},  # noqa: F821
    {"id": 6016, "occurred_at": ago(days=1, hours=2), "source": "identity", "event_type": "session.login",
     "actor_type": "external", "target_type": None, "target_id": None,
     "workspace_id": WS, "payload": {"provider": "github", "ip": "10.12.4.71"}},  # noqa: F821
]


def audit_route(m, q):
    rows = AUDIT
    source = q.get("source", [""])[0]
    target_type = q.get("target_type", [""])[0]
    target_id = q.get("target_id", [""])[0]
    if source:
        rows = [r for r in rows if r["source"] == source]
    if target_type:
        rows = [r for r in rows if r.get("target_type") == target_type]
    if target_id:
        rows = [r for r in rows if r.get("target_id") == target_id]
    limit = int(q.get("limit", ["200"])[0])
    return 200, {"source": source or None, "target_type": target_type or None, "audit_records": rows[:limit]}


# --- Org secrets --------------------------------------------------------------

SECRETS = [
    {"id": "sec_01J8ZK1A9M", "slug": "anthropic-prod", "name": "Anthropic Production", "kind": "model_provider",
     "provider": "anthropic", "auth_type": "api_key", "key_version": "v2", "status": "active",
     "masked": "sk-ant-••••••••7f3a", "metadata": {}, "created_at": ago(days=21), "updated_at": ago(days=3)},
    {"id": "sec_01J8ZK7E2B", "slug": "e2b-sandbox", "name": "E2B sandbox key", "kind": "runtime",
     "provider": "e2b", "auth_type": "api_key", "key_version": "v1", "status": "active",
     "masked": "e2b_••••••••c21d", "metadata": {}, "created_at": ago(minutes=41), "updated_at": ago(minutes=41)},
    {"id": "sec_01J8ZJ3C5N", "slug": "stripe-test", "name": "Stripe test key", "kind": "custom_api",
     "provider": "stripe", "auth_type": "api_key", "key_version": "v1", "status": "disabled",
     "masked": "sk_test_••••••••9Qz1", "metadata": {}, "created_at": ago(days=40), "updated_at": ago(days=12)},
]


def secrets_route(m, q):
    return 200, {"secrets": SECRETS}


# --- Personal credentials ---------------------------------------------------

CREDENTIALS = [
    {"id": "ucr_01J8ZG1H", "kind": "github_pat", "display_name": "", "last_used_at": ago(minutes=28),
     "created_at": ago(days=34), "updated_at": ago(days=34)},
    {"id": "ucr_01J8ZG2K", "kind": "notion_integration", "display_name": "", "last_used_at": ago(days=6),
     "created_at": ago(days=9), "updated_at": ago(days=9)},
]


def credentials_route(m, q):
    return 200, {"credentials": CREDENTIALS}


def credential_kinds_route(m, q):
    return 200, {"items": [
        {"id": "ck_01", "code": "github_pat", "display_name": "GitHub Access Token", "description": "", "built_in": True, "source": "platform_oauth"},
        {"id": "ck_02", "code": "notion_integration", "display_name": "Notion Integration Token", "description": "", "built_in": True, "source": "platform_oauth"},
        {"id": "ck_03", "code": "slack_bot_token", "display_name": "Slack Bot Token", "description": "", "built_in": True, "source": "platform_oauth"},
    ]}


# --- Connections (workspace IM connectors) -----------------------------------

CONNECTORS = [
    {"id": "imc_01J8ZF1", "workspace_id": WS, "workspace_name": "MiniMax · Infra", "platform": "feishu",  # noqa: F821
     "app_id": "cli_a7c3e91f2b4d8e01", "enabled": True,
     "config": {"app_secret_ref": "sec_01J8ZF1S", "verification_token_ref": "", "encrypt_key_ref": "",
                "bot_open_id": "ou_9f2a1c", "event_mode": "websocket"},
     "created_at": ago(days=30), "updated_at": ago(days=2)},
    {"id": "imc_01J8ZF2", "workspace_id": WS, "workspace_name": "MiniMax · Infra", "platform": "slack",  # noqa: F821
     "app_id": "A07KX2P9Q4R", "enabled": False,
     "config": {"bot_token_ref": "sec_01J8ZF2B", "app_token_ref": "sec_01J8ZF2A", "signing_secret_ref": "",
                "event_mode": "socket"},
     "created_at": ago(days=5), "updated_at": ago(hours=6)},
]


def connectors_route(m, q):
    return 200, {"connectors": CONNECTORS, "master_key_configured": True}


# --- Settings: workspace auth providers -------------------------------------

def auth_providers_route(m, q):
    return 200, {"workspace_id": WS, "providers": [  # noqa: F821
        {"id": "password", "type": "password", "label": "Email & password", "enabled": True, "configured": True,
         "required_env": [], "missing_env": []},
        {"id": "github", "type": "oauth", "label": "GitHub", "enabled": False, "configured": False,
         "callback_url": "http://127.0.0.1:18080/api/v1/auth/github/callback",
         "required_env": ["PARSAR_AUTH_GITHUB_CLIENT_ID", "PARSAR_AUTH_GITHUB_CLIENT_SECRET"],
         "missing_env": ["PARSAR_AUTH_GITHUB_CLIENT_SECRET"],
         "docs_url": "https://github.com/MiniMax-AI-Dev/parsar/blob/main/docs/auth.md#github"},
        {"id": "okta", "type": "oidc", "label": "Okta", "enabled": False, "configured": True,
         "callback_url": "http://127.0.0.1:18080/api/v1/auth/oidc/callback",
         "required_env": ["PARSAR_AUTH_OIDC_ISSUER", "PARSAR_AUTH_OIDC_CLIENT_ID", "PARSAR_AUTH_OIDC_CLIENT_SECRET"],
         "missing_env": [],
         "docs_url": "https://github.com/MiniMax-AI-Dev/parsar/blob/main/docs/auth.md#oidc"},
    ]}


ROUTES = [
    (rf"^{BASE}/usage$", usage_route),
    (rf"^{BASE}/audit-records$", audit_route),
    (rf"^{BASE}/secrets$", secrets_route),
    (r"^/api/v1/me/credentials$", credentials_route),
    (rf"^{BASE}/credential-kinds$", credential_kinds_route),
    (rf"^{BASE}/connectors$", connectors_route),
    (rf"^{BASE}/auth/providers$", auth_providers_route),
]
