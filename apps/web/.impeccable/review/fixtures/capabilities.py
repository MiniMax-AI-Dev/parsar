"""Fixtures for ?admin=capabilities (list, marketplace, connectors, detail).

Receives WS, NOW, iso from mock-api.py. Capability ids line up with
agents.py (cap_github / cap_review / cap_sentry) so the agent picker and
the enabled-agent counts agree across both pages.

agents.py sorts before this file and also answers a few capability paths
for the agent picker; the mock is first-match, so once the server has
assembled ROUTES this module promotes its own routes to the front.
"""
import sys
import threading
from datetime import timedelta

WS_OTHER = "ws_other"


def ago(**kw):
    return iso(NOW - timedelta(**kw))


def cap(cid, ctype, name, desc, created_days, updated, versions, creds=None, published=False, deprecated=False):
    return {
        "id": cid, "workspace_id": WS, "type": ctype, "name": name, "description": desc,
        "scope": "public" if published else "private", "visibility": "public" if published else "workspace",
        "status": "active", "required_credentials": creds or [], "deprecated_at": ago(days=2) if deprecated else None,
        "creator_id": "usr_fj", "created_at": ago(days=created_days), "updated_at": updated,
        "latest_version": versions[0][1], "latest_version_id": versions[0][0], "latest_version_created_at": ago(days=versions[0][2]),
        "_versions": versions,
    }


OWN = [
    cap("cap_github", "mcp", "GitHub MCP", "Repositories, pull requests, issues", 60, ago(days=3),
        (("capv_gh_3", "1.4.0", 3), ("capv_gh_2", "1.3.2", 19), ("capv_gh_1", "1.2.0", 41)),
        creds=[{"kind": "github_token", "required": True}], published=True),
    cap("cap_review", "skill", "review-checklist", "House style for PR reviews", 20, ago(days=10),
        (("capv_rv_2", "0.3.1", 10), ("capv_rv_1", "0.3.0", 20))),
    cap("cap_release_notes", "skill", "release-notes", "Drafts release notes from merged pull requests since the last tag.", 60, ago(days=6),
        (("capv_rn_1", "1.0.3", 6),), published=True),
    cap("cap_linear_plugin", "plugin", "linear-sync", "Claude plugin bundle that mirrors Linear issues into the sandbox.", 12, ago(days=3),
        (("capv_ls_1", "0.9.1", 3),), creds=[{"kind": "linear_api_key", "required": True}]),
    cap("cap_postgres_mcp", "mcp", "postgres-readonly", "Read-only SQL over the analytics replica.", 90, ago(days=30),
        (("capv_pg_1", "0.4.0", 30),), creds=[{"kind": "postgres_dsn", "required": True}], deprecated=True),
]

INSTALLS = [
    {
        **cap("cap_sentry", "mcp", "Sentry MCP", "Issues and traces from Sentry", 90, ago(days=1),
              (("capv_se_5", "2.0.0", 1),), creds=[{"kind": "sentry_token", "required": True}], published=True),
        "workspace_id": WS_OTHER, "from_marketplace": True, "source_workspace_id": WS_OTHER,
        "source_workspace_name": "MiniMax · Platform", "pinned_version": "1.9.2", "pinned_version_id": "capv_se_4",
        "enabled_agent_count": 4, "creator_id": "usr_x",
    },
]


def version_rows(c):
    rows = []
    for vid, v, days in c["_versions"]:
        row = {"id": vid, "capability_id": c["id"], "version": v, "creator_id": c["creator_id"], "created_at": ago(days=days), "schema_version": 1}
        if c["type"] == "mcp":
            row["canonical_spec"] = {"schema_version": 1, "kind": "mcp", "mcp": {"servers": [{
                "name": c["name"].lower().replace(" ", "-"), "command": "docker", "args": ["run", "-i", "ghcr.io/github/github-mcp-server"],
                "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": {"mode": "credential_ref", "credential_kind_code": "github_token"}}}]}}
            row["source_payload"] = {"raw_text": '{"mcpServers": {"github": {"command": "docker"}}}', "source_format": "json"}
        elif c["type"] == "skill":
            row["canonical_spec"] = {"schema_version": 1, "kind": "skill", "skill": {
                "slug": c["name"], "title": c["name"].replace("-", " ").title(), "description": c["description"],
                "instruction": "You are a careful code reviewer. When the user pastes a diff, walk through correctness, risk and style.\n\nKeep responses concise.",
                "trigger": "when the user pastes a diff"}}
        else:
            row["canonical_spec"] = {"schema_version": 1, "kind": "plugin", "plugin": {
                "name": c["name"], "version": v, "description": c["description"], "author": "MiniMax Infra",
                "oss_key": f"plugin/{c['name']}-{v}.zip", "sha256": "9f2c4e8a1b7d3f6e0c5a2b4d8e1f7a3c9b6d2e4f8a1c3b5d7e9f0a2c4e6b8d1f", "upload_source": "zip"}}
            row["oss_key"] = f"plugin/{c['name']}-{v}.zip"
        rows.append(row)
    return rows


