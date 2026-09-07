#!/usr/bin/env python3
"""Minimal mock of the Parsar API for visual verification of the web UI.

Core fixtures (me, workspaces, agent runs) live here. Page-specific fixtures
live in ./fixtures/<page>.py, each exposing `ROUTES = [(regex, handler)]`
where handler(match, query) -> (status, body). Later files win on ties.
Unknown GETs answer an empty list-shaped body; POST/PUT/PATCH/DELETE answer {}.
"""
import glob
import importlib.util
import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
WS = "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f"
NOW = datetime(2026, 9, 5, 9, 47, 8, tzinfo=timezone.utc)
HERE = os.path.dirname(os.path.abspath(__file__))


def iso(dt):
    return dt.strftime("%Y-%m-%dT%H:%M:%SZ")


RUNS = []


def add(i, agent, status, conv, connector, started_min_ago, dur_s, created_min_ago, error=None):
    rid = f"run_01J8Z{i:02d}KX2P9Q{i:02d}"
    created = NOW - timedelta(minutes=created_min_ago)
    started = NOW - timedelta(minutes=started_min_ago) if started_min_ago is not None else None
    finished = started + timedelta(seconds=dur_s) if started and dur_s is not None else None
    RUNS.append({
        "id": rid, "workspace_id": WS, "conversation_id": conv, "agent_id": f"agt_{agent}",
        "agent_name": agent, "agent_slug": agent, "connector_type": connector, "status": status,
        "error_summary": error, "created_at": iso(created),
        "started_at": iso(started) if started else None, "finished_at": iso(finished) if finished else None,
    })


add(1, "reviewer-bot", "running", "conv_01J8ZM3Q7K", "agent_daemon", 4, None, 4)
add(2, "release-notes", "completed", "conv_01J8ZKX2P9", "agent_daemon", 28, 171, 29)
add(3, "reviewer-bot", "completed", "conv_01J8ZKR8T4", "agent_daemon", 66, 363, 67)
add(4, "migrate-helper", "failed", "conv_01J8ZK9A1M", "http-agent", 121, 48, 122, "migration 0042: relation \"agent_runs\" already exists")
add(5, "reviewer-bot", "queued", "conv_01J8ZJH6W2", "agent_daemon", None, None, 130)
add(6, "docs-writer", "completed", "conv_01J8ZJ3C5N", "agent_daemon", 181, 97, 182)
add(7, "migrate-helper", "cancelled", "conv_01J8ZHT0X8", "http-agent", 300, 12, 301)
add(8, "reviewer-bot", "completed", "conv_01J8ZH2R9Q", "agent_daemon", 1500, 224, 1501)
add(9, "release-notes", "interrupted", "conv_01J8ZGF4K7", "agent_daemon", 1600, 320, 1601, "daemon disconnected during tool call")
add(10, "docs-writer", "completed", "conv_01J8ZFQ1B3", "agent_daemon", 1700, 58, 1701)

EVENTS = {}
for r in RUNS:
    base = datetime.fromisoformat(r["created_at"].replace("Z", "+00:00"))
    evs = [{"id": f"{r['id']}-e1", "sequence": 1, "event_kind": "run.queued", "payload": {"position": 1}, "occurred_at": iso(base)}]
    if r["started_at"]:
        evs += [
            {"id": f"{r['id']}-e2", "sequence": 2, "event_kind": "run.started", "payload": {"source": r["connector_type"]}, "occurred_at": r["started_at"]},
            {"id": f"{r['id']}-e3", "sequence": 3, "event_kind": "tool.call", "payload": {"name": "read_file", "action": "read"}, "occurred_at": r["started_at"]},
            {"id": f"{r['id']}-e4", "sequence": 4, "event_kind": "tool.result", "payload": {"name": "read_file"}, "occurred_at": r["started_at"]},
            {"id": f"{r['id']}-e5", "sequence": 5, "event_kind": "message.delta", "payload": {"delta": "x" * 1420}, "occurred_at": r["started_at"]},
            {"id": f"{r['id']}-e6", "sequence": 6, "event_kind": "tool.call", "payload": {"name": "bash", "action": "make check-web"}, "occurred_at": r["started_at"]},
        ]
    if r["status"] == "completed":
        evs.append({"id": f"{r['id']}-e9", "sequence": 9, "event_kind": "run.completed", "payload": {}, "occurred_at": r["finished_at"]})
    if r["status"] == "failed":
        evs.append({"id": f"{r['id']}-e9", "sequence": 9, "event_kind": "run.failed", "payload": {"error": r["error_summary"], "source": "daemon"}, "occurred_at": r["finished_at"]})
    if r["status"] == "cancelled":
        evs.append({"id": f"{r['id']}-e9", "sequence": 9, "event_kind": "run.cancelled", "payload": {"reason": "user_clicked_cancel", "source": "web"}, "occurred_at": r["finished_at"]})
    EVENTS[r["id"]] = evs


