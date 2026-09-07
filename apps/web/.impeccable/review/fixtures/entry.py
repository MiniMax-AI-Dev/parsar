"""Fixtures for the entry surfaces: bootstrap status, auth providers,
discoverable workspaces (join landing + discover dialog) and invite lookup.
Login (401 /me) and setup (needed=true) are produced by route interception
in shoot-entry.mjs so the shared mock stays authenticated for other agents."""

DISCOVERABLE = [
    {"id": "7c1e9b2a-3f4d-4a5b-8c6d-1e2f3a4b5c6d", "name": "MiniMax · Platform", "slug": "minimax-platform",
     "visibility": "public", "member_count": 14, "has_pending_request": False},
    {"id": "b2d4f6a8-1c3e-4f5a-9b7d-2e4f6a8c0d1e", "name": "Data Engineering", "slug": "data-eng",
     "visibility": "public", "member_count": 9, "has_pending_request": True},
    {"id": "c3e5a7b9-2d4f-4a6b-8c0d-3f5a7b9c1d2e", "name": "Developer Experience", "slug": "devx",
     "visibility": "public", "member_count": 6, "has_pending_request": False},
    {"id": "d4f6b8c0-3e5a-4b7c-9d1e-4a6b8c0d2e3f", "name": "Security", "slug": "security",
     "visibility": "public", "member_count": 4, "has_pending_request": False},
    {"id": "e5a7c9d1-4f6b-4c8d-8e2f-5b7c9d1e3f4a", "name": "Research · Agents", "slug": "research-agents",
     "visibility": "public", "member_count": 21, "has_pending_request": False},
    {"id": "f6b8d0e2-5a7c-4d9e-9f3a-6c8d0e2f4a5b", "name": "Growth", "slug": "growth",
     "visibility": "public", "member_count": 3, "has_pending_request": False},
]


def discoverable(m, q):
    needle = (q.get("q", [""])[0] or "").strip().lower()
    rows = [w for w in DISCOVERABLE if not needle or needle in w["name"].lower() or needle in w["slug"]]
    offset = int(q.get("offset", ["0"])[0])
    limit = int(q.get("limit", ["20"])[0])
    return 200, {"workspaces": rows[offset:offset + limit], "total": len(rows), "limit": limit, "offset": offset}


def bootstrap_status(m, q):
    return 200, {"needed": False, "has_owners": True, "owner_count": 1, "dev_auth_enabled": False, "public_url": ""}


def providers(m, q):
    return 200, {"providers": [
        {"id": "password", "type": "password", "label": "Password", "enabled": True},
        {"id": "feishu", "type": "oauth", "label": "Feishu", "enabled": True, "login_url": "/api/v1/auth/feishu/start"},
    ]}


def invite_info(m, q):
    return 200, {"workspace_name": "MiniMax · Infra", "email": "lin.chen@minimax.io", "role": "member"}


invite_info.methods = ("POST",)


def join_request(m, q):
    return 200, {"id": "jr_01J8ZQ", "workspace_id": m.group(1), "status": "pending"}


join_request.methods = ("POST", "DELETE")

ROUTES = [
    (r"^/api/v1/me/discoverable-workspaces$", discoverable),
    (r"^/api/v1/bootstrap/status$", bootstrap_status),
    (r"^/api/v1/auth/providers$", providers),
    (r"^/api/v1/invite/info$", invite_info),
    (r"^/api/v1/workspaces/([^/]+)/join-requests(?:/mine)?$", join_request),
]
