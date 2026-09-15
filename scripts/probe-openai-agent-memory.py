#!/usr/bin/env python3
"""Probe hosted sandbox memory continuity; pause/resume remains untested."""

import argparse
import base64
import getpass
import hashlib
import hmac
import json
import os
from pathlib import Path
import secrets
import sys
import time


PROBE = r'''
import hashlib, hmac, http.server, json, os, pathlib, resource
import secrets, subprocess, sys, time, urllib.request

ROOT = pathlib.Path(__file__).resolve().parent
MARKER = ROOT / "memory-probe-marker.txt"
URL = "http://127.0.0.1:18763/"

def query(action):
    with urllib.request.urlopen(URL + action, timeout=3) as r:
        return json.load(r)

def serve():
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    secret = secrets.token_bytes(32)
    started = time.time()
    boot = pathlib.Path("/proc/sys/kernel/random/boot_id").read_text().strip()
    start_ticks = pathlib.Path("/proc/self/stat").read_text().split(") ", 1)[1].split()[19]
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_GET(self):
            action = self.path[1:]
            result = {"pid": os.getpid(), "boot_id": boot,
                      "process_start_ticks": start_ticks, "started_at": started,
                      "ticks": self.server.ticks, "observed_at": time.time(),
                      "commitment": hashlib.sha256(secret).hexdigest()}
            if action.startswith("reveal/"):
                challenge = action.split("/", 1)[1]
                result.update(secret=secret.hex(), challenge=challenge,
                              proof=hmac.new(secret, challenge.encode(), hashlib.sha256).hexdigest())
            elif action == "stop":
                self.server.running = False
            elif action != "peek":
                self.send_error(400)
                return
            body = json.dumps(result).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
    with http.server.HTTPServer(("127.0.0.1", 18763), Handler) as server:
        server.running, server.ticks, server.timeout = True, 0, 0.2
        while server.running and time.time() - started < 86400:
            server.handle_request()
            server.ticks += 1

def collect(action):
    result = {"file_exists": MARKER.exists(),
              "file_marker": MARKER.read_text() if MARKER.exists() else None}
    try:
        result.update(reachable=True, memory=query(action))
    except Exception as exc:
        result.update(reachable=False, error=type(exc).__name__)
    return result

def resources():
    limits = {}
    for name in ("memory.max", "memory.high", "cpu.max", "cpuset.cpus.effective",
                 "memory/memory.limit_in_bytes", "cpu/cpu.cfs_quota_us", "cpu/cpu.cfs_period_us"):
        path = pathlib.Path("/sys/fs/cgroup") / name
        if path.is_file():
            limits[name] = path.read_text().strip()
    disk = os.statvfs(ROOT)
    return {"visible_cpu_count": os.cpu_count(), "cpu_affinity_count": len(os.sched_getaffinity(0)),
            "cgroup": limits, "proc_meminfo": pathlib.Path("/proc/meminfo").read_text(),
            "workspace_disk_total_bytes": disk.f_blocks * disk.f_frsize,
            "workspace_disk_available_bytes": disk.f_bavail * disk.f_frsize,
            "limitation": "Observed environment visibility, not a guaranteed resource allocation."}

if sys.argv[1] == "serve":
    serve()
else:
    mode, output = sys.argv[1:3]
    if mode == "start":
        if MARKER.exists():
            raise RuntimeError("Refusing to overwrite an existing experiment")
        MARKER.write_text(secrets.token_hex(16))
        process = subprocess.Popen([sys.executable, __file__, "serve"],
                                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                   stderr=subprocess.DEVNULL, start_new_session=True)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            result = collect("peek")
            if result["reachable"]:
                break
            if process.poll() is not None:
                raise RuntimeError("Memory daemon exited during startup")
            time.sleep(0.2)
        else:
            process.terminate()
            process.wait(timeout=5)
            raise TimeoutError("Memory daemon did not start")
    elif mode == "check":
        result = collect("reveal/" + sys.argv[3])
    elif mode == "stop":
        query("stop")
        result = {"stop_requested": True}
    elif mode == "absent":
        result = collect("peek")
    elif mode == "specs":
        result = resources()
    else:
        raise ValueError("Unknown mode")
    target = pathlib.Path(output)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(result))
    print(json.dumps(result))
'''

