#!/usr/bin/env python3
"""Qualify exact-placement retirement; never a production ownership receipt."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import uuid

from operator_retirement import OperatorRetirement

TEST = "server::processor::tests::retirement_qualification::native_placement_worker"
LABEL = "parsar.retirement-qualification"


def docker(*args, timeout=15):
    return subprocess.check_output(["docker", "--host", "unix:///var/run/docker.sock", *args], text=True, timeout=timeout).strip()


def inspect(instance):
    return json.loads(docker("inspect", instance))[0]


def process_identity(pid):
    try:
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        return {"start": fields[19], "state": fields[0], "group": fields[2], "session": fields[3]}
    except FileNotFoundError:
        return None


def observe_retired(instance, token, cgroup, members):
    state = inspect(instance)
    assert state["Config"]["Labels"][LABEL] == token
    if state["State"]["Running"] or state["State"]["Pid"] != 0:
        return False
    events = cgroup / "cgroup.events"
    if events.exists() and "populated 0" not in events.read_text().splitlines():
        return False
    for pid, original in members.items():
        current = process_identity(pid)
        if current and current["start"] == original["start"] and current["state"] != "Z":
            return False
    return True


def await_condition(condition, seconds, label):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if condition():
            return
        time.sleep(0.025)
    raise RuntimeError("timed out: " + label)


def qualify(args):
    runtime = (Path.home() / ".parsar").resolve()
    root = args.output.resolve()
    if not root.is_relative_to(runtime) or root == runtime:
        raise ValueError("output must be a new directory below ~/.parsar")
    binary = args.binary.resolve(strict=True)
    if os.getuid() == 0:
        raise ValueError("run qualification as the ordinary executor user")
    if not args.image.startswith("sha256:") or len(args.image) != 71:
        raise ValueError("use the qualified immutable image ID")
    root.mkdir(parents=True, mode=0o700, exist_ok=False)
    workspace = root / "workspace"
    workspace.mkdir(mode=0o700)
    (workspace / "write.bin").write_bytes(b"initial")
    filesystem = subprocess.check_output(["stat", "-f", "-c", "%T", str(workspace)], text=True).strip()
    if filesystem not in {"ext2/ext3", "xfs", "btrfs", "tmpfs"}:
        raise ValueError("this qualification requires a supported local filesystem")
    token = uuid.uuid4().hex
    instances = []
    removed = set()
    controller = OperatorRetirement(args.controller, token, workspace) if args.controller else None
    evidence = {"qualified": False, "instrumented": True, "model_calls": 0,
                "image": args.image, "filesystem": filesystem, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "scope": "task-owned local-filesystem Docker placement; no Core/remote authority claim"}

    def start(mode):
        name = "parsar-retirement-" + token[:12] + "-" + mode
        container_path = "/qualification/.parsar/task"
        instance = docker("create", "--name", name, "--label", LABEL + "=" + token,
                          "--label", "parsar.runtime.placement=" + token,
                          "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
                          "--restart", "no", "--user", f"{os.getuid()}:{os.getgid()}",
                          "--env", "HOME=/qualification", "--env", "PARSAR_RETIREMENT_MODE=" + mode,
                          "--env", "PARSAR_RETIREMENT_WORKSPACE=" + container_path,
                          "--mount", f"type=bind,src={binary},dst=/native-test,readonly",
                          "--mount", f"type=bind,src={workspace},dst={container_path}",
                          "--workdir", container_path, "--entrypoint", "/native-test", args.image,
                          "--exact", TEST, "--ignored", "--nocapture")
        instances.append(instance)
        docker("start", instance)
        return instance

    try:
        neighbor = None
        if controller:
            neighbor = docker("run", "-d", "--label", LABEL + "=" + token,
                              "--network", "none", "--entrypoint", "sleep", args.image, "60")
            instances.append(neighbor)
            neighbor_init = inspect(neighbor)["State"]["Pid"]
        old = start("held")
        await_condition(lambda: (workspace / "worker-entered").exists(), 7, "native worker gate")
        state = inspect(old)
        init = state["State"]["Pid"]
        assert init > 0 and state["State"]["Running"]
        host_config = state["HostConfig"]
        assert not host_config["Privileged"] and host_config["PidMode"] != "host"
        assert host_config["RestartPolicy"]["Name"] == "no"
        assert "ALL" in host_config["CapDrop"]
        assert "no-new-privileges" in host_config["SecurityOpt"]
        groups = Path(f"/proc/{init}/cgroup").read_text().splitlines()
        group = next(line[3:] for line in groups if line.startswith("0::"))
        cgroup = Path("/sys/fs/cgroup") / group.lstrip("/")
        assert cgroup != Path("/sys/fs/cgroup") and (cgroup / "cgroup.events").exists()
        assert "populated 1" in (cgroup / "cgroup.events").read_text().splitlines()
        members = {}
        for procs in cgroup.rglob("cgroup.procs"):
            for pid in procs.read_text().splitlines():
                identity = process_identity(pid)
                if identity:
                    members[int(pid)] = identity
        assert init in members and len(members) >= 3
        descendant = int((workspace / "descendant.pid").read_text())
        native_command = int((workspace / "command.pid").read_text())
        # The namespace PID has to be the leader of its own session, separately
        # from the native command; this is not merely another process-group member.
        stat = docker("exec", old, "cat", f"/proc/{descendant}/stat")
        fields = stat.rsplit(")", 1)[1].split()
        assert int(fields[2]) == descendant and int(fields[3]) == descendant
        assert descendant != native_command
        assert (workspace / "write.bin").read_bytes() == b"initial"
        assert not observe_retired(old, token, cgroup, members)
        try:
            observe_retired("parsar-missing-" + token, token, cgroup, members)
        except subprocess.CalledProcessError:
            evidence["missing_retirement_observation_rejected"] = True
        else:
            raise AssertionError("missing supervisor evidence cannot authorize a successor")
        assert len(instances) == (2 if controller else 1) and not (workspace / "successor-receipt").exists()
        evidence.update(old_instance=old, cgroup=str(cgroup), observed_members=members,
                        detached_namespace_pid=descendant, live_owner_rejected=True)
        began = time.monotonic()
        if controller:
            controller.enroll(old)
            evidence.update(controller.retire(old))
            removed.add(old)
            settled = lambda: controller.observe(old, cgroup, members, process_identity)
            assert inspect(neighbor)["State"]["Pid"] == neighbor_init
            evidence["neighbor_untouched"] = True
        else:
            docker("stop", "--timeout", "1", old)
            settled = lambda: observe_retired(old, token, cgroup, members)
            evidence["stopped_state"] = inspect(old)["State"]
        await_condition(settled, 3, "placement retirement")
        evidence["stop_seconds"] = time.monotonic() - began
        counts = [(workspace / name).stat().st_size for name in ("heartbeat", "descendant-heartbeat")]
        assert (workspace / "write.bin").read_bytes() == b"initial"
        successor = start("successor")
        exit_code = docker("wait", successor)
        assert exit_code == "0", "successor native test failed"
        assert (workspace / "successor-receipt").read_bytes() == b"acknowledged"
        assert (workspace / "write.bin").read_bytes() == b"new-owner"
        time.sleep(0.2)
        assert [(workspace / name).stat().st_size for name in ("heartbeat", "descendant-heartbeat")] == counts
        assert settled()
        assert (workspace / "write.bin").read_bytes() == b"new-owner"
        evidence.update(qualified=True, successor_instance=successor, old_write_receipt="unknown",
                        old_mutation_replayed=False, detached_effects_stopped=True,
                        successor_native_receipt=True, retained_bytes="new-owner")
    finally:
        cleanup, cleanup_errors = [], []
        for instance in reversed(instances):
            if instance in removed:
                cleanup.append(instance)
                continue
            try:
                state = inspect(instance)
                assert state["Config"]["Labels"][LABEL] == token
                logs = subprocess.run(["docker", "logs", instance], text=True, capture_output=True, timeout=10)
                (root / (instance + ".log")).write_text(logs.stdout + logs.stderr)
                docker("rm", "--force", instance)
                cleanup.append(instance)
            except Exception as error:
                cleanup_errors.append({"instance": instance, "error": str(error)})
        evidence["removed_task_instances"] = cleanup
        evidence["cleanup_errors"] = cleanup_errors
        (root / "result.json").write_text(json.dumps(evidence, indent=2) + "\n")
        if cleanup_errors:
            raise RuntimeError("task cleanup incomplete; inspect retained result.json")
    print(json.dumps(evidence))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True, help="exact-manifest codex-exec-server test binary")
    parser.add_argument("--image", required=True, help="existing qualified immutable executor image ID")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--controller", type=Path, help="candidate parsar-daemon for local Runtime retirement acceptance")
    qualify(parser.parse_args())