VERSIONS = {c["id"]: version_rows(c) for c in OWN + INSTALLS}


def public(c):
    return {k: v for k, v in c.items() if k != "_versions"}


MARKETPLACE = [
    {**public(INSTALLS[0]), "capability_id": "cap_sentry", "installed": True, "self_published": False, "install_count": 4, "installed_agent_count": 4},
    {"capability_id": "cap_jira_mcp", "id": "cap_jira_mcp", "workspace_id": WS_OTHER, "type": "mcp", "name": "jira-cloud",
     "description": "Search, create and transition Jira Cloud issues from an agent run.", "visibility": "public", "status": "active",
     "source_workspace_id": WS_OTHER, "source_workspace_name": "MiniMax · Platform",
     "required_credentials": [{"kind": "jira_api_token", "required": True}], "latest_version_id": "capv_jira_3",
     "latest_version": "2.1.0", "latest_version_created_at": ago(days=9), "installed": False, "self_published": False,
     "install_count": 7, "creator_id": "usr_x", "created_at": ago(days=40), "updated_at": ago(days=9)},
    {**public(OWN[0]), "capability_id": "cap_github", "source_workspace_id": WS, "source_workspace_name": "MiniMax · Infra",
     "installed": False, "self_published": True, "install_count": 3},
    {"capability_id": "cap_pdf_skill", "id": "cap_pdf_skill", "workspace_id": "ws_data", "type": "skill", "name": "pdf-extract",
     "description": "Extracts tables and text from PDFs with a small Python helper script.", "visibility": "public", "status": "active",
     "source_workspace_id": "ws_data", "source_workspace_name": "Data · Tooling",
     "required_credentials": [], "latest_version_id": "capv_pdf_2", "latest_version": "1.1.0",
     "latest_version_created_at": ago(days=14), "installed": False, "self_published": False, "install_count": 11,
     "creator_id": "usr_d", "created_at": ago(days=50), "updated_at": ago(days=14)},
]

MARKETPLACE_DETAIL = {
    "cap_pdf_skill": {"capability_id": "cap_pdf_skill", "type": "skill", "version_id": "capv_pdf_2", "version": "1.1.0",
                      "git_repo_url": "https://github.com/minimax-data/agent-skills", "git_ref": "8e1f7a3c", "path": "skills/pdf-extract",
                      "skill": {"slug": "pdf-extract", "title": "PDF extract", "description": "Extracts tables and text from PDFs.",
                                "instruction": "# PDF extract\n\nRun `scripts/extract.py <file>` and summarise the tables it prints.\n\nNever guess numbers that are not in the output.",
                                "trigger": "when the user attaches a PDF",
                                "files": [{"path": "scripts/extract.py", "content": "import sys\nimport pdfplumber\n\nwith pdfplumber.open(sys.argv[1]) as pdf:\n    for page in pdf.pages:\n        print(page.extract_text())\n", "kind": "script"},
                                          {"path": "references/tables.md", "content": "# Table conventions\n\nAlways keep the header row.\n", "kind": "markdown"}]}},
    "cap_jira_mcp": {"capability_id": "cap_jira_mcp", "type": "mcp", "version_id": "capv_jira_3", "version": "2.1.0",
                     "mcp": {"servers": [{"name": "jira", "command": "npx", "args": ["-y", "@atlassian/mcp-server-jira"],
                                          "env": {"JIRA_SITE": {"mode": "literal", "value": "https://minimax.atlassian.net"},
                                                  "JIRA_API_TOKEN": {"mode": "credential_ref", "credential_kind_code": "jira_api_token"}},
                                          "startup_timeout_sec": 30}]}},
    "cap_sentry": {"capability_id": "cap_sentry", "type": "mcp", "version_id": "capv_se_5", "version": "2.0.0",
                   "mcp": {"servers": [{"name": "sentry", "command": "npx", "args": ["-y", "@sentry/mcp-server"],
                                        "env": {"SENTRY_AUTH_TOKEN": {"mode": "inline_secret", "redacted": True}}}]}},
    "cap_github": {"capability_id": "cap_github", "type": "mcp", "version_id": "capv_gh_3", "version": "1.4.0",
                   "mcp": {"servers": [{"name": "github", "command": "docker", "args": ["run", "-i", "ghcr.io/github/github-mcp-server"],
                                        "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": {"mode": "credential_ref", "credential_kind_code": "github_token"}}}]}},
}

