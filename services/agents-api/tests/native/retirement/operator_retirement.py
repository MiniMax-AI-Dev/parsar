"""Explicit local controller acceptance used by the native placement fixture."""

import hashlib
import json
from pathlib import Path
import subprocess


class OperatorRetirement:
    def __init__(self, binary, token, workspace):
        self.binary = str(binary.resolve(strict=True))
        self.token = token
        self.workspace = workspace
        self.receipt = None

    def command(self, action, instance, *extra):
        return [self.binary, "placement", action, "--container", instance, *extra]

    def enroll(self, instance):
        rejected = subprocess.run(self.command("enroll", instance, "--owner", "wrong-owner",
                                               "--workspace", str(self.workspace)), capture_output=True, timeout=10)
        assert rejected.returncode != 0, "unbound owner was accepted"
        out = subprocess.check_output(self.command("enroll", instance, "--owner", self.token,
                                                  "--workspace", str(self.workspace)), text=True, timeout=10)
        assert json.loads(out)["state"] == "enrolled"

    def retire(self, instance):
        # Separate controller processes race on the same exact durable binding.
        children = [subprocess.Popen(self.command("retire", instance), stdout=subprocess.PIPE,
                                     stderr=subprocess.PIPE, text=True) for _ in range(2)]
        results = []
        try:
            for child in children:
                out, error = child.communicate(timeout=10)
                assert child.returncode == 0, error
                results.append(json.loads(out))
        finally:
            for child in children:
                if child.poll() is None:
                    child.kill()
                    child.wait()
        assert results[0] == results[1]
        self.receipt = results[0]
        assert self.receipt["state"] == "retired"
        assert self.receipt["target"]["container"] == instance
        assert self.receipt["owner"] == self.token
        again = subprocess.check_output(self.command("retire", instance), text=True, timeout=10)
        assert json.loads(again) == self.receipt
        return {"controller_sha256": hashlib.sha256(Path(self.binary).read_bytes()).hexdigest(),
                "local_receipt": self.receipt, "concurrent_and_fresh_process_receipts_equal": True,
                "wrong_owner_rejected": True}

    def observe(self, instance, cgroup, members, process_identity):
        assert self.receipt and self.receipt["target"]["container"] == instance
        absent = subprocess.run(["docker", "--host", "unix:///var/run/docker.sock", "inspect", instance],
                                capture_output=True, timeout=10)
        assert absent.returncode != 0, "retired exact container must be removed"
        events = cgroup / "cgroup.events"
        if events.exists() and "populated 0" not in events.read_text().splitlines():
            return False
        for pid, original in members.items():
            current = process_identity(pid)
            if current and current["start"] == original["start"] and current["state"] not in {"Z", "X"}:
                return False
        return True