DOC = "https://developers.openai.com/api/docs/guides/agents-api/"


def dump(path, value, key):
    text = json.dumps(value, indent=2, ensure_ascii=False).replace(key, "[REDACTED]")
    path.write_text(text + "\n")


def read_api(call):
    from openai import APIConnectionError, APIStatusError
    for attempt in range(4):
        try:
            return call()
        except (APIConnectionError, APIStatusError) as exc:
            status = getattr(exc, "status_code", None)
            if attempt == 3 or (status is not None and status not in {404, 408, 429} and status < 500):
                raise
            print(f"Retrying read after {type(exc).__name__} (status={status})", flush=True)
            time.sleep(2 ** attempt)


def wait_turn(client, session_id, previous, deadline):
    next_notice = time.monotonic() + 30
    while time.monotonic() < deadline:
        session = read_api(lambda: client.beta.agents.sessions.retrieve(session_id))
        if session.status in {"failed", "requires_action"}:
            raise RuntimeError(f"Session {session.status}: {session.error}; {session.required_actions}")
        for turn in read_api(lambda: list(client.beta.agents.sessions.turns.list(session_id, order="desc", limit=100))):
            if turn.id in previous or turn.subagent_id is not None:
                continue
            if turn.status == "completed":
                return turn
            if turn.status in {"failed", "cancelled"}:
                raise RuntimeError(f"Turn {turn.status}: {turn.error}")
        if time.monotonic() >= next_notice:
            print(f"Waiting for turn; session status={session.status}", flush=True)
            next_notice = time.monotonic() + 30
        time.sleep(3)
    raise TimeoutError("Turn did not finish before the deadline")


def artifact(client, session_id, turn_id, path):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        for item in read_api(lambda: list(client.beta.agents.sessions.artifacts.list(session_id, limit=100))):
            if item.turn_id == turn_id and item.path == path:
                content = read_api(lambda: client.beta.agents.sessions.artifacts.content(item.id, session_id=session_id).read())
                return json.loads(content)
        time.sleep(2)
    raise RuntimeError(f"Missing artifact {path} from completed turn {turn_id}")


def run_phase(client, session_id, phase, report_dir, key, timeout, challenge=""):
    previous = {t.id for t in read_api(lambda: list(client.beta.agents.sessions.turns.list(session_id, limit=100)))}
    output = f"/workspace/outputs/{phase}.json"
    command = f"python3 /workspace/memory_probe.py {phase} {output}"
    if challenge:
        command += " " + challenge
    print(f"Running {phase} phase", flush=True)
    client.beta.agents.sessions.events.create(session_id, events=[{
        "type": "agent.session.input.message",
        "input": [{"role": "user", "content": [{"type": "input_text", "text":
            f"Run this exact shell command once: {command}\n"
            "Do not edit the script, recreate the process, or fabricate outputs. "
            "Do not kill background processes except when the command is stop. "
            "If it fails, report the error without attempting a repair. Finish this turn."}]}],
    }])
    try:
        turn = wait_turn(client, session_id, previous, time.monotonic() + timeout)
    except Exception:
        try:
            dump(report_dir / f"{phase}-failure-session.json",
                 client.beta.agents.sessions.retrieve(session_id).model_dump(mode="json"), key)
            dump(report_dir / f"{phase}-failure-items.json", [x.model_dump(mode="json") for x in
                 client.beta.agents.sessions.items.list(session_id, order="asc", limit=100)], key)
        except Exception as diagnostic_error:
            print(f"Could not save failure diagnostics: {type(diagnostic_error).__name__}", flush=True)
        raise
    items = read_api(lambda: [x.model_dump(mode="json") for x in
                    client.beta.agents.sessions.items.list(session_id, order="asc", limit=100)
                    if x.turn_id == turn.id])
    dump(report_dir / f"{phase}-items.json", items, key)
    dump(report_dir / f"{phase}-turn.json", turn.model_dump(mode="json"), key)
    result = artifact(client, session_id, turn.id, output)
    dump(report_dir / f"{phase}.json", result, key)
    return result