def detail(r):
    d = dict(r)
    d.update({
        "requested_by_type": "user", "requested_by_id": "usr_fj", "metadata": {}, "updated_at": r["created_at"],
        "runtime": {
            "id": "rt_01J8ZR", "name": "mbp-fanjingluo", "type": "agent_daemon", "provider": "local",
            "connector_type": r["connector_type"], "agent_kind": "claude_code", "runtime_mode": "sandbox",
            "execution_place": "docker", "governance_mode": "workspace", "device_id": "dev_7f3a9c",
            "sandbox_id": "sbx_2b81", "managed_model_id": "claude-opus-5",
            "capabilities": {"approvals": True, "artifacts": True, "streaming": True, "user_input": False},
            "liveness": "online", "hostname": "mbp-fanjingluo.local", "version": "0.9.2",
            "last_heartbeat_at": iso(NOW - timedelta(seconds=12)), "working_directory": "~/dev/parsar",
            "captured_at": r["started_at"] or r["created_at"],
        },
        "artifacts": [
            {"id": "art_1", "medium": "git", "kind": "diff", "name": "store-split.diff", "uri": "refs/heads/feat/split-routes"},
            {"id": "art_2", "medium": "s3", "kind": "log", "name": "run.log", "uri": "s3://parsar/logs/run.log"},
        ] if r["status"] == "completed" else [],
        "events": EVENTS[r["id"]],
    })
    return d


def core_routes():
    def me(m, q):
        return 200, {"user_id": "usr_fj", "email": "fanjingluo@minimax.io", "name": "fanjingluo", "avatar_url": ""}

    def my_ws(m, q):
        return 200, {"user_id": "usr_fj", "workspaces": [
            {"id": WS, "name": "MiniMax · Infra", "slug": "minimax-infra", "visibility": "private", "role": "admin",
             "created_at": iso(NOW), "updated_at": iso(NOW)}]}

    def runs(m, q):
        statuses = [s for s in q.get("status", [""])[0].split(",") if s]
        rows = [r for r in RUNS if not statuses or r["status"] in statuses]
        offset = int(q.get("offset", ["0"])[0]); limit = int(q.get("limit", ["20"])[0])
        return 200, {"agent_runs": rows[offset:offset + limit], "total": len(rows), "limit": limit, "offset": offset, "statuses": statuses or None}

    def run_detail(m, q):
        for r in RUNS:
            if r["id"] == m.group(1):
                return 200, detail(r)
        return 404, {"error": "not_found"}

    def run_events(m, q):
        return 200, {"events": EVENTS.get(m.group(2), [])}

    return [
        (r"^/api/v1/me$", me),
        (r"^/api/v1/me/workspaces$", my_ws),
        (r"^/api/v1/me/discoverable-workspaces$", lambda m, q: (200, {"workspaces": [], "total": 0})),
        (r"^/api/v1/auth/providers$", lambda m, q: (200, {"providers": []})),
        (r"^/api/v1/workspaces/([^/]+)/agent-runs$", runs),
        (r"^/api/v1/agent-runs/([^/]+)$", run_detail),
        (r"^/api/v1/workspaces/([^/]+)/agent-runs/([^/]+)/events$", run_events),
    ]


def load_fixtures():
    # First match wins. Files load in name order; a fixture may set
    # `PRIORITY = n` (default 0) to be consulted before lower-priority files
    # when two pages serve the same path (stable within a priority).
    loaded = []
    for path in sorted(glob.glob(os.path.join(HERE, "fixtures", "*.py"))):
        spec = importlib.util.spec_from_file_location("fixture_" + os.path.basename(path)[:-3], path)
        mod = importlib.util.module_from_spec(spec)
        mod.WS, mod.NOW, mod.iso = WS, NOW, iso  # shared helpers for fixtures
        try:
            spec.loader.exec_module(mod)
            fixture_routes = [(re.compile(p), h) for p, h in getattr(mod, "ROUTES", [])]
            loaded.append((getattr(mod, "PRIORITY", 0), fixture_routes))
            sys.stderr.write(f"mock: loaded {os.path.basename(path)} ({len(fixture_routes)} routes)\n")
        except Exception as e:  # noqa: BLE001
            sys.stderr.write(f"mock: FAILED to load {path}: {e}\n")
    loaded.sort(key=lambda item: -item[0])
    return [route for _, fixture_routes in loaded for route in fixture_routes]


ROUTES = [(re.compile(p), h) for p, h in core_routes()]
ROUTES = load_fixtures() + ROUTES  # fixtures first so they may override core


class H(BaseHTTPRequestHandler):
    def _json(self, body, status=200):
        raw = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _dispatch(self, method):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        for rx, handler in ROUTES:
            m = rx.match(u.path)
            if m:
                try:
                    accepts = getattr(handler, "methods", ("GET",))
                    if method not in accepts and not (method == "GET"):
                        continue
                    status, body = handler(m, q)
                    return self._json(body, status)
                except Exception as e:  # noqa: BLE001
                    return self._json({"error": "fixture_error", "detail": str(e)}, 500)
        if method != "GET":
            return self._json({})
        p = u.path
        if "audit" in p:
            return self._json({"entries": [], "items": [], "total": 0})
        if "plugin" in p:
            return self._json({"plugins": [], "items": []})
        if p.endswith("/agents"):
            return self._json({"agents": [], "total": 0})
        return self._json({"items": [], "total": 0})

    def do_GET(self):
        self._dispatch("GET")

    def do_POST(self):
        self._dispatch("POST")

    def do_PUT(self):
        self._dispatch("PUT")

    def do_PATCH(self):
        self._dispatch("PATCH")

    def do_DELETE(self):
        self._dispatch("DELETE")

    def log_message(self, fmt, *args):
        sys.stderr.write("mock: " + (fmt % args) + "\n")


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", PORT), H).serve_forever()