DIRECTORY = [
    {"id": "notion", "name": "Notion", "description": "Search pages and databases in a Notion workspace and append to them.",
     "publisher": {"name": "Notion Labs", "url": "https://www.notion.so"}, "homepage_url": "https://developers.notion.com/docs/mcp",
     "repository_url": "https://github.com/makenotion/notion-mcp-server", "verified": True, "categories": ["Docs", "Productivity"],
     "featured_rank": 1, "version": "1.8.1", "transport": "streamable-http", "authentication": "oauth2", "connected": False,
     "url": "https://mcp.notion.com/mcp", "installed": False, "installed_capability_id": None},
    {"id": "linear", "name": "Linear", "description": "Create and update Linear issues, cycles and projects.",
     "publisher": {"name": "Linear", "url": "https://linear.app"}, "homepage_url": "https://linear.app/docs/mcp",
     "repository_url": None, "verified": True, "categories": ["Issue tracking"], "featured_rank": 2, "version": "0.6.0",
     "transport": "streamable-http", "authentication": "oauth2", "connected": True, "url": "https://mcp.linear.app/mcp",
     "installed": True, "installed_capability_id": "cap_linear_plugin"},
    {"id": "context7", "name": "Context7", "description": "Up-to-date library documentation injected into the model context.",
     "publisher": {"name": "Upstash", "url": "https://upstash.com"}, "homepage_url": "https://context7.com",
     "repository_url": "https://github.com/upstash/context7", "verified": False, "categories": ["Docs"], "featured_rank": 5,
     "version": "1.0.14", "transport": "streamable-http", "authentication": "none", "connected": False,
     "url": "https://mcp.context7.com/mcp", "installed": False, "installed_capability_id": None},
]


def find(rows, key, value):
    for row in rows:
        if row.get(key) == value:
            return row
    return None


def list_caps(m, q):
    ctype = q.get("type", [""])[0]
    name = q.get("name", [""])[0].strip().lower()

    def keep(c):
        if ctype and not (c["type"] == ctype or (ctype == "bundle" and c["type"] == "plugin")):
            return False
        return not name or name in (c["name"] + " " + c["description"]).lower()

    own = [public(c) for c in OWN if keep(c)]
    inst = [public(c) for c in INSTALLS if keep(c)]
    return 200, {"workspace_id": WS, "capabilities": own, "marketplace_installs": inst, "total": len(own) + len(inst), "page": 1, "page_size": 20}


def get_cap(m, q):
    row = find(OWN, "id", m.group(2)) or find(INSTALLS, "id", m.group(2))
    return (200, public(row)) if row else (404, {"error": "not_found"})


def versions(m, q):
    return 200, {"capability_id": m.group(2), "versions": VERSIONS.get(m.group(2), [])}


def marketplace_detail(m, q):
    d = MARKETPLACE_DETAIL.get(m.group(1))
    return (200, {"capability": d}) if d else (404, {"error": "not_found"})


def directory_item(m, q):
    row = find(DIRECTORY, "id", m.group(2))
    return (200, row) if row else (404, {"error": "not_found"})


ROUTES = [
    (r"^/api/v1/workspaces/([^/]+)/capabilities$", list_caps),
    (r"^/api/v1/workspaces/([^/]+)/capabilities/marketplace-installs$", lambda m, q: (200, {"installs": [public(c) for c in INSTALLS]})),
    (r"^/api/v1/workspaces/([^/]+)/capabilities/([^/]+)/versions$", versions),
    (r"^/api/v1/workspaces/([^/]+)/capabilities/([^/]+)/install-count$", lambda m, q: (200, {"count": 3})),
    (r"^/api/v1/workspaces/([^/]+)/capabilities/([^/]+)/enabled-agents$",
     lambda m, q: (200, {"agents": [{"agent_id": "agt_01", "name": "reviewer-bot", "version": "1.9.2"}, {"agent_id": "agt_02", "name": "release-notes", "version": "1.9.2"}]})),
    (r"^/api/v1/workspaces/([^/]+)/capabilities/([^/]+)$", get_cap),
    (r"^/api/v1/capabilities/marketplace$", lambda m, q: (200, {"capabilities": MARKETPLACE})),
    (r"^/api/v1/capabilities/marketplace/([^/]+)$", marketplace_detail),
    (r"^/api/v1/workspaces/([^/]+)/mcp-directory$", lambda m, q: (200, {"items": DIRECTORY})),
    (r"^/api/v1/workspaces/([^/]+)/mcp-directory/([^/]+)$", directory_item),
    (r"^/api/v1/workspaces/([^/]+)/credential-kinds$", lambda m, q: (200, {"items": [
        {"id": "ck_1", "code": "github_token", "display_name": "GitHub token", "description": "Personal access token", "value_schema": {},
         "built_in": True, "source": "platform_oauth", "created_at": ago(days=90), "updated_at": ago(days=90)}]})),
]

_MINE = {h for _, h in ROUTES}


def _promote():
    main = sys.modules.get("__main__")
    routes = getattr(main, "ROUTES", None)
    if not isinstance(routes, list):
        return
    mine = [r for r in routes if r[1] in _MINE]
    rest = [r for r in routes if r[1] not in _MINE]
    routes[:] = mine + rest


threading.Timer(0.3, _promote).start()