def compare(before, after, challenge):
    result = {"file_survived": after.get("file_exists") is True
              and after.get("file_marker") == before.get("file_marker"),
              "memory_verified": False, "same_process_identity": None}
    if not before.get("reachable") or not after.get("reachable"):
        result["limitation"] = "Daemon unreachable; this alone cannot prove memory was lost."
        return result
    first, second = before["memory"], after["memory"]
    secret = bytes.fromhex(second["secret"])
    result["memory_verified"] = (
        hashlib.sha256(secret).hexdigest() == first["commitment"]
        and second["challenge"] == challenge
        and hmac.compare_digest(second["proof"],
            hmac.new(secret, challenge.encode(), hashlib.sha256).hexdigest()))
    result["same_process_identity"] = all(first[k] == second[k] for k in
        ("pid", "boot_id", "process_start_ticks", "started_at"))
    result["daemon_ticks_elapsed"] = second["ticks"] - first["ticks"]
    result["observation_seconds_elapsed"] = second["observed_at"] - first["observed_at"]
    return result


def cleanup(client, session_id):
    from openai import APIStatusError
    state = read_api(lambda: client.beta.agents.sessions.retrieve(session_id))
    if state.status in {"in_progress", "requires_action"}:
        client.beta.agents.sessions.events.create(session_id, events=[{"type": "agent.session.input.cancel"}])
    for attempt in range(6):
        try:
            client.beta.agents.sessions.delete(session_id)
            return "session_deleted_cleanup_requested"
        except APIStatusError as exc:
            if exc.status_code == 404:
                return "session_already_deleted"
            if exc.status_code != 409 or attempt == 5:
                raise
            time.sleep(3)


