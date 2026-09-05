# Fixtures for the Runtime (?admin=runtime) and Connectors (?admin=connectors)
# settings pages. Receives WS, NOW, iso from mock-api.py.
from datetime import timedelta

def ago(**kw):
    return iso(NOW - timedelta(**kw))  # noqa: F821  (injected)


def device(i, name, host, version, liveness, hb_s, kinds, active=0, sandbox=False, agent=None):
    cfg = {
        "supported_agent_kinds": [
            {"kind": k, "available": ok, "version": v, "capabilities": {"streaming": True, "permissions": True, "usage": ok, "resume": ok}}
            for (k, ok, v) in kinds
        ],
        "agent_daemon_active_requests": active,
        "daemon_capabilities": {"streaming": True, "permissions": True, "usage": True, "resume": False},
    }
    if sandbox:
        cfg.update({"created_by": "sandbox_provider", "daemon_mode": "sandbox", "sandbox_kind": "docker",
                    "sandbox_id": "sbx_2b81f0c9", "agent_id": agent})
    return {
        "id": f"rt_01J8ZR{i:02d}QW7K3M{i:02d}", "workspace_id": WS, "type": "agent_daemon", "name": name,  # noqa: F821
        "admin_state": "enabled", "liveness": liveness,
        "provider": "agent_daemon_sandbox" if sandbox else "agent_daemon",
        "owner_user_id": "usr_fj", "hostname": host, "version": version,
        "last_heartbeat_at": ago(seconds=hb_s) if hb_s is not None else None,
        "pairing_token_expires_at": ago(minutes=-25) if liveness == "pending_pairing" else None,
        "config": cfg, "created_at": ago(days=3 + i), "updated_at": ago(seconds=hb_s or 0),
    }


RUNTIMES = [
    device(1, "mbp-fanjingluo", "mbp-fanjingluo.local", "0.9.2", "online", 12,
           [("claude_code", True, "1.0.112"), ("codex", True, "0.42.0"), ("opencode", False, None)], active=1),
    device(2, "build-box-02", "build-box-02.infra.minimax.io", "0.9.1", "offline", 3 * 3600 + 240,
           [("claude_code", True, "1.0.98")]),
    device(3, "qa-linux-01", "", "", "pending_pairing", None, []),
    device(4, "sbx-reviewer-bot", "sbx-2b81f0c9", "0.9.2", "online", 4,
           [("claude_code", True, "1.0.112")], sandbox=True, agent="agt_reviewer-bot"),
]

SANDBOXES = [{
    "binding_id": "bnd_01J8ZS7M2K9QX4", "workspace_id": WS, "agent_id": "agt_reviewer-bot", "name": None,  # noqa: F821
    "cache_key": "ws:0f4d2c6e/agent:reviewer-bot/tpl:parsar-base", "sandbox_id": "sbx_2b81f0c9",
    "template_id": "parsar-base:2026.09", "status": "running", "status_kind": "live",
    "created_at": ago(hours=5), "last_active_at": ago(minutes=4), "expires_at": iso(NOW + timedelta(days=9, hours=3)),  # noqa: F821
    "metadata": {},
}]

CONNECTOR_USAGE = {"connectors": [
    {"connector_type": "feishu", "label": "Feishu / Lark", "status": "ready", "agent_count": 3,
     "agent_slugs": ["reviewer-bot", "release-notes", "docs-writer"]},
    {"connector_type": "slack", "label": "Slack", "status": "needs_config", "agent_count": 0, "agent_slugs": []},
]}

IM_CONNECTORS = {"master_key_configured": True, "connectors": [
    {"id": "imc_01J8ZT1", "workspace_id": WS, "workspace_name": "MiniMax · Infra", "platform": "feishu",  # noqa: F821
     "app_id": "cli_a7f3e9c2b1d04e5f", "enabled": True,
     "config": {"app_secret_ref": "sec_01J8ZT9A", "verification_token_ref": "", "encrypt_key_ref": "",
                "bot_open_id": "ou_9f2c1e7d", "event_mode": "websocket"},
     "created_at": ago(days=40), "updated_at": ago(days=2)},
]}


def runtimes(m, q):
    rows = RUNTIMES
    t = q.get("type", [""])[0]
    if t:
        rows = [r for r in rows if r["type"] == t]
    live = q.get("liveness", [""])[0]
    if live:
        rows = [r for r in rows if r["liveness"] == live]
    place = q.get("placement", [""])[0]
    if place == "local_device":
        rows = [r for r in rows if r["provider"] == "agent_daemon"]
    return 200, {"runtimes": rows}


def create_pairing(m, q):
    rt = device(9, "new-device", "", "", "pending_pairing", None, [])
    return 200, {"runtime": rt, "pairing_token": "rtk_7f3a9c2e1b8d4f60a5c3e9b1d2f4a6c8"}
create_pairing.methods = ("POST",)


def test_connection(m, q):
    return 200, {
        "overall": "partial", "started_at": ago(seconds=9), "duration_ms": 8400, "sandbox_id": "sbx_2b81f0c9",
        "checks": [
            {"name": "daemon_paired", "pass": True, "duration_ms": 120, "error": None},
            {"name": "daemon_online", "pass": True, "duration_ms": 80, "error": None},
            {"name": "prompt_roundtrip", "pass": False, "duration_ms": 8200,
             "error": {"category": "promptTimeout", "detail": "context deadline exceeded after 8s waiting for first token"}},
        ],
    }
test_connection.methods = ("POST",)


ROUTES = [
    # GET-only handler first; the dispatcher skips it for POST and falls through to create_pairing.
    (r"^/api/v1/workspaces/([^/]+)/runtimes$", runtimes),
    (r"^/api/v1/workspaces/([^/]+)/runtimes$", create_pairing),
    (r"^/api/v1/workspaces/([^/]+)/runtime/status$", lambda m, q: (200, {
        "has_credential": True, "credential_masked": "e2b_•••7f3a", "available": True,
        "sandbox_agent_count": 1, "profile": "oss", "configured_by": "self", "sandbox_image": "parsar-base:2026.09"})),
    (r"^/api/v1/workspaces/([^/]+)/sandboxes$", lambda m, q: (200, {"sandboxes": SANDBOXES})),
    (r"^/api/v1/workspaces/([^/]+)/agents/([^/]+)/sandbox/test-connection$", test_connection),
    (r"^/api/v1/workspaces/([^/]+)/agents/([^/]+)/sandbox$", lambda m, q: (200, SANDBOXES[0])),
    (r"^/api/v1/workspaces/([^/]+)/connector-usage$", lambda m, q: (200, CONNECTOR_USAGE)),
    (r"^/api/v1/workspaces/([^/]+)/connectors$", lambda m, q: (200, IM_CONNECTORS)),
    (r"^/api/v1/bootstrap/status$", lambda m, q: (200, {"needs_setup": False, "public_url": "https://parsar.minimax.io"})),
]
