#!/usr/bin/env python3
"""Exercise installer failures without accessing a Docker daemon."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
REAL_DOCKER = shutil.which("docker")
if REAL_DOCKER and subprocess.run([REAL_DOCKER, "compose", "version"], capture_output=True).returncode:
    REAL_DOCKER = None

MOCK = r'''#!/usr/bin/env python3
import json, os, pathlib, shlex, signal, subprocess, sys
args = sys.argv[1:]
root = pathlib.Path(os.environ["CASE_ROOT"])
mode = os.environ.get("CASE_MODE", "")
with (root / "calls").open("a") as f:
    f.write(json.dumps(args) + "\n")
if args[:2] == ["compose", "version"] or args[:1] == ["info"]:
    if mode == "docker": sys.exit(1)
    print(("windows" if mode == "windows" else "linux") if args[0] == "info" else ("2.14.0" if mode == "old-compose" else "2.15.1"))
elif "config" in args:
    if mode == "config": sys.exit(1)
    if os.environ.get("REAL_DOCKER"):
        result = subprocess.run([os.environ["REAL_DOCKER"], *args])
        sys.exit(result.returncode)
    if "--services" in args: print("postgres\nparsar-server")
elif "pull" in args:
    if mode == "pull": sys.exit(1)
    if mode == "signal":
        os.kill(os.getppid(), signal.SIGTERM)
        sys.exit(1)
    if mode == "lock":
        result = subprocess.run(["bash", os.environ["INSTALLER"], "--home", os.environ["INSTALL_HOME"], "--compose-file", os.environ["COMPOSE_SOURCE"]], capture_output=True)
        assert result.returncode and b"installation lock exists" in result.stderr
        (root / "lock-checked").touch()
elif "run" in args:
    if mode == "prepare" and "entries=$(ls" not in " ".join(args): sys.exit(1)
    if "id -u" in " ".join(args):
        print("12001:14001")
    elif os.environ.get("REAL_DOCKER"):
        config = subprocess.run([os.environ["REAL_DOCKER"], *args[:args.index("run")], "config", "--format", "json"], capture_output=True, text=True, check=True)
        service = "postgres" if "postgres" in args else "parsar-server"
        mounts = json.loads(config.stdout)["services"][service]["volumes"]
        command_index = args.index("-ec") if "-ec" in args else args.index("-c")
        command = args[command_index + 1]
        tail = args[command_index + 2:]
        if mode == "unreadable" and "entries=$(ls" in command: sys.exit(1)
        for mount in mounts:
            source = pathlib.Path(mount["source"].replace("$$", "$"))
            assert source.is_relative_to(root)
            source.mkdir(parents=True, exist_ok=True)
            command = command.replace(mount["target"], shlex.quote(str(source)))
            tail = [item.replace(mount["target"], str(source)) for item in tail]
        result = subprocess.run(["/bin/sh", args[command_index], command, *tail])
        sys.exit(result.returncode)
elif "up" in args:
    if mode == "up": sys.exit(1)
    (root / "started").touch()
elif "ps" in args and "-aq" in args:
    if mode == "inspect": sys.exit(1)
    if (root / "started").exists() or mode == "existing": print("test-container")
elif args[:1] == ["inspect"]:
    print("exited none" if mode == "health" or (mode == "not-ready" and (root / "readiness-failed").exists()) else "running healthy")
elif "exec" in args and "http://127.0.0.1:8080/readyz" in args and mode == "not-ready":
    (root / "readiness-failed").touch()
    sys.exit(1)
elif "port" in args:
    print("127.0.0.1:18080")
'''
DOWNLOAD = r'''#!/usr/bin/env python3
import os, pathlib, shutil, sys
args = sys.argv[1:]
target = pathlib.Path(args[args.index("-o") + 1])
if os.environ.get("CASE_MODE") == "download":
    target.write_text("partial")
    sys.exit(1)
shutil.copyfile(os.environ["COMPOSE_SOURCE"], target)
'''


class InstallerCases(unittest.TestCase):
    def setUp(self):
        parent = Path.home() / ".parsar" / "installer-tests"
        parent.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix="cases.", dir=parent)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "install home"
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for name, source in (("docker", MOCK), ("curl", DOWNLOAD), ("sudo", "#!/bin/sh\nexit 1\n"), ("chown", "#!/bin/sh\nexit 0\n"), ("stat", "#!/bin/sh\nprintf '%s:%s\\n' \"$(id -u)\" \"$(id -g)\"\n"), ("mv", "#!/bin/sh\nif [ \"$CASE_MODE\" = commit ] && [ \"${1##*/}\" = install.env ]; then exit 1; fi\nexec /bin/mv \"$@\"\n")):
            path = self.bin / name
            path.write_text(source)
            path.chmod(0o755)
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("PARSAR_", "COMPOSE_", "CASE_"))}
        self.env.update(PATH=f"{self.bin}:{os.environ['PATH']}", CASE_ROOT=str(self.root),
                        INSTALLER=str(REPO / "install.sh"), INSTALL_HOME=str(self.home),
                        COMPOSE_SOURCE=str(REPO / "docker-compose.yml"), REAL_DOCKER=REAL_DOCKER or "")

    def run_install(self, *args, mode="", download=False, success=True, pipe=False):
        self.env["CASE_MODE"] = mode
        command = ["bash", str(REPO / "install.sh"), "--home", str(self.home)]
        if not download:
            command += ["--compose-file", str(REPO / "docker-compose.yml")]
        command += list(args)
        if pipe:
            command = ["bash", "-s", "--", *command[2:]]
        result = subprocess.run(command, input=(REPO / "install.sh").read_text() if pipe else None,
                                cwd=self.root, env=self.env, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        if self.home.exists():
            self.assertEqual(list(self.home.glob(".install*")), [], "temporary files or lock leaked")
        return result

    def env_file(self):
        return self.home / ".env"

    def test_repeat_and_explicit_overrides(self):
        self.run_install("--port", "19090", "--bind", "0.0.0.0")
        before = self.env_file().read_bytes()
        self.run_install()
        self.assertEqual(self.env_file().read_bytes(), before)
        self.env["PARSAR_LOCAL_PORT"] = "19999"
        self.run_install("--port", "20000")
        self.assertIn('PARSAR_LOCAL_PORT="20000"', self.env_file().read_text())
        self.assertIn('PARSAR_BIND_ADDR="0.0.0.0"', self.env_file().read_text())

    def test_first_dry_run_and_download(self):
        self.run_install("--dry-run", download=True, pipe=True)
        self.assertEqual(list(self.home.iterdir()), [])
        self.run_install(download=True, pipe=True)
        self.assertTrue((self.home / "docker-compose.yml").is_file())
        self.assertEqual(self.env_file().stat().st_mode & 0o777, 0o600)

    def test_preflight_failures_preserve_files(self):
        self.run_install(download=True)
        before = self.env_file().read_bytes()
        compose = (self.home / "docker-compose.yml").read_bytes()
        for mode in ("docker", "download", "config", "pull", "signal", "commit", "inspect"):
            with self.subTest(mode=mode):
                self.run_install("--port", "20000", download=True, mode=mode, success=False)
                self.assertEqual(self.env_file().read_bytes(), before)
                self.assertEqual((self.home / "docker-compose.yml").read_bytes(), compose)

    def test_post_preflight_failure_is_retryable(self):
        for mode in ("prepare", "up", "health", "not-ready"):
            with self.subTest(mode=mode):
                self.run_install(mode=mode, success=False)
                before = self.env_file().read_bytes()
                self.run_install()
                self.assertEqual(self.env_file().read_bytes(), before)

    @unittest.skipUnless(REAL_DOCKER, "requires Compose parser")
    def test_core_initialization_and_preservation(self):
        self.run_install()
        core_file = self.home / "core" / "workspaces.json"
        self.assertEqual(core_file.read_text(), "[]\n")
        self.assertEqual(list(core_file.parent.glob(".workspaces.*")), [])
        core_file.write_text('[{"workspace_id":"preserved"}]\n')
        self.run_install()
        self.assertEqual(core_file.read_text(), '[{"workspace_id":"preserved"}]\n')
        self.assertTrue((self.home / "data" / ".parsar").is_dir())

    def test_lock(self):
        self.run_install(mode="lock")
        self.assertTrue((self.root / "lock-checked").exists())

    def test_invalid_arguments(self):
        for args in (("--port", "0"), ("--port", "65536"), ("--port", "x"),
                     ("--port", "99999999999999999999999"), ("--port",),
                     ("--port", "--dry-run"), ("--home", "relative"), ("--unknown",)):
            with self.subTest(args=args):
                self.run_install(*args, success=False)
                self.assertFalse(self.home.exists())

    def test_environment_and_core_paths_preserved(self):
        self.env["PARSAR_CORE_CONFIG_DIR"] = str(self.root / "custom core")
        self.env["PARSAR_DATA_DIR"] = str(self.root / "custom data")
        self.run_install()
        before = self.env_file().read_bytes()
        del self.env["PARSAR_CORE_CONFIG_DIR"], self.env["PARSAR_DATA_DIR"]
        self.run_install()
        self.assertEqual(self.env_file().read_bytes(), before)

    def test_quoted_path_and_port(self):
        self.home = self.root / "space $value #quote's \\path"
        self.run_install("--port", "018080", success=False)
        self.run_install("--port", "08080")
        if REAL_DOCKER:
            result = subprocess.run([REAL_DOCKER, "compose", "-f", str(REPO / "docker-compose.yml"),
                                     "--env-file", str(self.env_file()), "config", "--format", "json"],
                                    env=self.env, capture_output=True, text=True, check=True)
            config = json.loads(result.stdout)
            mounts = config["services"]["parsar-server"]["volumes"]
            self.assertEqual(next(v["source"] for v in mounts if v["target"] == "/var/lib/parsar").replace("$$", "$"), str(self.home / "data"))

    def test_missing_secrets_existing_install(self):
        self.run_install()
        old = self.env_file().read_text()
        self.env_file().write_text("\n".join(line for line in old.splitlines() if not line.startswith("PARSAR_MASTER_KEY=")))
        self.run_install(mode="existing", success=False)

    @unittest.skipUnless(REAL_DOCKER, "requires Compose parser")
    def test_empty_secrets_are_rejected(self):
        self.run_install()
        before = self.env_file().read_text()
        for value in ("", '""', "'' # comment", "${MISSING_INSTALLER_SECRET}"):
            self.env_file().write_text("\n".join(line for line in before.splitlines() if not line.startswith("PARSAR_MASTER_KEY=")) + "\nPARSAR_MASTER_KEY=" + value + "\n")
            self.run_install(success=False)

    @unittest.skipUnless(REAL_DOCKER, "requires Compose parser")
    def test_recovery_with_custom_data_requires_original_secrets(self):
        self.env["PARSAR_PG_DATA_DIR"] = str(self.root / "database")
        database = Path(self.env["PARSAR_PG_DATA_DIR"])
        database.mkdir()
        (database / "PG_VERSION").write_text("16")
        self.run_install(success=False)
        self.assertFalse(self.env_file().exists())
        self.assertFalse((self.root / "started").exists())
        self.assertEqual((database / "PG_VERSION").read_text(), "16")

    @unittest.skipUnless(REAL_DOCKER, "requires Compose parser")
    def test_unreadable_data_does_not_rotate_secrets(self):
        self.run_install(mode="unreadable", success=False)
        self.assertFalse(self.env_file().exists())
        self.assertFalse((self.root / "started").exists())

    def test_platform_preflight(self):
        for mode in ("windows", "old-compose"):
            self.run_install(mode=mode, success=False)
            self.assertFalse(self.home.exists())

    def test_tilde_home(self):
        self.env["HOME"] = str(self.root)
        self.run_install("--home", "~/install home")
        self.assertTrue(self.env_file().exists())

    def test_preseeded_env(self):
        self.home.mkdir()
        self.env_file().write_text("export PARSAR_LOCAL_PORT = 19090\nPARSAR_BIND_ADDR: 0.0.0.0\n")
        self.run_install()
        self.assertIn("export PARSAR_LOCAL_PORT = 19090", self.env_file().read_text())
        self.assertIn("PARSAR_BIND_ADDR: 0.0.0.0", self.env_file().read_text())
        self.run_install("--port", "20000")
        self.assertNotIn("19090", self.env_file().read_text())

    def test_symlink_rejected(self):
        self.home.mkdir()
        target = self.root / "unrelated"
        target.write_text("untouched")
        self.env_file().symlink_to(target)
        self.run_install(success=False)
        self.assertEqual(target.read_text(), "untouched")


if __name__ == "__main__":
    print("Compose parser: " + (REAL_DOCKER or "unavailable; parser integration skipped"), flush=True)
    unittest.main()