def main():
    parser = argparse.ArgumentParser(description=__doc__, epilog=
        "Requires the official openai Python package (tested with 3.14.0). "
        "Reads OPENAI_API_KEY or prompts without echo. Reports go under "
        "~/.parsar/experiments/agents-api-memory/. Always requests session cleanup. "
        "This paid experiment checks reconnect continuity and a stopped-process control; "
        "it does not prove recovery after sandbox suspension or expiry.")
    parser.add_argument("--model", default="gpt-5.6-sol")
    parser.add_argument("--idle-seconds", type=int, default=60,
                        help="Time with the API client closed; does not suspend the sandbox")
    parser.add_argument("--turn-timeout", type=int, default=300)
    parser.add_argument("--resources-only", action="store_true",
                        help="Only inspect visible CPU, memory, cgroup and disk information")
    args = parser.parse_args()
    if args.idle_seconds < 0 or args.turn_timeout <= 0:
        parser.error("Idle seconds must be nonnegative and turn timeout must be positive")
    from openai import DefaultHttpxClient, OpenAI, __version__
    os.umask(0o077)
    key = os.environ.get("OPENAI_API_KEY")
    if not key and sys.stdin.isatty():
        key = getpass.getpass("OpenAI API key (hidden): ")
    if not key or not key.strip():
        parser.error("Set OPENAI_API_KEY or run interactively for a hidden prompt")
    key = key.strip()
    report_dir = Path.home() / ".parsar/experiments/agents-api-memory" / (
        time.strftime("%Y%m%d-%H%M%S") + "-" + secrets.token_hex(3))
    report_dir.mkdir(parents=True)
    report = {"model": args.model, "sdk_version": __version__, "idle_seconds": args.idle_seconds,
              "endpoint": "https://api.openai.com/v1", "environment": "openai_hosted",
              "hibernate_restore": "NOT_TESTED: no documented hosted pause/resume operation",
              "sources": [DOC + "overview", DOC + "environments/openai-hosted"],
              "limitations": ["Closing the API client is not sandbox suspension.",
                              "No sandbox expiry or compute replacement is induced.",
                              "Artifacts and tool calls must be inspected if the model deviates."]}
    def audit(response):
        entry = {"at": time.time(), "method": response.request.method,
                 "path": response.request.url.path, "status": response.status_code,
                 "request_id": response.headers.get("x-request-id")}
        with (report_dir / "requests.jsonl").open("a") as log:
            log.write(json.dumps(entry) + "\n")

    def new_client():
        return OpenAI(api_key=key, base_url=report["endpoint"], timeout=30, max_retries=0,
                      http_client=DefaultHttpxClient(event_hooks={"response": [audit]}))
    client, session_id, exit_code = new_client(), None, 0
    print(f"Report directory: {report_dir}", flush=True)
    try:
        session = client.beta.agents.sessions.create(
            agent={"model": args.model, "reasoning": {"effort": "low"},
                   "instructions": "Execute the supplied experiment commands exactly. Keep responses short.",
                   "multi_agent": {"enabled": False}},
            environment={"type": "openai_hosted", "network": {"access": "disabled"},
                         "files": [{"type": "inline", "path": "/workspace/memory_probe.py",
                                    "data": base64.b64encode(PROBE.encode()).decode()}]},
            metadata={"purpose": "hosted-memory-continuity-probe"})
        session_id = session.id
        report["session_id"] = session_id
        dump(report_dir / "report.json", report, key)
        dump(report_dir / "created-session.json", session.model_dump(mode="json"), key)
        print(f"Created {session_id}", flush=True)
        if args.resources_only:
            report["resource_observation"] = run_phase(client, session_id, "specs", report_dir, key, args.turn_timeout)
        else:
            before = run_phase(client, session_id, "start", report_dir, key, args.turn_timeout)
            if not before.get("reachable"):
                raise RuntimeError("Memory daemon was not reachable at baseline")
            client.close()
            print(f"Client closed; leaving sandbox idle for {args.idle_seconds}s", flush=True)
            until = time.monotonic() + args.idle_seconds
            while time.monotonic() < until:
                time.sleep(min(30, max(0, until - time.monotonic())))
                print(f"Idle wait: {max(0, int(until - time.monotonic()))}s remaining", flush=True)
            client = new_client()
            restored = read_api(lambda: client.beta.agents.sessions.retrieve(session_id))
            dump(report_dir / "reconnected-session.json", restored.model_dump(mode="json"), key)
            report["same_environment_id"] = session.environment.id == restored.environment.id
            challenge = secrets.token_hex(32)
            after = run_phase(client, session_id, "check", report_dir, key, args.turn_timeout, challenge)
            report["continuity"] = compare(before, after, challenge)
            if after.get("reachable"):
                run_phase(client, session_id, "stop", report_dir, key, args.turn_timeout)
                absent = run_phase(client, session_id, "absent", report_dir, key, args.turn_timeout)
                report["negative_control"] = {
                    "process_unreachable_after_stop": absent.get("reachable") is False,
                    "file_survived": absent.get("file_marker") == before["file_marker"]}
        report["experiment_status"] = "completed"
    except (Exception, KeyboardInterrupt) as exc:
        exit_code = 1
        report["experiment_status"] = "incomplete"
        report["error"] = str(exc).replace(key, "[REDACTED]")
        report["request_id"] = getattr(exc, "request_id", None)
        if hasattr(exc, "request"):
            report["failed_request"] = {"method": exc.request.method, "path": exc.request.url.path}
        report["error_body"] = getattr(exc, "body", None)
        if session_id:
            try:
                dump(report_dir / "failure-items.json", [x.model_dump(mode="json") for x in
                     client.beta.agents.sessions.items.list(session_id, order="asc", limit=100)], key)
                dump(report_dir / "failure-turns.json", [x.model_dump(mode="json") for x in
                     client.beta.agents.sessions.turns.list(session_id, order="asc", limit=100)], key)
            except Exception:
                pass
        print(f"Experiment incomplete: {report['error']}", flush=True)
    finally:
        client.close()
        if session_id:
            try:
                with new_client() as final_client:
                    report["cleanup"] = cleanup(final_client, session_id)
            except Exception as exc:
                report["cleanup"] = {"error": str(exc).replace(key, "[REDACTED]")}
                exit_code = 1
        dump(report_dir / "report.json", report, key)
        print(json.dumps(report, indent=2).replace(key, "[REDACTED]"), flush=True)
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
