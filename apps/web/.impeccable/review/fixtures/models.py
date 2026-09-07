"""Models page fixtures: six catalog models across two providers, one shared
secret, two credential kinds, and a connectivity-test result per model.
Globals WS / NOW / iso are injected by mock-api.py."""
from datetime import timedelta

# Consulted before agents.py, which also answers /models and /secrets.
PRIORITY = 1

ME = "usr_fj"
OTHER = "usr_wcy"
SECRET_ID = "sec_01J8ZANTHROPIC01"


def ago(minutes):
    return iso(NOW - timedelta(minutes=minutes))


def health(status, minutes, endpoint, latency, **extra):
    h = {"status": status, "checked_at": ago(minutes), "endpoint_type": endpoint, "latency_ms": latency}
    h.update(extra)
    return h


def model(i, name, provider, adapter, base_url, key, mode, created_by, minutes_ago, config, secret_id=None, kind=None, status="active"):
    m = {
        "id": f"mdl_01J8Z{i:02d}M0DEL{i:02d}", "slug": key.replace(".", "-"), "name": name,
        "provider_type": provider, "adapter": adapter, "base_url": base_url, "model_key": key,
        "credential_mode": mode, "status": status, "config": config, "created_by": created_by,
        "created_at": ago(minutes_ago), "updated_at": ago(minutes_ago // 2),
    }
    if mode == "inline_secret":
        m["secret_id"] = secret_id if secret_id is not None else SECRET_ID
    else:
        m["credential_kind_code"] = kind or "anthropic_api_key"
    return m


ANTHROPIC = "https://api.anthropic.com"
GATEWAY = "https://llm-gateway.minimax.internal/v1"

MODELS = [
    model(1, "Claude Opus 4.5", "anthropic", "@ai-sdk/anthropic", ANTHROPIC, "claude-opus-4-5-20251101",
          "inline_secret", ME, 9 * 24 * 60,
          {"supported_endpoint_types": ["anthropic"],
           "health": health("healthy", 42, "anthropic", 812, http_status=200, sample="Hello! How can I help you today?")}),
    model(2, "Claude Sonnet 4.5", "anthropic", "@ai-sdk/anthropic", ANTHROPIC, "claude-sonnet-4-5-20250929",
          "inline_secret", ME, 9 * 24 * 60,
          {"supported_endpoint_types": ["anthropic"],
           "health": health("healthy", 42, "anthropic", 634, http_status=200, sample="Hi there.")}),
    model(3, "Claude Haiku 4.5 · personal", "anthropic", "@ai-sdk/anthropic", ANTHROPIC, "claude-haiku-4-5-20251001",
          "credential_ref", OTHER, 3 * 24 * 60,
          {"supported_endpoint_types": ["anthropic"]}, kind="anthropic_api_key"),
    model(4, "GLM-4.6 via gateway", "openai-compatible", "@ai-sdk/openai-compatible", GATEWAY, "glm-4.6",
          "inline_secret", ME, 26 * 60,
          {"supported_endpoint_types": ["openai", "openai-response"],
           "endpoint_base_urls": {"openai": GATEWAY, "openai-response": GATEWAY},
           "headers": {"X-Tenant": "infra"},
           "health": health("failed", 7, "openai", 1204, http_status=401,
                            error="401 Unauthorized: invalid api key (key rotated on 2026-09-04)")}),
    model(5, "DeepSeek V3.2", "openai-compatible", "@ai-sdk/openai-compatible", GATEWAY, "deepseek-v3.2",
          "inline_secret", ME, 26 * 60,
          {"supported_endpoint_types": ["openai"],
           "health": health("healthy", 3 * 60, "openai", 1580, http_status=200, sample="Sure, here is a short answer.")}),
    model(6, "Qwen3 Coder 480B", "openai-compatible", "@ai-sdk/openai-compatible", GATEWAY, "qwen3-coder-480b-a35b",
          "inline_secret", ME, 55,
          {"supported_endpoint_types": ["openai"]}, secret_id=""),
]

# The three rows fixtures/agents.py serves (same ids) so the Agents page
# resolves its model references whichever fixture answers /models.
AGENTS_PAGE_MODELS = [
    {"id": "mdl_claude_opus", "slug": "claude-opus-5", "name": "Claude Opus 5", "provider_type": "anthropic",
     "adapter": "anthropic", "base_url": "https://api.anthropic.com", "model_key": "claude-opus-5",
     "credential_mode": "credential_ref", "credential_kind_code": "anthropic_api_key", "status": "active",
     "config": {"protocols": ["anthropic"]}, "created_by": OTHER, "created_at": ago(40 * 24 * 60), "updated_at": ago(2 * 24 * 60)},
    {"id": "mdl_gpt5_codex", "slug": "gpt-5-codex", "name": "GPT-5 Codex", "provider_type": "openai",
     "adapter": "openai", "base_url": "https://api.openai.com/v1", "model_key": "gpt-5-codex",
     "credential_mode": "credential_ref", "credential_kind_code": "openai_api_key", "status": "active",
     "config": {"protocols": ["openai_responses"]}, "created_by": OTHER, "created_at": ago(30 * 24 * 60), "updated_at": ago(5 * 24 * 60)},
    {"id": "mdl_minimax_m2", "slug": "minimax-m2", "name": "MiniMax M2", "provider_type": "minimax",
     "adapter": "openai", "base_url": "https://api.minimax.io/v1", "model_key": "MiniMax-M2",
     "credential_mode": "inline_secret", "secret_id": "sec_m2", "status": "active",
     "config": {"protocols": ["openai_chat", "anthropic"]}, "created_by": ME, "created_at": ago(12 * 24 * 60), "updated_at": ago(24 * 60)},
]
MODELS = MODELS + AGENTS_PAGE_MODELS

SECRETS = [{
    "id": SECRET_ID, "slug": "anthropic-org-key", "name": "Anthropic · org key", "kind": "model_provider",
    "provider": "anthropic", "auth_type": "api_key", "key_version": "3", "status": "active",
    "masked": "sk-ant-…4f2a", "metadata": {}, "created_at": ago(30 * 24 * 60), "updated_at": ago(2 * 24 * 60),
}, {
    "id": "sec_01J8ZGATEWAY0002", "slug": "gateway-shared", "name": "LLM gateway · shared", "kind": "model_provider",
    "provider": "openai-compatible", "auth_type": "api_key", "key_version": "1", "status": "active",
    "masked": "gw-…91c0", "metadata": {}, "created_at": ago(27 * 60), "updated_at": ago(27 * 60),
}]

KINDS = [{
    "id": "ck_01", "code": "anthropic_api_key", "display_name": "Anthropic API Key",
    "description": "Personal Anthropic key, one per user.", "value_schema": {}, "built_in": True,
    "source": "builtin", "created_at": ago(60 * 24 * 60), "updated_at": ago(60 * 24 * 60),
}, {
    "id": "ck_02", "code": "openai_api_key", "display_name": "OpenAI API Key",
    "description": "Personal OpenAI-compatible key.", "value_schema": {}, "built_in": True,
    "source": "builtin", "created_at": ago(60 * 24 * 60), "updated_at": ago(60 * 24 * 60),
}]


def list_models(m, q):
    return 200, {"models": MODELS}


def list_secrets(m, q):
    return 200, {"secrets": SECRETS}


def list_kinds(m, q):
    return 200, {"items": KINDS, "total": len(KINDS)}


def test_model(m, q):
    target = next((x for x in MODELS if x["id"] == m.group(2)), MODELS[0])
    ok = target["config"].get("health", {}).get("status") != "failed"
    req = {"method": "POST", "url": target["base_url"] + "/chat/completions",
           "headers": {"authorization": "Bearer ••••", "content-type": "application/json"},
           "body": {"model": target["model_key"], "max_tokens": 8, "messages": [{"role": "user", "content": "ping"}]}}
    if ok:
        results = [{
            "endpoint_type": "openai", "supported": True, "success": True, "latency_ms": 1180, "http_status": 200,
            "sample": "pong", "request": req,
            "response": {"status": 200, "headers": {"content-type": "application/json"},
                         "body": {"id": "chatcmpl-9x2", "choices": [{"message": {"role": "assistant", "content": "pong"}}]}},
        }, {
            "endpoint_type": "openai-response", "supported": True, "success": True, "latency_ms": 1320, "http_status": 200,
            "sample": "pong", "request": dict(req, url=target["base_url"] + "/responses"),
            "response": {"status": 200, "body": {"id": "resp_7a1", "output_text": "pong"}},
        }]
        return 200, {"supported": True, "success": True, "latency_ms": 1180, "http_status": 200,
                     "endpoint_type": "openai", "sample": "pong", "healthy_count": 2, "total_count": 2, "results": results}
    results = [{
        "endpoint_type": "openai", "supported": True, "success": False, "latency_ms": 1204, "http_status": 401,
        "failure_stage": "auth", "error": "401 Unauthorized: invalid api key (key rotated on 2026-09-04)", "request": req,
        "response": {"status": 401, "headers": {"content-type": "application/json"},
                     "body": {"error": {"type": "authentication_error", "message": "invalid api key"}}},
    }, {
        "endpoint_type": "openai-response", "supported": False, "success": False, "latency_ms": 0,
        "error": "endpoint not advertised by gateway", "request": dict(req, url=target["base_url"] + "/responses"),
    }]
    return 200, {"supported": True, "success": False, "latency_ms": 1204, "http_status": 401, "endpoint_type": "openai",
                 "error": results[0]["error"], "healthy_count": 0, "total_count": 2, "results": results}


test_model.methods = ("POST",)

ROUTES = [
    (r"^/api/v1/workspaces/([^/]+)/models$", list_models),
    (r"^/api/v1/workspaces/([^/]+)/secrets$", list_secrets),
    (r"^/api/v1/workspaces/([^/]+)/credential-kinds$", list_kinds),
    (r"^/api/v1/workspaces/([^/]+)/models/([^/]+)/test$", test_model),
]
